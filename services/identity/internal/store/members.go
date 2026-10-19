package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/karmarat/platform/services/identity/internal/domain"
	"github.com/karmarat/platform/services/identity/internal/rbac"
)

// CompanyMember is one person who works at a company, with what they may do.
//
// The resolved permission set is included because the question callers actually
// ask is not "who works here" but "who here should hear about this" — and that
// is a permission question. Returning the roster without permissions would just
// move the join to every caller.
type CompanyMember struct {
	AccountID   string   `json:"accountId"`
	Email       string   `json:"email"`
	FullName    string   `json:"fullName"`
	JobTitle    string   `json:"jobTitle,omitempty"`
	IsOwner     bool     `json:"isOwner"`
	Roles       []string `json:"roles"`
	Permissions []string `json:"permissions"`
}

// ListCompanyMembers returns a company's active members.
//
// Only active memberships of active accounts: a suspended recruiter should stop
// receiving a tenant's notifications the moment they are suspended, not when
// someone remembers to prune a downstream list.
//
// Permissions are unioned across the member's roles and then sanitized against
// the registry, exactly as the token issuer does. A stale permission left on a
// role by an older deploy therefore cannot reach a caller through this endpoint
// when it could not reach one through a token.
func (s *Store) ListCompanyMembers(ctx context.Context, companyID string) ([]CompanyMember, error) {
	const query = `
		SELECT
			a.id,
			a.email,
			a.full_name,
			COALESCE(m.job_title, ''),
			m.is_owner,
			COALESCE(
				ARRAY_AGG(DISTINCT r.slug) FILTER (WHERE r.slug IS NOT NULL),
				'{}'
			) AS role_slugs,
			-- roles.permissions is jsonb, so its elements come out through
			-- jsonb_array_elements_text rather than unnest.
			COALESCE(
				ARRAY(
					SELECT DISTINCT jsonb_array_elements_text(r2.permissions)
					FROM membership_roles mr2
					JOIN roles r2 ON r2.id = mr2.role_id
					WHERE mr2.membership_id = m.id
				),
				'{}'
			) AS permissions,
			COALESCE(bool_or(r.is_super_admin), false) AS is_super_admin
		FROM company_memberships m
		JOIN accounts a ON a.id = m.account_id
		LEFT JOIN membership_roles mr ON mr.membership_id = m.id
		LEFT JOIN roles r ON r.id = mr.role_id
		WHERE m.company_id = $1
		  AND m.status = $2
		  AND a.status = $3
		GROUP BY a.id, a.email, a.full_name, m.job_title, m.is_owner, m.id
		ORDER BY m.is_owner DESC, a.full_name`

	// status is a Postgres enum on both tables; an untyped text parameter is
	// coerced to it, so the named Go string types are converted here rather than
	// left to pgx's inference.
	rows, err := s.pool.Query(ctx, query,
		companyID, string(domain.MembershipActive), string(domain.AccountActive))
	if err != nil {
		return nil, fmt.Errorf("store: list company members: %w", err)
	}
	defer rows.Close()

	var members []CompanyMember
	for rows.Next() {
		var member CompanyMember
		var isSuperAdmin bool
		if err := rows.Scan(
			&member.AccountID, &member.Email, &member.FullName,
			&member.JobTitle, &member.IsOwner,
			&member.Roles, &member.Permissions, &isSuperAdmin,
		); err != nil {
			return nil, fmt.Errorf("store: scan company member: %w", err)
		}

		// A company super admin holds everything in the company scope, the same
		// way the token issuer resolves it. Deriving it here keeps this endpoint
		// and a token from disagreeing about what someone may do.
		if isSuperAdmin {
			member.Permissions = rbac.PermissionsForScope(rbac.ScopeCompany)
		} else {
			member.Permissions = rbac.Sanitize(rbac.ScopeCompany, member.Permissions)
		}

		members = append(members, member)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list company members: %w", err)
	}

	return members, nil
}

