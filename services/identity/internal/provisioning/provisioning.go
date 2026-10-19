// Package provisioning sets up the identity side of a new tenant.
//
// The companies service owns company registration; when a company is admitted it
// calls in here so the tenant exists in the identity model: a projection row, the
// default role set, and an owner who can sign in.
package provisioning

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/karmarat/platform/packages/goshared/events"
	"github.com/karmarat/platform/packages/goshared/tenancy"
	"github.com/karmarat/platform/services/identity/internal/auth"
	"github.com/karmarat/platform/services/identity/internal/domain"
	"github.com/karmarat/platform/services/identity/internal/rbac"
	"github.com/karmarat/platform/services/identity/internal/store"
)

// Provisioner creates the identity records a tenant needs.
type Provisioner struct {
	store  *store.Store
	bus    *events.Bus
	logger *slog.Logger
}

// New builds a provisioner.
func New(st *store.Store, bus *events.Bus, logger *slog.Logger) *Provisioner {
	return &Provisioner{store: st, bus: bus, logger: logger}
}

// ProvisionCompanyInput describes a tenant to create.
type ProvisionCompanyInput struct {
	CompanyID     string `json:"companyId"`
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	OwnerEmail    string `json:"ownerEmail"`
	OwnerName     string `json:"ownerName"`
	OwnerPassword string `json:"ownerPassword,omitempty"`
	// State defaults to pending_review; a company admitted by an administrator
	// can be created active directly.
	State domain.CompanyState `json:"state,omitempty"`
}

// ProvisionCompanyResult reports what was created.
type ProvisionCompanyResult struct {
	CompanyID      string `json:"companyId"`
	Slug           string `json:"slug"`
	OwnerAccountID string `json:"ownerAccountId"`
	MembershipID   string `json:"membershipId"`
	// OwnerCreated distinguishes a brand-new account from an existing recruiter
	// who has now been added to a second company.
	OwnerCreated bool `json:"ownerCreated"`
}

