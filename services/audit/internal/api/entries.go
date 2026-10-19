package api

import (
	"net/http"

	"github.com/karmarat/platform/packages/goshared/httpx"
	"github.com/karmarat/platform/packages/goshared/tenancy"
)

// handleCompanyList returns one page of the caller's own trail.
//
// The tenant is the company on the verified principal and nothing else. There is
// no query parameter that widens it and no branch that skips it, because the
// only thing a reader can check about an audit trail is that it is complete —
// they cannot tell from the screen that a row belongs to somebody else.
func (a *API) handleCompanyList(w http.ResponseWriter, r *http.Request) {
	companyID, err := tenantOf(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	filter, err := filterFrom(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	page, err := a.store.ListForCompany(r.Context(), companyID, filter)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"entries":    page.Entries,
		"nextCursor": page.NextCursor,
	})
}

// handlePlatformList returns one page of every tenant's trail.
//
// The principal type is asserted again here, after the route guard has already
// asserted it. That is not belt and braces for its own sake: this is the only
// handler in the service whose query has no tenant predicate, so it is the one
// place where a future refactor that re-mounts a route — or mounts this handler
// behind the company guard by mistake — turns into a cross-tenant disclosure.
// The check costs a struct field comparison and removes that whole class of
// mistake.
func (a *API) handlePlatformList(w http.ResponseWriter, r *http.Request) {
	principal := tenancy.MustFromContext(r.Context())
	if !principal.IsPlatformAdmin() {
		httpx.WriteProblem(w, r, httpx.Forbidden("This endpoint is not available to your account type."))
		return
	}

	filter, err := filterFrom(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	page, err := a.store.ListForPlatform(r.Context(), filter)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"entries":    page.Entries,
		"nextCursor": page.NextCursor,
	})
}
