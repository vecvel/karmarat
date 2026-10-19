package events

import (
	"context"
	"log/slog"
	"strings"

	platformevents "github.com/karmarat/platform/packages/goshared/events"
	"github.com/karmarat/platform/services/interviews/internal/store"
)

// Consumer applies the applications service's facts to this service's data.
type Consumer struct {
	store  *store.Store
	logger *slog.Logger
}

// NewConsumer builds the event consumer.
func NewConsumer(st *store.Store, logger *slog.Logger) *Consumer {
	return &Consumer{store: st, logger: logger}
}

// Subjects are the events this consumer reacts to.
//
// They all come from one service and all concern one application, which is what
// lets a single durable subscription keep both the denormalized fields and the
// open rounds correct.
func Subjects() []string {
	return []string{
		platformevents.SubjectApplicationSubmitted,
		platformevents.SubjectApplicationStageChanged,
		platformevents.SubjectApplicationHired,
		platformevents.SubjectApplicationRejected,
		platformevents.SubjectApplicationWithdrawn,
	}
}

// applicationEvent is the applications service's published shape, narrowed to
// the fields this service acts on. Anything else it carries is ignored, so the
// publisher can add to the payload without breaking this consumer.
type applicationEvent struct {
	ApplicationID string `json:"applicationId"`
	CompanyID     string `json:"companyId"`
	CandidateName string `json:"candidateName"`
	JobTitle      string `json:"jobTitle"`
	Status        string `json:"status"`
}

// Handle applies one event.
//
// Returning an error nak's the message so JetStream redelivers it. Every branch
// is idempotent: the snapshot refresh ignores an event older than the row it
// would overwrite, and the cancellation only touches rounds that are still
// scheduled. A redelivery is therefore a no-op rather than a second effect, and
// a late delivery is a no-op rather than a regression.
func (c *Consumer) Handle(ctx context.Context, envelope platformevents.Envelope) error {
	payload, err := platformevents.Decode[applicationEvent](envelope)
	if err != nil {
		return err
	}

	companyID := firstNonEmpty(payload.CompanyID, envelope.CompanyID)
	if companyID == "" || payload.ApplicationID == "" {
		// Nothing to scope the work to, and redelivering cannot supply it.
		c.logger.Warn("application event was missing its identifiers",
			slog.String("subject", envelope.Subject),
			slog.String("event_id", envelope.ID))
		return nil
	}

	// Every one of these events carries the current name and title, so the
	// refresh runs first and unconditionally: a rejection is also the freshest
	// statement of who the candidate is.
	if _, err := c.store.RefreshApplicationSnapshot(ctx, companyID, payload.ApplicationID,
		strings.TrimSpace(payload.CandidateName), strings.TrimSpace(payload.JobTitle),
		envelope.OccurredAt); err != nil {
		return err
	}

	switch envelope.Subject {
	case platformevents.SubjectApplicationRejected,
		platformevents.SubjectApplicationWithdrawn,
		platformevents.SubjectApplicationHired:
		// An application that is closed has no upcoming rounds. Leaving them
		// booked wastes an interviewer's afternoon and, worse, has a candidate
		// turn up to a conversation about a job they are already out of.
		//
		// Hired counts. A candidate moved to hired after the technical still had
		// the onsite on the calendar, and this subject was subscribed to but fell
		// through the switch — so the one outcome everybody is happy about was
		// the one that left a stale interview booked.
		cancelled, err := c.store.CancelOpenInterviews(ctx, companyID, payload.ApplicationID,
			reasonFor(envelope.Subject))
		if err != nil {
			return err
		}
		if cancelled > 0 {
			c.logger.Info("cancelled interviews on a closed application",
				slog.String("company_id", companyID),
				slog.String("application_id", payload.ApplicationID),
				slog.String("subject", envelope.Subject),
				slog.Int64("interviews", cancelled))
		}
		return nil

	default:
		return nil
	}
}

// reasonFor is the cancellation reason a candidate and a panel both see, so an
// interview that vanishes from a calendar says why it did.
func reasonFor(subject string) string {
	switch subject {
	case platformevents.SubjectApplicationWithdrawn:
		return "The candidate withdrew their application."
	case platformevents.SubjectApplicationHired:
		// Said differently on purpose: an interviewer seeing "the application was
		// closed" against a hire would read it as a rejection.
		return "The candidate was hired, so the remaining rounds are not needed."
	default:
		return "The application was closed."
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
