// Package rbac is the platform's permission catalogue.
//
// Permissions are `<feature>.<action>` strings, exactly as in the company
// portal. Features are registered here once; the role editor, the token issuer,
// and every service guard read from this single list, so adding a capability is
// a registry entry rather than a change to the authorization system.
package rbac

import (
	"fmt"
	"sort"
	"strings"
)

// Scope separates what the platform's own staff may do from what a company's
// users may do inside their tenant. A platform role can never carry a company
// permission, which is what keeps support staff out of customer pipelines.
type Scope string

const (
	ScopePlatform  Scope = "platform"
	ScopeCompany   Scope = "company"
	ScopeCandidate Scope = "candidate"
)

// Action is one capability a feature exposes.
type Action struct {
	Name        string
	Label       string
	Description string
	// Sensitive actions are highlighted in the permission matrix.
	Sensitive bool
}

// Feature is a unit of functionality that owns a set of permissions.
type Feature struct {
	Key         string
	Name        string
	Description string
	Scope       Scope
	Group       string
	Order       int
	Actions     []Action
}

// Permission is a feature/action pair flattened into its key.
type Permission struct {
	Key         string
	FeatureKey  string
	FeatureName string
	Action      string
	Label       string
	Description string
	Scope       Scope
	Group       string
	Sensitive   bool
}

var crud = []Action{
	{Name: "create", Label: "Create", Description: "Create new records"},
	{Name: "read", Label: "Read", Description: "View and list records"},
	{Name: "update", Label: "Update", Description: "Edit existing records"},
	{Name: "delete", Label: "Delete", Description: "Permanently remove records", Sensitive: true},
}

func withCrud(extra ...Action) []Action {
	actions := make([]Action, 0, len(crud)+len(extra))
	actions = append(actions, crud...)
	actions = append(actions, extra...)
	return actions
}

