package store

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/karmarat/platform/services/audit/internal/domain"
)

// The predicate builder is the whole tenant boundary of this service written
// down in one function, so it is worth testing without a database: if the
// company predicate ever stops leading the WHERE clause, or a filter value ever
// reaches the SQL as text instead of as a parameter, these fail.

func TestAppendFiltersKeepsTheTenantPredicateFirst(t *testing.T) {
	from := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)

	where, args := appendFilters(
		[]string{"company_id = $1"},
		[]any{"0f8fad5b-d9cb-469f-a165-70867728950e"},
		domain.Filter{
			Action:     "application.rejected",
			EntityType: "application",
			EntityID:   "app_1",
			ActorID:    "acc_1",
			From:       &from,
			To:         &to,
		},
	)

	if where[0] != "company_id = $1" {
		t.Fatalf("first predicate = %q, want the tenant filter", where[0])
	}
	if args[0] != "0f8fad5b-d9cb-469f-a165-70867728950e" {
		t.Fatalf("first argument = %v, want the tenant", args[0])
	}

	// Six filters were supplied, so six predicates and six arguments were added
	// on top of the tenant's. A predicate that carried its value inline would
	// leave the counts out of step.
	if len(where) != 7 || len(args) != 7 {
		t.Fatalf("built %d predicates for %d arguments, want 7 and 7", len(where), len(args))
	}

	clause := whereClause(where)
	for _, value := range []string{"application.rejected", "app_1", "acc_1"} {
		if strings.Contains(clause, value) {
			t.Fatalf("filter value %q was interpolated into the SQL: %s", value, clause)
		}
	}

	// Placeholders must run 1..n with no gaps, or pgx binds the wrong argument
	// to the wrong column and the tenant filter silently compares against an
	// action.
	for i := 1; i <= len(args); i++ {
		placeholder := "$" + strconv.Itoa(i)
		if !strings.Contains(clause, placeholder) {
			t.Fatalf("placeholder %s is missing from %s", placeholder, clause)
		}
	}
}

func TestAppendFiltersSkipsWhatWasNotAskedFor(t *testing.T) {
	where, args := appendFilters(
		[]string{"company_id = $1"},
		[]any{"0f8fad5b-d9cb-469f-a165-70867728950e"},
		domain.Filter{},
	)

	if len(where) != 1 || len(args) != 1 {
		t.Fatalf("an empty filter added predicates: %v", where)
	}
}

// The platform feed starts with no predicates at all, and an empty WHERE clause
// has to be absent rather than the string " WHERE ".
func TestWhereClauseOmitsItselfWhenEmpty(t *testing.T) {
	if got := whereClause(nil); got != "" {
		t.Fatalf("whereClause(nil) = %q, want an empty string", got)
	}

	where, args := appendFilters(nil, nil, domain.Filter{Action: "job.published"})
	if len(args) != 1 {
		t.Fatalf("expected one argument, got %d", len(args))
	}
	if got := whereClause(where); got != " WHERE action = $1" {
		t.Fatalf("whereClause = %q", got)
	}
}

// nullableUUID is what keeps a platform-wide event out of every company's
// trail: NULL never satisfies `company_id = $1`.
func TestNullableUUIDMapsAnAbsentTenantToNull(t *testing.T) {
	if got := nullableUUID(""); got != nil {
		t.Fatalf("nullableUUID(\"\") = %#v, want nil", got)
	}
	if got := nullableUUID("0f8fad5b-d9cb-469f-a165-70867728950e"); got != "0f8fad5b-d9cb-469f-a165-70867728950e" {
		t.Fatalf("nullableUUID dropped a real tenant: %#v", got)
	}
}
