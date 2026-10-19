package routing

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/karmarat/platform/packages/goshared/tenancy"
)

// newSortedTable builds a table from arbitrary routes with the same ordering
// NewTable applies, so the matcher can be exercised against prefix shapes the
// production table does not happen to contain yet.
//
// TestNewTableIsOrderedByPrefixLength is what keeps this ordering honest
// against the real table.
func newSortedTable(routes []Route) *Table {
	sorted := make([]Route, len(routes))
	copy(sorted, routes)
	sort.SliceStable(sorted, func(i, j int) bool {
		return len(sorted[i].Prefix) > len(sorted[j].Prefix)
	})
	return &Table{routes: sorted}
}

// The routing table is the platform's outer boundary: a route that is reachable
// from the wrong portal is a tenant isolation failure, not a routing bug. These
// tests pin that boundary rather than the mechanics of prefix matching alone.

func TestTableMatch(t *testing.T) {
	t.Setenv("JOBS_URL", "http://jobs.test")
	t.Setenv("IDENTITY_URL", "http://identity.test")

	table := NewTable()

	tests := []struct {
		name        string
		path        string
		wantMatch   bool
		wantService string
		wantPrefix  string
	}{
		{name: "exact prefix", path: "/api/v1/jobs", wantMatch: true, wantService: "jobs", wantPrefix: "/api/v1/jobs"},
		{name: "child path", path: "/api/v1/jobs/job_123", wantMatch: true, wantService: "jobs", wantPrefix: "/api/v1/jobs"},
		{name: "deep child path", path: "/api/v1/jobs/job_123/applications", wantMatch: true, wantService: "jobs", wantPrefix: "/api/v1/jobs"},

		// The separator matters: without it "/api/v1/jobsecret" would be served
		// by the jobs route and a future "/api/v1/jobseekers" service would be
		// silently shadowed.
		{name: "prefix is not a substring match", path: "/api/v1/jobsecret", wantMatch: false},
		{name: "sibling of a prefix", path: "/api/v1/job", wantMatch: false},

		{name: "public board", path: "/api/v1/public/jobs", wantMatch: true, wantService: "jobs", wantPrefix: "/api/v1/public/jobs"},
		{name: "public apply", path: "/api/v1/public/apply/job_1", wantMatch: true, wantService: "applications", wantPrefix: "/api/v1/public/apply"},
		{name: "jwks", path: "/.well-known/jwks.json", wantMatch: true, wantService: "identity", wantPrefix: "/.well-known/jwks.json"},

		{name: "unknown path", path: "/api/v1/nope", wantMatch: false},
		{name: "root", path: "/", wantMatch: false},
		{name: "empty", path: "", wantMatch: false},
		{name: "probe outside the api surface", path: "/etc/passwd", wantMatch: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			route, matched := table.Match(tc.path)

			if matched != tc.wantMatch {
				t.Fatalf("Match(%q) matched = %v, want %v", tc.path, matched, tc.wantMatch)
			}
			if !tc.wantMatch {
				return
			}
			if route.Service != tc.wantService {
				t.Errorf("Match(%q) service = %q, want %q", tc.path, route.Service, tc.wantService)
			}
			if route.Prefix != tc.wantPrefix {
				t.Errorf("Match(%q) prefix = %q, want %q", tc.path, route.Prefix, tc.wantPrefix)
			}
		})
	}
}

// TestTableMatchLongestPrefixWins covers the case the production table does not
// currently contain but the sort exists to protect: a specific route nested
// under a general one must not be shadowed by it.
func TestTableMatchLongestPrefixWins(t *testing.T) {
	table := newSortedTable([]Route{
		{Prefix: "/api/v1/jobs", Service: "jobs"},
		{Prefix: "/api/v1/jobs/archive", Service: "archive"},
		{Prefix: "/api/v1/jobs/archive/exports", Service: "exports"},
	})

	tests := []struct {
		path        string
		wantService string
	}{
		{path: "/api/v1/jobs", wantService: "jobs"},
		{path: "/api/v1/jobs/job_1", wantService: "jobs"},
		{path: "/api/v1/jobs/archive", wantService: "archive"},
		{path: "/api/v1/jobs/archive/2024", wantService: "archive"},
		{path: "/api/v1/jobs/archive/exports", wantService: "exports"},
		{path: "/api/v1/jobs/archive/exports/csv", wantService: "exports"},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			route, matched := table.Match(tc.path)
			if !matched {
				t.Fatalf("Match(%q) did not match", tc.path)
			}
			if route.Service != tc.wantService {
				t.Errorf("Match(%q) service = %q, want %q", tc.path, route.Service, tc.wantService)
			}
		})
	}
}

