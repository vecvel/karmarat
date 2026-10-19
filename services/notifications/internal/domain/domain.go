// Package domain holds the notifications service's entities and rules.
//
// Nothing here does I/O. Which event reaches which person, and on which
// channel, is the only part of this service worth reasoning about carefully, so
// it is expressed as pure functions over plain values and tested without a
// database or a broker.
package domain

import (
	"errors"
	"strings"
	"time"

	"github.com/karmarat/platform/packages/goshared/tenancy"
)

// NoCompany is the sentinel tenant used for principals that have none.
//
// Preferences and the recipient directory are keyed by (principal, account,
// company). A nullable company would make every NULL distinct in that key, so a
// candidate would collect a new row on every write instead of updating one.
const NoCompany = "00000000-0000-0000-0000-000000000000"

// Type names a kind of notification.
//
// The value is stored, sent to clients and used as a preference key, so it is a
// stable vocabulary rather than a display string.
type Type string

const (
	TypeApplicationSubmitted    Type = "application.submitted"
	TypeApplicationStageChanged Type = "application.stage_changed"
	TypeApplicationRejected     Type = "application.rejected"
	TypeApplicationHired        Type = "application.hired"
	TypeInterviewScheduled      Type = "interview.scheduled"
	TypeOfferSent               Type = "offer.sent"
	TypeOfferAccepted           Type = "offer.accepted"
	TypeOfferDeclined           Type = "offer.declined"
	TypeMessageReceived         Type = "message.received"
	TypeCandidateApproached     Type = "candidate.approached"
	TypeSubscriptionExpired     Type = "subscription.expired"
	TypeSupportTicketReplied    Type = "support.ticket_replied"
	TypeJobPublished            Type = "job.published"
)

// AllTypes is the catalogue the preferences API exposes.
//
// It is ordered deliberately: the preferences screen renders it in this order,
// and an alphabetical sort would separate the three application events.
var AllTypes = []Type{
	TypeApplicationSubmitted,
	TypeApplicationStageChanged,
	TypeApplicationRejected,
	TypeApplicationHired,
	TypeInterviewScheduled,
	TypeOfferSent,
	TypeOfferAccepted,
	TypeOfferDeclined,
	TypeMessageReceived,
	TypeCandidateApproached,
	TypeSubscriptionExpired,
	TypeSupportTicketReplied,
	TypeJobPublished,
}

// Valid reports whether the type is one this service knows how to deliver.
func (t Type) Valid() bool {
	for _, known := range AllTypes {
		if known == t {
			return true
		}
	}
	return false
}

// Channel is a way of reaching someone.
type Channel string

const (
	// ChannelInApp is the row in the bell menu, pushed live over SSE.
	ChannelInApp Channel = "in_app"
	// ChannelEmail is an SMTP message.
	ChannelEmail Channel = "email"
)

// AllChannels is the catalogue the preferences API exposes.
var AllChannels = []Channel{ChannelInApp, ChannelEmail}

// Valid reports whether the channel is one this service can deliver on.
func (c Channel) Valid() bool {
	return c == ChannelInApp || c == ChannelEmail
}

// Recipient is one addressable person.
//
// It is built from the verified principal on an inbound request, or resolved
// from the directory on a fan-out; a request body never contributes to it.
type Recipient struct {
	PrincipalType tenancy.PrincipalType
	AccountID     string
	// CompanyID is the tenant for a company principal and empty otherwise.
	CompanyID string
	// Email and Name are carried only when the recipient was just resolved for
	// a fan-out; the inbox endpoints do not need them.
	Email string
	Name  string
}

// TenantKey returns the company identifier as the database stores it, mapping
// the tenant-less principals onto the sentinel.
func (r Recipient) TenantKey() string {
	if r.CompanyID == "" {
		return NoCompany
	}
	return r.CompanyID
}

// StreamKey is the pub/sub channel this recipient's live events travel on.
//
// The tenant is part of the key, not just the account id, so a publish that
// somehow carried the wrong company lands on a channel nobody is listening to
// rather than in another tenant's stream.
func (r Recipient) StreamKey() string {
	return string(r.PrincipalType) + ":" + r.TenantKey() + ":" + r.AccountID
}

// Addressable reports whether the recipient can be sent an email.
func (r Recipient) Addressable() bool {
	return strings.Contains(r.Email, "@")
}

// Notification is one delivered fact.
type Notification struct {
	ID            string
	PrincipalType tenancy.PrincipalType
	AccountID     string
	// CompanyID is the tenant the notification belongs to, empty when there is
	// none.
	CompanyID string
	Type      Type
	Title     string
	Body      string
	// Link is a portal-relative path, never an absolute URL: the row outlives
	// any particular hostname.
	Link string
	// Payload carries identifiers the front end needs to deep-link or group.
	// It never carries a token, a credential or a message body.
	Payload map[string]any
	ReadAt  *time.Time
	// EventID is the platform event that produced this row, and the key that
	// makes a redelivery a no-op.
	EventID   string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Read reports whether the recipient has seen the notification.
func (n Notification) Read() bool { return n.ReadAt != nil }

// Errors a client can reach. The text is the message the client is shown, so it
// is written for a person rather than for a log.
var (
	// ErrNotificationNotFound also covers another recipient's row: the filtered
	// query cannot tell the two apart, which is the point.
	ErrNotificationNotFound = errors.New("This notification could not be found.")
	// ErrInvalidCursor is returned rather than silently restarting the list.
	ErrInvalidCursor = errors.New("That pagination cursor is not valid. Start from the first page.")
	// ErrNotAddressable means the request came from a principal this service
	// cannot build an inbox for.
	ErrNotAddressable = errors.New("This endpoint is not available to your account type.")
)

// FieldErrors is the field-to-messages map a 422 carries.
type FieldErrors map[string][]string

// ValidationError is a single rejected field.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

// Invalid builds a validation error for one field.
func Invalid(field, message string) *ValidationError {
	return &ValidationError{Field: field, Message: message}
}