// features is the registry. Adding an entry is all that is needed to make a new
// capability configurable for every role in its scope.
var features = []Feature{
	// -------------------------------------------------------------- company --
	{
		Key: "jobs", Name: "Job Requisitions", Scope: ScopeCompany, Group: "recruitment", Order: 1,
		Description: "Openings, descriptions, salary bands, and the publish workflow",
		Actions: withCrud(
			Action{Name: "publish_portal", Label: "Publish to Company Portal",
				Description: "Make a job visible on the company's own careers portal", Sensitive: true},
			Action{Name: "publish_network", Label: "Publish to Karmarat Jobs",
				Description: "List a job on the shared candidate job portal", Sensitive: true},
			Action{Name: "manage_form", Label: "Configure Application Form",
				Description: "Define the custom fields and questions applicants must answer"},
			Action{Name: "duplicate", Label: "Duplicate Requisition", Description: "Clone a job into a new draft"},
			Action{Name: "export", Label: "Export Requisitions", Description: "Download the requisition list"},
		),
	},
	{
		Key: "applications", Name: "Applications & Pipeline", Scope: ScopeCompany, Group: "recruitment", Order: 2,
		Description: "Applicant tracking, screening, and stage progression",
		Actions: withCrud(
			Action{Name: "advance_stage", Label: "Advance Pipeline Stage",
				Description: "Move an applicant between recruitment stages", Sensitive: true},
			Action{Name: "reject", Label: "Reject Applicants",
				Description: "Reject an application with a recorded reason", Sensitive: true},
			Action{Name: "manage_stages", Label: "Manage Pipeline Stages",
				Description: "Define the stages applications move through"},
			Action{Name: "bulk_update", Label: "Bulk Stage Actions", Description: "Apply a change to many applications"},
			Action{Name: "export", Label: "Export Applications", Description: "Download the pipeline"},
		),
	},
	{
		Key: "candidates", Name: "Candidates & Talent Pool", Scope: ScopeCompany, Group: "recruitment", Order: 3,
		Description: "Applicant profiles, resumes, and the company's own talent pool",
		Actions: withCrud(
			Action{Name: "download_resume", Label: "Download Resumes",
				Description: "Retrieve a candidate's uploaded CV", Sensitive: true},
			Action{Name: "export", Label: "Export Candidates", Description: "Download the candidate directory"},
		),
	},
	{
		Key: "talent_search", Name: "Talent Discovery", Scope: ScopeCompany, Group: "recruitment", Order: 4,
		Description: "Search publicly discoverable candidates on the Karmarat network",
		Actions: []Action{
			{Name: "search", Label: "Search Candidate Network",
				Description: "Find candidates who made their profile discoverable"},
			{Name: "approach", Label: "Approach Candidates",
				Description: "Open a direct conversation with a discoverable candidate", Sensitive: true},
		},
	},
	{
		Key: "interviews", Name: "Interviews & Panels", Scope: ScopeCompany, Group: "recruitment", Order: 5,
		Description: "Scheduling, interview rounds, and evaluation scorecards",
		Actions: withCrud(
			Action{Name: "submit_scorecard", Label: "Submit Scorecards", Description: "Record structured interview feedback"},
			Action{Name: "view_scorecards", Label: "View All Scorecards",
				Description: "Read feedback submitted by other interviewers", Sensitive: true},
		),
	},
	{
		Key: "offers", Name: "Offers", Scope: ScopeCompany, Group: "recruitment", Order: 6,
		Description: "Offer letters, compensation packages, and approvals",
		Actions: withCrud(
			Action{Name: "approve", Label: "Approve Offers", Description: "Sign off on a compensation package", Sensitive: true},
			Action{Name: "send", Label: "Send Offers", Description: "Dispatch an approved offer", Sensitive: true},
			Action{Name: "view_compensation", Label: "View Compensation",
				Description: "See salary, bonus, and equity figures", Sensitive: true},
		),
	},
	{
		Key: "messaging", Name: "Candidate Messaging", Scope: ScopeCompany, Group: "recruitment", Order: 7,
		Description: "Direct conversations with applicants and sourced candidates",
		Actions: []Action{
			{Name: "read", Label: "Read Conversations", Description: "View message threads"},
			{Name: "send", Label: "Send Messages", Description: "Reply to and start conversations", Sensitive: true},
			{Name: "read_all", Label: "Read All Conversations",
				Description: "View threads owned by other recruiters", Sensitive: true},
		},
	},
	{
		Key: "reports", Name: "Reports & Analytics", Scope: ScopeCompany, Group: "recruitment", Order: 8,
		Description: "Time to hire, source funnel, and recruiter performance",
		Actions: []Action{
			{Name: "read", Label: "View Reports", Description: "Access recruitment analytics"},
			{Name: "export", Label: "Export Reports", Description: "Download analytics datasets"},
		},
	},
	{
		Key: "company_profile", Name: "Company Profile", Scope: ScopeCompany, Group: "administration", Order: 20,
		Description: "Company identity, branding, and careers portal configuration",
		Actions: []Action{
			{Name: "read", Label: "View Company Profile", Description: "See company details"},
			{Name: "update", Label: "Edit Company Profile", Description: "Change company details and branding"},
		},
	},
	{
		Key: "recruiters", Name: "Team & Recruiters", Scope: ScopeCompany, Group: "administration", Order: 21,
		Description: "The company's own users and their access",
		Actions: withCrud(
			Action{Name: "invite", Label: "Invite Team Members", Description: "Send an invitation to join the company"},
			Action{Name: "assign_roles", Label: "Assign Roles", Description: "Grant or remove a member's roles", Sensitive: true},
			Action{Name: "manage_status", Label: "Suspend / Restore Members",
				Description: "Control whether a member can sign in", Sensitive: true},
		),
	},
	{
		Key: "company_roles", Name: "Roles & Permissions", Scope: ScopeCompany, Group: "administration", Order: 22,
		Description: "Custom roles and the permission matrix for this company",
		Actions: withCrud(
			Action{Name: "assign_permissions", Label: "Grant & Revoke Permissions",
				Description: "Change what a role may do", Sensitive: true},
		),
	},
	{
		Key: "billing", Name: "Subscription & Billing", Scope: ScopeCompany, Group: "administration", Order: 23,
		Description: "The company's plan, invoices, and payment methods",
		Actions: []Action{
			{Name: "read", Label: "View Subscription", Description: "See the current plan and invoices"},
			{Name: "manage", Label: "Manage Subscription",
				Description: "Change plan, update payment method, or cancel", Sensitive: true},
		},
	},
	{
		Key: "support", Name: "Help & Support", Scope: ScopeCompany, Group: "administration", Order: 24,
		Description: "Support tickets raised with the Karmarat platform",
		Actions: []Action{
			{Name: "read", Label: "View Tickets", Description: "See the company's support requests"},
			{Name: "create", Label: "Raise Tickets", Description: "Open a new support request"},
			{Name: "reply", Label: "Reply to Tickets", Description: "Respond on an existing request"},
		},
	},
	{
		Key: "company_audit", Name: "Audit Trail", Scope: ScopeCompany, Group: "administration", Order: 25,
		Description: "Record of changes made inside the company account",
		Actions: []Action{
			{Name: "read", Label: "View Audit Trail", Description: "Inspect recorded activity"},
			{Name: "export", Label: "Export Audit Trail", Description: "Download audit events"},
		},
	},

	// ------------------------------------------------------------- platform --
	{
		Key: "platform_companies", Name: "Companies", Scope: ScopePlatform, Group: "tenants", Order: 1,
		Description: "Registered companies and their lifecycle",
		Actions: withCrud(
			Action{Name: "approve", Label: "Approve Registrations",
				Description: "Admit a newly registered company", Sensitive: true},
			Action{Name: "suspend", Label: "Suspend Companies",
				Description: "Revoke a company's access to its portal", Sensitive: true},
			Action{Name: "impersonate", Label: "Enter Company Account",
				Description: "Open a time-boxed, fully audited support session inside a tenant", Sensitive: true},
		),
	},
	{
		Key: "platform_candidates", Name: "Candidates", Scope: ScopePlatform, Group: "tenants", Order: 2,
		Description: "Candidate accounts registered on the jobs portal",
		Actions: withCrud(
			Action{Name: "suspend", Label: "Suspend Candidates",
				Description: "Revoke a candidate's access", Sensitive: true},
		),
	},
	{
		Key: "plans", Name: "SaaS Plans", Scope: ScopePlatform, Group: "billing", Order: 10,
		Description: "Subscription plans, pricing, and durations",
		Actions: withCrud(
			Action{Name: "publish", Label: "Publish Plans", Description: "Offer a plan to companies", Sensitive: true},
		),
	},
	{
		Key: "subscriptions", Name: "Subscriptions", Scope: ScopePlatform, Group: "billing", Order: 11,
		Description: "Company subscriptions and their state",
		Actions: withCrud(
			Action{Name: "override", Label: "Override Subscription",
				Description: "Grant, extend, or end a subscription outside of billing", Sensitive: true},
		),
	},
	{
		Key: "payments", Name: "Payments", Scope: ScopePlatform, Group: "billing", Order: 12,
		Description: "Transactions, invoices, and gateway configuration",
		Actions: []Action{
			{Name: "read", Label: "View Payments", Description: "Inspect transactions and invoices"},
			{Name: "refund", Label: "Issue Refunds", Description: "Refund a transaction", Sensitive: true},
			{Name: "configure", Label: "Configure Gateway",
				Description: "Manage payment provider credentials", Sensitive: true},
		},
	},
	{
		Key: "platform_support", Name: "Support Desk", Scope: ScopePlatform, Group: "operations", Order: 20,
		Description: "Tickets raised by companies",
		Actions: []Action{
			{Name: "read", Label: "View Tickets", Description: "See incoming support requests"},
			{Name: "reply", Label: "Reply to Tickets", Description: "Respond to a company"},
			{Name: "assign", Label: "Assign Tickets", Description: "Route a ticket to a colleague"},
			{Name: "close", Label: "Close Tickets", Description: "Resolve a support request"},
		},
	},
	{
		Key: "platform_users", Name: "Platform Staff", Scope: ScopePlatform, Group: "administration", Order: 30,
		Description: "Accounts that operate the Karmarat platform",
		Actions: withCrud(
			Action{Name: "assign_roles", Label: "Assign Staff Roles",
				Description: "Grant or remove a staff member's roles", Sensitive: true},
			Action{Name: "manage_status", Label: "Suspend / Restore Staff",
				Description: "Control whether a staff account can sign in", Sensitive: true},
		),
	},
	{
		Key: "platform_roles", Name: "Platform Roles", Scope: ScopePlatform, Group: "administration", Order: 31,
		Description: "Roles and permissions for platform staff",
		Actions: withCrud(
			Action{Name: "assign_permissions", Label: "Grant & Revoke Permissions",
				Description: "Change what a staff role may do", Sensitive: true},
		),
	},
	{
		Key: "platform_settings", Name: "Platform Configuration", Scope: ScopePlatform, Group: "administration", Order: 32,
		Description: "Global settings, branding, and feature availability",
		Actions: []Action{
			{Name: "read", Label: "View Configuration", Description: "See platform settings"},
			{Name: "update", Label: "Change Configuration", Description: "Modify platform settings", Sensitive: true},
		},
	},
	{
		Key: "platform_audit", Name: "Platform Audit Trail", Scope: ScopePlatform, Group: "administration", Order: 33,
		Description: "Record of every platform-level action",
		Actions: []Action{
			{Name: "read", Label: "View Audit Trail", Description: "Inspect platform activity"},
			{Name: "export", Label: "Export Audit Trail", Description: "Download audit events"},
		},
	},

	// ------------------------------------------------------------ candidate --
	{
		Key: "candidate_profile", Name: "My Profile", Scope: ScopeCandidate, Group: "candidate", Order: 1,
		Description: "The candidate's own profile, resume, and visibility",
		Actions: []Action{
			{Name: "read", Label: "View Profile", Description: "See own profile"},
			{Name: "update", Label: "Edit Profile", Description: "Change own profile and documents"},
			{Name: "manage_visibility", Label: "Control Discoverability",
				Description: "Choose whether recruiters can find this profile"},
			{Name: "delete", Label: "Delete Account", Description: "Permanently remove the account", Sensitive: true},
		},
	},
	{
		Key: "candidate_applications", Name: "My Applications", Scope: ScopeCandidate, Group: "candidate", Order: 2,
		Description: "Jobs the candidate has applied to and their status",
		Actions: []Action{
			{Name: "read", Label: "View Applications", Description: "Track application status"},
			{Name: "create", Label: "Apply to Jobs", Description: "Submit an application"},
			{Name: "withdraw", Label: "Withdraw Applications", Description: "Retract a submitted application"},
		},
	},
	{
		Key: "candidate_messaging", Name: "My Messages", Scope: ScopeCandidate, Group: "candidate", Order: 3,
		Description: "Conversations with recruiters",
		Actions: []Action{
			{Name: "read", Label: "Read Messages", Description: "View conversations"},
			{Name: "send", Label: "Send Messages", Description: "Reply to recruiters"},
		},
	},
}

