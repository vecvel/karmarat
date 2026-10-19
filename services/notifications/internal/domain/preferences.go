package domain

import (
	"github.com/karmarat/platform/packages/goshared/tenancy"
)

// ChannelSet is one notification type's per-channel switches.
type ChannelSet struct {
	InApp bool `json:"inApp"`
	Email bool `json:"email"`
}

// Preferences is what a recipient has chosen, and only that.
//
// A type the recipient never touched is absent rather than stored with its
// default, so adding a notification type turns it on for everyone who would
// want it instead of leaving it off for every account that predates it.
type Preferences struct {
	Channels map[Type]ChannelSet `json:"channels"`
}

// NewPreferences returns empty preferences — every type at its default.
func NewPreferences() Preferences {
	return Preferences{Channels: map[Type]ChannelSet{}}
}

// defaultChannels is the platform's opinion about what is worth interrupting
// someone for.
//
// The rule behind the table: anything that changes a person's day arrives by
// email as well as in the product, and anything that is merely a number going
// up stays in the bell menu. A recruiter whose pipeline gets forty applications
// a day would filter our mail into a folder within a week, which costs us the
// channel for the offer acceptance that actually matters.
var defaultChannels = map[Type]ChannelSet{
	TypeApplicationSubmitted:    {InApp: true, Email: false},
	TypeApplicationStageChanged: {InApp: true, Email: true},
	TypeApplicationRejected:     {InApp: true, Email: true},
	TypeApplicationHired:        {InApp: true, Email: true},
	TypeInterviewScheduled:      {InApp: true, Email: true},
	TypeOfferSent:               {InApp: true, Email: true},
	TypeOfferAccepted:           {InApp: true, Email: true},
	TypeOfferDeclined:           {InApp: true, Email: true},
	TypeMessageReceived:         {InApp: true, Email: false},
	TypeCandidateApproached:     {InApp: true, Email: true},
	TypeSubscriptionExpired:     {InApp: true, Email: true},
	TypeSupportTicketReplied:    {InApp: true, Email: true},
	TypeJobPublished:            {InApp: true, Email: false},
}

// DefaultChannels returns the out-of-the-box setting for a type.
//
// An unknown type defaults to in-app only: a type this build does not know
// about should still be visible in the product, but must not reach someone's
// mailbox with copy nobody has reviewed.
func DefaultChannels(t Type) ChannelSet {
	if set, ok := defaultChannels[t]; ok {
		return set
	}
	return ChannelSet{InApp: true}
}

// For returns the channels in force for a type, defaults included.
func (p Preferences) For(t Type) ChannelSet {
	if set, ok := p.Channels[t]; ok {
		return set
	}
	return DefaultChannels(t)
}

// Allows reports whether a type may be delivered on a channel.
func (p Preferences) Allows(t Type, channel Channel) bool {
	set := p.For(t)
	switch channel {
	case ChannelInApp:
		return set.InApp
	case ChannelEmail:
		return set.Email
	default:
		return false
	}
}

// Effective is the full catalogue with every default resolved, which is what
// the preferences screen renders.
func (p Preferences) Effective() map[Type]ChannelSet {
	out := make(map[Type]ChannelSet, len(AllTypes))
	for _, t := range AllTypes {
		out[t] = p.For(t)
	}
	return out
}

// RelevantTypes are the types a principal of this kind can ever receive.
//
// The preferences screen uses it so a candidate is not offered a switch for
// "an application was submitted to your company", which would do nothing.
func RelevantTypes(principal tenancy.PrincipalType) []Type {
	switch principal {
	case tenancy.PrincipalCandidate:
		return []Type{
			TypeApplicationStageChanged,
			TypeApplicationRejected,
			TypeApplicationHired,
			TypeInterviewScheduled,
			TypeOfferSent,
			TypeMessageReceived,
			TypeCandidateApproached,
		}
	case tenancy.PrincipalCompany:
		return []Type{
			TypeApplicationSubmitted,
			TypeApplicationHired,
			TypeOfferAccepted,
			TypeOfferDeclined,
			TypeMessageReceived,
			TypeSubscriptionExpired,
			TypeSupportTicketReplied,
			TypeJobPublished,
		}
	case tenancy.PrincipalPlatform:
		return []Type{TypeSupportTicketReplied}
	default:
		return nil
	}
}

// Merge applies a client's update onto the stored preferences.
//
// It is a merge rather than a replace: the request carries the switches the
// person just moved, and a replace would silently reset every type the client
// build did not know about — which is exactly what happens during a rollout,
// when an old tab PUTs a payload missing this week's new type.
func (p Preferences) Merge(update map[Type]ChannelSet) (Preferences, FieldErrors) {
	merged := NewPreferences()
	for t, set := range p.Channels {
		merged.Channels[t] = set
	}

	problems := FieldErrors{}
	for t, set := range update {
		if !t.Valid() {
			problems["channels."+string(t)] = []string{"There is no notification type with that name."}
			continue
		}
		merged.Channels[t] = set
	}

	if len(problems) > 0 {
		return Preferences{}, problems
	}
	return merged, nil
}
