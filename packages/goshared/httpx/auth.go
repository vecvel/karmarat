package httpx

import (
	"errors"
	"net/http"
	"strings"

	"github.com/karmarat/platform/packages/goshared/tenancy"
	"github.com/karmarat/platform/packages/goshared/tokens"
)

// Trusted headers the gateway sets after it has verified a token.
//
// Only the gateway may set these; it strips whatever a client sent so a caller
// cannot assert an identity by adding a header.
const (
	HeaderPrincipalType = "X-Principal-Type"
	HeaderPrincipalID   = "X-Principal-ID"
	HeaderCompanyID     = "X-Company-ID"
	HeaderCompanySlug   = "X-Company-Slug"
	HeaderPermissions   = "X-Permissions"
	HeaderRoles         = "X-Roles"
	HeaderSessionID     = "X-Session-ID"
	HeaderPrincipalMail = "X-Principal-Email"
)

// Authenticate verifies a bearer token and attaches the principal.
//
// Requests without a token continue as anonymous: the handler decides what
// public access means, so one middleware serves both the public job board and
// the authenticated portals.
func Authenticate(verifier *tokens.Verifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := bearerToken(r)
			if raw == "" {
				next.ServeHTTP(w, r.WithContext(tenancy.WithPrincipal(r.Context(), tenancy.Anonymous())))
				return
			}

			claims, err := verifier.Verify(raw)
			if err != nil {
				if errors.Is(err, tokens.ErrExpiredToken) {
					WriteProblem(w, r, NewProblem(http.StatusUnauthorized, "token_expired",
						"Unauthorized", "Your session has expired. Please sign in again."))
					return
				}
				WriteProblem(w, r, Unauthorized("The supplied credentials are not valid."))
				return
			}

			next.ServeHTTP(w, r.WithContext(tenancy.WithPrincipal(r.Context(), claims.Principal())))
		})
	}
}

// TrustGatewayHeaders reconstructs the principal from gateway-set headers.
//
// Internal services sit behind the gateway and skip re-verifying the signature
// on every hop; the gateway is the one place a token is checked.
func TrustGatewayHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principalType := tenancy.PrincipalType(r.Header.Get(HeaderPrincipalType))
		if principalType == "" || !principalType.Valid() {
			next.ServeHTTP(w, r.WithContext(tenancy.WithPrincipal(r.Context(), tenancy.Anonymous())))
			return
		}

		principal := tenancy.Principal{
			Type:        principalType,
			Subject:     r.Header.Get(HeaderPrincipalID),
			CompanyID:   r.Header.Get(HeaderCompanyID),
			SessionID:   r.Header.Get(HeaderSessionID),
			Email:       r.Header.Get(HeaderPrincipalMail),
			Roles:       splitList(r.Header.Get(HeaderRoles)),
			Permissions: splitList(r.Header.Get(HeaderPermissions)),
		}

		next.ServeHTTP(w, r.WithContext(tenancy.WithPrincipal(r.Context(), principal)))
	})
}

// RequireAuth rejects anonymous requests.
func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal := tenancy.MustFromContext(r.Context())
		if !principal.IsAuthenticated() {
			WriteProblem(w, r, Unauthorized("You must be signed in to perform this action."))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequirePrincipal rejects principals of the wrong kind.
//
// A candidate token must not reach a company endpoint even if it somehow carried
// the right permission string, so the portal boundary is enforced by type.
func RequirePrincipal(allowed ...tenancy.PrincipalType) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal := tenancy.MustFromContext(r.Context())
			if !principal.IsAuthenticated() {
				WriteProblem(w, r, Unauthorized("You must be signed in to perform this action."))
				return
			}

			for _, kind := range allowed {
				if principal.Type == kind {
					next.ServeHTTP(w, r)
					return
				}
			}

			WriteProblem(w, r, Forbidden("This endpoint is not available to your account type."))
		})
	}
}

// RequirePermission rejects principals missing any of the listed permissions.
func RequirePermission(permissions ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal := tenancy.MustFromContext(r.Context())
			if !principal.IsAuthenticated() {
				WriteProblem(w, r, Unauthorized("You must be signed in to perform this action."))
				return
			}

			for _, permission := range permissions {
				if !principal.Can(permission) {
					WriteProblem(w, r, PermissionDenied(permissions))
					return
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RequireAnyPermission rejects principals holding none of the listed permissions.
func RequireAnyPermission(permissions ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal := tenancy.MustFromContext(r.Context())
			if !principal.IsAuthenticated() {
				WriteProblem(w, r, Unauthorized("You must be signed in to perform this action."))
				return
			}
			if !principal.CanAny(permissions...) {
				WriteProblem(w, r, PermissionDenied(permissions))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// PermissionDenied builds the 403 returned when a permission check fails.
func PermissionDenied(required []string) *Problem {
	problem := NewProblem(http.StatusForbidden, "forbidden", "Forbidden",
		"You do not have permission to perform this action.")
	problem.Errors = map[string][]string{"required": required}
	return problem
}

func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if header == "" {
		return ""
	}
	scheme, token, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}

func splitList(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
