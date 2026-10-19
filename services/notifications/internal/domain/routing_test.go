package domain

import (
	"strings"
	"testing"

	"github.com/karmarat/platform/packages/goshared/events"
	"github.com/karmarat/platform/packages/goshared/tenancy"
)

const (
	testCompany   = "11111111-1111-1111-1111-111111111111"
	testCandidate = "acc_candidate"
)

// wantAudience is the part of an audience a test asserts on. Copy is checked
// separately; who hears about an event is the rule worth pinning down.
type wantAudience struct {
	principal  tenancy.PrincipalType
	accountID  string
	companyID  string
	permission string
}

func TestRouteAudiences(t *testing.T) {
	t.Parallel()

	applicationPayload := map[string]any{
		"applicationId":  "app_1",
		"companyId":      testCompany,
		"jobId":          "job_1",
		"jobTitle":       "Staff Engineer",
		"candidateId":    testCandidate,
		"candidateName":  "Ada Lovelace",
		"candidateEmail": "ada@example.com",
		"stageId":        "stg_1",
		"stageName":      "Interview",
	}

	tests := []struct {
		name     string
		event    Event
		wantType Type
		want     []wantAudience
	}{
		{
			name:     "an application notifies the company, not the applicant",
			event:    Event{Subject: events.SubjectApplicationSubmitted, Payload: applicationPayload},
			wantType: TypeApplicationSubmitted,
			want: []wantAudience{
				{principal: tenancy.PrincipalCompany, companyID: testCompany, permission: "applications.read"},
			},
		},
		{
			name:     "a stage change notifies the candidate",
			event:    Event{Subject: events.SubjectApplicationStageChanged, Payload: applicationPayload},
			wantType: TypeApplicationStageChanged,
			want: []wantAudience{
				{principal: tenancy.PrincipalCandidate, accountID: testCandidate, companyID: testCompany},
			},
		},
		{
			name:     "a rejection notifies the candidate",
			event:    Event{Subject: events.SubjectApplicationRejected, Payload: applicationPayload},
			wantType: TypeApplicationRejected,
			want: []wantAudience{
				{principal: tenancy.PrincipalCandidate, accountID: testCandidate, companyID: testCompany},
			},
		},
		{
			name:     "a hire notifies both sides",
			event:    Event{Subject: events.SubjectApplicationHired, Payload: applicationPayload},
			wantType: TypeApplicationHired,
			want: []wantAudience{
				{principal: tenancy.PrincipalCandidate, accountID: testCandidate, companyID: testCompany},
				{principal: tenancy.PrincipalCompany, companyID: testCompany, permission: "applications.read"},
			},
		},
		{
			name: "an interview notifies the candidate",
			event: Event{Subject: events.SubjectInterviewScheduled, Payload: map[string]any{
				"interviewId":        "int_1",
				"companyId":          testCompany,
				"candidateAccountId": testCandidate,
				"jobTitle":           "Staff Engineer",
			}},
			wantType: TypeInterviewScheduled,
			want: []wantAudience{
				{principal: tenancy.PrincipalCandidate, accountID: testCandidate, companyID: testCompany},
			},
		},
		{
			name: "an offer notifies the candidate",
			event: Event{Subject: events.SubjectOfferSent, Payload: map[string]any{
				"offerId":            "ofr_1",
				"companyId":          testCompany,
				"candidateAccountId": testCandidate,
			}},
			wantType: TypeOfferSent,
			want: []wantAudience{
				{principal: tenancy.PrincipalCandidate, accountID: testCandidate, companyID: testCompany},
			},
		},
		{
			name: "an accepted offer notifies the company",
			event: Event{Subject: events.SubjectOfferAccepted, Payload: map[string]any{
				"offerId":   "ofr_1",
				"companyId": testCompany,
			}},
			wantType: TypeOfferAccepted,
			want: []wantAudience{
				{principal: tenancy.PrincipalCompany, companyID: testCompany, permission: "offers.read"},
			},
		},
		{
			name: "a declined offer notifies the company",
			event: Event{Subject: events.SubjectOfferDeclined, Payload: map[string]any{
				"offerId":   "ofr_1",
				"companyId": testCompany,
			}},
			wantType: TypeOfferDeclined,
			want: []wantAudience{
				{principal: tenancy.PrincipalCompany, companyID: testCompany, permission: "offers.read"},
			},
		},
		{
			name: "a company's message notifies the candidate",
			event: Event{Subject: events.SubjectMessageSent, Payload: map[string]any{
				"conversationId":     "cnv_1",
				"companyId":          testCompany,
				"candidateAccountId": testCandidate,
				"senderType":         "company",
				"subject":            "Staff Engineer",
			}},
			wantType: TypeMessageReceived,
			want: []wantAudience{
				{principal: tenancy.PrincipalCandidate, accountID: testCandidate, companyID: testCompany},
			},
		},
		{
			name: "a candidate's message notifies the company",
			event: Event{Subject: events.SubjectMessageSent, Payload: map[string]any{
				"conversationId":     "cnv_1",
				"companyId":          testCompany,
				"candidateAccountId": testCandidate,
				"senderType":         "candidate",
				"subject":            "Staff Engineer",
			}},
			wantType: TypeMessageReceived,
			want: []wantAudience{
				{principal: tenancy.PrincipalCompany, companyID: testCompany, permission: "messaging.read"},
			},
		},
		{
			name: "an approach notifies the candidate",
			event: Event{Subject: events.SubjectCandidateApproached, Payload: map[string]any{
				"approachId": "apr_1",
				"companyId":  testCompany,
				"accountId":  testCandidate,
			}},
			wantType: TypeCandidateApproached,
			want: []wantAudience{
				{principal: tenancy.PrincipalCandidate, accountID: testCandidate, companyID: testCompany},
			},
		},
		{
			name: "an expiry notifies whoever can pay",
			event: Event{Subject: events.SubjectSubscriptionExpired, Payload: map[string]any{
				"companyId":      testCompany,
				"subscriptionId": "sub_1",
			}},
			wantType: TypeSubscriptionExpired,
			want: []wantAudience{
				{principal: tenancy.PrincipalCompany, companyID: testCompany, permission: "billing.read"},
			},
		},
		{
			name: "a desk reply goes to the company",
			event: Event{Subject: events.SubjectSupportTicketReplied, Payload: map[string]any{
				"ticketId":  "tkt_1",
				"companyId": testCompany,
				"audience":  "company",
			}},
			wantType: TypeSupportTicketReplied,
			want: []wantAudience{
				{principal: tenancy.PrincipalCompany, companyID: testCompany, permission: "support.read"},
			},
		},
		{
			name: "a company reply goes to the desk",
			event: Event{Subject: events.SubjectSupportTicketReplied, Payload: map[string]any{
				"ticketId":  "tkt_1",
				"companyId": testCompany,
				"audience":  "platform",
			}},
			wantType: TypeSupportTicketReplied,
			want: []wantAudience{
				{principal: tenancy.PrincipalPlatform, permission: "platform_support.read"},
			},
		},
		{
			name: "a published job notifies the hiring team",
			event: Event{Subject: events.SubjectJobPublished, Payload: map[string]any{
				"jobId":     "job_1",
				"companyId": testCompany,
				"title":     "Staff Engineer",
			}},
			wantType: TypeJobPublished,
			want: []wantAudience{
				{principal: tenancy.PrincipalCompany, companyID: testCompany, permission: "jobs.read"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			routing, ok := Route(tc.event)
			if !ok {
				t.Fatalf("Route(%s) declined an event it should have routed", tc.event.Subject)
			}
			if routing.Type != tc.wantType {
				t.Errorf("type = %q, want %q", routing.Type, tc.wantType)
			}
			if routing.Title == "" {
				t.Error("routing produced no title")
			}
			if len(routing.Audiences) != len(tc.want) {
				t.Fatalf("got %d audiences, want %d", len(routing.Audiences), len(tc.want))
			}
			for i, want := range tc.want {
				got := routing.Audiences[i]
				if got.PrincipalType != want.principal {
					t.Errorf("audience %d: principal = %q, want %q", i, got.PrincipalType, want.principal)
				}
				if got.AccountID != want.accountID {
					t.Errorf("audience %d: account = %q, want %q", i, got.AccountID, want.accountID)
				}
				if got.CompanyID != want.companyID {
					t.Errorf("audience %d: company = %q, want %q", i, got.CompanyID, want.companyID)
				}
				if got.Permission != want.permission {
					t.Errorf("audience %d: permission = %q, want %q", i, got.Permission, want.permission)
				}
			}
		})
	}
}

// An event that cannot name a recipient must be declined rather than routed to
// a guess. Every case here is permanent, so the consumer acks it instead of
// asking JetStream to redeliver something that will never improve.
func TestRouteDeclines(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		event Event
	}{
		{
			name:  "a subject we do not notify on",
			event: Event{Subject: events.SubjectCompanyRegistered, Payload: map[string]any{"companyId": testCompany}},
		},
		{
			name:  "an application with no tenant",
			event: Event{Subject: events.SubjectApplicationSubmitted, Payload: map[string]any{"applicationId": "app_1"}},
		},
		{
			name: "a stage change with no candidate",
			event: Event{Subject: events.SubjectApplicationStageChanged, Payload: map[string]any{
				"applicationId": "app_1", "companyId": testCompany,
			}},
		},
		{
			name: "a message from neither side",
			event: Event{Subject: events.SubjectMessageSent, Payload: map[string]any{
				"conversationId": "cnv_1", "companyId": testCompany, "senderType": "robot",
			}},
		},
		{
			name: "a company message with no candidate to send it to",
			event: Event{Subject: events.SubjectMessageSent, Payload: map[string]any{
				"conversationId": "cnv_1", "companyId": testCompany, "senderType": "company",
			}},
		},
		{
			name:  "an expiry with no tenant",
			event: Event{Subject: events.SubjectSubscriptionExpired, Payload: map[string]any{"subscriptionId": "sub_1"}},
		},
		{
			name: "a support reply for a company we cannot name",
			event: Event{Subject: events.SubjectSupportTicketReplied, Payload: map[string]any{
				"ticketId": "tkt_1", "audience": "company",
			}},
		},
		{
			name:  "an empty payload",
			event: Event{Subject: events.SubjectJobPublished, Payload: map[string]any{}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, ok := Route(tc.event); ok {
				t.Fatalf("Route(%s) routed an event it should have declined", tc.event.Subject)
			}
		})
	}
}

