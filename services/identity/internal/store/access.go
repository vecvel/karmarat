package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/karmarat/platform/packages/goshared/idgen"
	"github.com/karmarat/platform/services/identity/internal/domain"
	"github.com/karmarat/platform/services/identity/internal/rbac"
)

/* -------------------------------------------------------------------------- */
/* Roles                                                                      */
/* -------------------------------------------------------------------------- */

const roleColumns = `
	id, coalesce(company_id, ''), realm, slug, name, coalesce(description, ''),
	coalesce(badge, ''), permissions, is_super_admin, is_system, created_at, updated_at`

func scanRole(row pgx.Row) (domain.Role, error) {
	var r domain.Role
	var permissions []byte

	if err := row.Scan(
		&r.ID, &r.CompanyID, &r.Realm, &r.Slug, &r.Name, &r.Description,
		&r.Badge, &permissions, &r.IsSuperAdmin, &r.IsSystem, &r.CreatedAt, &r.UpdatedAt,
	); err != nil {
		return domain.Role{}, err
	}

	if len(permissions) > 0 {
		_ = json.Unmarshal(permissions, &r.Permissions)
	}
	return r, nil
}

// scopeFor maps a realm onto the permission scope its roles draw from.
func scopeFor(realm domain.Realm) rbac.Scope {
	switch realm {
	case domain.RealmPlatform:
		return rbac.ScopePlatform
	case domain.RealmCandidate:
		return rbac.ScopeCandidate
	default:
		return rbac.ScopeCompany
	}
}

// SeedRoles creates the default roles for a scope if they are not present.
//
// Idempotent, so it can run on every boot for the platform scope and on every
// company creation without duplicating rows or resetting a customer's edits.
func (s *Store) SeedRoles(ctx context.Context, tx pgx.Tx, realm domain.Realm, companyID string, defaults []rbac.DefaultRole) error {
	scope := scopeFor(realm)

	for _, def := range defaults {
		permissions, err := json.Marshal(def.Resolve(scope))
		if err != nil {
			return fmt.Errorf("store: marshal role permissions: %w", err)
		}

		// A super-admin role always holds everything in its scope, so its
		// permission list is refreshed when new features are registered.
		var conflict string
		if def.IsSuperAdmin {
			conflict = `DO UPDATE SET permissions = EXCLUDED.permissions, updated_at = now()`
		} else {
			conflict = `DO NOTHING`
		}

		target := "(company_id, lower(slug)) WHERE company_id IS NOT NULL"
		if companyID == "" {
			target = "(lower(slug)) WHERE company_id IS NULL"
		}

		query := fmt.Sprintf(`
			INSERT INTO roles (id, company_id, realm, slug, name, description, badge,
			                   permissions, is_super_admin, is_system)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			ON CONFLICT %s %s`, target, conflict)

		if _, err := s.exec(ctx, tx, query,
			idgen.New("role"), nullable(companyID), realm, def.Slug, def.Name,
			def.Description, def.Badge, permissions, def.IsSuperAdmin, def.IsSystem,
		); err != nil {
			return fmt.Errorf("store: seed role %s: %w", def.Slug, err)
		}
	}

	return nil
}

// ListRoles returns the roles in a scope. An empty companyID lists platform roles.
func (s *Store) ListRoles(ctx context.Context, companyID string) ([]domain.Role, error) {
	var query string
	var args []any

	if companyID == "" {
		query = `SELECT ` + roleColumns + ` FROM roles WHERE company_id IS NULL
		         ORDER BY is_super_admin DESC, is_system DESC, name`
	} else {
		query = `SELECT ` + roleColumns + ` FROM roles WHERE company_id = $1
		         ORDER BY is_super_admin DESC, is_system DESC, name`
		args = append(args, companyID)
	}

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list roles: %w", err)
	}
	defer rows.Close()

	var roles []domain.Role
	for rows.Next() {
		role, err := scanRole(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan role: %w", err)
		}
		roles = append(roles, role)
	}
	return roles, rows.Err()
}

// FindRoleBySlug resolves a role within its scope.
func (s *Store) FindRoleBySlug(ctx context.Context, tx pgx.Tx, companyID, slug string) (domain.Role, error) {
	var query string
	var args []any

	if companyID == "" {
		query = `SELECT ` + roleColumns + ` FROM roles WHERE company_id IS NULL AND lower(slug) = lower($1)`
		args = append(args, slug)
	} else {
		query = `SELECT ` + roleColumns + ` FROM roles WHERE company_id = $1 AND lower(slug) = lower($2)`
		args = append(args, companyID, slug)
	}

	role, err := scanRole(s.queryRow(ctx, tx, query, args...))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Role{}, domain.ErrRoleNotFound
		}
		return domain.Role{}, fmt.Errorf("store: find role by slug: %w", err)
	}
	return role, nil
}

