package events

import (
	"context"
	"log/slog"
	"strings"
	"time"

	platformevents "github.com/karmarat/platform/packages/goshared/events"
	"github.com/karmarat/platform/services/applications/internal/store"
)

// Consumer applies other services' facts to this service's data.
type Consumer struct {
	store  *store.Store
	logger *slog.Logger
}

// NewConsumer builds the event consumer.
func NewConsumer(st *store.Store, logger *slog.Logger) *Consumer {
	return &Consumer{store: st, logger: logger}
}

// Subjects are the events this consumer reacts to.
func Subjects() []string {
	return []string{
		// Both, because a company admitted by an administrator is created active
		// and never emits "approved". Seeding is idempotent, so a tenant that
		// emits both is seeded once.
		platformevents.SubjectCompanyRegistered,
		platformevents.SubjectCompanyApproved,
		platformevents.SubjectJobClosed,
	}
}

type companyApproved struct {
	CompanyID string `json:"companyId"`
}

type jobClosed struct {
	JobID     string     `json:"jobId"`
	CompanyID string     `json:"companyId"`
	ClosedAt  *time.Time `json:"closedAt"`
}

// Handle applies one event.
//
// Returning an error nak's the message so JetStream redelivers it. Every branch
// is idempotent — seeding checks for existing rows, closing a job only touches
// applications not already marked — so a redelivery is a no-op rather than a
// second effect.
func (c *Consumer) Handle(ctx context.Context, envelope platformevents.Envelope) error {
	switch envelope.Subject {
	case platformevents.SubjectCompanyRegistered, platformevents.SubjectCompanyApproved:
		payload, err := platformevents.Decode[companyApproved](envelope)
		if err != nil {
			return err
		}

		companyID := firstNonEmpty(payload.CompanyID, envelope.CompanyID)
		if companyID == "" {
			// Nothing to scope the seed to; redelivering cannot supply it.
			c.logger.Warn("company event carried no company id",
				slog.String("event_id", envelope.ID))
			return nil
		}

		seeded, err := c.store.SeedCompanyDefaults(ctx, companyID)
		if err != nil {
			return err
		}
		if seeded {
			c.logger.Info("seeded default pipeline", slog.String("company_id", companyID))
		}
		return nil

	case platformevents.SubjectJobClosed:
		payload, err := platformevents.Decode[jobClosed](envelope)
		if err != nil {
			return err
		}

		companyID := firstNonEmpty(payload.CompanyID, envelope.CompanyID)
		if companyID == "" || payload.JobID == "" {
			c.logger.Warn("job closed event was missing its identifiers",
				slog.String("event_id", envelope.ID))
			return nil
		}

		closedAt := envelope.OccurredAt
		if payload.ClosedAt != nil {
			closedAt = *payload.ClosedAt
		}

		affected, err := c.store.MarkJobClosed(ctx, companyID, payload.JobID, closedAt)
		if err != nil {
			return err
		}
		if affected > 0 {
			c.logger.Info("marked applications on a closed job",
				slog.String("company_id", companyID),
				slog.String("job_id", payload.JobID),
				slog.Int64("applications", affected))
		}
		return nil

	default:
		return nil
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
