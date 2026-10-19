package team

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/karmarat/platform/packages/goshared/events"
	"github.com/karmarat/platform/services/identity/internal/auth"
	"github.com/karmarat/platform/services/identity/internal/domain"
	"github.com/karmarat/platform/services/identity/internal/rbac"
	"github.com/karmarat/platform/services/identity/internal/store"
)

// Service implements a company's management of its own team and roles.
type Service struct {
	store  *store.Store
	bus    *events.Bus
	policy auth.PasswordPolicy
	logger *slog.Logger
}

// New builds the team service.
func New(st *store.Store, bus *events.Bus, logger *slog.Logger) *Service {
	return &Service{store: st, bus: bus, policy: auth.DefaultPasswordPolicy(), logger: logger}
}

/* -------------------------------------------------------------------------- */
/* Roster                                                                     */
/* -------------------------------------------------------------------------- */

// ListMembers returns the company's people.
func (s *Service) ListMembers(ctx context.Context, actor Actor) ([]store.RosterMember, error) {
	return s.store.ListCompanyRoster(ctx, actor.CompanyID)
}

// AddMemberInput describes someone being given access to a company.
type AddMemberInput struct {
	Email         string
	FullName      string
	Password      string
	JobTitle      string
	RoleIDs       []string
	PrimaryRoleID string
}

// AddMemberResult reports what happened, so the caller can tell a new colleague
// from one who already recruits elsewhere on the platform.
type AddMemberResult struct {
	Member store.RosterMember `json:"member"`
	// AccountCreated is false when an existing login was attached instead.
	AccountCreated bool `json:"accountCreated"`
	// Reactivated is true when a previously removed membership was restored.
	Reactivated bool `json:"reactivated"`
}

// AddMember gives someone access to the company.
//
// There is no email delivery on the platform, so this is not an invitation: the
// administrator sets an initial password and passes it on out of band. An
// address that already has a company login is attached to this tenant with the
// password it already has — forcing a second account on the same person would
// give them two inboxes and one of them no way in.
//
// An address that already has a *membership* of this company is a different
// operation wearing the same request. Reinstating somebody reactivates their
// membership and rewrites their roles, which is exactly what SetMemberActive and
// AssignRoles do — so it runs the same guards they do. Without that, this
// endpoint was a way to demote a suspended super admin, and to un-suspend
// somebody, holding nothing but `recruiters.create`.
func (s *Service) AddMember(ctx context.Context, actor Actor, in AddMemberInput) (*AddMemberResult, error) {
	email := strings.ToLower(strings.TrimSpace(in.Email))
	if !looksLikeEmail(email) {
		return nil, domain.Invalid("email", "A valid email address is required.")
	}
	if strings.TrimSpace(in.FullName) == "" {
		return nil, domain.Invalid("fullName", "A name is required.")
	}

	roles, err := s.resolveRoles(ctx, actor.CompanyID, in.RoleIDs)
	if err != nil {
		return nil, err
	}
	if err := GuardDelegation(actor, grantedBy(roles), "this role assignment"); err != nil {
		return nil, err
	}

	primaryRoleID, err := resolvePrimary(roles, in.PrimaryRoleID)
	if err != nil {
		return nil, err
	}

	result := &AddMemberResult{}

	// Resolve who this is, and whether they already belong here, before anything
	// is written: the guards below need the access the existing member holds,
	// and a guard that runs inside the write is a guard that has already let
	// half the change happen.
	// Known gap: this lookup spans the whole company realm, so attaching an
	// existing account tells the caller that address already recruits somewhere
	// on the platform, and attaches that person without their consent. The fix
	// is an invitation they accept — `MembershipInvited` exists and
	// resolveCompanyMembership already refuses it — which needs email delivery
	// the platform does not have. Documented in docs/contracts.md rather than
	// papered over here.
	accountID := ""
	if account, findErr := s.store.FindAccountByEmail(ctx, domain.RealmCompany, email); findErr == nil {
		accountID = account.ID
	} else if !errors.Is(findErr, domain.ErrAccountNotFound) {
		return nil, findErr
	}

	var existing *store.RosterMember
	if accountID != "" {
		// FindAnyRosterMember, not FindRosterMember: a removed membership still
		// occupies the (account, company) key, and it is the one this path most
		// needs to see.
		member, memErr := s.store.FindAnyRosterMember(ctx, actor.CompanyID, accountID)
		switch {
		case memErr == nil:
			existing = &member
		case errors.Is(memErr, domain.ErrMemberNotFound):
		default:
			return nil, memErr
		}
	}

	if existing != nil {
		if existing.Status == string(domain.MembershipActive) {
			return nil, domain.Invalid("email", "This person is already a member of the company.")
		}
		if err := s.guardReinstatement(ctx, actor, *existing, roles); err != nil {
			return nil, err
		}
	}

	err = s.store.InTx(ctx, func(tx pgx.Tx) error {
		if accountID == "" {
			if problems := s.policy.Validate(in.Password); len(problems) > 0 {
				return &auth.PasswordPolicyError{Problems: problems}
			}
			hash, hashErr := auth.HashPassword(in.Password)
			if hashErr != nil {
				return hashErr
			}
			created, createErr := s.store.CreateAccount(ctx, tx, store.CreateAccountInput{
				Realm:        domain.RealmCompany,
				Email:        email,
				PasswordHash: hash,
				FullName:     strings.TrimSpace(in.FullName),
				Status:       domain.AccountActive,
			})
			if createErr != nil {
				return createErr
			}
			accountID = created.ID
			result.AccountCreated = true
		}

		membershipID := ""
		if existing != nil {
			// Inside the transaction, so a reinstatement that fails to land its
			// roles does not leave somebody active holding whatever they held
			// before they were removed.
			if err := s.store.SetMembershipStatus(ctx, tx,
				existing.MembershipID, domain.MembershipActive); err != nil {
				return err
			}
			membershipID = existing.MembershipID
			result.Reactivated = true
		} else {
			// Ownership is never granted here: it decides who can still sign in
			// while the subscription has lapsed, and it is set once, when the
			// tenant is provisioned.
			membership, memErr := s.store.CreateMembership(ctx, tx, store.CreateMembershipInput{
				AccountID: accountID,
				CompanyID: actor.CompanyID,
				Status:    domain.MembershipActive,
				IsOwner:   false,
				JobTitle:  strings.TrimSpace(in.JobTitle),
				InvitedBy: actor.AccountID,
			})
			if memErr != nil {
				return memErr
			}
			membershipID = membership.ID
		}

		return s.store.ReplaceMembershipRoles(ctx, tx, membershipID,
			roleIDs(roles), primaryRoleID, actor.AccountID)
	})
	if err != nil {
		return nil, err
	}

	member, err := s.store.FindRosterMember(ctx, actor.CompanyID, accountID)
	if err != nil {
		return nil, err
	}
	result.Member = member

	s.logger.Info("company member added",
		slog.String("company_id", actor.CompanyID),
		slog.String("account_id", accountID),
		slog.Bool("account_created", result.AccountCreated),
		slog.Bool("reactivated", result.Reactivated),
	)

	return result, nil
}

