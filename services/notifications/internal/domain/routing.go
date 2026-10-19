package domain

import (
	"strings"
	"time"

	"github.com/karmarat/platform/packages/goshared/events"
	"github.com/karmarat/platform/packages/goshared/tenancy"
)

// Permission keys used to decide who at a company hears about a fan-out.
//
// Every one of these is declared in services/identity/internal/rbac/registry.go
// and only mirrored here so a typo is a compile error at one place rather than
// a string literal spread through the routing table. This service invents no
// permission of its own: reading your own inbox is not a capability a role
// grants, it is what having an account means.
const (
	permApplicationsRead    = "applications.read"
	permOffersRead          = "offers.read"
	permMessagingRead       = "messaging.read"
	permBillingRead         = "billing.read"
	permSupportRead         = "support.read"
	permJobsRead            = "jobs.read"
	permPlatformSupportRead = "platform_support.read"
)

// Event is a platform event reduced to what routing needs.
//
// It is a plain struct rather than the bus envelope so the routing table can be
// tested without a broker, and so a change to the envelope does not ripple into
// the rules.
type Event struct {
	ID         string
	Subject    string
	CompanyID  string
	ActorID    string
	OccurredAt time.Time
	Payload    map[string]any
}

// Audience is who should hear about an event.
//
// An audience either names one account outright — a candidate, whose id travels
// in the payload — or describes a group the directory has to expand, which is
// how a company-side event reaches the people at that company who are allowed
// to act on it.
type Audience struct {
	PrincipalType tenancy.PrincipalType
	// AccountID addresses one person. Empty means "expand CompanyID (or the
	// platform staff) through the directory, gated by Permission".
	AccountID string
	CompanyID string
	// Permission gates an expanded audience. Empty means everyone in it.
	Permission string
	// Email and Name come from the event when it carries them, so a candidate
	// who has never opened the product can still be mailed.
	Email string
	Name  string
	// Title, Body and Link override the routing's copy for this audience.
	// One fact can need two sentences: a hire is "you got the job" to the
	// person and "the requisition is filled" to the team, and sending either
	// side the other's wording is worse than sending nothing.
	Title string
	Body  string
	Link  string
}

// Routing is the notification an event turns into, and who gets it.
type Routing struct {
	Type      Type
	Title     string
	Body      string
	Link      string
	Payload   map[string]any
	Audiences []Audience
}

// Copy is the wording one audience receives.
func (r Routing) Copy(audience Audience) (title, body, link string) {
	title, body, link = r.Title, r.Body, r.Link
	if audience.Title != "" {
		title = audience.Title
	}
	if audience.Body != "" {
		body = audience.Body
	}
	if audience.Link != "" {
		link = audience.Link
	}
	return title, body, link
}

// Subjects lists the platform events this service consumes.
func Subjects() []string {
	return []string{
		events.SubjectApplicationSubmitted,
		events.SubjectApplicationStageChanged,
		events.SubjectApplicationRejected,
		events.SubjectApplicationHired,
		events.SubjectInterviewScheduled,
		events.SubjectOfferSent,
		events.SubjectOfferAccepted,
		events.SubjectOfferDeclined,
		events.SubjectMessageSent,
		events.SubjectCandidateApproached,
		events.SubjectSubscriptionExpired,
		events.SubjectSupportTicketReplied,
		events.SubjectJobPublished,
	}
}

