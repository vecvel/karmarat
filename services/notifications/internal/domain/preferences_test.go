package domain

import (
	"testing"

	"github.com/karmarat/platform/packages/goshared/tenancy"
)

func TestPreferencesFallBackToDefaults(t *testing.T) {
	t.Parallel()

	prefs := NewPreferences()

	tests := []struct {
		name      string
		kind      Type
		channel   Channel
		wantAllow bool
	}{
		{"a rejection is worth an email", TypeApplicationRejected, ChannelEmail, true},
		{"a rejection is in the product too", TypeApplicationRejected, ChannelInApp, true},
		{"a new application is not worth an email", TypeApplicationSubmitted, ChannelEmail, false},
		{"a new application is still in the product", TypeApplicationSubmitted, ChannelInApp, true},
		{"a message is not worth an email", TypeMessageReceived, ChannelEmail, false},
		{"a published job is not worth an email", TypeJobPublished, ChannelEmail, false},
		{"an expiry is worth an email", TypeSubscriptionExpired, ChannelEmail, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := prefs.Allows(tc.kind, tc.channel); got != tc.wantAllow {
				t.Errorf("Allows(%q, %q) = %v, want %v", tc.kind, tc.channel, got, tc.wantAllow)
			}
		})
	}
}

// A type this build has never heard of must not reach anybody's mailbox: the
// copy has not been reviewed and the address list has not been reasoned about.
func TestUnknownTypeDefaultsToInAppOnly(t *testing.T) {
	t.Parallel()

	set := DefaultChannels(Type("something.invented"))
	if !set.InApp {
		t.Error("an unknown type should still be visible in the product")
	}
	if set.Email {
		t.Error("an unknown type must not be emailed")
	}
}

func TestPreferencesMerge(t *testing.T) {
	t.Parallel()

	stored := Preferences{Channels: map[Type]ChannelSet{
		TypeApplicationRejected: {InApp: true, Email: false},
	}}

	merged, problems := stored.Merge(map[Type]ChannelSet{
		TypeMessageReceived: {InApp: true, Email: true},
	})
	if len(problems) != 0 {
		t.Fatalf("a valid update was rejected: %v", problems)
	}

	// The update took effect...
	if !merged.Allows(TypeMessageReceived, ChannelEmail) {
		t.Error("the update was not applied")
	}
	// ...and the setting the client did not mention survived. A replace here
	// would silently turn rejection emails back on.
	if merged.Allows(TypeApplicationRejected, ChannelEmail) {
		t.Error("an untouched preference was reset by the merge")
	}
	// ...and the original is unchanged, because a merge that mutated its
	// receiver would leak one request's update into the next.
	if _, present := stored.Channels[TypeMessageReceived]; present {
		t.Error("Merge mutated the stored preferences")
	}
}

func TestPreferencesMergeRejectsUnknownType(t *testing.T) {
	t.Parallel()

	_, problems := NewPreferences().Merge(map[Type]ChannelSet{
		Type("application.invented"): {InApp: true},
	})
	if len(problems) == 0 {
		t.Fatal("an unknown notification type was accepted")
	}
	if _, named := problems["channels.application.invented"]; !named {
		t.Errorf("the rejected field was not named: %v", problems)
	}
}

// The preferences screen offers a principal only the switches that would do
// something. A candidate has no pipeline to be told about.
func TestRelevantTypesArePerPrincipal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		principal tenancy.PrincipalType
		absent    Type
		present   Type
	}{
		{tenancy.PrincipalCandidate, TypeApplicationSubmitted, TypeApplicationRejected},
		{tenancy.PrincipalCompany, TypeApplicationRejected, TypeApplicationSubmitted},
		{tenancy.PrincipalPlatform, TypeApplicationSubmitted, TypeSupportTicketReplied},
	}

	for _, tc := range tests {
		t.Run(string(tc.principal), func(t *testing.T) {
			t.Parallel()

			relevant := map[Type]bool{}
			for _, kind := range RelevantTypes(tc.principal) {
				relevant[kind] = true
			}
			if relevant[tc.absent] {
				t.Errorf("%s was offered %q, which it can never receive", tc.principal, tc.absent)
			}
			if !relevant[tc.present] {
				t.Errorf("%s was not offered %q, which it does receive", tc.principal, tc.present)
			}
		})
	}

	if got := RelevantTypes(tenancy.PrincipalAnonymous); got != nil {
		t.Errorf("an anonymous principal was offered %v", got)
	}
}

func TestEffectiveCoversTheWholeCatalogue(t *testing.T) {
	t.Parallel()

	effective := NewPreferences().Effective()
	if len(effective) != len(AllTypes) {
		t.Fatalf("Effective() returned %d types, want %d", len(effective), len(AllTypes))
	}
	for _, kind := range AllTypes {
		if _, present := effective[kind]; !present {
			t.Errorf("Effective() omitted %q", kind)
		}
	}
}