var (
	featureIndex    = map[string]Feature{}
	permissionIndex = map[string]Permission{}
	allPermissions  []Permission
)

func init() {
	for _, feature := range features {
		if _, duplicate := featureIndex[feature.Key]; duplicate {
			panic(fmt.Sprintf("rbac: duplicate feature key %q", feature.Key))
		}
		featureIndex[feature.Key] = feature

		for _, action := range feature.Actions {
			key := feature.Key + "." + action.Name
			if _, duplicate := permissionIndex[key]; duplicate {
				panic(fmt.Sprintf("rbac: duplicate permission %q", key))
			}

			permission := Permission{
				Key:         key,
				FeatureKey:  feature.Key,
				FeatureName: feature.Name,
				Action:      action.Name,
				Label:       action.Label,
				Description: action.Description,
				Scope:       feature.Scope,
				Group:       feature.Group,
				Sensitive:   action.Sensitive,
			}
			permissionIndex[key] = permission
			allPermissions = append(allPermissions, permission)
		}
	}

	sort.Slice(allPermissions, func(i, j int) bool {
		return allPermissions[i].Key < allPermissions[j].Key
	})
}

// Features returns every registered feature.
func Features() []Feature { return features }

// FeaturesForScope returns the features available in a scope.
func FeaturesForScope(scope Scope) []Feature {
	var out []Feature
	for _, feature := range features {
		if feature.Scope == scope {
			out = append(out, feature)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Order < out[j].Order })
	return out
}