// RosterMember is one person on a company's team, as its own administrators see
// them.
//
// Deliberately a different shape and a different query from CompanyMember:
// that one answers "who should hear about this", and so must exclude anyone
// suspended, while this one answers "who works here", and so must include them —
// a suspended recruiter you cannot see is a suspended recruiter you cannot
// restore. Folding the two into one filtered query would mean one caller's
// change to the filter silently rewrites the other's meaning.
type RosterMember struct {
	AccountID     string     `json:"accountId"`
	MembershipID  string     `json:"membershipId"`
	Email         string     `json:"email"`
	FullName      string     `json:"fullName"`
	JobTitle      string     `json:"jobTitle,omitempty"`
	IsOwner       bool       `json:"isOwner"`
	Status        string     `json:"status"`
	AccountStatus string     `json:"accountStatus"`
	RoleIDs       []string   `json:"roleIds"`
	RoleSlugs     []string   `json:"roles"`
	RoleNames     []string   `json:"roleNames"`
	PrimaryRoleID string     `json:"primaryRoleId,omitempty"`
	Permissions   []string   `json:"permissions"`
	IsSuperAdmin  bool       `json:"isSuperAdmin"`
	LastLoginAt   *time.Time `json:"lastLoginAt"`
	CreatedAt     time.Time  `json:"createdAt"`
}

const rosterQuery = `
	SELECT
		a.id,
		m.id,
		a.email,
		a.full_name,
		COALESCE(m.job_title, ''),
		m.is_owner,
		m.status,
		a.status,
		COALESCE(ARRAY(
			SELECT r2.id FROM membership_roles mr2
			JOIN roles r2 ON r2.id = mr2.role_id
			WHERE mr2.membership_id = m.id
			ORDER BY r2.name
		), '{}') AS role_ids,
		COALESCE(ARRAY(
			SELECT r2.slug FROM membership_roles mr2
			JOIN roles r2 ON r2.id = mr2.role_id
			WHERE mr2.membership_id = m.id
			ORDER BY r2.name
		), '{}') AS role_slugs,
		COALESCE(ARRAY(
			SELECT r2.name FROM membership_roles mr2
			JOIN roles r2 ON r2.id = mr2.role_id
			WHERE mr2.membership_id = m.id
			ORDER BY r2.name
		), '{}') AS role_names,
		COALESCE((
			SELECT mr2.role_id FROM membership_roles mr2
			WHERE mr2.membership_id = m.id AND mr2.is_primary
			LIMIT 1
		), '') AS primary_role_id,
		-- roles.permissions is jsonb, so its elements come out through
		-- jsonb_array_elements_text rather than unnest.
		COALESCE(ARRAY(
			SELECT DISTINCT jsonb_array_elements_text(r2.permissions)
			FROM membership_roles mr2
			JOIN roles r2 ON r2.id = mr2.role_id
			WHERE mr2.membership_id = m.id
		), '{}') AS permissions,
		COALESCE((
			SELECT bool_or(r2.is_super_admin) FROM membership_roles mr2
			JOIN roles r2 ON r2.id = mr2.role_id
			WHERE mr2.membership_id = m.id
		), false) AS is_super_admin,
		a.last_login_at,
		m.created_at
	FROM company_memberships m
	JOIN accounts a ON a.id = m.account_id
	WHERE m.company_id = $1`

// excludeRemoved is appended by the callers that answer "who works here".
// FindAnyRosterMember deliberately omits it: reinstating somebody requires
// reading the membership that removing them left behind.
const excludeRemoved = ` AND m.status <> $2`

func scanRosterMember(row pgx.Row) (RosterMember, error) {
	var member RosterMember
	if err := row.Scan(
		&member.AccountID, &member.MembershipID, &member.Email, &member.FullName,
		&member.JobTitle, &member.IsOwner, &member.Status, &member.AccountStatus,
		&member.RoleIDs, &member.RoleSlugs, &member.RoleNames, &member.PrimaryRoleID,
		&member.Permissions, &member.IsSuperAdmin, &member.LastLoginAt, &member.CreatedAt,
	); err != nil {
		return RosterMember{}, err
	}

	// Resolved exactly as the token issuer resolves it, so this screen and a
	// token can never disagree about what somebody may do.
	if member.IsSuperAdmin {
		member.Permissions = rbac.PermissionsForScope(rbac.ScopeCompany)
	} else {
		member.Permissions = rbac.Sanitize(rbac.ScopeCompany, member.Permissions)
	}
	return member, nil
}

// ListCompanyRoster returns every member of a company except those already
// removed, newest owners first.
func (s *Store) ListCompanyRoster(ctx context.Context, companyID string) ([]RosterMember, error) {
	rows, err := s.pool.Query(ctx, rosterQuery+excludeRemoved+`
		ORDER BY m.is_owner DESC, a.full_name`,
		companyID, string(domain.MembershipRemoved))
	if err != nil {
		return nil, fmt.Errorf("store: list company roster: %w", err)
	}
	defer rows.Close()

	members := []RosterMember{}
	for rows.Next() {
		member, err := scanRosterMember(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan roster member: %w", err)
		}
		members = append(members, member)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list company roster: %w", err)
	}
	return members, nil
}

