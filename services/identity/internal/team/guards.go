// Package team is the company-facing surface over a tenant's own people and
// roles.
//
// It exists because the roster and the role set are identity's data, and the
// only way to reach them used to be `/internal/companies/{id}/members` — a
// route guarded by the service-to-service secret and deliberately not routed by
// the gateway. A customer could therefore not manage their own team at all.
//
// The rules that decide who may change whose access live in guards.go as pure
// functions over permission lists, so they can be tested exhaustively without a
// database and so a reader can see the whole authorization model on one screen.
package team

import (
	"fmt"
	"strings"

	"github.com/karmarat/platform/services/identity/internal/domain"
	"github.com/karmarat/platform/services/identity/internal/rbac"
	"github.com/karmarat/platform/services/identity/internal/store"
)

// Actor is the person making a change to a company's team.
//
// Permissions come from the verified access token, never from the request:
// that is the difference between an authorization model and a suggestion.
type Actor struct {
	AccountID   string
	CompanyID   string
	Permissions []string
}

// EscalationError reports a change refused because the actor does not hold
// everything it would have granted.
//
// It names the specific permissions rather than answering "forbidden", because
// an administrator who cannot see which permission blocked them will try again
// with the same one.
type EscalationError struct {
	// Missing are the permissions the actor lacks.
	Missing []string
	// Subject describes what was being changed, for the message.
	Subject string
}

func (e *EscalationError) Error() string {
	subject := e.Subject
	if subject == "" {
		subject = "this change"
	}
	return fmt.Sprintf("you cannot grant permissions you do not hold yourself — %s requires %s",
		subject, strings.Join(e.Missing, ", "))
}

// Unwrap lets callers match the sentinel while still reading the detail.
func (e *EscalationError) Unwrap() error { return domain.ErrPrivilegeEscalation }

/* -------------------------------------------------------------------------- */
/* The rules                                                                  */
/* -------------------------------------------------------------------------- */

// GuardDelegation refuses to hand out a permission the actor does not hold.
//
// This is the classic privilege-escalation path closed: without it, anyone with
// `company_roles.create` could mint a role holding every permission in the
// scope and assign it to themselves.
func GuardDelegation(actor Actor, want []string, subject string) error {
	missing := rbac.Subset(actor.Permissions, rbac.Sanitize(rbac.ScopeCompany, want))
	if len(missing) == 0 {
		return nil
	}
	return &EscalationError{Missing: missing, Subject: subject}
}

// GuardAuthorityOver refuses to let an actor change someone more privileged
// than themselves.
//
// Delegation alone is not enough. A hiring administrator who may assign the
// "Recruiter" role could otherwise assign it *to the owner*, replacing the
// owner's unrestricted access with a recruiter's — a demotion of someone they
// have no authority over, carried out entirely with permissions they hold.
// The rule is therefore symmetric: you may only act on a person whose access is
// contained within your own.
func GuardAuthorityOver(actor Actor, target []string, subject string) error {
	missing := rbac.Subset(actor.Permissions, rbac.Sanitize(rbac.ScopeCompany, target))
	if len(missing) == 0 {
		return nil
	}
	return &EscalationError{
		Missing: missing,
		Subject: subject + " holds access you do not have, so changing it",
	}
}

// GuardSelf refuses a change an actor makes to their own access.
//
// Separate from the delegation rule, and not redundant with it: an actor always
// holds exactly their own permissions, so every self-change passes delegation.
// What this stops is the administrator who suspends or deletes themselves and
// leaves the company one person short of being able to undo it, and the subtler
// case of someone quietly widening their own role.
func GuardSelf(actor Actor, targetAccountID, action string) error {
	if actor.AccountID != targetAccountID {
		return nil
	}
	return fmt.Errorf("%w: %s", domain.ErrSelfModification, action)
}

// GuardRolePermissionsMutable refuses to re-permission a role that owns its own
// permission list.
//
// A super-admin role always expands to the whole scope and is rewritten from
// the registry every time roles are seeded, so an edit here would be reverted
// the next time the tenant is provisioned. Refusing is more honest than
// accepting a change that silently disappears.
func GuardRolePermissionsMutable(role domain.Role) error {
	if !role.IsSuperAdmin {
		return nil
	}
	return fmt.Errorf("%w: %s holds every permission in this company by definition",
		domain.ErrRoleImmutable, role.Name)
}

// GuardRoleDeletable refuses to delete a role the platform seeds.
//
// Seeding is idempotent through `ON CONFLICT DO NOTHING`, so a deleted system
// role never comes back — a company that removes "Recruiter" has permanently
// changed what their account contains, with nothing to tell them so. Custom
// roles are theirs to delete.
func GuardRoleDeletable(role domain.Role) error {
	if !role.IsSystem {
		return nil
	}
	return fmt.Errorf("%w: %s is a built-in role", domain.ErrRoleImmutable, role.Name)
}

// GuardLastOwner refuses the change that would leave a company with no active
// owner.
//
// An owner is the only member who can still sign in while the subscription has
// lapsed, so a company with none is a company that cannot reach its own billing
// screen to fix the thing keeping everyone else out.
func GuardLastOwner(target store.RosterMember, remainingOwners int, action string) error {
	if !target.IsOwner || remainingOwners > 0 {
		return nil
	}
	return fmt.Errorf("%w: %s is the only owner, so they cannot be %s",
		domain.ErrLastOwner, target.FullName, action)
}

// GuardCapability refuses an action the actor holds no permission for.
//
// Route-level permission checks cover the common case, but one endpoint can do
// the work of another: adding an address that already has a membership
// reinstates and re-roles that person, which is what `recruiters.manage_status`
// and `recruiters.assign_roles` exist to gate. Without this, `recruiters.create`
// alone would be a way around both.
func GuardCapability(actor Actor, permission, action string) error {
	for _, held := range actor.Permissions {
		if held == permission {
			return nil
		}
	}
	return &EscalationError{Missing: []string{permission}, Subject: action}
}

// GuardTargetIsManageable refuses to act on a membership that is already gone.
func GuardTargetIsManageable(target store.RosterMember) error {
	if target.Status != string(domain.MembershipRemoved) {
		return nil
	}
	return domain.ErrNoMembership
}
