// Package routing maps request paths onto backend services.
package routing

import (
	"sort"
	"strings"

	"github.com/karmarat/platform/packages/goshared/config"
	"github.com/karmarat/platform/packages/goshared/tenancy"
)

// Route describes one upstream service and the portals allowed to reach it.
type Route struct {
	// Prefix is matched against the request path.
	Prefix string
	// Service is the upstream name, used in logs and metrics.
	Service string
	// Target is the upstream base URL.
	Target string
	// Portals restricts which front doors may use the route. Empty means any.
	//
	// This is the outer boundary: a candidate session on jobs.{host} cannot reach
	// a company endpoint even with a valid token, because the route itself is not
	// exposed on that portal.
	Portals []tenancy.Portal
	// Principals restricts which principal types may use the route.
	Principals []tenancy.PrincipalType
	// Public allows unauthenticated access (the job board, sign-in, webhooks).
	Public bool
	// RequireCompany rejects requests that carry no resolved tenant.
	RequireCompany bool
}

// Table is the gateway's routing table, longest prefix first.
type Table struct {
	routes []Route
}

// NewTable builds the routing table from the environment.
//
// Upstream addresses are configuration, not code, so the same binary runs
// against docker-compose locally and against service DNS in a cluster.
func NewTable() *Table {
	// These fallbacks must match the ports in .env.example, scripts/dev.sh and
	// each service's own default. A fallback that disagrees does not fail
	// loudly: the gateway forwards to whatever is listening on that port,
	// carrying the caller's verified principal headers with it. TestServiceURLDefaults
	// pins them so the set cannot drift again.
	identity := config.String("IDENTITY_URL", "http://localhost:8081")
	companies := config.String("COMPANIES_URL", "http://localhost:8082")
	jobs := config.String("JOBS_URL", "http://localhost:8085")
	applications := config.String("APPLICATIONS_URL", "http://localhost:8086")
	candidates := config.String("CANDIDATES_URL", "http://localhost:8087")
	messaging := config.String("MESSAGING_URL", "http://localhost:8088")
	notifications := config.String("NOTIFICATIONS_URL", "http://localhost:8089")
	subscriptions := config.String("SUBSCRIPTIONS_URL", "http://localhost:8083")
	payments := config.String("PAYMENTS_URL", "http://localhost:8084")
	support := config.String("SUPPORT_URL", "http://localhost:8090")
	admin := config.String("ADMIN_URL", "http://localhost:8091")
	interviews := config.String("INTERVIEWS_URL", "http://localhost:8092")
	offers := config.String("OFFERS_URL", "http://localhost:8093")
	audit := config.String("AUDIT_URL", "http://localhost:8094")

	routes := []Route{
		// Authentication is reachable from every portal: each one has a sign-in.
		{Prefix: "/api/v1/auth", Service: "identity", Target: identity, Public: true},
		{Prefix: "/.well-known/jwks.json", Service: "identity", Target: identity, Public: true},
		{Prefix: "/api/v1/rbac/catalogue", Service: "identity", Target: identity},

		// Company portal.
		{
			Prefix: "/api/v1/company", Service: "companies", Target: companies,
			Portals:        []tenancy.Portal{tenancy.PortalCompany},
			Principals:     []tenancy.PrincipalType{tenancy.PrincipalCompany},
			RequireCompany: true,
		},
		{
			Prefix: "/api/v1/jobs", Service: "jobs", Target: jobs,
			Portals:        []tenancy.Portal{tenancy.PortalCompany},
			Principals:     []tenancy.PrincipalType{tenancy.PrincipalCompany},
			RequireCompany: true,
		},
		{
			Prefix: "/api/v1/applications", Service: "applications", Target: applications,
			Portals:        []tenancy.Portal{tenancy.PortalCompany},
			Principals:     []tenancy.PrincipalType{tenancy.PrincipalCompany},
			RequireCompany: true,
		},
		{
			Prefix: "/api/v1/candidates", Service: "candidates", Target: candidates,
			Portals:        []tenancy.Portal{tenancy.PortalCompany},
			Principals:     []tenancy.PrincipalType{tenancy.PrincipalCompany},
			RequireCompany: true,
		},
		{
			// Talent discovery is served by the candidates service but kept on its
			// own prefix: it reads platform-wide profiles rather than the company's
			// own records, so the two deserve separate routes and separate
			// permissions.
			Prefix: "/api/v1/talent", Service: "candidates", Target: candidates,
			Portals:        []tenancy.Portal{tenancy.PortalCompany},
			Principals:     []tenancy.PrincipalType{tenancy.PrincipalCompany},
			RequireCompany: true,
		},
		{
			// A company administering its own people and roles. Identity owns the
			// data; the internal `/internal/companies/{id}/members` route stays
			// unrouted, because it takes the tenant from the URL and this one
			// takes it from the token.
			Prefix: "/api/v1/recruiters", Service: "identity", Target: identity,
			Portals:        []tenancy.Portal{tenancy.PortalCompany},
			Principals:     []tenancy.PrincipalType{tenancy.PrincipalCompany},
			RequireCompany: true,
		},
		{
			Prefix: "/api/v1/company-roles", Service: "identity", Target: identity,
			Portals:        []tenancy.Portal{tenancy.PortalCompany},
			Principals:     []tenancy.PrincipalType{tenancy.PrincipalCompany},
			RequireCompany: true,
		},
		{
			Prefix: "/api/v1/interviews", Service: "interviews", Target: interviews,
			Portals:        []tenancy.Portal{tenancy.PortalCompany},
			Principals:     []tenancy.PrincipalType{tenancy.PrincipalCompany},
			RequireCompany: true,
		},
		{
			Prefix: "/api/v1/offers", Service: "offers", Target: offers,
			Portals:        []tenancy.Portal{tenancy.PortalCompany},
			Principals:     []tenancy.PrincipalType{tenancy.PrincipalCompany},
			RequireCompany: true,
		},
		{
			// A tenant's own trail. The platform-wide feed is a separate prefix
			// on the root portal: one route serving both would be one edit away
			// from showing a company every other company's activity.
			Prefix: "/api/v1/company-audit", Service: "audit", Target: audit,
			Portals:        []tenancy.Portal{tenancy.PortalCompany},
			Principals:     []tenancy.PrincipalType{tenancy.PrincipalCompany},
			RequireCompany: true,
		},
		{
			Prefix: "/api/v1/support", Service: "support", Target: support,
			Portals:        []tenancy.Portal{tenancy.PortalCompany},
			Principals:     []tenancy.PrincipalType{tenancy.PrincipalCompany},
			RequireCompany: true,
		},
		{
			Prefix: "/api/v1/billing", Service: "subscriptions", Target: subscriptions,
			Portals:        []tenancy.Portal{tenancy.PortalCompany},
			Principals:     []tenancy.PrincipalType{tenancy.PrincipalCompany},
			RequireCompany: true,
		},

		// Candidate portal.
		{
			Prefix: "/api/v1/me", Service: "candidates", Target: candidates,
			Portals:    []tenancy.Portal{tenancy.PortalJobs},
			Principals: []tenancy.PrincipalType{tenancy.PrincipalCandidate},
		},
		{
			Prefix: "/api/v1/my-applications", Service: "applications", Target: applications,
			Portals:    []tenancy.Portal{tenancy.PortalJobs},
			Principals: []tenancy.PrincipalType{tenancy.PrincipalCandidate},
		},

		// The public job board and each company's careers page are unauthenticated.
		{
			Prefix: "/api/v1/public/jobs", Service: "jobs", Target: jobs,
			Portals: []tenancy.Portal{tenancy.PortalJobs, tenancy.PortalCompany, tenancy.PortalPublic},
			Public:  true,
		},
		{
			Prefix: "/api/v1/public/apply", Service: "applications", Target: applications,
			Portals: []tenancy.Portal{tenancy.PortalJobs, tenancy.PortalCompany},
			Public:  true,
		},

		// Messaging and notifications serve both sides of a conversation.
		{
			Prefix: "/api/v1/messages", Service: "messaging", Target: messaging,
			Portals:    []tenancy.Portal{tenancy.PortalCompany, tenancy.PortalJobs},
			Principals: []tenancy.PrincipalType{tenancy.PrincipalCompany, tenancy.PrincipalCandidate},
		},
		{
			// The candidate half of messaging gets its own prefix on the jobs
			// portal. Sharing /api/v1/messages would mean one route carrying two
			// principal types, and the portal boundary is drawn by route.
			Prefix: "/api/v1/my-conversations", Service: "messaging", Target: messaging,
			Portals:    []tenancy.Portal{tenancy.PortalJobs},
			Principals: []tenancy.PrincipalType{tenancy.PrincipalCandidate},
		},
		{
			Prefix: "/api/v1/notifications", Service: "notifications", Target: notifications,
			Portals: []tenancy.Portal{tenancy.PortalCompany, tenancy.PortalJobs, tenancy.PortalRoot},
		},

		// Platform administration.
		{
			Prefix: "/api/v1/admin", Service: "admin", Target: admin,
			Portals:    []tenancy.Portal{tenancy.PortalRoot},
			Principals: []tenancy.PrincipalType{tenancy.PrincipalPlatform},
		},
		{
			Prefix: "/api/v1/plans", Service: "subscriptions", Target: subscriptions,
			Portals:    []tenancy.Portal{tenancy.PortalRoot, tenancy.PortalPublic},
			Principals: []tenancy.PrincipalType{tenancy.PrincipalPlatform},
		},
		{
			Prefix: "/api/v1/payments", Service: "payments", Target: payments,
			Portals:    []tenancy.Portal{tenancy.PortalRoot, tenancy.PortalCompany},
			Principals: []tenancy.PrincipalType{tenancy.PrincipalPlatform, tenancy.PrincipalCompany},
		},
		{
			Prefix: "/api/v1/subscriptions", Service: "subscriptions", Target: subscriptions,
			Portals:    []tenancy.Portal{tenancy.PortalRoot},
			Principals: []tenancy.PrincipalType{tenancy.PrincipalPlatform},
		},

		// The platform's view of tenant-owned data. These are deliberately
		// separate prefixes from the company-facing ones rather than the same
		// route widened to two principal types: a route that serves both is one
		// edit away from serving a company principal every tenant's records.
		{
			Prefix: "/api/v1/platform/companies", Service: "companies", Target: companies,
			Portals:    []tenancy.Portal{tenancy.PortalRoot},
			Principals: []tenancy.PrincipalType{tenancy.PrincipalPlatform},
		},
		{
			Prefix: "/api/v1/platform/support", Service: "support", Target: support,
			Portals:    []tenancy.Portal{tenancy.PortalRoot},
			Principals: []tenancy.PrincipalType{tenancy.PrincipalPlatform},
		},
		{
			Prefix: "/api/v1/platform/audit", Service: "audit", Target: audit,
			Portals:    []tenancy.Portal{tenancy.PortalRoot},
			Principals: []tenancy.PrincipalType{tenancy.PrincipalPlatform},
		},

		// A company's public careers page and the pricing page. Both are read by
		// visitors with no account at all, so they are public by route rather
		// than by a handler deciding to skip a check.
		{
			Prefix: "/api/v1/public/company", Service: "companies", Target: companies,
			Portals: []tenancy.Portal{tenancy.PortalCompany, tenancy.PortalPublic},
			Public:  true,
		},
		{
			Prefix: "/api/v1/public/plans", Service: "subscriptions", Target: subscriptions,
			Portals: []tenancy.Portal{tenancy.PortalPublic, tenancy.PortalRoot, tenancy.PortalCompany},
			Public:  true,
		},

		// Provider callbacks carry their own signature and never a session.
		{Prefix: "/api/v1/webhooks/payments", Service: "payments", Target: payments, Public: true},

		// Company registration happens before any tenant exists.
		{Prefix: "/api/v1/register/company", Service: "companies", Target: companies, Public: true},
	}

	// Longest prefix wins, so a specific route is never shadowed by a general one.
	sort.SliceStable(routes, func(i, j int) bool {
		return len(routes[i].Prefix) > len(routes[j].Prefix)
	})

	return &Table{routes: routes}
}

// Match finds the route serving a path.
func (t *Table) Match(path string) (Route, bool) {
	for _, route := range t.routes {
		if path == route.Prefix || strings.HasPrefix(path, route.Prefix+"/") {
			return route, true
		}
	}
	return Route{}, false
}

// Routes returns every configured route.
func (t *Table) Routes() []Route { return t.routes }

// AllowsPortal reports whether a route may be reached from a portal.
func (r Route) AllowsPortal(portal tenancy.Portal) bool {
	if len(r.Portals) == 0 {
		return true
	}
	for _, allowed := range r.Portals {
		if allowed == portal {
			return true
		}
	}
	return false
}

// AllowsPrincipal reports whether a principal type may use a route.
func (r Route) AllowsPrincipal(principal tenancy.PrincipalType) bool {
	if len(r.Principals) == 0 {
		return true
	}
	for _, allowed := range r.Principals {
		if allowed == principal {
			return true
		}
	}
	return false
}
