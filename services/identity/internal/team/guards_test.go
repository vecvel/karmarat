package team

import (
	"errors"
	"strings"
	"testing"

	"github.com/karmarat/platform/services/identity/internal/domain"
	"github.com/karmarat/platform/services/identity/internal/rbac"
	"github.com/karmarat/platform/services/identity/internal/store"
)

// A hiring administrator: real permissions, taken from the seeded role rather
// than invented, so a registry change that removes one of these breaks the test
// instead of leaving it asserting against a key nobody serves.
func hiringAdmin() Actor {
	for _, role := range rbac.CompanyRoles() {
		if role.Slug == "hiring_admin" {
			return Actor{
				AccountID:   "acc_admin",
				CompanyID:   "cmp_1",
				Permissions: role.Resolve(rbac.ScopeCompany),
			}
		}
	}
	panic("rbac: the hiring_admin role is no longer seeded")
}

func owner() Actor {
	return Actor{
		AccountID:   "acc_owner",
		CompanyID:   "cmp_1",
		Permissions: rbac.PermissionsForScope(rbac.ScopeCompany),
	}
}

func TestGuardDelegationAllowsWhatTheActorHolds(t *testing.T) {
	actor := hiringAdmin()

	if err := GuardDelegation(actor, []string{"jobs.read", "jobs.create"}, "this role"); err != nil {
		t.Fatalf("expected a hiring admin to be able to grant jobs.read and jobs.create: %v", err)
	}
}

func TestGuardDelegationRefusesWhatTheActorDoesNotHold(t *testing.T) {
	actor := hiringAdmin()

	// billing.manage is an owner capability: the seeded hiring administrator
	// does not hold it, so it must not be grantable by one.
	if actor.holds("billing.manage") {
		t.Fatal("test premise broken: the hiring admin now holds billing.manage")
	}

	err := GuardDelegation(actor, []string{"jobs.read", "billing.manage"}, "this role")
	if err == nil {
		t.Fatal("expected the grant to be refused")
	}
	if !errors.Is(err, domain.ErrPrivilegeEscalation) {
		t.Fatalf("expected a privilege-escalation error, got %v", err)
	}

	var escalation *EscalationError
	if !errors.As(err, &escalation) {
		t.Fatalf("expected an EscalationError, got %T", err)
	}
	if len(escalation.Missing) != 1 || escalation.Missing[0] != "billing.manage" {
		t.Fatalf("expected the message to name billing.manage, got %v", escalation.Missing)
	}
	if !strings.Contains(err.Error(), "billing.manage") {
		t.Fatalf("expected the message to name the blocking permission, got %q", err.Error())
	}
}

// The escalation path this rule exists to close: mint a role holding everything
// in the scope, then wear it.
func TestGuardDelegationRefusesMintingAnUnrestrictedRole(t *testing.T) {
	actor := hiringAdmin()

	err := GuardDelegation(actor, rbac.PermissionsForScope(rbac.ScopeCompany), "this role")
	if !errors.Is(err, domain.ErrPrivilegeEscalation) {
		t.Fatalf("expected a hiring admin to be refused the full company scope, got %v", err)
	}
}

func TestGuardDelegationIgnoresUnknownAndOutOfScopeKeys(t *testing.T) {
	actor := hiringAdmin()

	// A caller can post anything. A key the registry does not define grants
	// nothing, so it must not be treated as something the actor is missing —
	// otherwise a typo reads as an escalation attempt.
	if err := GuardDelegation(actor,
		[]string{"jobs.read", "not_a_feature.invented", "platform_companies.approve"}, "this role"); err != nil {
		t.Fatalf("expected unknown and out-of-scope keys to be dropped, got %v", err)
	}
}

func TestGuardAuthorityOverRefusesActingOnSomeoneMorePrivileged(t *testing.T) {
	actor := hiringAdmin()
	ownerPermissions := rbac.PermissionsForScope(rbac.ScopeCompany)

	err := GuardAuthorityOver(actor, ownerPermissions, "Dana")
	if !errors.Is(err, domain.ErrPrivilegeEscalation) {
		t.Fatalf("expected a hiring admin to be refused authority over an owner, got %v", err)
	}
	if !strings.Contains(err.Error(), "Dana") {
		t.Fatalf("expected the message to name the person, got %q", err.Error())
	}
}

