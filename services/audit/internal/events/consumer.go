// Package events records the platform's event stream as an audit trail.
//
// This is the only writer in the service: nothing creates an entry over HTTP.
// Every row therefore has an originating event, which is what makes the trail
// trustworthy — an entry cannot be authored, only observed.
package events

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	platformevents "github.com/karmarat/platform/packages/goshared/events"
	"github.com/karmarat/platform/services/audit/internal/domain"
	"github.com/karmarat/platform/services/audit/internal/store"
)

// DurableName is the consumer's identity on the stream.
//
// It is fixed and shared by every replica so the stream delivers each event to
// one of them and remembers the position across restarts. A durable named per
// instance would give every replica its own copy of the whole history.
const DurableName = "audit-recorder"

// Recorder writes platform events into the audit trail.
type Recorder struct {
	store  *store.Store
	logger *slog.Logger
}

// NewRecorder builds the event consumer.
func NewRecorder(st *store.Store, logger *slog.Logger) *Recorder {
	return &Recorder{store: st, logger: logger}
}

// Subjects is every subject the platform publishes.
//
// This service subscribes to the wildcard rather than to a list, because a list
// is a list somebody forgets to add to. A new service shipping a new event would
// otherwise be invisible in the trail, and the absence would look exactly like
// "nothing happened" — the failure mode an audit trail exists to rule out.
func Subjects() []string {
	return []string{platformevents.AllSubjects}
}

// Handle records one event.
//
// Returning an error nak's the message so JetStream redelivers it with backoff.
// The write is a conditional insert keyed on the event id, so a redelivery, a
// second replica and a replay after an outage all leave exactly one row.
//
// A malformed event is acked rather than nak'd. Redelivering cannot make a
// payload parse or supply a company id it never carried, and a message that
// nak's forever occupies the consumer's redelivery budget and delays every
// event behind it — which in this service means the trail stops recording,
// silently, because of one bad publisher.
func (r *Recorder) Handle(ctx context.Context, envelope platformevents.Envelope) error {
	payload := r.decodePayload(envelope)

	entityType, entityID := domain.EntityOf(envelope.Subject, payload)

	entry := domain.Entry{
		ID:            r.entryID(envelope),
		Subject:       envelope.Subject,
		Action:        domain.ActionOf(envelope.Subject),
		CompanyID:     r.tenantOf(envelope),
		ActorID:       strings.TrimSpace(envelope.ActorID),
		CorrelationID: strings.TrimSpace(envelope.CorrelationID),
		EntityType:    entityType,
		EntityID:      entityID,
		OccurredAt:    occurredAt(envelope),
		Payload:       domain.Redact(payload),
	}

	recorded, err := r.store.Record(ctx, entry)
	if err != nil {
		return err
	}
	if !recorded {
		r.logger.Debug("audit entry already recorded",
			slog.String("event_id", entry.ID),
			slog.String("subject", entry.Subject))
	}
	return nil
}

// decodePayload turns the event body into something the rest of this file can
// read, without ever discarding it.
//
// A payload that is not a JSON object — a bare string or an array from a
// publisher that changed shape — is still evidence, so it is kept as one field
// rather than dropped. Redaction then caps it like any other string.
func (r *Recorder) decodePayload(envelope platformevents.Envelope) map[string]any {
	if len(envelope.Payload) == 0 {
		return map[string]any{}
	}

	payload := map[string]any{}
	if err := json.Unmarshal(envelope.Payload, &payload); err == nil {
		return payload
	}

	r.logger.Warn("event payload was not a JSON object, recording it verbatim",
		slog.String("subject", envelope.Subject),
		slog.String("event_id", envelope.ID))
	return map[string]any{"_unstructured": string(envelope.Payload)}
}

// tenantOf decides which account, if any, the entry belongs to.
//
// The company id must be a uuid because that is the column's type. A malformed
// one would fail the insert, and since the insert failing nak's the message,
// one publisher with a bad id would stall the whole trail. Recording it with no
// tenant instead loses it from that company's screen but keeps it in the
// platform feed, where the malformed value is still visible in the payload —
// the fact survives, and the warning says where to look.
func (r *Recorder) tenantOf(envelope platformevents.Envelope) string {
	companyID := strings.TrimSpace(envelope.CompanyID)
	if companyID == "" {
		return ""
	}
	if _, err := uuid.Parse(companyID); err != nil {
		r.logger.Warn("event carried a company id that is not a uuid",
			slog.String("subject", envelope.Subject),
			slog.String("event_id", envelope.ID))
		return ""
	}
	return companyID
}

// entryID is the event's own id, and the reason a redelivery is a no-op.
//
// An envelope with no id should not exist — the bus mints one when a publisher
// does not — so reaching the fallback means a message arrived from outside
// Publish. Dropping it would lose a fact precisely when something unexpected is
// on the stream, so it is keyed by a digest of its own content instead: the same
// bytes redelivered hash the same, and de-duplication still holds.
func (r *Recorder) entryID(envelope platformevents.Envelope) string {
	if id := strings.TrimSpace(envelope.ID); id != "" {
		return id
	}

	digest := sha256.New()
	digest.Write([]byte(envelope.Subject))
	digest.Write([]byte{0})
	digest.Write([]byte(envelope.OccurredAt.UTC().Format(time.RFC3339Nano)))
	digest.Write([]byte{0})
	digest.Write(envelope.Payload)

	return "aud_" + hex.EncodeToString(digest.Sum(nil))[:32]
}

// occurredAt prefers the publisher's timestamp and falls back to now.
//
// A zero timestamp would sort to the bottom of every trail forever, where it
// would never be read again.
func occurredAt(envelope platformevents.Envelope) time.Time {
	if envelope.OccurredAt.IsZero() {
		return time.Now().UTC()
	}
	return envelope.OccurredAt.UTC()
}
