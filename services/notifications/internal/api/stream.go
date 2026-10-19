package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/karmarat/platform/packages/goshared/httpx"
	"github.com/karmarat/platform/services/notifications/internal/realtime"
)

// handleStream pushes a recipient's notifications as they happen.
//
// Server-sent events rather than a WebSocket, for three reasons that all point
// the same way here. The traffic is one-way — the server tells the client that
// something happened and the client acts on it over the normal REST API — so
// the duplex channel a WebSocket buys would go unused. SSE is ordinary HTTP,
// which means it inherits the gateway's authentication, its tracing and its
// proxying unchanged, where a WebSocket needs an Upgrade path through every hop
// and its own way of carrying the principal. And EventSource reconnects by
// itself, with Last-Event-ID, so a laptop closing its lid costs nothing;
// reconnection logic is a thing we would otherwise have to write and get right.
//
// The durable copy is already in Postgres before anything is published here. A
// frame that is missed — a client between reconnects, a Redis blip — costs an
// animation, not a notification: the next list request has the row.
func (a *API) handleStream(w http.ResponseWriter, r *http.Request) {
	recipient, err := recipientOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		// Without a flusher every frame would sit in a buffer until the
		// response ended, which for a stream is never.
		httpx.WriteProblem(w, r, httpx.Internal("Streaming is not available on this connection."))
		return
	}

	subscription, err := a.hub.Subscribe(r.Context(), recipient)
	if err != nil {
		a.logger.Error("could not open notification stream",
			slog.String("principal_type", string(recipient.PrincipalType)),
			slog.Any("error", err))
		httpx.WriteProblem(w, r, httpx.NewProblem(http.StatusServiceUnavailable, "stream_unavailable",
			"Service Unavailable", "Live notifications are temporarily unavailable. Please retry."))
		return
	}
	defer func() { _ = subscription.Close() }()

	w.Header().Set("Content-Type", "text/event-stream")
	// No-transform is what stops a compressing proxy from buffering the stream
	// into a block it will never finish filling.
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	// Nginx buffers proxied responses by default and would hold every frame.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// The reconnect delay the browser uses after the stream ends. Short,
	// because ending the stream is routine here rather than exceptional.
	fmt.Fprint(w, "retry: 2000\n\n")
	flusher.Flush()

	heartbeat := time.NewTicker(a.heartbeat)
	defer heartbeat.Stop()

	// The stream ends itself well before the server's write timeout would cut
	// it, and the client reconnects. That is a workaround, not a design: the
	// write deadline is set per connection before any handler runs, and
	// clearing it needs http.ResponseController to reach the underlying
	// ResponseWriter through the middleware chain — which it cannot, because
	// httpx's logging wrapper does not implement Unwrap. Until it does, a
	// bounded stream plus SSE's native reconnect is the honest way to keep a
	// long-lived connection alive without loosening the timeout for every
	// ordinary request.
	lifetime := time.NewTimer(a.streamLifetime)
	defer lifetime.Stop()

	for {
		select {
		case <-r.Context().Done():
			// The client went away — closed the tab, lost the network, or the
			// server is shutting down. Returning releases the subscription.
			return

		case <-lifetime.C:
			fmt.Fprint(w, "event: reconnect\ndata: {\"reason\":\"rotate\"}\n\n")
			flusher.Flush()
			return

		case frame, open := <-subscription.Frames():
			if !open {
				return
			}
			event, data, err := realtime.Decode(frame)
			if err != nil {
				// A frame we cannot read is dropped rather than forwarded: the
				// durable row is already in Postgres, so the client's next list
				// request has it either way.
				a.logger.Warn("dropping an unreadable frame", slog.Any("error", err))
				continue
			}
			// The payload is compact JSON with no newlines of its own, so it
			// needs no splitting across several data: lines.
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
			flusher.Flush()

		case <-heartbeat.C:
			// A comment, which EventSource ignores. Its only job is to put
			// bytes on the wire: a proxy that sees nothing for a minute closes
			// an idle connection, and the client would not find out until it
			// next expected a notification and none arrived.
			fmt.Fprint(w, ": keep-alive\n\n")
			flusher.Flush()
		}
	}
}
