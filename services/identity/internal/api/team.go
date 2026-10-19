package api

import (
	"net/http"
	"time"

	"github.com/karmarat/platform/packages/goshared/httpx"
	"github.com/karmarat/platform/packages/goshared/tenancy"
	"github.com/karmarat/platform/services/identity/internal/team"
)

/*
The company-facing team and role surface.

Everything here is scoped by `principal.RequireCompany()`. No handler reads a
company identifier from a path, a body or a query parameter, which is what makes
a cross-tenant read structurally impossible rather than a check somebody has to
remember. The account id in a path is resolved *within* that company, so an
identifier copied from another tenant reads as "not a member of your company".
*/

// companyRoute wraps a handler in the checks every team endpoint needs: a
// verified token, a company principal, and the permission the action requires.
//
// The permission is enforced here and not only in the gateway, because the
// gateway decides which routes a portal may reach, not which actions a person
// may take. A request that reaches this service with a valid company token has
// already passed the gateway.
func (a *API) companyRoute(permission string, handler http.HandlerFunc) http.Handler {
	return httpx.Authenticate(a.verifier)(
		httpx.RequirePrincipal(tenancy.PrincipalCompany)(
			httpx.RequirePermission(permission)(handler)))
}

// actorFrom builds the team actor from the verified principal.
func actorFrom(r *http.Request) (team.Actor, error) {
	principal := tenancy.MustFromContext(r.Context())

	companyID, err := principal.RequireCompany()
	if err != nil {
		return team.Actor{}, httpx.Forbidden("This action requires a company session.")
	}

	return team.Actor{
		AccountID:   principal.Subject,
		CompanyID:   companyID,
		Permissions: principal.Permissions,
	}, nil
}

/* -------------------------------------------------------------------------- */
/* Recruiters                                                                 */
/* -------------------------------------------------------------------------- */

func (a *API) handleListRecruiters(w http.ResponseWriter, r *http.Request) {
	actor, err := actorFrom(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	members, err := a.team.ListMembers(r.Context(), actor)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"recruiters": members})
}

type addRecruiterRequest struct {
	Email         string   `json:"email"`
	FullName      string   `json:"fullName"`
	Password      string   `json:"password,omitempty"`
	JobTitle      string   `json:"jobTitle,omitempty"`
	RoleIDs       []string `json:"roleIds"`
	PrimaryRoleID string   `json:"primaryRoleId,omitempty"`
}

func (a *API) handleAddRecruiter(w http.ResponseWriter, r *http.Request) {
	actor, err := actorFrom(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	var req addRecruiterRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	result, err := a.team.AddMember(r.Context(), actor, team.AddMemberInput{
		Email:         req.Email,
		FullName:      req.FullName,
		Password:      req.Password,
		JobTitle:      req.JobTitle,
		RoleIDs:       req.RoleIDs,
		PrimaryRoleID: req.PrimaryRoleID,
	})
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, result)
}

// fullName is absent on purpose: a person's name belongs to their account,
// which every company they recruit for shares.
type updateRecruiterRequest struct {
	JobTitle string `json:"jobTitle,omitempty"`
}

func (a *API) handleUpdateRecruiter(w http.ResponseWriter, r *http.Request) {
	actor, err := actorFrom(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	var req updateRecruiterRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	member, err := a.team.UpdateMember(r.Context(), actor, r.PathValue("accountID"), team.UpdateMemberInput{
		JobTitle: req.JobTitle,
	})
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, member)
}

type assignRolesRequest struct {
	RoleIDs       []string `json:"roleIds"`
	PrimaryRoleID string   `json:"primaryRoleId,omitempty"`
}

func (a *API) handleAssignRecruiterRoles(w http.ResponseWriter, r *http.Request) {
	actor, err := actorFrom(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	var req assignRolesRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	member, err := a.team.AssignRoles(r.Context(), actor,
		r.PathValue("accountID"), req.RoleIDs, req.PrimaryRoleID)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, member)
}

type recruiterStatusRequest struct {
	Active bool `json:"active"`
}

func (a *API) handleSetRecruiterStatus(w http.ResponseWriter, r *http.Request) {
	actor, err := actorFrom(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	var req recruiterStatusRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	member, err := a.team.SetMemberActive(r.Context(), actor, r.PathValue("accountID"), req.Active)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, member)
}

