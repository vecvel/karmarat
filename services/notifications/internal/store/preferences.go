package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/karmarat/platform/services/notifications/internal/domain"
)

// LoadPreferences returns what the recipient has chosen, or empty preferences
// when they have never changed anything.
//
// Absent is not an error: the defaults live in the domain package, and a row
// here only records a departure from them.
func (s *Store) LoadPreferences(ctx context.Context, recipient domain.Recipient) (domain.Preferences, error) {
	prefs := domain.NewPreferences()

	err := s.pool.QueryRow(ctx, `
		SELECT channels FROM notification_preferences
		WHERE principal_type = $1 AND account_id = $2 AND company_id = $3::uuid`,
		string(recipient.PrincipalType), recipient.AccountID, recipient.TenantKey(),
	).Scan(&prefs.Channels)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.NewPreferences(), nil
		}
		return domain.Preferences{}, fmt.Errorf("store: load preferences: %w", err)
	}
	if prefs.Channels == nil {
		prefs.Channels = map[domain.Type]domain.ChannelSet{}
	}
	return prefs, nil
}

// SavePreferences writes the recipient's choices.
func (s *Store) SavePreferences(ctx context.Context, recipient domain.Recipient, prefs domain.Preferences) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO notification_preferences (principal_type, account_id, company_id, channels)
		VALUES ($1, $2, $3::uuid, $4)
		ON CONFLICT (principal_type, account_id, company_id)
		DO UPDATE SET channels = excluded.channels, updated_at = now()`,
		string(recipient.PrincipalType), recipient.AccountID, recipient.TenantKey(), prefs.Channels,
	)
	if err != nil {
		return fmt.Errorf("store: save preferences: %w", err)
	}
	return nil
}

// LoadPreferencesFor returns the preferences of several recipients at once.
//
// A fan-out to a company asks about every member in one round trip; doing it
// one query per person would make the cost of an event grow with the size of
// the team that receives it.
func (s *Store) LoadPreferencesFor(
	ctx context.Context, recipients []domain.Recipient,
) (map[string]domain.Preferences, error) {
	out := make(map[string]domain.Preferences, len(recipients))
	if len(recipients) == 0 {
		return out, nil
	}

	// The three key columns are passed as parallel arrays and zipped back
	// together by unnest, which keeps this one statement whatever the size of
	// the audience.
	types := make([]string, 0, len(recipients))
	accounts := make([]string, 0, len(recipients))
	tenants := make([]string, 0, len(recipients))
	for _, recipient := range recipients {
		types = append(types, string(recipient.PrincipalType))
		accounts = append(accounts, recipient.AccountID)
		tenants = append(tenants, recipient.TenantKey())
	}

	rows, err := s.pool.Query(ctx, `
		SELECT p.principal_type, p.account_id, p.company_id::text, p.channels
		FROM notification_preferences p
		JOIN unnest($1::text[], $2::text[], $3::uuid[]) AS want(principal_type, account_id, company_id)
		  ON want.principal_type = p.principal_type
		 AND want.account_id = p.account_id
		 AND want.company_id = p.company_id`,
		types, accounts, tenants,
	)
	if err != nil {
		return nil, fmt.Errorf("store: load preferences for audience: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var principalType, accountID, tenant string
		channels := map[domain.Type]domain.ChannelSet{}
		if err := rows.Scan(&principalType, &accountID, &tenant, &channels); err != nil {
			return nil, fmt.Errorf("store: scan preferences: %w", err)
		}
		out[preferenceKey(principalType, accountID, tenant)] = domain.Preferences{Channels: channels}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: load preferences for audience: %w", err)
	}

	return out, nil
}

// PreferenceKey is how a recipient indexes into the map LoadPreferencesFor
// returns.
func PreferenceKey(recipient domain.Recipient) string {
	return preferenceKey(string(recipient.PrincipalType), recipient.AccountID, recipient.TenantKey())
}

func preferenceKey(principalType, accountID, tenant string) string {
	return principalType + "|" + accountID + "|" + tenant
}