// The envelope's tenant is the fallback when the payload does not repeat it, so
// a publisher that relies on PublishOptions.CompanyID alone still routes.
func TestRouteFallsBackToEnvelopeTenant(t *testing.T) {
	t.Parallel()

	routing, ok := Route(Event{
		Subject:   events.SubjectJobPublished,
		CompanyID: testCompany,
		Payload:   map[string]any{"jobId": "job_1", "title": "Staff Engineer"},
	})
	if !ok {
		t.Fatal("Route declined an event whose tenant was on the envelope")
	}
	if got := routing.Audiences[0].CompanyID; got != testCompany {
		t.Errorf("company = %q, want %q", got, testCompany)
	}
}

// The payload the notification keeps is an allow-list. An event that carries an
// address or a message body must not have it copied onto a row that is served
// back over the API and rendered into an email.
func TestRoutePayloadIsAnAllowList(t *testing.T) {
	t.Parallel()

	routing, ok := Route(Event{
		Subject: events.SubjectApplicationSubmitted,
		Payload: map[string]any{
			"applicationId":  "app_1",
			"companyId":      testCompany,
			"jobId":          "job_1",
			"candidateEmail": "ada@example.com",
			"answers":        map[string]any{"salary": "big"},
		},
	})
	if !ok {
		t.Fatal("Route declined a well-formed event")
	}
	for _, forbidden := range []string{"candidateEmail", "answers"} {
		if _, present := routing.Payload[forbidden]; present {
			t.Errorf("payload leaked %q onto the notification", forbidden)
		}
	}
	if routing.Payload["applicationId"] != "app_1" {
		t.Error("payload dropped the identifier the client needs to deep-link")
	}
}