func TestGuardAuthorityOverAllowsActingOnSomeoneLesser(t *testing.T) {
	if err := GuardAuthorityOver(hiringAdmin(), []string{"jobs.read", "applications.read"}, "Sam"); err != nil {
		t.Fatalf("expected a hiring admin to have authority over a recruiter: %v", err)
	}
	if err := GuardAuthorityOver(owner(), rbac.PermissionsForScope(rbac.ScopeCompany), "Dana"); err != nil {
		t.Fatalf("expected an owner to have authority over another owner: %v", err)
	}
}

func TestGuardSelfRefusesOnlyTheActorsOwnRecord(t *testing.T) {
	actor := hiringAdmin()

	if err := GuardSelf(actor, "acc_someone_else", "changing roles"); err != nil {
		t.Fatalf("expected another account to be allowed: %v", err)
	}

	err := GuardSelf(actor, actor.AccountID, "changing your own roles")
	if !errors.Is(err, domain.ErrSelfModification) {
		t.Fatalf("expected a self-modification error, got %v", err)
	}
}

func TestGuardRolePermissionsMutableProtectsOnlyTheSuperAdminRole(t *testing.T) {
	if err := GuardRolePermissionsMutable(domain.Role{Name: "Recruiter", IsSystem: true}); err != nil {
		t.Fatalf("expected a seeded non-super-admin role to be re-permissionable: %v", err)
	}

	err := GuardRolePermissionsMutable(domain.Role{Name: "Owner", IsSuperAdmin: true, IsSystem: true})
	if !errors.Is(err, domain.ErrRoleImmutable) {
		t.Fatalf("expected the owner role's permissions to be immutable, got %v", err)
	}
}

func TestGuardRoleDeletableProtectsSeededRoles(t *testing.T) {
	if err := GuardRoleDeletable(domain.Role{Name: "Night shift", IsSystem: false}); err != nil {
		t.Fatalf("expected a custom role to be deletable: %v", err)
	}

	err := GuardRoleDeletable(domain.Role{Name: "Interviewer", IsSystem: true})
	if !errors.Is(err, domain.ErrRoleImmutable) {
		t.Fatalf("expected a seeded role to be undeletable, got %v", err)
	}
}

func TestGuardLastOwner(t *testing.T) {
	soleOwner := store.RosterMember{FullName: "Dana", IsOwner: true}

	if err := GuardLastOwner(soleOwner, 1, "suspended"); err != nil {
		t.Fatalf("expected the change to be allowed while another owner remains: %v", err)
	}
	if err := GuardLastOwner(store.RosterMember{FullName: "Sam"}, 0, "suspended"); err != nil {
		t.Fatalf("expected a non-owner to be unaffected by the owner rule: %v", err)
	}

	err := GuardLastOwner(soleOwner, 0, "suspended")
	if !errors.Is(err, domain.ErrLastOwner) {
		t.Fatalf("expected the last owner to be protected, got %v", err)
	}
	if !strings.Contains(err.Error(), "Dana") {
		t.Fatalf("expected the message to name the owner, got %q", err.Error())
	}
}

func TestGuardTargetIsManageableRejectsARemovedMembership(t *testing.T) {
	if err := GuardTargetIsManageable(store.RosterMember{Status: string(domain.MembershipSuspended)}); err != nil {
		t.Fatalf("expected a suspended member to still be manageable: %v", err)
	}

	err := GuardTargetIsManageable(store.RosterMember{Status: string(domain.MembershipRemoved)})
	if !errors.Is(err, domain.ErrNoMembership) {
		t.Fatalf("expected a removed membership to read as no membership, got %v", err)
	}
}