// Route turns one platform event into the notification it should become.
//
// The second return value is false when the event is not one we notify on, or
// when it is missing the identifiers a notification needs. Both are permanent
// conditions: redelivering the same message cannot supply a company id it never
// had, so the caller acks rather than retrying forever.
//
// Every branch derives the recipient from the event's own identifiers. Nothing
// here reads a tenant from anywhere else, because there is nowhere else — the
// consumer has no request and no principal to be confused by.
func Route(ev Event) (Routing, bool) {
	company := str(ev.Payload, "companyId")
	if company == "" {
		company = ev.CompanyID
	}

	switch ev.Subject {

	// ---------------------------------------------------------------- pipeline

	case events.SubjectApplicationSubmitted:
		// The company side, not the candidate: the candidate has just pressed
		// the button and does not need to be told what they did.
		applicationID := str(ev.Payload, "applicationId")
		if company == "" || applicationID == "" {
			return Routing{}, false
		}
		return Routing{
			Type:    TypeApplicationSubmitted,
			Title:   "New application for " + jobTitle(ev.Payload),
			Body:    displayName(ev.Payload, "candidateName") + " applied.",
			Link:    "/applications/" + applicationID,
			Payload: ids(ev.Payload, "applicationId", "jobId", "candidateId", "stageId"),
			Audiences: []Audience{{
				PrincipalType: tenancy.PrincipalCompany,
				CompanyID:     company,
				Permission:    permApplicationsRead,
			}},
		}, true

	case events.SubjectApplicationStageChanged:
		candidate := str(ev.Payload, "candidateId")
		applicationID := str(ev.Payload, "applicationId")
		if candidate == "" || applicationID == "" {
			return Routing{}, false
		}
		return Routing{
			Type:    TypeApplicationStageChanged,
			Title:   "Your application moved forward",
			Body:    jobTitle(ev.Payload) + " is now at the " + stageName(ev.Payload) + " stage.",
			Link:    "/applications/" + applicationID,
			Payload: ids(ev.Payload, "applicationId", "jobId", "stageId", "stageName", "fromStageId"),
			Audiences: []Audience{{
				PrincipalType: tenancy.PrincipalCandidate,
				AccountID:     candidate,
				CompanyID:     company,
				Email:         str(ev.Payload, "candidateEmail"),
				Name:          str(ev.Payload, "candidateName"),
			}},
		}, true

	case events.SubjectApplicationRejected:
		candidate := str(ev.Payload, "candidateId")
		applicationID := str(ev.Payload, "applicationId")
		if candidate == "" || applicationID == "" {
			return Routing{}, false
		}
		// The rejection reason is not repeated to the candidate. It is an
		// internal label chosen from a company's own list ("not enough Go"),
		// written for a hiring team, and the event deliberately omits the note
		// that goes with it.
		return Routing{
			Type:    TypeApplicationRejected,
			Title:   "An update on your application",
			Body:    "Your application for " + jobTitle(ev.Payload) + " was not taken forward.",
			Link:    "/applications/" + applicationID,
			Payload: ids(ev.Payload, "applicationId", "jobId"),
			Audiences: []Audience{{
				PrincipalType: tenancy.PrincipalCandidate,
				AccountID:     candidate,
				CompanyID:     company,
				Email:         str(ev.Payload, "candidateEmail"),
				Name:          str(ev.Payload, "candidateName"),
			}},
		}, true

	case events.SubjectApplicationHired:
		candidate := str(ev.Payload, "candidateId")
		applicationID := str(ev.Payload, "applicationId")
		if candidate == "" || applicationID == "" {
			return Routing{}, false
		}
		// Both sides. A hire is the one pipeline event the company wants in
		// writing as much as the candidate does, but not in the same words.
		audiences := []Audience{{
			PrincipalType: tenancy.PrincipalCandidate,
			AccountID:     candidate,
			CompanyID:     company,
			Email:         str(ev.Payload, "candidateEmail"),
			Name:          str(ev.Payload, "candidateName"),
			Title:         "You have been hired",
			Body:          "Your application for " + jobTitle(ev.Payload) + " was successful.",
		}}
		if company != "" {
			audiences = append(audiences, Audience{
				PrincipalType: tenancy.PrincipalCompany,
				CompanyID:     company,
				Permission:    permApplicationsRead,
			})
		}
		return Routing{
			Type:      TypeApplicationHired,
			Title:     "Offer closed: " + jobTitle(ev.Payload),
			Body:      displayName(ev.Payload, "candidateName") + " was marked as hired.",
			Link:      "/applications/" + applicationID,
			Payload:   ids(ev.Payload, "applicationId", "jobId", "candidateId"),
			Audiences: audiences,
		}, true

	// -------------------------------------------------- interviews and offers
	//
	// No service publishes these yet. The field names read below are therefore
	// this consumer's half of a contract still to be met; they follow the
	// applications payload so an interviews service can reuse it unchanged.

	case events.SubjectInterviewScheduled:
		candidate := first(ev.Payload, "candidateAccountId", "candidateId")
		interviewID := str(ev.Payload, "interviewId")
		if candidate == "" || interviewID == "" {
			return Routing{}, false
		}
		return Routing{
			Type:    TypeInterviewScheduled,
			Title:   "Interview scheduled",
			Body:    "Your interview for " + jobTitle(ev.Payload) + " has been scheduled.",
			Link:    "/interviews/" + interviewID,
			Payload: ids(ev.Payload, "interviewId", "applicationId", "jobId", "scheduledAt", "mode"),
			Audiences: []Audience{{
				PrincipalType: tenancy.PrincipalCandidate,
				AccountID:     candidate,
				CompanyID:     company,
				Email:         str(ev.Payload, "candidateEmail"),
				Name:          str(ev.Payload, "candidateName"),
			}},
		}, true

	case events.SubjectOfferSent:
		candidate := first(ev.Payload, "candidateAccountId", "candidateId")
		offerID := str(ev.Payload, "offerId")
		if candidate == "" || offerID == "" {
			return Routing{}, false
		}
		// Deliberately no salary, equity or start date. Compensation is behind
		// offers.view_compensation inside the product, and an email is not a
		// place to re-publish it.
		return Routing{
			Type:    TypeOfferSent,
			Title:   "You have received an offer",
			Body:    "An offer for " + jobTitle(ev.Payload) + " is waiting for you.",
			Link:    "/offers/" + offerID,
			Payload: ids(ev.Payload, "offerId", "applicationId", "jobId"),
			Audiences: []Audience{{
				PrincipalType: tenancy.PrincipalCandidate,
				AccountID:     candidate,
				CompanyID:     company,
				Email:         str(ev.Payload, "candidateEmail"),
				Name:          str(ev.Payload, "candidateName"),
			}},
		}, true

	case events.SubjectOfferAccepted, events.SubjectOfferDeclined:
		offerID := str(ev.Payload, "offerId")
		if company == "" || offerID == "" {
			return Routing{}, false
		}
		accepted := ev.Subject == events.SubjectOfferAccepted
		notificationType, verb := TypeOfferDeclined, "declined"
		if accepted {
			notificationType, verb = TypeOfferAccepted, "accepted"
		}
		return Routing{
			Type:    notificationType,
			Title:   "Offer " + verb + ": " + jobTitle(ev.Payload),
			Body:    displayName(ev.Payload, "candidateName") + " " + verb + " the offer.",
			Link:    "/offers/" + offerID,
			Payload: ids(ev.Payload, "offerId", "applicationId", "jobId", "candidateId"),
			Audiences: []Audience{{
				PrincipalType: tenancy.PrincipalCompany,
				CompanyID:     company,
				Permission:    permOffersRead,
			}},
		}, true

	// ---------------------------------------------------------------- outreach

	case events.SubjectMessageSent:
		conversationID := str(ev.Payload, "conversationId")
		if conversationID == "" {
			return Routing{}, false
		}
		// The recipient is whichever side did not send. Notifying by sender
		// type rather than by comparing account ids keeps this correct when a
		// company principal sends on behalf of a colleague.
		//
		// The message preview the event carries is deliberately not rendered:
		// the conversation stays inside the product, and an inbox notification
		// that quoted it would put a candidate's words in a recruiter's mail
		// client with no way to retract them.
		subject := strings.TrimSpace(str(ev.Payload, "subject"))
		if subject == "" {
			subject = "your conversation"
		}
		switch str(ev.Payload, "senderType") {
		case "company":
			candidate := str(ev.Payload, "candidateAccountId")
			if candidate == "" {
				return Routing{}, false
			}
			return Routing{
				Type:    TypeMessageReceived,
				Title:   "New message",
				Body:    "You have a new message about " + subject + ".",
				Link:    "/messages/" + conversationID,
				Payload: ids(ev.Payload, "conversationId", "messageId", "applicationId", "jobId"),
				Audiences: []Audience{{
					PrincipalType: tenancy.PrincipalCandidate,
					AccountID:     candidate,
					CompanyID:     company,
				}},
			}, true
		case "candidate":
			if company == "" {
				return Routing{}, false
			}
			return Routing{
				Type:    TypeMessageReceived,
				Title:   "New message from a candidate",
				Body:    "There is a new reply in " + subject + ".",
				Link:    "/messages/" + conversationID,
				Payload: ids(ev.Payload, "conversationId", "messageId", "applicationId", "jobId"),
				Audiences: []Audience{{
					PrincipalType: tenancy.PrincipalCompany,
					CompanyID:     company,
					Permission:    permMessagingRead,
				}},
			}, true
		default:
			return Routing{}, false
		}

	case events.SubjectCandidateApproached:
		candidate := first(ev.Payload, "accountId", "candidateId")
		approachID := str(ev.Payload, "approachId")
		if candidate == "" || approachID == "" {
			return Routing{}, false
		}
		// The approach message itself is not repeated here even though the
		// event carries it: the candidate reads it in the product, where they
		// can reply, block, or report it.
		return Routing{
			Type:    TypeCandidateApproached,
			Title:   "A company would like to talk to you",
			Body:    "You have a new approach waiting in your inbox.",
			Link:    "/messages",
			Payload: ids(ev.Payload, "approachId", "jobId"),
			Audiences: []Audience{{
				PrincipalType: tenancy.PrincipalCandidate,
				AccountID:     candidate,
				CompanyID:     company,
			}},
		}, true

	// --------------------------------------------------- account and platform

	case events.SubjectSubscriptionExpired:
		if company == "" {
			return Routing{}, false
		}
		return Routing{
			Type:    TypeSubscriptionExpired,
			Title:   "Your subscription has expired",
			Body:    "Renew to restore access for your team.",
			Link:    "/settings/billing",
			Payload: ids(ev.Payload, "subscriptionId", "planId", "expiresAt"),
			Audiences: []Audience{{
				PrincipalType: tenancy.PrincipalCompany,
				CompanyID:     company,
				Permission:    permBillingRead,
			}},
		}, true

	case events.SubjectSupportTicketReplied:
		ticketID := str(ev.Payload, "ticketId")
		if ticketID == "" {
			return Routing{}, false
		}
		// Support tells us who the reply is news for: the desk hears about a
		// company's message, the company hears about the desk's. Deciding that
		// here from the author kind would duplicate a rule that already exists
		// where the reply was written.
		if str(ev.Payload, "audience") == "platform" {
			return Routing{
				Type:    TypeSupportTicketReplied,
				Title:   "New reply on a support ticket",
				Body:    ticketSubject(ev.Payload),
				Link:    "/support/tickets/" + ticketID,
				Payload: ids(ev.Payload, "ticketId", "messageId", "state", "priority"),
				Audiences: []Audience{{
					PrincipalType: tenancy.PrincipalPlatform,
					Permission:    permPlatformSupportRead,
				}},
			}, true
		}
		if company == "" {
			return Routing{}, false
		}
		return Routing{
			Type:    TypeSupportTicketReplied,
			Title:   "Support replied to your ticket",
			Body:    ticketSubject(ev.Payload),
			Link:    "/support/" + ticketID,
			Payload: ids(ev.Payload, "ticketId", "messageId", "state", "priority"),
			Audiences: []Audience{{
				PrincipalType: tenancy.PrincipalCompany,
				CompanyID:     company,
				Permission:    permSupportRead,
			}},
		}, true

	case events.SubjectJobPublished:
		jobID := str(ev.Payload, "jobId")
		if company == "" || jobID == "" {
			return Routing{}, false
		}
		return Routing{
			Type:    TypeJobPublished,
			Title:   jobTitle(ev.Payload) + " is live",
			Body:    "The requisition is now visible to candidates.",
			Link:    "/jobs/" + jobID,
			Payload: ids(ev.Payload, "jobId", "slug", "visibleOnPortal", "visibleOnNetwork"),
			Audiences: []Audience{{
				PrincipalType: tenancy.PrincipalCompany,
				CompanyID:     company,
				Permission:    permJobsRead,
			}},
		}, true

	default:
		return Routing{}, false
	}
}