// FindRoleByID resolves a role and verifies it belongs to the expected scope.
//
// The scope check is what stops one company from assigning another company's
// role by passing its identifier.
func (s *Store) FindRoleByID(ctx context.Context, companyID, roleID string) (domain.Role, error) {
	role, err := scanRole(s.pool.QueryRow(ctx, `SELECT `+roleColumns+` FROM roles WHERE id = $1`, roleID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Role{}, domain.ErrRoleNotFound
		}
		return domain.Role{}, fmt.Errorf("store: find role by id: %w", err)
	}

	if role.CompanyID != companyID {
		return domain.Role{}, domain.ErrRoleNotFound
	}
	return role, nil
}

// CreateRoleInput describes a new custom role.
type CreateRoleInput struct {
	CompanyID   string
	Realm       domain.Realm
	Slug        string
	Name        string
	Description string
	Badge       string
	Permissions []string
}

// CreateRole inserts a custom role.
func (s *Store) CreateRole(ctx context.Context, in CreateRoleInput) (domain.Role, error) {
	permissions, err := json.Marshal(rbac.Sanitize(scopeFor(in.Realm), in.Permissions))
	if err != nil {
		return domain.Role{}, fmt.Errorf("store: marshal permissions: %w", err)
	}

	query := `
		INSERT INTO roles (id, company_id, realm, slug, name, description, badge,
		                   permissions, is_super_admin, is_system)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, false, false)
		RETURNING ` + roleColumns

	role, err := scanRole(s.pool.QueryRow(ctx, query,
		idgen.New("role"), nullable(in.CompanyID), in.Realm, strings.ToLower(in.Slug),
		in.Name, in.Description, in.Badge, permissions))
	if err != nil {
		if isUniqueViolation(err) {
			return domain.Role{}, fmt.Errorf("a role with the identifier %q already exists", in.Slug)
		}
		return domain.Role{}, fmt.Errorf("store: create role: %w", err)
	}
	return role, nil
}

// UpdateRolePermissions replaces a role's permission list.
func (s *Store) UpdateRolePermissions(ctx context.Context, roleID string, scope rbac.Scope, permissions []string) error {
	encoded, err := json.Marshal(rbac.Sanitize(scope, permissions))
	if err != nil {
		return fmt.Errorf("store: marshal permissions: %w", err)
	}

	_, err = s.pool.Exec(ctx, `UPDATE roles SET permissions = $2 WHERE id = $1`, roleID, encoded)
	if err != nil {
		return fmt.Errorf("store: update role permissions: %w", err)
	}
	return nil
}

// UpdateRoleDetails changes a role's descriptive fields.
func (s *Store) UpdateRoleDetails(ctx context.Context, roleID, name, description, badge string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE roles SET name = $2, description = $3, badge = $4 WHERE id = $1`,
		roleID, name, description, badge)
	if err != nil {
		return fmt.Errorf("store: update role details: %w", err)
	}
	return nil
}

// DeleteRole removes a custom role, refusing while it is still assigned.
func (s *Store) DeleteRole(ctx context.Context, roleID string) error {
	var assigned int
	if err := s.pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM membership_roles WHERE role_id = $1)
		     + (SELECT count(*) FROM account_roles WHERE role_id = $1)`, roleID).Scan(&assigned); err != nil {
		return fmt.Errorf("store: count role assignments: %w", err)
	}
	if assigned > 0 {
		return domain.ErrRoleInUse
	}

	if _, err := s.pool.Exec(ctx, `DELETE FROM roles WHERE id = $1`, roleID); err != nil {
		return fmt.Errorf("store: delete role: %w", err)
	}
	return nil
}

/* -------------------------------------------------------------------------- */
/* Memberships                                                                */
/* -------------------------------------------------------------------------- */

// CreateMembershipInput links an account to a company.
type CreateMembershipInput struct {
	AccountID string
	CompanyID string
	Status    domain.MembershipStatus
	IsOwner   bool
	JobTitle  string
	InvitedBy string
}

// CreateMembership inserts a company membership.
func (s *Store) CreateMembership(ctx context.Context, tx pgx.Tx, in CreateMembershipInput) (domain.Membership, error) {
	id := idgen.New("mem")

	var joinedAt any
	if in.Status == domain.MembershipActive {
		joinedAt = time.Now()
	}

	row := s.queryRow(ctx, tx, `
		INSERT INTO company_memberships
			(id, account_id, company_id, status, is_owner, job_title, invited_by, invited_at, joined_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now(), $8)
		RETURNING id, account_id, company_id, status, is_owner,
		          coalesce(job_title, ''), joined_at, created_at`,
		id, in.AccountID, in.CompanyID, in.Status, in.IsOwner,
		nullable(in.JobTitle), nullable(in.InvitedBy), joinedAt)

	var m domain.Membership
	if err := row.Scan(&m.ID, &m.AccountID, &m.CompanyID, &m.Status,
		&m.IsOwner, &m.JobTitle, &m.JoinedAt, &m.CreatedAt); err != nil {
		if isUniqueViolation(err) {
			return domain.Membership{}, fmt.Errorf("this account is already a member of the company")
		}
		return domain.Membership{}, fmt.Errorf("store: create membership: %w", err)
	}
	return m, nil
}