// One fact, two audiences, two sentences: the person hired and the team that
// hired them must not be sent each other's wording.
func TestRouteCopyDiffersPerAudience(t *testing.T) {
	t.Parallel()

	routing, ok := Route(Event{
		Subject: events.SubjectApplicationHired,
		Payload: map[string]any{
			"applicationId": "app_1",
			"companyId":     testCompany,
			"candidateId":   testCandidate,
			"candidateName": "Ada Lovelace",
			"jobTitle":      "Staff Engineer",
		},
	})
	if !ok {
		t.Fatal("Route declined a well-formed hire")
	}

	candidateTitle, _, _ := routing.Copy(routing.Audiences[0])
	companyTitle, _, _ := routing.Copy(routing.Audiences[1])
	if candidateTitle == companyTitle {
		t.Errorf("both sides were sent the same title %q", candidateTitle)
	}
}

// A candidate's message is quoted nowhere. Messaging publishes a preview for
// convenience; repeating it in a notification would put one side's words in the
// other's mailbox with no way to retract them.
func TestRouteDoesNotQuoteMessageBodies(t *testing.T) {
	t.Parallel()

	const secret = "my salary expectation is 200k"
	routing, ok := Route(Event{
		Subject: events.SubjectMessageSent,
		Payload: map[string]any{
			"conversationId":     "cnv_1",
			"companyId":          testCompany,
			"candidateAccountId": testCandidate,
			"senderType":         "candidate",
			"subject":            "Staff Engineer",
			"preview":            secret,
		},
	})
	if !ok {
		t.Fatal("Route declined a well-formed message")
	}
	if strings.Contains(routing.Title, secret) || strings.Contains(routing.Body, secret) {
		t.Error("the message preview was copied into the notification copy")
	}
	if _, present := routing.Payload["preview"]; present {
		t.Error("the message preview was copied into the notification payload")
	}
}