// TestNewTableIsOrderedByPrefixLength pins the invariant Match depends on:
// the first matching route in the slice must also be the most specific one.
func TestNewTableIsOrderedByPrefixLength(t *testing.T) {
	routes := NewTable().Routes()

	for i := 1; i < len(routes); i++ {
		if len(routes[i-1].Prefix) < len(routes[i].Prefix) {
			t.Fatalf("route %q (len %d) sorts before the longer %q (len %d)",
				routes[i-1].Prefix, len(routes[i-1].Prefix),
				routes[i].Prefix, len(routes[i].Prefix))
		}
	}
}

func TestRouteAllowsPortal(t *testing.T) {
	table := NewTable()

	tests := []struct {
		name   string
		path   string
		portal tenancy.Portal
		want   bool
	}{
		// A company route exists only on a company portal. Reaching it from the
		// candidate job board or the admin console is the attack this prevents.
		{name: "company route on company portal", path: "/api/v1/jobs", portal: tenancy.PortalCompany, want: true},
		{name: "company route on jobs portal", path: "/api/v1/jobs", portal: tenancy.PortalJobs, want: false},
		{name: "company route on root portal", path: "/api/v1/jobs", portal: tenancy.PortalRoot, want: false},
		{name: "company route on public portal", path: "/api/v1/jobs", portal: tenancy.PortalPublic, want: false},

		{name: "applications on jobs portal", path: "/api/v1/applications", portal: tenancy.PortalJobs, want: false},
		{name: "candidates on root portal", path: "/api/v1/candidates", portal: tenancy.PortalRoot, want: false},
		{name: "billing on root portal", path: "/api/v1/billing", portal: tenancy.PortalRoot, want: false},

		// Admin is the mirror image: root only, never a tenant's portal.
		{name: "admin on root portal", path: "/api/v1/admin", portal: tenancy.PortalRoot, want: true},
		{name: "admin on company portal", path: "/api/v1/admin", portal: tenancy.PortalCompany, want: false},
		{name: "admin on jobs portal", path: "/api/v1/admin", portal: tenancy.PortalJobs, want: false},

		{name: "candidate profile on jobs portal", path: "/api/v1/me", portal: tenancy.PortalJobs, want: true},
		{name: "candidate profile on company portal", path: "/api/v1/me", portal: tenancy.PortalCompany, want: false},

		// Auth declares no portals: every front door has a sign-in.
		{name: "auth on company portal", path: "/api/v1/auth/login", portal: tenancy.PortalCompany, want: true},
		{name: "auth on jobs portal", path: "/api/v1/auth/login", portal: tenancy.PortalJobs, want: true},
		{name: "auth on root portal", path: "/api/v1/auth/login", portal: tenancy.PortalRoot, want: true},
		{name: "auth on public portal", path: "/api/v1/auth/login", portal: tenancy.PortalPublic, want: true},

		{name: "public jobs on jobs portal", path: "/api/v1/public/jobs", portal: tenancy.PortalJobs, want: true},
		{name: "public jobs on company portal", path: "/api/v1/public/jobs", portal: tenancy.PortalCompany, want: true},
		{name: "public jobs on root portal", path: "/api/v1/public/jobs", portal: tenancy.PortalRoot, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			route, matched := table.Match(tc.path)
			if !matched {
				t.Fatalf("no route matches %q", tc.path)
			}
			if got := route.AllowsPortal(tc.portal); got != tc.want {
				t.Errorf("%q AllowsPortal(%q) = %v, want %v", route.Prefix, tc.portal, got, tc.want)
			}
		})
	}
}