// FindMembership resolves an account's standing within one company.
func (s *Store) FindMembership(ctx context.Context, accountID, companyID string) (domain.Membership, error) {
	var m domain.Membership
	err := s.pool.QueryRow(ctx, `
		SELECT id, account_id, company_id, status, is_owner,
		       coalesce(job_title, ''), joined_at, created_at
		FROM company_memberships
		WHERE account_id = $1 AND company_id = $2`, accountID, companyID).
		Scan(&m.ID, &m.AccountID, &m.CompanyID, &m.Status,
			&m.IsOwner, &m.JobTitle, &m.JoinedAt, &m.CreatedAt)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Membership{}, domain.ErrNoMembership
		}
		return domain.Membership{}, fmt.Errorf("store: find membership: %w", err)
	}
	return m, nil
}

// CompanyMembership pairs a membership with the company it belongs to, for the
// account switcher shown when someone works for more than one company.
type CompanyMembership struct {
	Membership domain.Membership
	Company    domain.Company
}

// ListMembershipsForAccount returns every company an account belongs to.
func (s *Store) ListMembershipsForAccount(ctx context.Context, accountID string) ([]CompanyMembership, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT m.id, m.account_id, m.company_id, m.status, m.is_owner,
		       coalesce(m.job_title, ''), m.joined_at, m.created_at,
		       c.id, c.slug, c.name, c.state, c.subscription_state,
		       c.subscription_expires_at, c.entitlements, c.updated_at
		FROM company_memberships m
		JOIN companies c ON c.id = m.company_id
		WHERE m.account_id = $1 AND m.status <> 'removed'
		ORDER BY m.created_at`, accountID)
	if err != nil {
		return nil, fmt.Errorf("store: list memberships: %w", err)
	}
	defer rows.Close()

	var out []CompanyMembership
	for rows.Next() {
		var cm CompanyMembership
		var entitlements []byte

		if err := rows.Scan(
			&cm.Membership.ID, &cm.Membership.AccountID, &cm.Membership.CompanyID,
			&cm.Membership.Status, &cm.Membership.IsOwner, &cm.Membership.JobTitle,
			&cm.Membership.JoinedAt, &cm.Membership.CreatedAt,
			&cm.Company.ID, &cm.Company.Slug, &cm.Company.Name, &cm.Company.State,
			&cm.Company.SubscriptionState, &cm.Company.SubscriptionExpiresAt,
			&entitlements, &cm.Company.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("store: scan membership: %w", err)
		}

		if len(entitlements) > 0 {
			_ = json.Unmarshal(entitlements, &cm.Company.Entitlements)
		}
		out = append(out, cm)
	}
	return out, rows.Err()
}

// SetMembershipStatus changes a member's standing.
// A nil tx runs on the pool. Reinstating a member changes the status and the
// roles together, and a status change that committed on its own while the role
// assignment rolled back would leave somebody active holding whatever they held
// before they were removed.
func (s *Store) SetMembershipStatus(ctx context.Context, tx pgx.Tx, membershipID string, status domain.MembershipStatus) error {
	joined := ""
	if status == domain.MembershipActive {
		joined = ", joined_at = coalesce(joined_at, now())"
	}

	_, err := s.exec(ctx, tx,
		`UPDATE company_memberships SET status = $2`+joined+` WHERE id = $1`,
		membershipID, status)
	if err != nil {
		return fmt.Errorf("store: set membership status: %w", err)
	}
	return nil
}

// CountActiveOwners reports how many active owners a company has.
//
// Used to refuse the change that would leave a company with no one able to
// administer it.
func (s *Store) CountActiveOwners(ctx context.Context, companyID, excludingMembershipID string) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM company_memberships
		WHERE company_id = $1 AND is_owner = true AND status = 'active' AND id <> $2`,
		companyID, excludingMembershipID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("store: count active owners: %w", err)
	}
	return count, nil
}

/* -------------------------------------------------------------------------- */
/* Role assignment and permission resolution                                  */
/* -------------------------------------------------------------------------- */