// UpdateMemberInput changes what a company may say about one of its members.
//
// The name is deliberately absent. It lives on the account, which is shared by
// every company that person recruits for, so setting it here renamed them in
// tenants this actor has nothing to do with. Their own account settings are
// where a person's name is changed.
type UpdateMemberInput struct {
	JobTitle string
}

// UpdateMember changes a member's job title.
func (s *Service) UpdateMember(ctx context.Context, actor Actor, accountID string, in UpdateMemberInput) (*store.RosterMember, error) {
	target, err := s.store.FindRosterMember(ctx, actor.CompanyID, accountID)
	if err != nil {
		return nil, err
	}
	if err := GuardTargetIsManageable(target); err != nil {
		return nil, err
	}
	// A job title is not access, but it is still a change made *to* somebody,
	// and every other such change answers to the authority rule. A hiring
	// administrator retitling the owner is not a security breach; it is also
	// not their call.
	if err := GuardAuthorityOver(actor, target.Permissions, target.FullName); err != nil {
		return nil, err
	}

	if err := s.store.UpdateMembershipJobTitle(ctx,
		target.MembershipID, strings.TrimSpace(in.JobTitle)); err != nil {
		return nil, err
	}

	updated, err := s.store.FindRosterMember(ctx, actor.CompanyID, accountID)
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

// AssignRoles replaces the roles a member holds.
func (s *Service) AssignRoles(ctx context.Context, actor Actor, accountID string, wantRoleIDs []string, primaryRoleID string) (*store.RosterMember, error) {
	if err := GuardSelf(actor, accountID, "your own roles must be changed by another administrator"); err != nil {
		return nil, err
	}

	target, err := s.store.FindRosterMember(ctx, actor.CompanyID, accountID)
	if err != nil {
		return nil, err
	}
	if err := GuardTargetIsManageable(target); err != nil {
		return nil, err
	}
	if err := GuardAuthorityOver(actor, target.Permissions, target.FullName); err != nil {
		return nil, err
	}

	roles, err := s.resolveRoles(ctx, actor.CompanyID, wantRoleIDs)
	if err != nil {
		return nil, err
	}
	if err := GuardDelegation(actor, grantedBy(roles), "this role assignment"); err != nil {
		return nil, err
	}

	resolvedPrimary, err := resolvePrimary(roles, primaryRoleID)
	if err != nil {
		return nil, err
	}

	if err := s.guardAdministratorRemains(ctx, actor.CompanyID, target, holdsSuperAdmin(roles)); err != nil {
		return nil, err
	}

	if err := s.store.InTx(ctx, func(tx pgx.Tx) error {
		return s.store.ReplaceMembershipRoles(ctx, tx, target.MembershipID,
			roleIDs(roles), resolvedPrimary, actor.AccountID)
	}); err != nil {
		return nil, err
	}

	updated, err := s.store.FindRosterMember(ctx, actor.CompanyID, accountID)
	if err != nil {
		return nil, err
	}

	// A widened role takes effect at the next refresh, within the access
	// token's fifteen minutes, and costs nobody their session. A narrowed one
	// cannot wait: the permission was taken away for a reason, and a token
	// still carrying it is exactly the window that reason was about.
	if lost := rbac.Subset(updated.Permissions, target.Permissions); len(lost) > 0 {
		s.revokeSessions(ctx, accountID, actor.CompanyID, "roles_changed")
	}

	return &updated, nil
}

// SetMemberActive suspends or restores a member's access.
func (s *Service) SetMemberActive(ctx context.Context, actor Actor, accountID string, active bool) (*store.RosterMember, error) {
	action := "suspended"
	if active {
		action = "restored"
	}
	if err := GuardSelf(actor, accountID, "you cannot change your own access to the company"); err != nil {
		return nil, err
	}

	target, err := s.store.FindRosterMember(ctx, actor.CompanyID, accountID)
	if err != nil {
		return nil, err
	}
	if err := GuardTargetIsManageable(target); err != nil {
		return nil, err
	}
	if err := GuardAuthorityOver(actor, target.Permissions, target.FullName); err != nil {
		return nil, err
	}

	status := domain.MembershipSuspended
	if active {
		status = domain.MembershipActive
	} else {
		if err := s.guardLastOwner(ctx, actor.CompanyID, target, action); err != nil {
			return nil, err
		}
		if err := s.guardAdministratorRemains(ctx, actor.CompanyID, target, false); err != nil {
			return nil, err
		}
	}

	if err := s.store.SetMembershipStatus(ctx, nil, target.MembershipID, status); err != nil {
		return nil, err
	}

	if !active {
		s.revokeSessions(ctx, accountID, actor.CompanyID, "membership_suspended")
		s.publish(ctx, events.SubjectUserDeactivated, map[string]any{
			"companyId": actor.CompanyID,
			"accountId": accountID,
			"email":     target.Email,
			"reason":    "suspended_by_administrator",
		}, events.PublishOptions{CompanyID: actor.CompanyID, ActorID: actor.AccountID})
	}

	updated, err := s.store.FindRosterMember(ctx, actor.CompanyID, accountID)
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

// RemoveMember ends someone's membership of the company.
//
// The account itself survives: it may be the login somebody uses at another
// company, and deleting it here would take that access with it.
func (s *Service) RemoveMember(ctx context.Context, actor Actor, accountID string) error {
	if err := GuardSelf(actor, accountID, "you cannot remove yourself from the company"); err != nil {
		return err
	}

	target, err := s.store.FindRosterMember(ctx, actor.CompanyID, accountID)
	if err != nil {
		return err
	}
	if err := GuardTargetIsManageable(target); err != nil {
		return err
	}
	if err := GuardAuthorityOver(actor, target.Permissions, target.FullName); err != nil {
		return err
	}
	if err := s.guardLastOwner(ctx, actor.CompanyID, target, "removed"); err != nil {
		return err
	}
	if err := s.guardAdministratorRemains(ctx, actor.CompanyID, target, false); err != nil {
		return err
	}

	if err := s.store.SetMembershipStatus(ctx, nil, target.MembershipID, domain.MembershipRemoved); err != nil {
		return err
	}

	s.revokeSessions(ctx, accountID, actor.CompanyID, "membership_removed")
	s.publish(ctx, events.SubjectUserDeactivated, map[string]any{
		"companyId": actor.CompanyID,
		"accountId": accountID,
		"email":     target.Email,
		"reason":    "removed_by_administrator",
	}, events.PublishOptions{CompanyID: actor.CompanyID, ActorID: actor.AccountID})

	return nil
}

/* -------------------------------------------------------------------------- */
/* Roles                                                                      */
/* -------------------------------------------------------------------------- */

// RoleView is a role with the number of people holding it.
type RoleView struct {
	domain.Role
	MemberCount int
}

// ListRoles returns the company's roles.
func (s *Service) ListRoles(ctx context.Context, actor Actor) ([]RoleView, error) {
	roles, err := s.store.ListRoles(ctx, actor.CompanyID)
	if err != nil {
		return nil, err
	}

	counts, err := s.store.CountRoleAssignmentsByRole(ctx, actor.CompanyID)
	if err != nil {
		return nil, err
	}

	views := make([]RoleView, 0, len(roles))
	for _, role := range roles {
		// A super-admin role's stored list is refreshed from the registry at
		// every seed; reporting the resolved set keeps the matrix showing what
		// a token will actually carry.
		if role.IsSuperAdmin {
			role.Permissions = rbac.PermissionsForScope(rbac.ScopeCompany)
		}
		views = append(views, RoleView{Role: role, MemberCount: counts[role.ID]})
	}
	return views, nil
}

// CreateRoleInput describes a new custom role.
type CreateRoleInput struct {
	Name        string
	Slug        string
	Description string
	Badge       string
	Permissions []string
}

// CreateRole adds a custom role to the company.
func (s *Service) CreateRole(ctx context.Context, actor Actor, in CreateRoleInput) (*RoleView, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, domain.Invalid("name", "A role name is required.")
	}

	slug := strings.TrimSpace(in.Slug)
	if slug == "" {
		slug = slugify(name)
	}
	if slug == "" {
		return nil, domain.Invalid("slug", "A role identifier is required.")
	}

	if err := GuardDelegation(actor, in.Permissions, "this role"); err != nil {
		return nil, err
	}

	role, err := s.store.CreateRole(ctx, store.CreateRoleInput{
		CompanyID:   actor.CompanyID,
		Realm:       domain.RealmCompany,
		Slug:        slug,
		Name:        name,
		Description: strings.TrimSpace(in.Description),
		Badge:       strings.TrimSpace(in.Badge),
		Permissions: in.Permissions,
	})
	if err != nil {
		return nil, err
	}

	return &RoleView{Role: role}, nil
}

// UpdateRoleInput changes a role. Nil fields are left as they are.
type UpdateRoleInput struct {
	Name        *string
	Description *string
	Badge       *string
	Permissions *[]string
}

// UpdateRole changes a role's details, its permissions, or both.
func (s *Service) UpdateRole(ctx context.Context, actor Actor, roleID string, in UpdateRoleInput) (*RoleView, error) {
	role, err := s.store.FindRoleByID(ctx, actor.CompanyID, roleID)
	if err != nil {
		return nil, err
	}

	// You may only edit a role whose access is contained within your own.
	// Without this, an administrator could take a role more powerful than
	// themselves and strip it down — which is not an escalation, but is a way
	// to disable people they have no authority over.
	if err := GuardAuthorityOver(actor, role.Permissions, role.Name); err != nil {
		return nil, err
	}

	if in.Permissions != nil {
		if err := GuardRolePermissionsMutable(role); err != nil {
			return nil, err
		}
		if err := GuardDelegation(actor, *in.Permissions, "this role"); err != nil {
			return nil, err
		}
	}

	name := role.Name
	if in.Name != nil && strings.TrimSpace(*in.Name) != "" {
		name = strings.TrimSpace(*in.Name)
	}
	description := role.Description
	if in.Description != nil {
		description = strings.TrimSpace(*in.Description)
	}
	badge := role.Badge
	if in.Badge != nil {
		badge = strings.TrimSpace(*in.Badge)
	}

	if err := s.store.UpdateRoleDetails(ctx, role.ID, name, description, badge); err != nil {
		return nil, err
	}

	if in.Permissions != nil {
		if err := s.store.UpdateRolePermissions(ctx, role.ID, rbac.ScopeCompany, *in.Permissions); err != nil {
			return nil, err
		}
		// Everyone holding this role just had their access rewritten. The
		// tokens they are carrying still describe the old role.
		if lost := rbac.Subset(rbac.Sanitize(rbac.ScopeCompany, *in.Permissions), role.Permissions); len(lost) > 0 {
			s.revokeRoleHolderSessions(ctx, actor.CompanyID, role.ID)
		}
	}

	updated, err := s.store.FindRoleByID(ctx, actor.CompanyID, role.ID)
	if err != nil {
		return nil, err
	}

	counts, err := s.store.CountRoleAssignmentsByRole(ctx, actor.CompanyID)
	if err != nil {
		return nil, err
	}
	return &RoleView{Role: updated, MemberCount: counts[updated.ID]}, nil
}

// DeleteRole removes a custom role.
func (s *Service) DeleteRole(ctx context.Context, actor Actor, roleID string) error {
	role, err := s.store.FindRoleByID(ctx, actor.CompanyID, roleID)
	if err != nil {
		return err
	}
	if err := GuardRoleDeletable(role); err != nil {
		return err
	}
	if err := GuardAuthorityOver(actor, role.Permissions, role.Name); err != nil {
		return err
	}

	// The store refuses while the role is still assigned, so a delete can never
	// silently leave somebody with no roles at all.
	return s.store.DeleteRole(ctx, role.ID)
}

/* -------------------------------------------------------------------------- */
/* Internals                                                                  */
/* -------------------------------------------------------------------------- */

// resolveRoles loads the requested roles and refuses anything outside the
// tenant.
func (s *Service) resolveRoles(ctx context.Context, companyID string, ids []string) ([]domain.Role, error) {
	seen := map[string]struct{}{}
	roles := make([]domain.Role, 0, len(ids))

	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}

		// FindRoleByID compares the role's company to this one, so an
		// identifier copied from another tenant reads as "not found" rather
		// than as a role this company may assign.
		role, err := s.store.FindRoleByID(ctx, companyID, id)
		if err != nil {
			return nil, err
		}
		roles = append(roles, role)
	}

	if len(roles) == 0 {
		return nil, domain.Invalid("roleIds", "At least one role must be assigned.")
	}
	return roles, nil
}