func TestRouteAllowsPrincipal(t *testing.T) {
	table := NewTable()

	tests := []struct {
		name      string
		path      string
		principal tenancy.PrincipalType
		want      bool
	}{
		{name: "recruiter on company route", path: "/api/v1/jobs", principal: tenancy.PrincipalCompany, want: true},
		{name: "candidate on company route", path: "/api/v1/jobs", principal: tenancy.PrincipalCandidate, want: false},
		{name: "platform staff on company route", path: "/api/v1/jobs", principal: tenancy.PrincipalPlatform, want: false},

		{name: "candidate on candidate route", path: "/api/v1/my-applications", principal: tenancy.PrincipalCandidate, want: true},
		{name: "recruiter on candidate route", path: "/api/v1/my-applications", principal: tenancy.PrincipalCompany, want: false},

		{name: "platform staff on admin route", path: "/api/v1/admin", principal: tenancy.PrincipalPlatform, want: true},
		{name: "recruiter on admin route", path: "/api/v1/admin", principal: tenancy.PrincipalCompany, want: false},

		// Messaging serves both sides of a conversation, so it names both.
		{name: "recruiter on messaging", path: "/api/v1/messages", principal: tenancy.PrincipalCompany, want: true},
		{name: "candidate on messaging", path: "/api/v1/messages", principal: tenancy.PrincipalCandidate, want: true},
		{name: "platform staff on messaging", path: "/api/v1/messages", principal: tenancy.PrincipalPlatform, want: false},

		// An unrestricted route accepts any authenticated principal.
		{name: "any principal on notifications", path: "/api/v1/notifications", principal: tenancy.PrincipalCandidate, want: true},
		{name: "any principal on auth", path: "/api/v1/auth/login", principal: tenancy.PrincipalPlatform, want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			route, matched := table.Match(tc.path)
			if !matched {
				t.Fatalf("no route matches %q", tc.path)
			}
			if got := route.AllowsPrincipal(tc.principal); got != tc.want {
				t.Errorf("%q AllowsPrincipal(%q) = %v, want %v", route.Prefix, tc.principal, got, tc.want)
			}
		})
	}
}

// TestTenantScopedRoutesAreCompanyOnly guards the table against a future entry
// that requires a tenant but forgets to restrict the portal or the principal —
// the combination that would expose one company's data on another front door.
func TestTenantScopedRoutesAreCompanyOnly(t *testing.T) {
	for _, route := range NewTable().Routes() {
		if !route.RequireCompany {
			continue
		}

		t.Run(route.Prefix, func(t *testing.T) {
			if route.Public {
				t.Errorf("route requires a tenant but is marked public")
			}
			if !route.AllowsPortal(tenancy.PortalCompany) {
				t.Errorf("route requires a tenant but is not exposed on the company portal")
			}
			for _, portal := range []tenancy.Portal{tenancy.PortalRoot, tenancy.PortalJobs, tenancy.PortalPublic} {
				if route.AllowsPortal(portal) {
					t.Errorf("route requires a tenant but is reachable from the %q portal", portal)
				}
			}
			for _, principal := range []tenancy.PrincipalType{tenancy.PrincipalPlatform, tenancy.PrincipalCandidate, tenancy.PrincipalAnonymous} {
				if route.AllowsPrincipal(principal) {
					t.Errorf("route requires a tenant but accepts a %q principal", principal)
				}
			}
		})
	}
}

