// Package events is the platform's asynchronous backbone.
//
// Services publish facts about their own domain and subscribe to others'. A
// company being suspended, an application changing stage, a candidate being
// messaged — each is a durable event, so a consumer that was down still receives
// what it missed instead of silently diverging.
package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/karmarat/platform/packages/goshared/idgen"
)

// Subject names every event the platform publishes.
//
// The `karmarat.<domain>.<event>` shape lets a consumer subscribe to one
// event or to a whole domain with a wildcard.
const (
	StreamName = "KARMARAT"

	SubjectCompanyRegistered = "karmarat.company.registered"
	SubjectCompanyApproved   = "karmarat.company.approved"
	SubjectCompanySuspended  = "karmarat.company.suspended"
	SubjectCompanyUpdated    = "karmarat.company.updated"

	SubjectSubscriptionActivated = "karmarat.subscription.activated"
	SubjectSubscriptionRenewed   = "karmarat.subscription.renewed"
	SubjectSubscriptionExpired   = "karmarat.subscription.expired"
	SubjectSubscriptionCancelled = "karmarat.subscription.cancelled"

	SubjectPaymentSucceeded = "karmarat.payment.succeeded"
	SubjectPaymentFailed    = "karmarat.payment.failed"

	SubjectJobPublished   = "karmarat.job.published"
	SubjectJobUnpublished = "karmarat.job.unpublished"
	SubjectJobClosed      = "karmarat.job.closed"

	SubjectApplicationSubmitted    = "karmarat.application.submitted"
	SubjectApplicationStageChanged = "karmarat.application.stage_changed"
	SubjectApplicationRejected     = "karmarat.application.rejected"
	SubjectApplicationWithdrawn    = "karmarat.application.withdrawn"
	SubjectApplicationHired        = "karmarat.application.hired"

	SubjectInterviewScheduled = "karmarat.interview.scheduled"
	SubjectInterviewCancelled = "karmarat.interview.cancelled"
	SubjectInterviewCompleted = "karmarat.interview.completed"

	SubjectOfferSent     = "karmarat.offer.sent"
	SubjectOfferAccepted = "karmarat.offer.accepted"
	SubjectOfferDeclined = "karmarat.offer.declined"

	SubjectCandidateRegistered        = "karmarat.candidate.registered"
	SubjectCandidateVisibilityChanged = "karmarat.candidate.visibility_changed"
	SubjectCandidateProfileUpdated    = "karmarat.candidate.profile_updated"
	SubjectCandidateApproached        = "karmarat.candidate.approached"

	SubjectMessageSent      = "karmarat.message.sent"
	SubjectConversationOpen = "karmarat.message.conversation_opened"

	SubjectPlanPublished = "karmarat.plan.published"
	SubjectPlanRetired   = "karmarat.plan.retired"

	SubjectUserDeactivated = "karmarat.user.deactivated"
	SubjectSessionRevoked  = "karmarat.session.revoked"

	SubjectSupportTicketCreated = "karmarat.support.ticket_created"
	SubjectSupportTicketReplied = "karmarat.support.ticket_replied"

	SubjectNotificationRequested = "karmarat.notification.requested"
)

// AllSubjects is the wildcard the stream captures.
const AllSubjects = "karmarat.>"

// Envelope wraps every published event.
//
// The metadata is uniform so a consumer can audit, trace, and de-duplicate
// without knowing the payload's shape.
type Envelope struct {
	ID         string    `json:"id"`
	Subject    string    `json:"subject"`
	OccurredAt time.Time `json:"occurredAt"`
	// CompanyID scopes a tenant-owned event; empty for platform-wide events.
	CompanyID string `json:"companyId,omitempty"`
	// ActorID is who caused the event, for audit trails.
	ActorID string `json:"actorId,omitempty"`
	// CorrelationID ties an event back to the request that produced it.
	CorrelationID string          `json:"correlationId,omitempty"`
	Payload       json.RawMessage `json:"payload"`
}

// Bus publishes and consumes platform events.
type Bus struct {
	conn   *nats.Conn
	stream jetstream.JetStream
	logger *slog.Logger
}

// Connect opens a connection and ensures the platform stream exists.
func Connect(ctx context.Context, url, serviceName string, logger *slog.Logger) (*Bus, error) {
	conn, err := nats.Connect(url,
		nats.Name(serviceName),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			logger.Warn("nats disconnected", slog.Any("error", err))
		}),
		nats.ReconnectHandler(func(c *nats.Conn) {
			logger.Info("nats reconnected", slog.String("url", c.ConnectedUrl()))
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("events: connect: %w", err)
	}

	stream, err := jetstream.New(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("events: jetstream: %w", err)
	}

	bus := &Bus{conn: conn, stream: stream, logger: logger}
	if err := bus.ensureStream(ctx); err != nil {
		conn.Close()
		return nil, err
	}

	logger.Info("event bus connected", slog.String("url", conn.ConnectedUrl()))
	return bus, nil
}

func (b *Bus) ensureStream(ctx context.Context) error {
	_, err := b.stream.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:        StreamName,
		Description: "Karmarat platform domain events",
		Subjects:    []string{AllSubjects},
		Retention:   jetstream.LimitsPolicy,
		Storage:     jetstream.FileStorage,
		MaxAge:      30 * 24 * time.Hour,
		Duplicates:  5 * time.Minute,
	})
	if err != nil {
		return fmt.Errorf("events: ensure stream: %w", err)
	}
	return nil
}