// guardReinstatement applies to a returning member every rule that would apply
// if the same change were made through the endpoints that normally make it.
//
// Reinstating somebody does two things at once: it reactivates a membership,
// which is SetMemberActive's job, and it replaces their roles, which is
// AssignRoles'. Each of those refuses an actor who has no authority over the
// target, refuses an actor acting on themselves, and requires its own
// capability. Doing both through `recruiters.create` alone would be a way past
// all of it — and the target here is, by definition, somebody a colleague
// already suspended or removed.
func (s *Service) guardReinstatement(ctx context.Context, actor Actor, existing store.RosterMember, roles []domain.Role) error {
	if err := GuardSelf(actor, existing.AccountID,
		"your own access to the company must be restored by another administrator"); err != nil {
		return err
	}
	if err := GuardAuthorityOver(actor, existing.Permissions, existing.FullName); err != nil {
		return err
	}
	if err := GuardCapability(actor, "recruiters.manage_status",
		"restoring somebody's access"); err != nil {
		return err
	}

	// Only when the roles actually change. Re-adding a removed member with the
	// roles they already had is a restore, not a reassignment, and should not
	// demand a capability it does not use.
	if !sameRoleSet(existing.RoleIDs, roleIDs(roles)) {
		if err := GuardCapability(actor, "recruiters.assign_roles",
			"changing somebody's roles"); err != nil {
			return err
		}
	}

	return s.guardAdministratorRemains(ctx, actor.CompanyID, existing, holdsSuperAdmin(roles))
}