// FindRosterMember returns one member of a company.
//
// The company identifier is a parameter rather than something the caller
// filters afterwards: an account id from a URL must never resolve to a
// membership in another tenant, and here it structurally cannot.
func (s *Store) FindRosterMember(ctx context.Context, companyID, accountID string) (RosterMember, error) {
	member, err := scanRosterMember(s.pool.QueryRow(ctx,
		rosterQuery+excludeRemoved+` AND a.id = $3`,
		companyID, string(domain.MembershipRemoved), accountID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Identical whether the account does not exist or belongs to another
			// tenant, so a company cannot use this endpoint to probe for the
			// existence of an account it has nothing to do with.
			return RosterMember{}, domain.ErrMemberNotFound
		}
		return RosterMember{}, fmt.Errorf("store: find roster member: %w", err)
	}
	return member, nil
}

// FindAnyRosterMember returns a member of a company including one already
// removed.
//
// Separate from FindRosterMember, and used only when reinstating somebody:
// adding an address that already has a membership is a change to that existing
// member, and deciding whether the actor may make it needs the access that
// member currently holds. Reading it through the ordinary roster lookup would
// return "not a member" for exactly the people this path exists to restore, and
// the guards would then have nothing to check against.
func (s *Store) FindAnyRosterMember(ctx context.Context, companyID, accountID string) (RosterMember, error) {
	member, err := scanRosterMember(s.pool.QueryRow(ctx,
		rosterQuery+` AND a.id = $2`, companyID, accountID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RosterMember{}, domain.ErrMemberNotFound
		}
		return RosterMember{}, fmt.Errorf("store: find any roster member: %w", err)
	}
	return member, nil
}

// UpdateMembershipJobTitle changes the one descriptive field a company owns
// about one of its members.
//
// Neither the email nor the name is updatable here, for the same reason: both
// belong to the account, not to the membership, and the account is shared by
// every company that person recruits for. Writing `accounts.full_name` from a
// company-scoped endpoint meant one tenant's administrator renamed that person
// inside every other tenant too — a cross-tenant write reached through a screen
// that looks entirely local. The job title lives on the membership and is
// genuinely this company's to set.
//
// A person changes their own name through their own account settings.
func (s *Store) UpdateMembershipJobTitle(ctx context.Context, membershipID, jobTitle string) error {
	if _, err := s.pool.Exec(ctx,
		`UPDATE company_memberships SET job_title = $2 WHERE id = $1`,
		membershipID, nullable(jobTitle)); err != nil {
		return fmt.Errorf("store: update membership job title: %w", err)
	}
	return nil
}

// RevokeMembershipSessions ends one person's live sessions inside one company.
//
// Scoped to the company on purpose: a recruiter suspended by one tenant keeps
// working at the other companies they recruit for, and revoking every session
// they hold would let any company administrator sign a person out of a tenant
// they have no authority over.
func (s *Store) RevokeMembershipSessions(ctx context.Context, accountID, companyID, reason string) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE sessions SET revoked_at = now(), revoked_reason = $3
		WHERE account_id = $1 AND company_id = $2 AND revoked_at IS NULL`,
		accountID, companyID, reason)
	if err != nil {
		return 0, fmt.Errorf("store: revoke membership sessions: %w", err)
	}
	return tag.RowsAffected(), nil
}

// CountRoleAssignmentsByRole returns the number of holders of every role in a
// company, so a role list can be rendered without one query per row.
func (s *Store) CountRoleAssignmentsByRole(ctx context.Context, companyID string) (map[string]int, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.id, count(mr.membership_id)
		FROM roles r
		LEFT JOIN membership_roles mr ON mr.role_id = r.id
		LEFT JOIN company_memberships m ON m.id = mr.membership_id AND m.status <> $2
		WHERE r.company_id = $1
		GROUP BY r.id`, companyID, string(domain.MembershipRemoved))
	if err != nil {
		return nil, fmt.Errorf("store: count role assignments by role: %w", err)
	}
	defer rows.Close()

	counts := map[string]int{}
	for rows.Next() {
		var roleID string
		var count int
		if err := rows.Scan(&roleID, &count); err != nil {
			return nil, fmt.Errorf("store: scan role assignment count: %w", err)
		}
		counts[roleID] = count
	}
	return counts, rows.Err()
}
