// Package domain holds the audit service's entities and rules.
//
// The audit service has no commands of its own — nothing is created over HTTP —
// so the rules that live here are the ones that turn an arbitrary platform event
// into a row somebody can read, filter and believe: which action it was, what it
// happened to, and which parts of the payload are safe to keep.
package domain

import (
	"errors"
	"strings"
	"time"
)

// SubjectPrefix is the namespace every platform subject carries.
//
// It is the same on every event, so repeating it in the "action" a reader
// filters on adds thirteen characters to every row and distinguishes nothing.
const SubjectPrefix = "karmarat."

// Entry is one recorded platform event.
//
// The id is the event's own id: see the primary key comment in the migration.
type Entry struct {
	ID      string `json:"id"`
	Subject string `json:"subject"`
	Action  string `json:"action"`
	// CompanyID is empty for platform-wide facts, which belong to no tenant.
	CompanyID     string    `json:"companyId,omitempty"`
	ActorID       string    `json:"actorId,omitempty"`
	CorrelationID string    `json:"correlationId,omitempty"`
	EntityType    string    `json:"entityType,omitempty"`
	EntityID      string    `json:"entityId,omitempty"`
	OccurredAt    time.Time `json:"occurredAt"`
	// RecordedAt is when this service caught up, which differs from OccurredAt
	// after an outage and is worth showing when the two disagree.
	RecordedAt time.Time      `json:"recordedAt"`
	Payload    map[string]any `json:"payload"`
}

// Sentinel errors the API layer maps onto problem documents.
var (
	ErrInvalidCursor = errors.New("the supplied cursor is not valid")
)

// ValidationError reports a caller mistake: a filter that is malformed or a
// range that cannot match anything.
//
// It exists so the API layer can answer with a 422 naming the offending field
// rather than collapsing every rejected input into a generic 500.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

// Invalid builds a validation error for a field.
func Invalid(field, message string) error {
	return &ValidationError{Field: field, Message: message}
}

// ActionOf renders a subject in the form a person reads and filters on.
//
// "karmarat.application.stage_changed" becomes "application.stage_changed".
// A subject that does not carry the platform namespace is kept whole rather than
// mangled: an unexpected subject is exactly the thing an audit trail should
// still show, and shortening it by guesswork would hide where it came from.
func ActionOf(subject string) string {
	trimmed := strings.TrimSpace(subject)
	return strings.TrimPrefix(trimmed, SubjectPrefix)
}

// DomainOf returns the middle segment of a subject — "application", "job" — or
// an empty string when the subject is not in the platform's shape.
func DomainOf(subject string) string {
	action := ActionOf(subject)
	domain, _, found := strings.Cut(action, ".")
	if !found {
		return ""
	}
	return domain
}

// subjectEntityKey names, for each event domain, the payload field that holds
// the id of the thing the event is about.
//
// Sessions and users are both recorded against the account they concern, because
// "who was deactivated" is the question somebody reading a security trail is
// asking; the session id is carried separately in the payload.
var subjectEntityKey = map[string]string{
	"application":  "applicationId",
	"job":          "jobId",
	"candidate":    "candidateId",
	"offer":        "offerId",
	"interview":    "interviewId",
	"company":      "companyId",
	"user":         "accountId",
	"session":      "accountId",
	"role":         "roleId",
	"subscription": "companyId",
	"payment":      "companyId",
}

// entityFallback is the order in which an id is picked when the subject's own
// key is absent, most specific first.
//
// "Most specific" means: the narrower the thing the id names, the more useful it
// is as a filter. An application id identifies one submission; a company id
// identifies the whole tenant and would make every unmatched event in an account
// look like it happened "to the company", which is true and useless.
var entityFallback = []struct {
	key        string
	entityType string
}{
	{"applicationId", "application"},
	{"offerId", "offer"},
	{"interviewId", "interview"},
	{"jobId", "job"},
	{"candidateId", "candidate"},
	{"roleId", "role"},
	{"accountId", "account"},
	{"companyId", "company"},
}

// entityTypeForKey names what each id field points at.
var entityTypeForKey = map[string]string{
	"applicationId": "application",
	"offerId":       "offer",
	"interviewId":   "interview",
	"jobId":         "job",
	"candidateId":   "candidate",
	"roleId":        "role",
	"accountId":     "account",
	"companyId":     "company",
}

// EntityOf decides which record an event is about.
//
// A single event names several: an application's stage change carries the job
// and the candidate alongside the application. "Which one is this about?" has to
// be decided rather than guessed at query time, or a reader filtering by job
// would miss every application event on that job — or, worse, a reader filtering
// by candidate would catch events that merely mentioned them.
//
// The subject already answers the question, so it is consulted first:
// `karmarat.application.stage_changed` is about an application, therefore the
// applicationId is the entity even though two other ids are present. Only when
// the subject's own key is missing from the payload does a fixed priority order
// pick the narrowest id that is there, so a notification or support event still
// anchors to something filterable. An event carrying none of them records no
// entity at all, which is honest: a made-up anchor would be filtered on.
func EntityOf(subject string, payload map[string]any) (entityType, entityID string) {
	if key, ok := subjectEntityKey[DomainOf(subject)]; ok {
		if id := stringField(payload, key); id != "" {
			return entityTypeForKey[key], id
		}
	}

	for _, candidate := range entityFallback {
		if id := stringField(payload, candidate.key); id != "" {
			return candidate.entityType, id
		}
	}

	return "", ""
}

// stringField reads one id out of a decoded payload.
//
// Lookup is normalised because the publishers are a mix of Go and NestJS
// services: `applicationId`, `application_id` and `applicationID` all reach the
// bus, and an entity that resolves for two services out of three is worse than
// one that resolves for none, because nobody notices the gap.
func stringField(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}

	if raw, ok := payload[key]; ok {
		if value, ok := raw.(string); ok {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				return trimmed
			}
		}
	}

	wanted := normalizeKey(key)
	for name, raw := range payload {
		if normalizeKey(name) != wanted {
			continue
		}
		if value, ok := raw.(string); ok {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

// normalizeKey folds case and drops separators so `api_key`, `apiKey` and
// `api-key` compare equal.
func normalizeKey(key string) string {
	var b strings.Builder
	b.Grow(len(key))
	for _, r := range strings.ToLower(key) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}
