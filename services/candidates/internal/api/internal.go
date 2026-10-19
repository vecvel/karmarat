package api

import (
	"net/http"

	"github.com/karmarat/platform/packages/goshared/httpx"
	"github.com/karmarat/platform/services/candidates/internal/domain"
)

// internalProfile is the minimal shape other services need.
//
// Applications wants a display name for the pipeline, messaging wants to know a
// conversation's participant is real. Neither needs a phone number, a salary
// expectation or a visibility setting, so none are here: an internal endpoint is
// still an endpoint, and a service compromised elsewhere should not be able to
// dump the candidate database through it.
type internalProfile struct {
	AccountID    string `json:"accountId"`
	ProfileID    string `json:"profileId"`
	FullName     string `json:"fullName"`
	Email        string `json:"email"`
	Headline     string `json:"headline,omitempty"`
	Discoverable bool   `json:"discoverable"`
}

func (a *API) handleInternalProfile(w http.ResponseWriter, r *http.Request) {
	profile, err := a.store.FindProfileByAccount(r.Context(), r.PathValue("accountId"))
	if err != nil {
		// A deleted profile is reported as absent rather than as a distinct
		// state: a caller has nothing different to do, and the distinction would
		// confirm the account once existed.
		if err == domain.ErrProfileDeleted {
			httpx.WriteProblem(w, r, httpx.NotFound("That candidate could not be found."))
			return
		}
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, internalProfile{
		AccountID:    profile.AccountID,
		ProfileID:    profile.ID,
		FullName:     profile.FullName,
		Email:        profile.Email,
		Headline:     profile.Headline,
		Discoverable: profile.Visibility.Discoverable,
	})
}