// sameRoleSet compares two role id sets ignoring order.
func sameRoleSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	index := make(map[string]struct{}, len(a))
	for _, id := range a {
		index[id] = struct{}{}
	}
	for _, id := range b {
		if _, ok := index[id]; !ok {
			return false
		}
	}
	return true
}

// guardLastOwner refuses a change that would leave the company ownerless.
func (s *Service) guardLastOwner(ctx context.Context, companyID string, target store.RosterMember, action string) error {
	remaining, err := s.store.CountActiveOwners(ctx, companyID, target.MembershipID)
	if err != nil {
		return err
	}
	return GuardLastOwner(target, remaining, action)
}

// guardAdministratorRemains refuses a change that would leave the company with
// nobody holding unrestricted access.
//
// Distinct from the owner check: ownership decides who may sign in while the
// subscription has lapsed, while a super-admin role decides who may administer
// anything at all. A company can lose the second while keeping the first — an
// owner stripped of their role can still get in, and can then do nothing.
func (s *Service) guardAdministratorRemains(ctx context.Context, companyID string, target store.RosterMember, targetKeepsSuperAdmin bool) error {
	if !target.IsSuperAdmin || targetKeepsSuperAdmin {
		return nil
	}

	roster, err := s.store.ListCompanyRoster(ctx, companyID)
	if err != nil {
		return err
	}
	for _, member := range roster {
		if member.AccountID == target.AccountID {
			continue
		}
		if member.IsSuperAdmin && member.Status == string(domain.MembershipActive) {
			return nil
		}
	}

	return fmt.Errorf("%w: %s is the only person with unrestricted access, so that access cannot be removed",
		domain.ErrLastOwner, target.FullName)
}