func (a *API) handleRemoveRecruiter(w http.ResponseWriter, r *http.Request) {
	actor, err := actorFrom(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	if err := a.team.RemoveMember(r.Context(), actor, r.PathValue("accountID")); err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.NoContent(w)
}

/* -------------------------------------------------------------------------- */
/* Roles                                                                      */
/* -------------------------------------------------------------------------- */

// roleView is the wire shape of a company role.
//
// Written out rather than serialising domain.Role directly: the domain type has
// no JSON tags, and giving it some would let an unrelated field added to the
// entity leak onto the public surface by default.
type roleView struct {
	ID           string    `json:"id"`
	Slug         string    `json:"slug"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	Badge        string    `json:"badge"`
	Permissions  []string  `json:"permissions"`
	IsSuperAdmin bool      `json:"isSuperAdmin"`
	IsSystem     bool      `json:"isSystem"`
	MemberCount  int       `json:"memberCount"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

func toRoleView(view team.RoleView) roleView {
	permissions := view.Permissions
	if permissions == nil {
		permissions = []string{}
	}
	return roleView{
		ID: view.ID, Slug: view.Slug, Name: view.Name,
		Description: view.Description, Badge: view.Badge,
		Permissions: permissions, IsSuperAdmin: view.IsSuperAdmin, IsSystem: view.IsSystem,
		MemberCount: view.MemberCount, CreatedAt: view.CreatedAt, UpdatedAt: view.UpdatedAt,
	}
}

func (a *API) handleListCompanyRoles(w http.ResponseWriter, r *http.Request) {
	actor, err := actorFrom(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	roles, err := a.team.ListRoles(r.Context(), actor)
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	views := make([]roleView, 0, len(roles))
	for _, role := range roles {
		views = append(views, toRoleView(role))
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"roles": views})
}

type createRoleRequest struct {
	Name        string   `json:"name"`
	Slug        string   `json:"slug,omitempty"`
	Description string   `json:"description,omitempty"`
	Badge       string   `json:"badge,omitempty"`
	Permissions []string `json:"permissions"`
}

func (a *API) handleCreateCompanyRole(w http.ResponseWriter, r *http.Request) {
	actor, err := actorFrom(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	var req createRoleRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	// Creating a role with permissions is granting permissions, so it needs the
	// same capability as changing them on an existing one. Without this,
	// `company_roles.create` alone would be a way around
	// `company_roles.assign_permissions`.
	if len(req.Permissions) > 0 {
		if err := requireAssignPermissions(r); err != nil {
			httpx.WriteProblem(w, r, err)
			return
		}
	}

	role, err := a.team.CreateRole(r.Context(), actor, team.CreateRoleInput{
		Name:        req.Name,
		Slug:        req.Slug,
		Description: req.Description,
		Badge:       req.Badge,
		Permissions: req.Permissions,
	})
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, toRoleView(*role))
}

// updateRoleRequest uses pointers so an omitted field is left alone and an
// explicit `"description": ""` clears it. A plain string cannot tell those apart.
type updateRoleRequest struct {
	Name        *string   `json:"name,omitempty"`
	Description *string   `json:"description,omitempty"`
	Badge       *string   `json:"badge,omitempty"`
	Permissions *[]string `json:"permissions,omitempty"`
}

func (a *API) handleUpdateCompanyRole(w http.ResponseWriter, r *http.Request) {
	actor, err := actorFrom(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	var req updateRoleRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	if req.Permissions != nil {
		if err := requireAssignPermissions(r); err != nil {
			httpx.WriteProblem(w, r, err)
			return
		}
	}

	role, err := a.team.UpdateRole(r.Context(), actor, r.PathValue("roleID"), team.UpdateRoleInput{
		Name:        req.Name,
		Description: req.Description,
		Badge:       req.Badge,
		Permissions: req.Permissions,
	})
	if err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, toRoleView(*role))
}

func (a *API) handleDeleteCompanyRole(w http.ResponseWriter, r *http.Request) {
	actor, err := actorFrom(r)
	if err != nil {
		httpx.WriteProblem(w, r, err)
		return
	}

	if err := a.team.DeleteRole(r.Context(), actor, r.PathValue("roleID")); err != nil {
		httpx.WriteProblem(w, r, mapError(err))
		return
	}

	httpx.NoContent(w)
}

// requireAssignPermissions checks the capability that changing a permission
// matrix needs, on top of the one the route already required.
func requireAssignPermissions(r *http.Request) error {
	const permission = "company_roles.assign_permissions"

	if tenancy.MustFromContext(r.Context()).Can(permission) {
		return nil
	}
	return httpx.PermissionDenied([]string{permission})
}