// Permissions returns every registered permission.
func Permissions() []Permission { return allPermissions }

// PermissionsForScope returns the permission keys valid in a scope.
func PermissionsForScope(scope Scope) []string {
	var keys []string
	for _, permission := range allPermissions {
		if permission.Scope == scope {
			keys = append(keys, permission.Key)
		}
	}
	return keys
}

// Lookup returns a permission by key.
func Lookup(key string) (Permission, bool) {
	permission, ok := permissionIndex[key]
	return permission, ok
}

// Sanitize drops unknown keys and keys outside the scope, then sorts and
// de-duplicates what remains.
//
// Every write to a role's permission list passes through here, so a stale or
// hand-crafted key can never end up granting something the registry does not
// define.
func Sanitize(scope Scope, keys []string) []string {
	seen := make(map[string]struct{}, len(keys))
	out := make([]string, 0, len(keys))

	for _, key := range keys {
		key = strings.TrimSpace(key)
		permission, known := permissionIndex[key]
		if !known || permission.Scope != scope {
			continue
		}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}

	sort.Strings(out)
	return out
}

// Subset reports whether every permission in `want` is present in `held`.
//
// This is the delegation rule: an administrator may only grant permissions they
// hold themselves, which is what blocks the classic privilege-escalation path of
// minting an all-powerful role and assigning it to yourself.
func Subset(held, want []string) (missing []string) {
	index := make(map[string]struct{}, len(held))
	for _, key := range held {
		index[key] = struct{}{}
	}
	for _, key := range want {
		if _, ok := index[key]; !ok {
			missing = append(missing, key)
		}
	}
	return missing
}