// ReplaceMembershipRoles sets exactly which roles a company member holds.
func (s *Store) ReplaceMembershipRoles(ctx context.Context, tx pgx.Tx, membershipID string, roleIDs []string, primaryRoleID, assignedBy string) error {
	if len(roleIDs) == 0 {
		return errors.New("at least one role must be assigned")
	}

	if _, err := s.exec(ctx, tx, `DELETE FROM membership_roles WHERE membership_id = $1`, membershipID); err != nil {
		return fmt.Errorf("store: clear membership roles: %w", err)
	}

	if primaryRoleID == "" {
		primaryRoleID = roleIDs[0]
	}

	for _, roleID := range roleIDs {
		if _, err := s.exec(ctx, tx, `
			INSERT INTO membership_roles (membership_id, role_id, is_primary, assigned_by)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (membership_id, role_id) DO UPDATE SET is_primary = EXCLUDED.is_primary`,
			membershipID, roleID, roleID == primaryRoleID, nullable(assignedBy)); err != nil {
			return fmt.Errorf("store: assign membership role: %w", err)
		}
	}
	return nil
}

// ReplaceAccountRoles sets exactly which platform roles an account holds.
func (s *Store) ReplaceAccountRoles(ctx context.Context, tx pgx.Tx, accountID string, roleIDs []string, primaryRoleID, assignedBy string) error {
	if len(roleIDs) == 0 {
		return errors.New("at least one role must be assigned")
	}

	if _, err := s.exec(ctx, tx, `DELETE FROM account_roles WHERE account_id = $1`, accountID); err != nil {
		return fmt.Errorf("store: clear account roles: %w", err)
	}

	if primaryRoleID == "" {
		primaryRoleID = roleIDs[0]
	}

	for _, roleID := range roleIDs {
		if _, err := s.exec(ctx, tx, `
			INSERT INTO account_roles (account_id, role_id, is_primary, assigned_by)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (account_id, role_id) DO UPDATE SET is_primary = EXCLUDED.is_primary`,
			accountID, roleID, roleID == primaryRoleID, nullable(assignedBy)); err != nil {
			return fmt.Errorf("store: assign account role: %w", err)
		}
	}
	return nil
}

// Access is the resolved authorization state used to mint a token.
type Access struct {
	Roles        []string
	RoleNames    []string
	Permissions  []string
	IsSuperAdmin bool
	PrimaryRole  string
}

// ResolveMembershipAccess unions the permissions of a member's roles.
//
// A super-admin role expands to every permission in the scope, so the token
// carries a concrete list and no downstream service needs a special case.
func (s *Store) ResolveMembershipAccess(ctx context.Context, membershipID string) (Access, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.slug, r.name, r.permissions, r.is_super_admin, mr.is_primary
		FROM membership_roles mr
		JOIN roles r ON r.id = mr.role_id
		WHERE mr.membership_id = $1`, membershipID)
	if err != nil {
		return Access{}, fmt.Errorf("store: resolve membership access: %w", err)
	}
	defer rows.Close()

	return collectAccess(rows, rbac.ScopeCompany)
}

// ResolveAccountAccess unions the permissions of an account's platform roles.
func (s *Store) ResolveAccountAccess(ctx context.Context, accountID string, scope rbac.Scope) (Access, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.slug, r.name, r.permissions, r.is_super_admin, ar.is_primary
		FROM account_roles ar
		JOIN roles r ON r.id = ar.role_id
		WHERE ar.account_id = $1`, accountID)
	if err != nil {
		return Access{}, fmt.Errorf("store: resolve account access: %w", err)
	}
	defer rows.Close()

	return collectAccess(rows, scope)
}

func collectAccess(rows pgx.Rows, scope rbac.Scope) (Access, error) {
	access := Access{}
	unique := map[string]struct{}{}

	for rows.Next() {
		var slug, name string
		var permissions []byte
		var isSuperAdmin, isPrimary bool

		if err := rows.Scan(&slug, &name, &permissions, &isSuperAdmin, &isPrimary); err != nil {
			return Access{}, fmt.Errorf("store: scan role access: %w", err)
		}

		access.Roles = append(access.Roles, slug)
		access.RoleNames = append(access.RoleNames, name)
		if isPrimary || access.PrimaryRole == "" {
			access.PrimaryRole = slug
		}

		if isSuperAdmin {
			access.IsSuperAdmin = true
			for _, key := range rbac.PermissionsForScope(scope) {
				unique[key] = struct{}{}
			}
			continue
		}

		var keys []string
		if len(permissions) > 0 {
			_ = json.Unmarshal(permissions, &keys)
		}
		for _, key := range keys {
			unique[key] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return Access{}, fmt.Errorf("store: iterate role access: %w", err)
	}

	for key := range unique {
		access.Permissions = append(access.Permissions, key)
	}
	access.Permissions = rbac.Sanitize(scope, access.Permissions)

	return access, nil
}
