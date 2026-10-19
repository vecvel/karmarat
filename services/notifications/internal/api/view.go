package api

import (
	"time"

	"github.com/karmarat/platform/services/notifications/internal/domain"
)

// NotificationView is one notification as a client sees it.
//
// It is exported because the SSE stream and the list endpoint must agree: a
// client that renders a row from the list and then the same row from a live
// frame should not have to branch on where it came from.
type NotificationView struct {
	ID        string         `json:"id"`
	Type      domain.Type    `json:"type"`
	Title     string         `json:"title"`
	Body      string         `json:"body,omitempty"`
	Link      string         `json:"link,omitempty"`
	Payload   map[string]any `json:"payload"`
	Read      bool           `json:"read"`
	ReadAt    *time.Time     `json:"readAt,omitempty"`
	CreatedAt time.Time      `json:"createdAt"`
}

// ToNotificationView shapes a stored notification for the wire.
//
// The recipient's own account id is deliberately absent: a client only ever
// receives its own notifications, so echoing the id back would add a field that
// is either redundant or, if it ever differed, a bug worth failing on rather
// than rendering.
func ToNotificationView(n domain.Notification) NotificationView {
	payload := n.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	return NotificationView{
		ID:        n.ID,
		Type:      n.Type,
		Title:     n.Title,
		Body:      n.Body,
		Link:      n.Link,
		Payload:   payload,
		Read:      n.Read(),
		ReadAt:    n.ReadAt,
		CreatedAt: n.CreatedAt,
	}
}

// channelView is one type's switches as the preferences endpoint returns them.
type channelView struct {
	Type  domain.Type `json:"type"`
	InApp bool        `json:"inApp"`
	Email bool        `json:"email"`
}

// preferencesView is the whole preferences screen in one document.
//
// Only the types the principal can actually receive are listed. Returning the
// full catalogue would offer a candidate a switch for "an application was
// submitted to your company", which would do nothing however they set it.
type preferencesView struct {
	Types    []channelView `json:"types"`
	Channels []string      `json:"channels"`
}

func toPreferencesView(prefs domain.Preferences, relevant []domain.Type) preferencesView {
	view := preferencesView{
		Types:    make([]channelView, 0, len(relevant)),
		Channels: make([]string, 0, len(domain.AllChannels)),
	}
	for _, t := range relevant {
		set := prefs.For(t)
		view.Types = append(view.Types, channelView{Type: t, InApp: set.InApp, Email: set.Email})
	}
	for _, channel := range domain.AllChannels {
		view.Channels = append(view.Channels, string(channel))
	}
	return view
}
