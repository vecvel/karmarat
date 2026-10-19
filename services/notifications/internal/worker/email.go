// Package worker drains the email queue.
//
// Sending is deliberately not part of consuming an event. An SMTP conversation
// takes seconds and fails often, and doing it inside the consumer would hold a
// JetStream ack open until the relay answered — one slow mail server would then
// stall every notification behind it, including the in-app ones that have
// nothing to do with email.
package worker

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/karmarat/platform/packages/goshared/tenancy"
	"github.com/karmarat/platform/services/notifications/internal/mail"
	"github.com/karmarat/platform/services/notifications/internal/store"
)

// PortalURLs are the front doors a link in an email can point at.
//
// A notification stores a portal-relative path, not a URL: the row outlives any
// particular hostname, and the same notification renders differently depending
// on which portal the recipient signs in to. The absolute link is composed here,
// at send time, from configuration.
type PortalURLs struct {
	Company string
	Jobs    string
	Root    string
}

// For returns the base URL a principal of this kind signs in at.
func (p PortalURLs) For(principal string) string {
	switch tenancy.PrincipalType(principal) {
	case tenancy.PrincipalCompany:
		return p.Company
	case tenancy.PrincipalCandidate:
		return p.Jobs
	case tenancy.PrincipalPlatform:
		return p.Root
	default:
		return p.Jobs
	}
}

// EmailConfig configures the worker.
type EmailConfig struct {
	Store   *store.Store
	Mailer  *mail.Mailer
	Logger  *slog.Logger
	Portals PortalURLs
	// Batch is how many messages one pass claims.
	Batch int
	// BaseBackoff is the first retry delay; each further attempt triples it.
	BaseBackoff time.Duration
}

// Email drains the queue.
type Email struct {
	store   *store.Store
	mailer  *mail.Mailer
	logger  *slog.Logger
	portals PortalURLs
	batch   int
	backoff time.Duration
}

// NewEmail builds the worker.
func NewEmail(cfg EmailConfig) *Email {
	if cfg.Batch <= 0 {
		cfg.Batch = 25
	}
	if cfg.BaseBackoff <= 0 {
		cfg.BaseBackoff = 30 * time.Second
	}
	return &Email{
		store:   cfg.Store,
		mailer:  cfg.Mailer,
		logger:  cfg.Logger,
		portals: cfg.Portals,
		batch:   cfg.Batch,
		backoff: cfg.BaseBackoff,
	}
}

// Run drains the queue until the context is cancelled.
func (w *Email) Run(ctx context.Context, interval time.Duration) {
	if !w.mailer.Configured() {
		// Worth one loud line at boot rather than a warning per queued message:
		// a deployment without SMTP configured still records what it would have
		// sent, and the queue is there to drain once it is.
		w.logger.Warn("email worker idle: no SMTP relay configured, mail will queue but not send")
		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := w.Drain(ctx); err != nil {
				w.logger.Error("email drain failed", slog.Any("error", err))
			}
		}
	}
}

// Drain sends one batch and reports how many messages went out.
func (w *Email) Drain(ctx context.Context) (int, error) {
	pending, err := w.store.ClaimDueEmails(ctx, w.batch, int(w.backoff.Seconds()))
	if err != nil {
		return 0, err
	}

	sent := 0
	for _, queued := range pending {
		sendErr := w.mailer.Send(ctx, mail.Message{
			ToAddress:   queued.ToAddress,
			ToName:      queued.ToName,
			Subject:     queued.Subject,
			Template:    queued.Template,
			Title:       text(queued.Data, "title"),
			Body:        text(queued.Data, "body"),
			ActionLabel: text(queued.Data, "actionLabel"),
			ActionURL:   w.actionURL(queued.Data),
		})
		if sendErr == nil {
			if err := w.store.MarkEmailSent(ctx, queued.ID); err != nil {
				return sent, err
			}
			sent++
			continue
		}

		// A malformed address or a missing template will fail identically on
		// every attempt, so it is dead-lettered now instead of occupying the
		// queue for five rounds of backoff.
		permanent := errors.Is(sendErr, mail.ErrUndeliverable)
		exhausted := permanent || queued.Exhausted()

		if err := w.store.MarkEmailFailed(ctx, queued.ID, sendErr.Error(), exhausted); err != nil {
			return sent, err
		}

		level := slog.LevelWarn
		if exhausted {
			level = slog.LevelError
		}
		w.logger.Log(ctx, level, "email delivery failed",
			slog.String("email_id", queued.ID),
			slog.Int("attempt", queued.Attempts),
			slog.Bool("dead_lettered", exhausted),
			slog.Any("error", sendErr))
	}

	return sent, nil
}

// actionURL composes the absolute link from the stored portal and path.
//
// A path that is not rooted, or a portal with no configured base, yields no
// button at all rather than a broken one: a mail with a dead link is worse than
// a mail that simply tells you to open the product.
func (w *Email) actionURL(data map[string]any) string {
	link := text(data, "link")
	if !strings.HasPrefix(link, "/") {
		return ""
	}
	base := strings.TrimRight(w.portals.For(text(data, "portal")), "/")
	if base == "" {
		return ""
	}
	return base + link
}

func text(data map[string]any, key string) string {
	value, ok := data[key].(string)
	if !ok {
		return ""
	}
	return value
}
