// Package realtime carries a new notification from whichever replica created it
// to whichever replica is holding the recipient's open connection.
//
// Those are rarely the same process. An SSE stream is pinned to one instance
// for its whole life, while the consumer that produces the notification is
// wherever JetStream happened to deliver the event, so a purely in-process fan
// out would light up one replica's clients and silently miss the rest. Redis
// pub/sub is the smallest thing that closes that gap: it is fire-and-forget,
// which is exactly right here, because the durable copy is already in Postgres
// and a client that missed a live frame reloads its list.
package realtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/karmarat/platform/services/notifications/internal/domain"
)

// channelPrefix namespaces this service's keys inside a shared Redis.
const channelPrefix = "notifications:stream:"

// Hub publishes and subscribes to per-recipient streams.
type Hub struct {
	client *redis.Client
	logger *slog.Logger
}

// New builds a hub. A nil client makes publishing a no-op and subscribing
// yield a stream that never fires, which is what lets the HTTP surface be
// exercised in a test without Redis.
func New(client *redis.Client, logger *slog.Logger) *Hub {
	return &Hub{client: client, logger: logger}
}

// Frame is one live event travelling between replicas.
//
// The two halves are separated on the wire as well as here: the SSE handler
// writes Event on the `event:` line and Data, untouched, on the `data:` line.
// Wrapping the payload again inside the data would make every client unwrap
// something the protocol already models.
type Frame struct {
	// Event names the SSE event type the browser dispatches on.
	Event string `json:"event"`
	// Data is the notification, already shaped for the client.
	Data any `json:"data"`
}

// EventNotification is the frame type carrying a new notification.
const EventNotification = "notification"

// Decode splits a published frame back into its event name and its payload.
//
// The payload is left as raw JSON: it was shaped for the client before it was
// published, and re-encoding it here would only risk changing it.
func Decode(raw []byte) (event string, data []byte, err error) {
	var frame struct {
		Event string          `json:"event"`
		Data  json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &frame); err != nil {
		return "", nil, fmt.Errorf("realtime: decode frame: %w", err)
	}
	if frame.Event == "" {
		frame.Event = EventNotification
	}
	return frame.Event, frame.Data, nil
}

// Publish sends a frame to one recipient's stream.
//
// A failure is logged rather than returned: the notification is already in
// Postgres, and failing the consumer over a cache hiccup would redeliver the
// event and risk a second row instead of a missed animation.
func (h *Hub) Publish(ctx context.Context, recipient domain.Recipient, frame Frame) {
	if h.client == nil {
		return
	}

	encoded, err := json.Marshal(frame)
	if err != nil {
		h.logger.Error("realtime: could not encode frame", slog.Any("error", err))
		return
	}

	if err := h.client.Publish(ctx, channelFor(recipient), encoded).Err(); err != nil {
		h.logger.Warn("realtime: could not publish frame",
			slog.String("principal_type", string(recipient.PrincipalType)),
			slog.Any("error", err))
	}
}

// Subscription is one client's live feed.
type Subscription struct {
	pubsub *redis.PubSub
	frames <-chan []byte
}

// Frames yields encoded frames until the subscription is closed. A nil-client
// hub returns a channel that never fires, so a caller's select still blocks on
// its context rather than spinning.
func (s *Subscription) Frames() <-chan []byte { return s.frames }

// Close releases the subscription.
func (s *Subscription) Close() error {
	if s.pubsub == nil {
		return nil
	}
	return s.pubsub.Close()
}

// Subscribe opens a recipient's stream.
//
// The channel name carries the principal type and the tenant as well as the
// account id, so a frame published with the wrong tenant lands on a channel
// nobody is listening to instead of in another company's browser.
func (h *Hub) Subscribe(ctx context.Context, recipient domain.Recipient) (*Subscription, error) {
	if h.client == nil {
		return &Subscription{}, nil
	}

	pubsub := h.client.Subscribe(ctx, channelFor(recipient))

	// Confirm the subscription before the handler writes its first byte. Redis
	// subscribes lazily, and without this a notification created in the gap
	// between the HTTP response starting and the subscription landing would be
	// dropped with nothing to show for it.
	confirmCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := pubsub.Receive(confirmCtx); err != nil {
		_ = pubsub.Close()
		return nil, fmt.Errorf("realtime: subscribe: %w", err)
	}

	out := make(chan []byte, 16)
	go func() {
		defer close(out)
		for msg := range pubsub.Channel() {
			select {
			case out <- []byte(msg.Payload):
			case <-ctx.Done():
				return
			}
		}
	}()

	return &Subscription{pubsub: pubsub, frames: out}, nil
}

// channelFor is the Redis channel a recipient's frames travel on.
func channelFor(recipient domain.Recipient) string {
	return channelPrefix + recipient.StreamKey()
}