// TestPublicRoutesAreDeliberate lists the routes reachable without a session.
// Adding one should be a conscious edit to this list, not a silent change.
func TestPublicRoutesAreDeliberate(t *testing.T) {
	expected := map[string]bool{
		// Sign-in, and the key every service verifies tokens against.
		"/api/v1/auth":           true,
		"/.well-known/jwks.json": true,

		// Read by visitors with no account: the job boards, a company's careers
		// page, and the pricing page. Each returns a deliberately reduced view —
		// the handlers build a public shape rather than filtering a private one.
		"/api/v1/public/jobs":    true,
		"/api/v1/public/company": true,
		"/api/v1/public/plans":   true,

		// Applying requires a signed-in candidate; "public" here is the route's
		// name, not its guard, because the tie between an application and one
		// person is what makes the apply-once rule enforceable.
		"/api/v1/public/apply": true,

		// Carries a provider signature over the raw body instead of a session.
		"/api/v1/webhooks/payments": true,

		// Registration necessarily happens before any tenant exists.
		"/api/v1/register/company": true,
	}

	actual := map[string]bool{}
	for _, route := range NewTable().Routes() {
		if route.Public {
			actual[route.Prefix] = true
		}
	}

	for prefix := range actual {
		if !expected[prefix] {
			t.Errorf("route %q is public but is not in the expected set", prefix)
		}
	}
	for prefix := range expected {
		if !actual[prefix] {
			t.Errorf("route %q was expected to be public but is not", prefix)
		}
	}
}

// TestServiceURLDefaultsMatchTheDocumentedPorts pins the fallback addresses to
// the ports every other part of the system uses.
//
// This is not pedantry. A fallback that points at the wrong port does not fail
// closed: the gateway happily forwards to whichever service is listening there,
// and it forwards the caller's already-verified principal headers with it. A
// request authorized for jobs then arrives at candidates carrying company
// permissions. The defaults and .env.example disagreed for six services before
// this test existed.
func TestServiceURLDefaultsMatchTheDocumentedPorts(t *testing.T) {
	// Parsed from .env.example so the test reads the same source an operator
	// does, rather than a second hand-maintained copy of the truth.
	documented := parseDocumentedPorts(t)

	// Clear every override so NewTable falls back to its literals.
	for name := range documented {
		t.Setenv(name, "")
	}

	table := NewTable()
	seen := map[string]string{}
	for _, route := range table.Routes() {
		seen[route.Service] = route.Target
	}

	for _, service := range []string{
		"identity", "companies", "subscriptions", "payments", "jobs",
		"applications", "candidates", "messaging", "notifications",
		"support", "admin",
	} {
		target, routed := seen[service]
		if !routed {
			continue // not every service has a route yet
		}
		want := documented[strings.ToUpper(service)+"_URL"]
		if want == "" {
			t.Fatalf("%s is routed but .env.example does not document %s_URL",
				service, strings.ToUpper(service))
		}
		if target != want {
			t.Errorf("%s routes to %s, but .env.example documents %s", service, target, want)
		}
	}
}

func parseDocumentedPorts(t *testing.T) map[string]string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", ".env.example"))
	if err != nil {
		t.Skipf("cannot read .env.example: %v", err)
	}

	ports := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		name, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if !found || !strings.HasSuffix(name, "_URL") {
			continue
		}
		ports[name] = value
	}
	if len(ports) == 0 {
		t.Fatal(".env.example documents no *_URL settings; the test cannot check anything")
	}
	return ports
}

// TestCompanyRolesIsNotSwallowedByTheCompanyPrefix pins a collision that string
// prefixes make easy to create.
//
// `/api/v1/company-roles` and `/api/v1/company` share eleven characters. A match
// on bare `strings.HasPrefix` would send every role request to the companies
// service, which serves a tenant's profile and knows nothing about roles — and
// it would arrive there carrying a verified company principal, so it would fail
// as a confusing 404 rather than as an authorization error.
func TestCompanyRolesIsNotSwallowedByTheCompanyPrefix(t *testing.T) {
	table := NewTable()

	cases := map[string]string{
		"/api/v1/company-roles":           "identity",
		"/api/v1/company-roles/role_1":    "identity",
		"/api/v1/company/profile":         "companies",
		"/api/v1/company":                 "companies",
		"/api/v1/recruiters":              "identity",
		"/api/v1/recruiters/acc_1/roles":  "identity",
		"/api/v1/recruiters/acc_1/status": "identity",
	}

	for path, wantService := range cases {
		route, ok := table.Match(path)
		if !ok {
			t.Errorf("%s: no route matched", path)
			continue
		}
		if route.Service != wantService {
			t.Errorf("%s routed to %q, want %q", path, route.Service, wantService)
		}
	}
}