// PublishOptions carries the metadata attached to an event.
type PublishOptions struct {
	CompanyID     string
	ActorID       string
	CorrelationID string
	// ID overrides the generated event id, which is also the broker's
	// de-duplication key.
	//
	// A service draining a transactional outbox must set it to the outbox row's
	// id. Otherwise a crash between publishing and marking the row sent produces
	// a second event with a fresh id on the retry, and JetStream — which
	// de-duplicates on the id it was given — has no way to recognize it as the
	// same fact.
	ID string
}

// Publish writes an event to the stream.
//
// The event ID doubles as the JetStream de-duplication key, so a retried publish
// after a network blip does not deliver the same fact twice.
func (b *Bus) Publish(ctx context.Context, subject string, payload any, opts PublishOptions) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("events: marshal payload for %s: %w", subject, err)
	}

	eventID := opts.ID
	if eventID == "" {
		eventID = idgen.New("evt")
	}

	envelope := Envelope{
		ID:            eventID,
		Subject:       subject,
		OccurredAt:    time.Now().UTC(),
		CompanyID:     opts.CompanyID,
		ActorID:       opts.ActorID,
		CorrelationID: opts.CorrelationID,
		Payload:       body,
	}

	encoded, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("events: marshal envelope for %s: %w", subject, err)
	}

	if _, err := b.stream.Publish(ctx, subject, encoded,
		jetstream.WithMsgID(envelope.ID),
	); err != nil {
		return fmt.Errorf("events: publish %s: %w", subject, err)
	}

	b.logger.Debug("event published",
		slog.String("subject", subject),
		slog.String("event_id", envelope.ID),
		slog.String("company_id", opts.CompanyID),
	)
	return nil
}

// Handler processes a delivered event.
type Handler func(ctx context.Context, envelope Envelope) error

// SubscribeOptions configures a durable consumer.
type SubscribeOptions struct {
	// Durable names the consumer so redelivery survives a restart.
	Durable string
	// Subjects filters which events reach the handler.
	Subjects []string
	// MaxDeliver caps redelivery attempts before a message is parked.
	MaxDeliver int
	// AckWait is how long a handler may take before redelivery.
	AckWait time.Duration
}

// Subscribe attaches a durable consumer.
//
// A handler that returns an error nak's the message so JetStream redelivers it
// with backoff; a permanently failing message is terminated after MaxDeliver so
// one bad payload cannot block the whole subject.
func (b *Bus) Subscribe(ctx context.Context, opts SubscribeOptions, handler Handler) (jetstream.ConsumeContext, error) {
	if opts.Durable == "" {
		return nil, errors.New("events: durable consumer name is required")
	}
	if opts.MaxDeliver == 0 {
		opts.MaxDeliver = 5
	}
	if opts.AckWait == 0 {
		opts.AckWait = 30 * time.Second
	}
	if len(opts.Subjects) == 0 {
		opts.Subjects = []string{AllSubjects}
	}

	consumer, err := b.stream.CreateOrUpdateConsumer(ctx, StreamName, jetstream.ConsumerConfig{
		Durable:        opts.Durable,
		FilterSubjects: opts.Subjects,
		AckPolicy:      jetstream.AckExplicitPolicy,
		DeliverPolicy:  jetstream.DeliverAllPolicy,
		MaxDeliver:     opts.MaxDeliver,
		AckWait:        opts.AckWait,
		BackOff: []time.Duration{
			time.Second, 5 * time.Second, 30 * time.Second, 2 * time.Minute,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("events: create consumer %s: %w", opts.Durable, err)
	}

	return consumer.Consume(func(msg jetstream.Msg) {
		var envelope Envelope
		if err := json.Unmarshal(msg.Data(), &envelope); err != nil {
			// A malformed envelope will never parse; redelivery cannot help.
			b.logger.Error("event: malformed envelope, terminating",
				slog.String("subject", msg.Subject()),
				slog.Any("error", err),
			)
			_ = msg.Term()
			return
		}

		if err := handler(ctx, envelope); err != nil {
			b.logger.Error("event handler failed",
				slog.String("subject", envelope.Subject),
				slog.String("event_id", envelope.ID),
				slog.String("consumer", opts.Durable),
				slog.Any("error", err),
			)
			_ = msg.Nak()
			return
		}

		if err := msg.Ack(); err != nil {
			b.logger.Warn("event ack failed",
				slog.String("event_id", envelope.ID),
				slog.Any("error", err),
			)
		}
	})
}

// Decode unmarshals an envelope's payload.
func Decode[T any](envelope Envelope) (T, error) {
	var payload T
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return payload, fmt.Errorf("events: decode %s payload: %w", envelope.Subject, err)
	}
	return payload, nil
}

// HealthCheck returns a readiness probe for the connection.
func (b *Bus) HealthCheck() func(context.Context) error {
	return func(context.Context) error {
		if b.conn == nil || !b.conn.IsConnected() {
			return errors.New("events: not connected to NATS")
		}
		return nil
	}
}

// Close drains in-flight messages and closes the connection.
func (b *Bus) Close() error {
	if b.conn == nil {
		return nil
	}
	return b.conn.Drain()
}