func (s *Service) revokeSessions(ctx context.Context, accountID, companyID, reason string) {
	revoked, err := s.store.RevokeMembershipSessions(ctx, accountID, companyID, reason)
	if err != nil {
		s.logger.Error("failed to revoke sessions after an access change",
			slog.String("account_id", accountID),
			slog.String("company_id", companyID),
			slog.Any("error", err))
		return
	}
	if revoked > 0 {
		s.publish(ctx, events.SubjectSessionRevoked, map[string]any{
			"companyId": companyID,
			"accountId": accountID,
			"reason":    reason,
			"sessions":  revoked,
		}, events.PublishOptions{CompanyID: companyID, ActorID: accountID})
	}
}

// revokeRoleHolderSessions ends the sessions of everyone holding a role whose
// permissions were just narrowed.
func (s *Service) revokeRoleHolderSessions(ctx context.Context, companyID, roleID string) {
	roster, err := s.store.ListCompanyRoster(ctx, companyID)
	if err != nil {
		s.logger.Error("failed to read roster after a role change",
			slog.String("company_id", companyID), slog.Any("error", err))
		return
	}

	for _, member := range roster {
		for _, held := range member.RoleIDs {
			if held == roleID {
				s.revokeSessions(ctx, member.AccountID, companyID, "role_permissions_changed")
				break
			}
		}
	}
}

