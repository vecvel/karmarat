package tenancy

import (
	"context"
	"strings"
)

// Portal identifies which of the platform's front doors a request arrived at.
type Portal string

const (
	// PortalPublic is the marketing site at {hostname}.
	PortalPublic Portal = "public"
	// PortalRoot is the platform administration console at root.{hostname}.
	PortalRoot Portal = "root"
	// PortalJobs is the candidate job portal at jobs.{hostname}.
	PortalJobs Portal = "jobs"
	// PortalCompany is a company's careers portal at {slug}.{hostname}.
	PortalCompany Portal = "company"
)

// HostContext is the result of resolving an incoming Host header.
type HostContext struct {
	Portal Portal
	// CompanySlug is set only for PortalCompany.
	CompanySlug string
	// Host is the normalized hostname the request arrived on.
	Host string
}

// reservedSubdomains can never be claimed as a company slug.
var reservedSubdomains = map[string]Portal{
	"root":  PortalRoot,
	"admin": PortalRoot,
	"jobs":  PortalJobs,
	"www":   PortalPublic,
}

// ReservedSlugs lists subdomains a company may not register, so slug validation
// and host routing can never disagree about what is reserved.
var ReservedSlugs = []string{
	"root", "admin", "jobs", "www", "api", "app", "cdn", "static", "assets",
	"mail", "smtp", "ftp", "status", "help", "support", "docs", "blog",
	"dashboard", "portal", "auth", "login", "signup", "billing", "payments",
	"internal", "system", "platform", "karmarat",
}

// IsReservedSlug reports whether a slug is claimed by the platform.
func IsReservedSlug(slug string) bool {
	slug = strings.ToLower(strings.TrimSpace(slug))
	for _, reserved := range ReservedSlugs {
		if slug == reserved {
			return true
		}
	}
	return false
}

// ResolveHost maps an incoming Host header onto a portal.
//
// Routing by hostname rather than by path means a company's portal, the
// candidate portal, and the admin console cannot be reached through each other's
// URLs, and the tenant is established before any handler runs.
func ResolveHost(host, platformHostname string) HostContext {
	host = normalizeHost(host)
	platformHostname = normalizeHost(platformHostname)

	if host == "" || host == platformHostname {
		return HostContext{Portal: PortalPublic, Host: host}
	}

	suffix := "." + platformHostname
	if !strings.HasSuffix(host, suffix) {
		// An unknown host (a custom domain, or a direct IP) is treated as public.
		return HostContext{Portal: PortalPublic, Host: host}
	}

	subdomain := strings.TrimSuffix(host, suffix)
	if subdomain == "" {
		return HostContext{Portal: PortalPublic, Host: host}
	}

	// Only the left-most label is meaningful; deeper nesting is not routable.
	if strings.Contains(subdomain, ".") {
		parts := strings.Split(subdomain, ".")
		subdomain = parts[len(parts)-1]
	}

	if portal, reserved := reservedSubdomains[subdomain]; reserved {
		return HostContext{Portal: portal, Host: host}
	}

	return HostContext{Portal: PortalCompany, CompanySlug: subdomain, Host: host}
}

func normalizeHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	// Drop the port, and any IPv6 brackets around the address.
	if idx := strings.LastIndex(host, ":"); idx != -1 && !strings.Contains(host[idx:], "]") {
		host = host[:idx]
	}
	host = strings.Trim(host, "[]")
	return strings.TrimSuffix(host, ".")
}

type hostKey struct{}

// WithHost stores the resolved host context on the request context.
func WithHost(ctx context.Context, hc HostContext) context.Context {
	return context.WithValue(ctx, hostKey{}, hc)
}

// HostFromContext returns the resolved host context for the request.
func HostFromContext(ctx context.Context) (HostContext, bool) {
	hc, ok := ctx.Value(hostKey{}).(HostContext)
	return hc, ok
}