// ProvisionCompany creates the tenant, its roles, and its owner.
//
// The whole thing runs in one transaction: a company that exists without an
// owner, or with a half-seeded role set, would be unreachable by anyone.
func (p *Provisioner) ProvisionCompany(ctx context.Context, in ProvisionCompanyInput) (*ProvisionCompanyResult, error) {
	slug := strings.ToLower(strings.TrimSpace(in.Slug))

	if err := ValidateSlug(slug); err != nil {
		return nil, err
	}
	if in.CompanyID == "" {
		return nil, domain.Invalid("companyId", "A company id is required.")
	}
	// Identity stores the tenant key as text, but every other service stores it
	// as a uuid column. A company provisioned with anything else is therefore
	// half-created: it signs in, its roles resolve, and then jobs, applications
	// and notifications all answer 500 with "invalid input syntax for type
	// uuid". Refusing here turns that into one clear error at the one moment
	// somebody can still do something about it.
	if _, err := uuid.Parse(in.CompanyID); err != nil {
		return nil, domain.Invalid("companyId", "A company id must be a UUID.")
	}
	if strings.TrimSpace(in.OwnerEmail) == "" {
		return nil, domain.Invalid("ownerEmail", "An owner email is required.")
	}

	state := in.State
	if state == "" {
		state = domain.CompanyPendingReview
	}

	result := &ProvisionCompanyResult{CompanyID: in.CompanyID, Slug: slug}

	err := p.store.InTx(ctx, func(tx pgx.Tx) error {
		// An existing tenant keeps its billing state: UpsertCompany leaves the
		// subscription columns alone on conflict, so a retried provisioning call
		// cannot cancel a paying customer's subscription. `SubscriptionNone`
		// below is the value for a genuinely new row only.
		if err := p.store.UpsertCompany(ctx, tx, domain.Company{
			ID:                in.CompanyID,
			Slug:              slug,
			Name:              in.Name,
			State:             state,
			SubscriptionState: domain.SubscriptionNone,
		}); err != nil {
			return err
		}

		if err := p.store.SeedRoles(ctx, tx, domain.RealmCompany, in.CompanyID, rbac.CompanyRoles()); err != nil {
			return err
		}

		ownerRole, err := p.store.FindRoleBySlug(ctx, tx, in.CompanyID, "owner")
		if err != nil {
			return err
		}

		// The owner may already recruit for another company; reuse that login
		// rather than forcing a second account on the same person.
		account, err := p.store.FindAccountByEmail(ctx, domain.RealmCompany, in.OwnerEmail)
		switch {
		case err == nil:
			result.OwnerAccountID = account.ID
		case isNotFound(err):
			passwordHash := ""
			if in.OwnerPassword != "" {
				hashed, hashErr := auth.HashPassword(in.OwnerPassword)
				if hashErr != nil {
					return hashErr
				}
				passwordHash = hashed
			}

			// Without a password the account stays pending until the owner sets
			// one through the invitation link.
			status := domain.AccountPending
			if passwordHash != "" {
				status = domain.AccountActive
			}

			created, createErr := p.store.CreateAccount(ctx, tx, store.CreateAccountInput{
				Realm:        domain.RealmCompany,
				Email:        in.OwnerEmail,
				PasswordHash: passwordHash,
				FullName:     strings.TrimSpace(in.OwnerName),
				Status:       status,
			})
			if createErr != nil {
				return createErr
			}
			result.OwnerAccountID = created.ID
			result.OwnerCreated = true
		default:
			return err
		}

		// A membership that already exists is reused rather than inserted again.
		// CreateMembership is a bare INSERT, so without this a retried
		// provisioning call — the whole reason the subscription is preserved
		// above — would still fail on the (account, company) unique index and
		// roll the transaction back, making the preservation unreachable.
		membership, err := p.store.FindMembership(ctx, result.OwnerAccountID, in.CompanyID)
		if errors.Is(err, domain.ErrNoMembership) {
			membership, err = p.store.CreateMembership(ctx, tx, store.CreateMembershipInput{
				AccountID: result.OwnerAccountID,
				CompanyID: in.CompanyID,
				Status:    domain.MembershipActive,
				IsOwner:   true,
				JobTitle:  "Owner",
			})
		}
		if err != nil {
			return err
		}
		result.MembershipID = membership.ID

		return p.store.ReplaceMembershipRoles(ctx, tx, membership.ID,
			[]string{ownerRole.ID}, ownerRole.ID, "")
	})
	if err != nil {
		return nil, err
	}

	p.publish(ctx, events.SubjectCompanyRegistered, map[string]any{
		"companyId":      in.CompanyID,
		"slug":           slug,
		"name":           in.Name,
		"state":          state,
		"ownerAccountId": result.OwnerAccountID,
	}, events.PublishOptions{CompanyID: in.CompanyID, ActorID: result.OwnerAccountID})

	p.logger.Info("company provisioned",
		slog.String("company_id", in.CompanyID),
		slog.String("slug", slug),
		slog.Bool("owner_created", result.OwnerCreated),
	)

	return result, nil
}

// UpdateSubscription applies a billing change to the identity projection.
//
// Losing entitlement closes the portal, so live sessions inside that tenant are
// ended rather than left to run until their access tokens expire.
func (p *Provisioner) UpdateSubscription(
	ctx context.Context,
	companyID string,
	state domain.SubscriptionState,
	expiresAt *time.Time,
	entitlements map[string]any,
) error {
	before, err := p.store.FindCompanyByID(ctx, companyID)
	if err != nil {
		return err
	}

	if err := p.store.UpdateCompanySubscription(ctx, companyID, state, expiresAt, entitlements); err != nil {
		return err
	}

	if before.SubscriptionState.Entitled() && !state.Entitled() {
		revoked, err := p.store.RevokeCompanySessions(ctx, companyID, "subscription_lapsed")
		if err != nil {
			p.logger.Error("failed to revoke sessions after subscription lapse",
				slog.String("company_id", companyID), slog.Any("error", err))
		} else if revoked > 0 {
			p.logger.Info("revoked sessions after subscription lapse",
				slog.String("company_id", companyID), slog.Int64("sessions", revoked))
		}
	}

	return nil
}

