// Package tenancy carries who is calling and which tenant they are acting in.
//
// Karmarat serves three kinds of principal from one platform:
//
//	platform   staff operating root.{hostname}; no tenant of their own
//	company    a recruiter or company admin, always scoped to one company
//	candidate  a job seeker on jobs.{hostname}; global, never tenant-scoped
//
// Every request carries a Principal, and every tenant-owned query is filtered by
// its CompanyID. The type exists so that "which company is this?" is answered by
// the authenticated token rather than by a request parameter a caller could edit.
package tenancy

import (
	"context"
	"errors"
	"strings"
)

// PrincipalType identifies the kind of actor behind a request.
type PrincipalType string

const (
	PrincipalPlatform  PrincipalType = "platform"
	PrincipalCompany   PrincipalType = "company"
	PrincipalCandidate PrincipalType = "candidate"
	// PrincipalService is used for authenticated service-to-service calls.
	PrincipalService PrincipalType = "service"
	// PrincipalAnonymous covers public traffic such as the careers job board.
	PrincipalAnonymous PrincipalType = "anonymous"
)

// Valid reports whether the principal type is one the platform recognizes.
func (p PrincipalType) Valid() bool {
	switch p {
	case PrincipalPlatform, PrincipalCompany, PrincipalCandidate, PrincipalService, PrincipalAnonymous:
		return true
	default:
		return false
	}
}

var (
	// ErrNoPrincipal means the request never passed through authentication.
	ErrNoPrincipal = errors.New("tenancy: no principal on context")
	// ErrNotCompanyScoped means a tenant-owned operation was attempted without a company.
	ErrNotCompanyScoped = errors.New("tenancy: principal is not scoped to a company")
	// ErrCrossTenant means a principal tried to touch another company's data.
	ErrCrossTenant = errors.New("tenancy: cross-tenant access denied")
)

// Principal is the authenticated actor behind a request.
type Principal struct {
	Type PrincipalType
	// Subject is the user, candidate, or service identifier.
	Subject string
	// CompanyID is set only for company principals.
	CompanyID string
	// Roles are the role slugs assigned within the principal's scope.
	Roles []string
	// Permissions are the resolved `<feature>.<action>` keys.
	Permissions []string
	// SessionID ties the request back to a refresh-token session.
	SessionID string
	// Email is carried for auditing and support, never for authorization.
	Email string
}

// Anonymous is the principal used for unauthenticated public traffic.
func Anonymous() Principal {
	return Principal{Type: PrincipalAnonymous}
}

// IsAuthenticated reports whether a real identity is attached.
func (p Principal) IsAuthenticated() bool {
	return p.Type != "" && p.Type != PrincipalAnonymous && p.Subject != ""
}

// IsPlatformAdmin reports whether the principal operates the platform itself.
func (p Principal) IsPlatformAdmin() bool {
	return p.Type == PrincipalPlatform
}

// Can reports whether the principal holds a permission.
//
// Platform staff are not implicitly granted tenant permissions: operating the
// platform and acting inside a customer's account are deliberately separate, so
// a support engineer cannot silently read a company's pipeline.
func (p Principal) Can(permission string) bool {
	for _, held := range p.Permissions {
		if held == permission {
			return true
		}
	}
	return false
}

// CanAny reports whether the principal holds at least one of the permissions.
func (p Principal) CanAny(permissions ...string) bool {
	for _, permission := range permissions {
		if p.Can(permission) {
			return true
		}
	}
	return false
}

// RequireCompany returns the principal's company, or an error when it has none.
//
// Repositories call this instead of reading a company identifier from the
// request, which is what keeps one company's data out of another's queries.
func (p Principal) RequireCompany() (string, error) {
	if p.Type != PrincipalCompany || p.CompanyID == "" {
		return "", ErrNotCompanyScoped
	}
	return p.CompanyID, nil
}

// AssertCompany checks that a record's owner matches the principal's company.
func (p Principal) AssertCompany(recordCompanyID string) error {
	companyID, err := p.RequireCompany()
	if err != nil {
		return err
	}
	if !strings.EqualFold(companyID, recordCompanyID) {
		return ErrCrossTenant
	}
	return nil
}

type principalKey struct{}

// WithPrincipal stores the authenticated principal on the context.
func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, principal)
}

// FromContext returns the request's principal.
func FromContext(ctx context.Context) (Principal, error) {
	principal, ok := ctx.Value(principalKey{}).(Principal)
	if !ok {
		return Principal{}, ErrNoPrincipal
	}
	return principal, nil
}

// MustFromContext returns the request's principal, or the anonymous principal.
func MustFromContext(ctx context.Context) Principal {
	principal, err := FromContext(ctx)
	if err != nil {
		return Anonymous()
	}
	return principal
}
