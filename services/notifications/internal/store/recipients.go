package store

import (
	"context"
	"fmt"

	"github.com/karmarat/platform/packages/goshared/tenancy"
	"github.com/karmarat/platform/services/notifications/internal/domain"
)

// maxFanOut caps how many people one event may notify.
//
// A company with a thousand seats and a job board that gets a thousand
// applications a day would otherwise turn one event into a million rows. The
// cap is deliberately far above a real hiring team: reaching it means something
// is wrong, and the log line that reports it is the point.
const maxFanOut = 500

// TouchRecipient records that a verified principal exists.
//
// Everything written here comes from the gateway's own headers, which it sets
// after verifying a token and strips from whatever the client sent. The
// permission list is the resolved one from that request, so a role change takes
// effect for fan-out the next time the person loads a page.
func (s *Store) TouchRecipient(ctx context.Context, principal tenancy.Principal) error {
	recipient := domain.Recipient{
		PrincipalType: principal.Type,
		AccountID:     principal.Subject,
		CompanyID:     principal.CompanyID,
	}

	permissions := principal.Permissions
	if permissions == nil {
		permissions = []string{}
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO notification_recipients (
			principal_type, account_id, company_id, email, permissions, last_seen_at)
		VALUES ($1, $2, $3::uuid, $4, $5, now())
		ON CONFLICT (principal_type, account_id, company_id) DO UPDATE SET
			email = CASE WHEN excluded.email <> '' THEN excluded.email
			             ELSE notification_recipients.email END,
			permissions = excluded.permissions,
			last_seen_at = now()`,
		string(recipient.PrincipalType), recipient.AccountID, recipient.TenantKey(),
		principal.Email, permissions,
	)
	if err != nil {
		return fmt.Errorf("store: touch recipient: %w", err)
	}
	return nil
}

// RememberRecipient records an address learned from an event payload.
//
// A candidate who applied through a careers portal and has not signed in since
// still has to be told their application moved. The event carries their address
// because the application snapshot does, so this is the one path where a
// recipient arrives from somewhere other than a verified request — and it can
// only ever write a candidate's own row, keyed by the account id the event
// names.
func (s *Store) RememberRecipient(ctx context.Context, recipient domain.Recipient) error {
	if recipient.AccountID == "" {
		return nil
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO notification_recipients (
			principal_type, account_id, company_id, email, display_name)
		VALUES ($1, $2, $3::uuid, $4, $5)
		ON CONFLICT (principal_type, account_id, company_id) DO UPDATE SET
			email = CASE WHEN notification_recipients.email = '' THEN excluded.email
			             ELSE notification_recipients.email END,
			display_name = CASE WHEN notification_recipients.display_name = '' THEN excluded.display_name
			                    ELSE notification_recipients.display_name END`,
		string(recipient.PrincipalType), recipient.AccountID, recipient.TenantKey(),
		recipient.Email, recipient.Name,
	)
	if err != nil {
		return fmt.Errorf("store: remember recipient: %w", err)
	}
	return nil
}

// FindRecipient loads one directory entry, mainly to recover an email address.
func (s *Store) FindRecipient(ctx context.Context, recipient domain.Recipient) (domain.Recipient, error) {
	found := recipient
	err := s.pool.QueryRow(ctx, `
		SELECT email, display_name FROM notification_recipients
		WHERE principal_type = $1 AND account_id = $2 AND company_id = $3::uuid`,
		string(recipient.PrincipalType), recipient.AccountID, recipient.TenantKey(),
	).Scan(&found.Email, &found.Name)
	if err != nil {
		// A miss is normal — the person has simply never been seen — and the
		// caller falls back to whatever the event carried.
		return recipient, nil
	}
	if found.Email == "" {
		found.Email = recipient.Email
	}
	if found.Name == "" {
		found.Name = recipient.Name
	}
	return found, nil
}

// ExpandAudience resolves a group audience into the people in it.
//
// The permission filter is what stops a coordinator who cannot open the
// pipeline from being mailed about it. It reads the permissions as they were on
// that person's last signed-in request, which is a deliberate trade: the
// alternative is a synchronous call to identity on every event, which would put
// the whole notification pipeline behind another service's availability.
func (s *Store) ExpandAudience(ctx context.Context, audience domain.Audience) ([]domain.Recipient, error) {
	tenant := domain.NoCompany
	if audience.PrincipalType == tenancy.PrincipalCompany {
		if audience.CompanyID == "" {
			return nil, nil
		}
		tenant = audience.CompanyID
	}

	rows, err := s.pool.Query(ctx, `
		SELECT account_id, email, display_name
		FROM notification_recipients
		WHERE principal_type = $1
		  AND company_id = $2::uuid
		  AND ($3 = '' OR $3 = ANY(permissions))
		ORDER BY account_id
		LIMIT $4`,
		string(audience.PrincipalType), tenant, audience.Permission, maxFanOut+1,
	)
	if err != nil {
		return nil, fmt.Errorf("store: expand audience: %w", err)
	}
	defer rows.Close()

	recipients := make([]domain.Recipient, 0, 16)
	for rows.Next() {
		recipient := domain.Recipient{
			PrincipalType: audience.PrincipalType,
			CompanyID:     audience.CompanyID,
		}
		if err := rows.Scan(&recipient.AccountID, &recipient.Email, &recipient.Name); err != nil {
			return nil, fmt.Errorf("store: scan recipient: %w", err)
		}
		recipients = append(recipients, recipient)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: expand audience: %w", err)
	}

	return recipients, nil
}

// FanOutLimit is the cap ExpandAudience applies, exposed so the consumer can
// report when an audience hit it.
const FanOutLimit = maxFanOut