/* -------------------------------------------------------------------------- */
/* Payload readers                                                            */
/* -------------------------------------------------------------------------- */

// str reads a string field, tolerating a field that is absent or of another
// type. A malformed payload should cost us one notification, not the consumer.
func str(payload map[string]any, key string) string {
	value, ok := payload[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

// first returns the first of several spellings that carries a value, which is
// what lets one branch serve a payload whose field name is still settling.
func first(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := str(payload, key); value != "" {
			return value
		}
	}
	return ""
}

// ids copies the identifying fields worth keeping onto the notification.
//
// It is an allow-list rather than the whole payload: an event may carry a
// candidate's email or a message preview, and everything in here is served back
// over the API and rendered into templates.
func ids(payload map[string]any, keys ...string) map[string]any {
	out := make(map[string]any, len(keys))
	for _, key := range keys {
		if value, ok := payload[key]; ok && value != nil {
			out[key] = value
		}
	}
	return out
}

func jobTitle(payload map[string]any) string {
	if title := first(payload, "jobTitle", "title"); title != "" {
		return title
	}
	return "this role"
}

func stageName(payload map[string]any) string {
	if stage := str(payload, "stageName"); stage != "" {
		return stage
	}
	return "next"
}

func ticketSubject(payload map[string]any) string {
	if subject := str(payload, "subject"); subject != "" {
		return subject
	}
	return "Open the ticket to read the reply."
}

// displayName falls back to a neutral noun rather than an empty string, so copy
// never reads "  applied."
func displayName(payload map[string]any, key string) string {
	if name := str(payload, key); name != "" {
		return name
	}
	return "A candidate"
}