// SetCompanyState changes a tenant's lifecycle state.
func (p *Provisioner) SetCompanyState(ctx context.Context, companyID string, state domain.CompanyState) error {
	company, err := p.store.FindCompanyByID(ctx, companyID)
	if err != nil {
		return err
	}

	company.State = state
	if err := p.store.UpsertCompany(ctx, nil, company); err != nil {
		return err
	}

	if state == domain.CompanySuspended || state == domain.CompanyClosed {
		if _, err := p.store.RevokeCompanySessions(ctx, companyID, "company_"+string(state)); err != nil {
			p.logger.Error("failed to revoke sessions after state change",
				slog.String("company_id", companyID), slog.Any("error", err))
		}
	}

	return nil
}

// SeedPlatformRoles creates the platform's own role set. Safe to run on boot.
func (p *Provisioner) SeedPlatformRoles(ctx context.Context) error {
	return p.store.InTx(ctx, func(tx pgx.Tx) error {
		return p.store.SeedRoles(ctx, tx, domain.RealmPlatform, "", rbac.PlatformRoles())
	})
}

// EnsurePlatformSuperAdmin creates the first platform administrator.
//
// A fresh installation has no way in until this account exists; it is a no-op
// once any platform account is present.
func (p *Provisioner) EnsurePlatformSuperAdmin(ctx context.Context, email, name, password string) (created bool, err error) {
	if strings.TrimSpace(email) == "" || password == "" {
		return false, nil
	}

	if _, err := p.store.FindAccountByEmail(ctx, domain.RealmPlatform, email); err == nil {
		return false, nil
	} else if !isNotFound(err) {
		return false, err
	}

	if problems := auth.DefaultPasswordPolicy().Validate(password); len(problems) > 0 {
		return false, fmt.Errorf("bootstrap password is too weak: %s", strings.Join(problems, "; "))
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return false, err
	}

	err = p.store.InTx(ctx, func(tx pgx.Tx) error {
		if err := p.store.SeedRoles(ctx, tx, domain.RealmPlatform, "", rbac.PlatformRoles()); err != nil {
			return err
		}

		role, err := p.store.FindRoleBySlug(ctx, tx, "", "super_admin")
		if err != nil {
			return err
		}

		account, err := p.store.CreateAccount(ctx, tx, store.CreateAccountInput{
			Realm:        domain.RealmPlatform,
			Email:        email,
			PasswordHash: hash,
			FullName:     name,
			Status:       domain.AccountActive,
		})
		if err != nil {
			return err
		}

		return p.store.ReplaceAccountRoles(ctx, tx, account.ID, []string{role.ID}, role.ID, "")
	})
	if err != nil {
		return false, err
	}

	p.logger.Info("platform super admin created", slog.String("email", email))
	return true, nil
}

// ValidateSlug checks that a company slug is usable as a subdomain.
//
// The slug becomes a hostname, so it must be DNS-safe, and it must not collide
// with a portal the platform already routes.
func ValidateSlug(slug string) error {
	slug = strings.ToLower(strings.TrimSpace(slug))

	if len(slug) < 3 || len(slug) > 40 {
		return domain.Invalid("slug", "A company address must be between 3 and 40 characters.")
	}
	if tenancy.IsReservedSlug(slug) {
		return domain.Invalid("slug", fmt.Sprintf("%q is reserved by the platform.", slug))
	}
	if strings.HasPrefix(slug, "-") || strings.HasSuffix(slug, "-") {
		return domain.Invalid("slug", "A company address cannot start or end with a hyphen.")
	}
	if strings.Contains(slug, "--") {
		return domain.Invalid("slug", "A company address cannot contain consecutive hyphens.")
	}

	for _, r := range slug {
		isLower := r >= 'a' && r <= 'z'
		isDigit := r >= '0' && r <= '9'
		if !isLower && !isDigit && r != '-' {
			return domain.Invalid("slug",
				"A company address may only contain lowercase letters, digits, and hyphens.")
		}
	}

	return nil
}

func (p *Provisioner) publish(ctx context.Context, subject string, payload any, opts events.PublishOptions) {
	if p.bus == nil {
		return
	}
	if err := p.bus.Publish(context.WithoutCancel(ctx), subject, payload, opts); err != nil {
		p.logger.Error("failed to publish event",
			slog.String("subject", subject), slog.Any("error", err))
	}
}

func isNotFound(err error) bool {
	return errors.Is(err, domain.ErrAccountNotFound) || errors.Is(err, domain.ErrCompanyNotFound)
}
