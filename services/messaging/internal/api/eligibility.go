package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/karmarat/platform/services/messaging/internal/candidates"
	"github.com/karmarat/platform/services/messaging/internal/domain"
)

// mayOpenConversation decides whether a company may start a thread with a
// candidate who has not written to it first.
//
// It reports whether the candidate has applied as well as whether contact is
// allowed, because the caller needs the same fact to decide the conversation's
// origin: a thread labelled "application" skips the daily cap, so that label has
// to come from a row rather than from the request that asked for it.
//
// The rule has two branches and they are checked cheapest-first:
//
//  1. the candidate applied to one of this company's jobs — an application is
//     standing permission to be contacted about it, and the fact is already in
//     this service's projection, so the common case costs no network hop;
//  2. otherwise the candidate must be discoverable and must not have blocked
//     this company.
//
// Branch 2 is split across two sources, and not by choice. The candidates
// service owns both halves, but its internal profile endpoint exposes only
// `discoverable`; the per-company block list is not on it. So discoverability is
// asked of the authority on every request — it is a consent switch the candidate
// can flip at any moment and a stale copy would hold the door open after they
// closed it — while the block list is read from this service's projection of the
// visibility events, which carry `hideFromCompanies`.
//
// The gap is worth closing upstream: `GET /internal/candidates/{accountId}`
// should answer "may company X contact this person?" rather than leaving two
// services to assemble the answer from different sources.
func (a *API) mayOpenConversation(ctx context.Context, companyID, candidateAccountID string) (bool, error) {
	applied, err := a.store.HasApplied(ctx, companyID, candidateAccountID)
	if err != nil {
		return false, err
	}
	if applied {
		return true, nil
	}

	profile, err := a.candidates.Fetch(ctx, candidateAccountID)
	switch {
	case err == nil:
	case errors.Is(err, candidates.ErrNotFound):
		return false, domain.ErrCandidateNotFound
	default:
		// Failing closed is the only safe direction. Treating an unreachable
		// directory as consent would mean an outage in the candidates service
		// silently opens every inbox on the platform.
		a.logger.Warn("candidate eligibility could not be established",
			slog.String("company_id", companyID),
			slog.Any("error", err))
		return false, domain.ErrDirectoryUnavailable
	}

	if !profile.Discoverable {
		return false, domain.ErrCandidateUnreachable
	}

	visibility, err := a.store.FindVisibility(ctx, candidateAccountID)
	if err != nil {
		return false, err
	}
	if visibility.Deleted || visibility.BlocksCompany(companyID) {
		// Same error as "not discoverable" on purpose: distinguishing them would
		// tell a company it had specifically been blocked, which is the one thing
		// a block list must not reveal.
		return false, domain.ErrCandidateUnreachable
	}

	return false, nil
}

// checkDailyOpenLimit enforces the cap on company-initiated conversations.
//
// Two layers, because they fail in different directions. The Redis sliding
// window is the fast path and absorbs a burst without touching the database; the
// database count is the one that actually binds, because redisx fails *open* on
// a cache outage and an opened conversation is a message in a stranger's inbox
// that cannot be recalled. A cache outage should cost latency, not the spam
// control.
func (a *API) checkDailyOpenLimit(ctx context.Context, companyID string) error {
	const window = 24 * time.Hour

	if a.limiter != nil {
		result, err := a.limiter.Allow(ctx, "conversations:"+companyID, a.dailyOpenLimit, window)
		if err != nil {
			a.logger.Warn("conversation rate limiter unavailable, falling back to the database count",
				slog.String("company_id", companyID),
				slog.Any("error", err))
		} else if !result.Allowed {
			return domain.ErrDailyLimitReached
		}
	}

	opened, err := a.store.CountConversationsOpenedSince(ctx, companyID, time.Now().Add(-window))
	if err != nil {
		return fmt.Errorf("api: daily conversation count: %w", err)
	}
	if opened >= a.dailyOpenLimit {
		return domain.ErrDailyLimitReached
	}

	return nil
}
