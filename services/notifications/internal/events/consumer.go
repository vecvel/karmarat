// Package events turns platform facts into notifications.
//
// This is the only writer of notifications: nothing creates one over HTTP.
// Every row therefore has an originating event, which is what makes
// de-duplication possible at all — the event id is the key the database refuses
// to store twice.
package events

import (
	"context"
	"encoding/json"
	"log/slog"

	platformevents "github.com/karmarat/platform/packages/goshared/events"
	"github.com/karmarat/platform/packages/goshared/tenancy"
	"github.com/karmarat/platform/services/notifications/internal/api"
	"github.com/karmarat/platform/services/notifications/internal/domain"
	"github.com/karmarat/platform/services/notifications/internal/realtime"
	"github.com/karmarat/platform/services/notifications/internal/store"
)

// Consumer applies platform events to this service's data.
type Consumer struct {
	store  *store.Store
	hub    *realtime.Hub
	logger *slog.Logger
}

// NewConsumer builds the event consumer.
func NewConsumer(st *store.Store, hub *realtime.Hub, logger *slog.Logger) *Consumer {
	return &Consumer{store: st, hub: hub, logger: logger}
}

// Subjects are the events this consumer reacts to.
func Subjects() []string { return domain.Subjects() }

// Handle turns one event into notifications.
//
// Idempotence is the whole design. JetStream redelivers on any error, a
// deployment runs several replicas, and a person must not be buzzed twice for
// one fact — so both writes this makes are conditional inserts whose uniqueness
// the database enforces: the notification on (event id, recipient), the email on
// a dedupe key derived from the same pair.
//
// Returning an error nak's the message. A missing identifier or an unknown
// subject is not an error: redelivering cannot supply a company id the event
// never had, so those are logged and acked.
func (c *Consumer) Handle(ctx context.Context, envelope platformevents.Envelope) error {
	payload := map[string]any{}
	if len(envelope.Payload) > 0 {
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			// A payload that is not an object will never become one. Acking it
			// costs one notification; nak'ing it blocks the subject.
			c.logger.Warn("event payload was not a JSON object",
				slog.String("subject", envelope.Subject),
				slog.String("event_id", envelope.ID))
			return nil
		}
	}

	routing, ok := domain.Route(domain.Event{
		ID:         envelope.ID,
		Subject:    envelope.Subject,
		CompanyID:  envelope.CompanyID,
		ActorID:    envelope.ActorID,
		OccurredAt: envelope.OccurredAt,
		Payload:    payload,
	})
	if !ok {
		c.logger.Debug("event produced no notification",
			slog.String("subject", envelope.Subject),
			slog.String("event_id", envelope.ID))
		return nil
	}

	for _, audience := range routing.Audiences {
		if err := c.deliver(ctx, envelope, routing, audience); err != nil {
			return err
		}
	}
	return nil
}

// deliver resolves one audience and notifies everyone in it.
func (c *Consumer) deliver(
	ctx context.Context,
	envelope platformevents.Envelope,
	routing domain.Routing,
	audience domain.Audience,
) error {
	recipients, err := c.resolve(ctx, audience)
	if err != nil {
		return err
	}
	if len(recipients) == 0 {
		// Nobody to tell. For a company audience this usually means no member
		// with the required permission has ever signed in, which is worth
		// seeing in a log rather than discovering as silence.
		c.logger.Info("no recipients for event",
			slog.String("subject", envelope.Subject),
			slog.String("event_id", envelope.ID),
			slog.String("principal_type", string(audience.PrincipalType)),
			slog.String("permission", audience.Permission))
		return nil
	}
	if len(recipients) > store.FanOutLimit {
		c.logger.Warn("audience hit the fan-out cap, some recipients were skipped",
			slog.String("subject", envelope.Subject),
			slog.Int("cap", store.FanOutLimit))
		recipients = recipients[:store.FanOutLimit]
	}

	preferences, err := c.store.LoadPreferencesFor(ctx, recipients)
	if err != nil {
		return err
	}

	for _, recipient := range recipients {
		prefs, ok := preferences[store.PreferenceKey(recipient)]
		if !ok {
			prefs = domain.NewPreferences()
		}

		if err := c.notify(ctx, envelope, routing, audience, recipient, prefs); err != nil {
			return err
		}
	}
	return nil
}

