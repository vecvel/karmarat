// Package expiry closes offers whose deadline has passed.
package expiry

import (
	"context"
	"log/slog"
	"time"

	"github.com/karmarat/platform/services/offers/internal/store"
)

// Worker sweeps outstanding offers past their expiry date.
//
// Expiry is the one state change no actor causes, so nothing else in the service
// can write it: a request-driven design would leave an offer "sent" until
// somebody happened to look at it, and a list would show a live offer that ran
// out three weeks ago. Mutations do not wait for this worker — they compare the
// deadline themselves — so a sweep that is late costs accuracy in a listing and
// never correctness in a decision.
type Worker struct {
	store  *store.Store
	logger *slog.Logger
}

// New builds the expiry worker.
func New(st *store.Store, logger *slog.Logger) *Worker {
	return &Worker{store: st, logger: logger}
}

// Run sweeps on an interval until the context is cancelled.
func (w *Worker) Run(ctx context.Context, interval time.Duration) {
	// Once at startup, because a process that was down over a weekend has a
	// backlog and the first tick could be a whole interval away.
	w.sweep(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.sweep(ctx)
		}
	}
}

func (w *Worker) sweep(ctx context.Context) {
	expired, err := w.store.ExpireDueOffers(ctx)
	if err != nil {
		w.logger.Error("offer expiry sweep failed", slog.Any("error", err))
		return
	}
	if expired > 0 {
		w.logger.Info("expired offers past their deadline", slog.Int64("offers", expired))
	}
}