func (s *Service) publish(ctx context.Context, subject string, payload any, opts events.PublishOptions) {
	if s.bus == nil {
		return
	}
	if err := s.bus.Publish(context.WithoutCancel(ctx), subject, payload, opts); err != nil {
		s.logger.Error("failed to publish event",
			slog.String("subject", subject), slog.Any("error", err))
	}
}

// grantedBy is what a set of roles actually confers, with a super-admin role
// expanded exactly as the token issuer expands it.
func grantedBy(roles []domain.Role) []string {
	unique := map[string]struct{}{}
	for _, role := range roles {
		keys := role.Permissions
		if role.IsSuperAdmin {
			keys = rbac.PermissionsForScope(rbac.ScopeCompany)
		}
		for _, key := range keys {
			unique[key] = struct{}{}
		}
	}

	out := make([]string, 0, len(unique))
	for key := range unique {
		out = append(out, key)
	}
	return rbac.Sanitize(rbac.ScopeCompany, out)
}

func holdsSuperAdmin(roles []domain.Role) bool {
	for _, role := range roles {
		if role.IsSuperAdmin {
			return true
		}
	}
	return false
}

func roleIDs(roles []domain.Role) []string {
	ids := make([]string, 0, len(roles))
	for _, role := range roles {
		ids = append(ids, role.ID)
	}
	return ids
}

// resolvePrimary picks the role shown as somebody's title, refusing a primary
// that is not among the roles being assigned.
func resolvePrimary(roles []domain.Role, want string) (string, error) {
	want = strings.TrimSpace(want)
	if want == "" {
		return roles[0].ID, nil
	}
	for _, role := range roles {
		if role.ID == want {
			return want, nil
		}
	}
	return "", domain.Invalid("primaryRoleId", "The primary role must be one of the assigned roles.")
}

// slugify derives a role identifier from its name.
func slugify(name string) string {
	var b strings.Builder
	lastDash := true // leading dashes are dropped

	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case !lastDash:
			b.WriteByte('_')
			lastDash = true
		}
	}

	return strings.Trim(b.String(), "_")
}

func looksLikeEmail(value string) bool {
	at := strings.Index(value, "@")
	dot := strings.LastIndex(value, ".")
	return at > 0 && dot > at+1 && dot < len(value)-1 && !strings.Contains(value, " ")
}