// notify writes one person's notification and queues their email.
//
// Preferences are consulted before anything is written, not after: a recipient
// who turned a type off should have no row created, not a row that is hidden.
// The two channels are independent — somebody may want the bell and not the
// mail, or the mail and not the bell — so neither is conditional on the other.
func (c *Consumer) notify(
	ctx context.Context,
	envelope platformevents.Envelope,
	routing domain.Routing,
	audience domain.Audience,
	recipient domain.Recipient,
	prefs domain.Preferences,
) error {
	title, body, link := routing.Copy(audience)

	if prefs.Allows(routing.Type, domain.ChannelInApp) {
		created, isNew, err := c.store.Create(ctx, store.CreateInput{
			Recipient: recipient,
			CompanyID: audience.CompanyID,
			Type:      routing.Type,
			Title:     title,
			Body:      body,
			Link:      link,
			Payload:   routing.Payload,
			EventID:   envelope.ID,
		})
		if err != nil {
			return err
		}
		if isNew {
			// Publishing after the commit: a frame the client acts on must
			// correspond to a row it can then fetch.
			c.hub.Publish(ctx, recipient, realtime.Frame{
				Event: realtime.EventNotification,
				Data:  api.ToNotificationView(created),
			})
		}
	}

	if !prefs.Allows(routing.Type, domain.ChannelEmail) {
		return nil
	}
	if !recipient.Addressable() {
		c.logger.Debug("recipient has no address on file, email skipped",
			slog.String("subject", envelope.Subject),
			slog.String("principal_type", string(recipient.PrincipalType)))
		return nil
	}

	// The queued row carries a portal and a path, never a URL. The worker
	// composes the absolute link at send time, and nothing signed, secret or
	// credential-bearing goes into the message.
	_, err := c.store.QueueEmail(ctx, store.EmailInput{
		DedupeKey:      envelope.ID + "|" + recipient.StreamKey(),
		NotificationID: "",
		Recipient:      recipient,
		Subject:        title,
		Template:       "notification",
		Data: map[string]any{
			"title":       title,
			"body":        body,
			"link":        link,
			"portal":      string(recipient.PrincipalType),
			"actionLabel": "Open in Karmarat",
		},
	})
	return err
}

// resolve turns an audience into the people in it.
//
// An audience that names an account is one person; anything else is a group the
// directory expands. Either way the recipient's own tenant is set from the
// principal kind, not from the event: a candidate belongs to no company however
// many companies their notifications mention, and keying their preferences or
// their live stream by a company would give them a different inbox per employer
// they applied to.
func (c *Consumer) resolve(ctx context.Context, audience domain.Audience) ([]domain.Recipient, error) {
	if audience.AccountID == "" {
		return c.store.ExpandAudience(ctx, audience)
	}

	recipient := domain.Recipient{
		PrincipalType: audience.PrincipalType,
		AccountID:     audience.AccountID,
		CompanyID:     recipientTenant(audience),
		Email:         audience.Email,
		Name:          audience.Name,
	}

	if recipient.Addressable() {
		// The event knew their address — an application snapshot carries one —
		// so record it. A candidate who applied through a careers portal and
		// never signed in is otherwise unreachable by mail.
		if err := c.store.RememberRecipient(ctx, recipient); err != nil {
			return nil, err
		}
		return []domain.Recipient{recipient}, nil
	}

	// No address in the event: fall back to whatever the directory holds.
	found, err := c.store.FindRecipient(ctx, recipient)
	if err != nil {
		return nil, err
	}
	return []domain.Recipient{found}, nil
}

// recipientTenant is the company a recipient *belongs to*, which is not the
// same as the company an event is *about*.
func recipientTenant(audience domain.Audience) string {
	if audience.PrincipalType == tenancy.PrincipalCompany {
		return audience.CompanyID
	}
	return ""
}