func TestResolvePrimaryRefusesARoleThatIsNotBeingAssigned(t *testing.T) {
	roles := []domain.Role{{ID: "role_a"}, {ID: "role_b"}}

	got, err := resolvePrimary(roles, "")
	if err != nil || got != "role_a" {
		t.Fatalf("expected the first role to be the default primary, got %q (%v)", got, err)
	}
	if got, err := resolvePrimary(roles, "role_b"); err != nil || got != "role_b" {
		t.Fatalf("expected role_b to be accepted, got %q (%v)", got, err)
	}

	// Otherwise a member's displayed title could name a role they do not hold.
	if _, err := resolvePrimary(roles, "role_c"); err == nil {
		t.Fatal("expected a primary outside the assigned set to be refused")
	}
}

func TestGrantedByExpandsASuperAdminRole(t *testing.T) {
	granted := grantedBy([]domain.Role{
		{ID: "role_owner", IsSuperAdmin: true, Permissions: nil},
	})

	// The stored list is empty for a super-admin role; what it actually confers
	// is the whole scope, and the delegation check must see that.
	if len(granted) != len(rbac.PermissionsForScope(rbac.ScopeCompany)) {
		t.Fatalf("expected a super-admin role to expand to the whole company scope, got %d keys", len(granted))
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Night Shift Recruiter": "night_shift_recruiter",
		"  Lead  Sourcer  ":     "lead_sourcer",
		"Tier-1 Support":        "tier_1_support",
		"!!!":                   "",
	}

	for name, want := range cases {
		if got := slugify(name); got != want {
			t.Errorf("slugify(%q) = %q, want %q", name, got, want)
		}
	}
}

// holds is a test helper: the production path never asks this question directly,
// it asks rbac.Subset.
func (a Actor) holds(permission string) bool {
	for _, key := range a.Permissions {
		if key == permission {
			return true
		}
	}
	return false
}

func TestGuardCapabilityGatesAnActionTheRouteDidNotRequire(t *testing.T) {
	actor := hiringAdmin()

	if err := GuardCapability(actor, "recruiters.manage_status", "restoring access"); err != nil {
		t.Fatalf("expected a hiring admin to hold recruiters.manage_status: %v", err)
	}

	// The seeded hiring administrator does not author roles; only an owner does.
	err := GuardCapability(actor, "company_roles.create", "creating a role")
	if !errors.Is(err, domain.ErrPrivilegeEscalation) {
		t.Fatalf("expected the missing capability to be refused, got %v", err)
	}
	if !strings.Contains(err.Error(), "company_roles.create") {
		t.Fatalf("expected the message to name the permission, got %q", err.Error())
	}
}

// The reinstatement hole this exists to close: an actor who may create members
// but not manage their status must not be able to un-suspend somebody by
// "adding" them again.
func TestGuardCapabilityRefusesTheReinstatementShortcut(t *testing.T) {
	// A custom role a company might plausibly define: onboard people, nothing else.
	coordinator := Actor{
		AccountID:   "acc_sam",
		CompanyID:   "cmp_1",
		Permissions: []string{"recruiters.create", "recruiters.read"},
	}

	if err := GuardCapability(coordinator, "recruiters.manage_status", "restoring access"); err == nil {
		t.Fatal("expected somebody holding only recruiters.create to be refused a status change")
	}
	if err := GuardCapability(coordinator, "recruiters.assign_roles", "changing roles"); err == nil {
		t.Fatal("expected somebody holding only recruiters.create to be refused a role change")
	}
}

func TestSameRoleSetIgnoresOrder(t *testing.T) {
	if !sameRoleSet([]string{"role_a", "role_b"}, []string{"role_b", "role_a"}) {
		t.Fatal("expected the same ids in a different order to compare equal")
	}
	if sameRoleSet([]string{"role_a"}, []string{"role_a", "role_b"}) {
		t.Fatal("expected a different size to compare unequal")
	}
	if sameRoleSet([]string{"role_a"}, []string{"role_b"}) {
		t.Fatal("expected different ids to compare unequal")
	}
	// Restoring a removed member with exactly the roles they held is not a
	// reassignment, and must not demand the capability for one.
	if !sameRoleSet(nil, nil) {
		t.Fatal("expected two empty sets to compare equal")
	}
}
