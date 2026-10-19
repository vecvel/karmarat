// Package domain holds the identity service's entities and rules.
package domain

import (
	"errors"
	"time"

	"github.com/karmarat/platform/packages/goshared/tenancy"
)

// Realm separates the three sign-in surfaces.
type Realm string

const (
	RealmPlatform  Realm = "platform"
	RealmCompany   Realm = "company"
	RealmCandidate Realm = "candidate"
)

// Valid reports whether the realm is one the platform serves.
func (r Realm) Valid() bool {
	switch r {
	case RealmPlatform, RealmCompany, RealmCandidate:
		return true
	default:
		return false
	}
}

// PrincipalType maps a realm onto the principal a token will carry.
func (r Realm) PrincipalType() tenancy.PrincipalType {
	switch r {
	case RealmPlatform:
		return tenancy.PrincipalPlatform
	case RealmCompany:
		return tenancy.PrincipalCompany
	case RealmCandidate:
		return tenancy.PrincipalCandidate
	default:
		return tenancy.PrincipalAnonymous
	}
}

// AccountStatus is the lifecycle state of a login identity.
type AccountStatus string

const (
	AccountPending     AccountStatus = "pending"
	AccountActive      AccountStatus = "active"
	AccountSuspended   AccountStatus = "suspended"
	AccountDeactivated AccountStatus = "deactivated"
)

// CanSignIn reports whether an account in this state may authenticate.
func (s AccountStatus) CanSignIn() bool {
	return s == AccountActive
}

// Account is a login identity.
type Account struct {
	ID               string
	Realm            Realm
	Email            string
	PasswordHash     string
	FullName         string
	Status           AccountStatus
	EmailVerifiedAt  *time.Time
	FailedLoginCount int
	LockedUntil      *time.Time
	LastLoginAt      *time.Time
	LastLoginIP      string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// IsLocked reports whether the account is currently locked out.
func (a Account) IsLocked(now time.Time) bool {
	return a.LockedUntil != nil && a.LockedUntil.After(now)
}

// CompanyState is the lifecycle of a tenant.
type CompanyState string

const (
	CompanyPendingReview CompanyState = "pending_review"
	CompanyActive        CompanyState = "active"
	CompanySuspended     CompanyState = "suspended"
	CompanyClosed        CompanyState = "closed"
)

// SubscriptionState mirrors the billing service's view of a company's plan.
type SubscriptionState string

const (
	SubscriptionNone      SubscriptionState = "none"
	SubscriptionTrialing  SubscriptionState = "trialing"
	SubscriptionActive    SubscriptionState = "active"
	SubscriptionPastDue   SubscriptionState = "past_due"
	SubscriptionExpired   SubscriptionState = "expired"
	SubscriptionCancelled SubscriptionState = "cancelled"
)

// Entitled reports whether the state grants access to the company portal.
//
// `past_due` still admits the tenant: cutting off a paying customer the moment a
// card fails loses more than it protects. Expiry and cancellation do not.
func (s SubscriptionState) Entitled() bool {
	switch s {
	case SubscriptionTrialing, SubscriptionActive, SubscriptionPastDue:
		return true
	default:
		return false
	}
}

// Company is the identity service's projection of a tenant.
type Company struct {
	ID                    string
	Slug                  string
	Name                  string
	State                 CompanyState
	SubscriptionState     SubscriptionState
	SubscriptionExpiresAt *time.Time
	Entitlements          map[string]any
	UpdatedAt             time.Time
}

// PortalAvailable reports whether the company's careers portal may be entered.
//
// A company that registered but has not yet subscribed can sign in — it needs to
// reach checkout — but the portal itself stays closed until billing says so.
func (c Company) PortalAvailable() bool {
	return c.State == CompanyActive && c.SubscriptionState.Entitled()
}

// MembershipStatus is a user's standing within a company.
type MembershipStatus string

const (
	MembershipInvited   MembershipStatus = "invited"
	MembershipActive    MembershipStatus = "active"
	MembershipSuspended MembershipStatus = "suspended"
	MembershipRemoved   MembershipStatus = "removed"
)

// Membership links an account to a company.
type Membership struct {
	ID        string
	AccountID string
	CompanyID string
	Status    MembershipStatus
	IsOwner   bool
	JobTitle  string
	JoinedAt  *time.Time
	CreatedAt time.Time
}

// Role is a named set of permissions within a scope.
type Role struct {
	ID           string
	CompanyID    string // empty for platform roles
	Realm        Realm
	Slug         string
	Name         string
	Description  string
	Badge        string
	Permissions  []string
	IsSuperAdmin bool
	IsSystem     bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Session is a refresh-token session.
type Session struct {
	ID           string
	AccountID    string
	MembershipID string
	CompanyID    string
	ExpiresAt    time.Time
	RevokedAt    *time.Time
	IPAddress    string
	UserAgent    string
	CreatedAt    time.Time
	LastUsedAt   time.Time
}

// Active reports whether the session may still be exchanged for a token.
func (s Session) Active(now time.Time) bool {
	return s.RevokedAt == nil && s.ExpiresAt.After(now)
}

// Errors returned by the identity domain. Handlers map these onto HTTP
// responses; the messages are safe to show a user.
var (
	ErrAccountNotFound     = errors.New("account not found")
	ErrEmailTaken          = errors.New("an account with this email already exists")
	ErrInvalidCredentials  = errors.New("email or password is incorrect")
	ErrAccountLocked       = errors.New("account is temporarily locked after repeated failed sign-ins")
	ErrAccountInactive     = errors.New("account is not active")
	ErrCompanyNotFound     = errors.New("company not found")
	ErrSlugTaken           = errors.New("that company address is already taken")
	ErrCompanyUnavailable  = errors.New("this company portal is not currently available")
	ErrNoMembership        = errors.New("no access to this company")
	ErrMemberNotFound      = errors.New("this person is not a member of your company")
	ErrMembershipInactive  = errors.New("access to this company has been suspended")
	ErrSessionNotFound     = errors.New("session not found or already ended")
	ErrSessionExpired      = errors.New("session has expired")
	ErrRoleNotFound        = errors.New("role not found")
	ErrRoleImmutable       = errors.New("this role cannot be modified")
	ErrRoleInUse           = errors.New("role is still assigned to one or more users")
	ErrLastOwner           = errors.New("the last owner of a company cannot be removed")
	ErrSelfModification    = errors.New("you cannot change your own access")
	ErrPrivilegeEscalation = errors.New("you cannot grant permissions you do not hold yourself")
	ErrTokenInvalid        = errors.New("token is invalid or has already been used")
	ErrTokenExpired        = errors.New("token has expired")
)

// ValidationError reports a caller mistake: a field that is missing, malformed,
// or not allowed.
//
// It exists so the API layer can answer with a 422 and the offending field
// rather than collapsing every rejected input into a generic 500.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

// Invalid builds a validation error for a field.
func Invalid(field, message string) error {
	return &ValidationError{Field: field, Message: message}
}
