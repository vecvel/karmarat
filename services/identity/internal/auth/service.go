package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/karmarat/platform/packages/goshared/events"
	"github.com/karmarat/platform/packages/goshared/tenancy"
	"github.com/karmarat/platform/packages/goshared/tokens"
	"github.com/karmarat/platform/services/identity/internal/domain"
	"github.com/karmarat/platform/services/identity/internal/rbac"
	"github.com/karmarat/platform/services/identity/internal/store"
)

// Lockout policy. Five attempts is enough to absorb typos without giving an
// attacker a useful number of guesses.
const (
	maxFailedLogins = 5
	lockoutDuration = 15 * time.Minute
)

// Service implements the identity use cases.
type Service struct {
	store      *store.Store
	issuer     *tokens.Issuer
	bus        *events.Bus
	logger     *slog.Logger
	refreshTTL time.Duration
	policy     PasswordPolicy
}

// Config configures the service.
type Config struct {
	Store      *store.Store
	Issuer     *tokens.Issuer
	Bus        *events.Bus
	Logger     *slog.Logger
	RefreshTTL time.Duration
}

// NewService builds the identity service.
func NewService(cfg Config) *Service {
	if cfg.RefreshTTL == 0 {
		cfg.RefreshTTL = 30 * 24 * time.Hour
	}
	return &Service{
		store:      cfg.Store,
		issuer:     cfg.Issuer,
		bus:        cfg.Bus,
		logger:     cfg.Logger,
		refreshTTL: cfg.RefreshTTL,
		policy:     DefaultPasswordPolicy(),
	}
}

// TokenPair is what a successful authentication returns.
type TokenPair struct {
	AccessToken  string    `json:"accessToken"`
	RefreshToken string    `json:"refreshToken"`
	ExpiresAt    time.Time `json:"expiresAt"`
	TokenType    string    `json:"tokenType"`
}

// Identity describes the authenticated actor, for the client to render.
type Identity struct {
	AccountID    string   `json:"accountId"`
	Email        string   `json:"email"`
	FullName     string   `json:"fullName"`
	Realm        string   `json:"realm"`
	CompanyID    string   `json:"companyId,omitempty"`
	CompanySlug  string   `json:"companySlug,omitempty"`
	CompanyName  string   `json:"companyName,omitempty"`
	Roles        []string `json:"roles"`
	RoleNames    []string `json:"roleNames"`
	Permissions  []string `json:"permissions"`
	IsSuperAdmin bool     `json:"isSuperAdmin"`
}

// AuthResult bundles the tokens with the identity behind them.
type AuthResult struct {
	Tokens   TokenPair `json:"tokens"`
	Identity Identity  `json:"identity"`
	// Companies lists the other companies this account may switch into.
	Companies []CompanySummary `json:"companies,omitempty"`
}

// CompanySummary is a company an account belongs to.
type CompanySummary struct {
	ID              string `json:"id"`
	Slug            string `json:"slug"`
	Name            string `json:"name"`
	PortalAvailable bool   `json:"portalAvailable"`
	IsOwner         bool   `json:"isOwner"`
}

// RequestContext carries the details recorded alongside an authentication.
type RequestContext struct {
	IPAddress string
	UserAgent string
}

/* -------------------------------------------------------------------------- */
/* Registration                                                               */
/* -------------------------------------------------------------------------- */

// RegisterCandidateInput is a self-service candidate signup on jobs.{hostname}.
type RegisterCandidateInput struct {
	Email    string
	Password string
	FullName string
}

// RegisterCandidate creates a candidate account and signs them in.
func (s *Service) RegisterCandidate(ctx context.Context, in RegisterCandidateInput, rc RequestContext) (*AuthResult, error) {
	if problems := s.policy.Validate(in.Password); len(problems) > 0 {
		return nil, &PasswordPolicyError{Problems: problems}
	}

	hash, err := HashPassword(in.Password)
	if err != nil {
		return nil, err
	}

	var account domain.Account
	err = s.store.InTx(ctx, func(tx pgx.Tx) error {
		account, err = s.store.CreateAccount(ctx, tx, store.CreateAccountInput{
			Realm: domain.RealmCandidate,
			Email: in.Email,
			// Candidates are usable immediately; email verification is a separate,
			// non-blocking step so a job seeker is never stuck at a mail client.
			Status:       domain.AccountActive,
			PasswordHash: hash,
			FullName:     strings.TrimSpace(in.FullName),
		})
		if err != nil {
			return err
		}

		if err := s.store.SeedRoles(ctx, tx, domain.RealmCandidate, "",
			[]rbac.DefaultRole{rbac.CandidateRole()}); err != nil {
			return err
		}

		role, err := s.store.FindRoleBySlug(ctx, tx, "", rbac.CandidateRole().Slug)
		if err != nil {
			return err
		}

		return s.store.ReplaceAccountRoles(ctx, tx, account.ID, []string{role.ID}, role.ID, "")
	})
	if err != nil {
		return nil, err
	}

	s.publish(ctx, events.SubjectCandidateRegistered, map[string]any{
		"accountId": account.ID,
		"email":     account.Email,
		"fullName":  account.FullName,
	}, events.PublishOptions{ActorID: account.ID})

	s.audit(ctx, store.AuthEvent{
		AccountID: account.ID, Event: "candidate.registered",
		IPAddress: rc.IPAddress, UserAgent: rc.UserAgent,
	})

	result, _, err := s.issueFor(ctx, account, nil, rc)
	return result, err
}

/* -------------------------------------------------------------------------- */
/* Sign in                                                                    */
/* -------------------------------------------------------------------------- */

// LoginInput is a sign-in attempt at one of the portals.
type LoginInput struct {
	Realm    domain.Realm
	Email    string
	Password string
	// CompanySlug is required when signing in at a company portal; it decides
	// which tenant the resulting session is scoped to.
	CompanySlug string
}

// Login authenticates an account and opens a session.
//
// Every failure path returns the same error and costs the same work: an attacker
// must not be able to tell a wrong password from an unregistered address.
func (s *Service) Login(ctx context.Context, in LoginInput, rc RequestContext) (*AuthResult, error) {
	if !in.Realm.Valid() {
		return nil, fmt.Errorf("unknown sign-in realm")
	}

	account, err := s.store.FindAccountByEmail(ctx, in.Realm, in.Email)
	if err != nil {
		if errors.Is(err, domain.ErrAccountNotFound) {
			// Spend the same time hashing so the response cannot be timed.
			_ = VerifyPassword(in.Password, DummyHash)
			s.audit(ctx, store.AuthEvent{
				Event: "login.unknown_account", IPAddress: rc.IPAddress,
				UserAgent: rc.UserAgent, Metadata: map[string]any{"email": in.Email, "realm": in.Realm},
			})
			return nil, domain.ErrInvalidCredentials
		}
		return nil, err
	}

	now := time.Now()
	if account.IsLocked(now) {
		s.audit(ctx, store.AuthEvent{
			AccountID: account.ID, Event: "login.locked_out",
			IPAddress: rc.IPAddress, UserAgent: rc.UserAgent,
		})
		return nil, domain.ErrAccountLocked
	}

	if account.PasswordHash == "" {
		return nil, domain.ErrInvalidCredentials
	}

	if err := VerifyPassword(in.Password, account.PasswordHash); err != nil {
		locked, lockErr := s.store.RecordLoginFailure(ctx, account.ID, maxFailedLogins, lockoutDuration)
		if lockErr != nil {
			s.logger.Error("failed to record login failure", slog.Any("error", lockErr))
		}

		s.audit(ctx, store.AuthEvent{
			AccountID: account.ID, Event: "login.failed",
			IPAddress: rc.IPAddress, UserAgent: rc.UserAgent,
			Metadata: map[string]any{"locked": locked},
		})

		if locked {
			return nil, domain.ErrAccountLocked
		}
		return nil, domain.ErrInvalidCredentials
	}

	if !account.Status.CanSignIn() {
		s.audit(ctx, store.AuthEvent{
			AccountID: account.ID, Event: "login.inactive_account",
			IPAddress: rc.IPAddress, UserAgent: rc.UserAgent,
			Metadata: map[string]any{"status": account.Status},
		})
		return nil, domain.ErrAccountInactive
	}

	// Company sign-in must resolve to a tenant the account actually belongs to.
	var membership *store.CompanyMembership
	if in.Realm == domain.RealmCompany {
		resolved, err := s.resolveCompanyMembership(ctx, account.ID, in.CompanySlug)
		if err != nil {
			s.audit(ctx, store.AuthEvent{
				AccountID: account.ID, Event: "login.company_denied",
				IPAddress: rc.IPAddress, UserAgent: rc.UserAgent,
				Metadata: map[string]any{"slug": in.CompanySlug, "reason": err.Error()},
			})
			return nil, err
		}
		membership = resolved
	}

	if err := s.store.RecordLoginSuccess(ctx, account.ID, rc.IPAddress); err != nil {
		s.logger.Error("failed to record login success", slog.Any("error", err))
	}

	s.audit(ctx, store.AuthEvent{
		AccountID: account.ID,
		CompanyID: companyIDOf(membership),
		Event:     "login.succeeded",
		IPAddress: rc.IPAddress, UserAgent: rc.UserAgent,
	})

	result, _, err := s.issueFor(ctx, account, membership, rc)
	return result, err
}

// resolveCompanyMembership checks that an account may enter a company portal.
func (s *Service) resolveCompanyMembership(ctx context.Context, accountID, slug string) (*store.CompanyMembership, error) {
	if strings.TrimSpace(slug) == "" {
		return nil, domain.ErrCompanyNotFound
	}

	company, err := s.store.FindCompanyBySlug(ctx, slug)
	if err != nil {
		return nil, err
	}

	membership, err := s.store.FindMembership(ctx, accountID, company.ID)
	if err != nil {
		return nil, err
	}

	switch membership.Status {
	case domain.MembershipActive:
	case domain.MembershipInvited:
		return nil, fmt.Errorf("your invitation has not been accepted yet")
	default:
		return nil, domain.ErrMembershipInactive
	}

	// An owner may still sign in while the subscription is lapsed so they can
	// reach billing and fix it; everyone else is held at the door.
	if !company.PortalAvailable() && !membership.IsOwner {
		return nil, domain.ErrCompanyUnavailable
	}

	return &store.CompanyMembership{Membership: membership, Company: company}, nil
}

/* -------------------------------------------------------------------------- */
/* Token issuing                                                              */
/* -------------------------------------------------------------------------- */

// issueFor mints an access/refresh pair for an authenticated account.
func (s *Service) issueFor(ctx context.Context, account domain.Account, membership *store.CompanyMembership, rc RequestContext) (*AuthResult, domain.Session, error) {
	var (
		access       store.Access
		companyID    string
		companySlug  string
		companyName  string
		membershipID string
		err          error
	)

	if membership != nil {
		companyID = membership.Company.ID
		companySlug = membership.Company.Slug
		companyName = membership.Company.Name
		membershipID = membership.Membership.ID
		access, err = s.store.ResolveMembershipAccess(ctx, membershipID)
	} else {
		access, err = s.store.ResolveAccountAccess(ctx, account.ID, scopeForRealm(account.Realm))
	}
	if err != nil {
		return nil, domain.Session{}, err
	}

	rawRefresh, refreshHash, err := tokens.NewRefreshToken()
	if err != nil {
		return nil, domain.Session{}, err
	}

	session, err := s.store.CreateSession(ctx, nil, store.CreateSessionInput{
		AccountID:        account.ID,
		MembershipID:     membershipID,
		CompanyID:        companyID,
		RefreshTokenHash: refreshHash,
		IPAddress:        rc.IPAddress,
		UserAgent:        rc.UserAgent,
		ExpiresAt:        time.Now().Add(s.refreshTTL),
	})
	if err != nil {
		return nil, domain.Session{}, err
	}

	accessToken, claims, err := s.issuer.Issue(tokens.IssueInput{
		Subject:       account.ID,
		PrincipalType: account.Realm.PrincipalType(),
		CompanyID:     companyID,
		CompanySlug:   companySlug,
		Email:         account.Email,
		Name:          account.FullName,
		Roles:         access.Roles,
		Permissions:   access.Permissions,
		SessionID:     session.ID,
	})
	if err != nil {
		return nil, domain.Session{}, err
	}

	result := &AuthResult{
		Tokens: TokenPair{
			AccessToken:  accessToken,
			RefreshToken: rawRefresh,
			ExpiresAt:    claims.ExpiresAt.Time,
			TokenType:    "Bearer",
		},
		Identity: Identity{
			AccountID:    account.ID,
			Email:        account.Email,
			FullName:     account.FullName,
			Realm:        string(account.Realm),
			CompanyID:    companyID,
			CompanySlug:  companySlug,
			CompanyName:  companyName,
			Roles:        access.Roles,
			RoleNames:    access.RoleNames,
			Permissions:  access.Permissions,
			IsSuperAdmin: access.IsSuperAdmin,
		},
	}

	// Someone who recruits for more than one company needs to know where else
	// they can go without signing in again.
	if account.Realm == domain.RealmCompany {
		memberships, err := s.store.ListMembershipsForAccount(ctx, account.ID)
		if err != nil {
			s.logger.Warn("failed to list memberships", slog.Any("error", err))
		}
		for _, cm := range memberships {
			if cm.Membership.Status != domain.MembershipActive {
				continue
			}
			result.Companies = append(result.Companies, CompanySummary{
				ID:              cm.Company.ID,
				Slug:            cm.Company.Slug,
				Name:            cm.Company.Name,
				PortalAvailable: cm.Company.PortalAvailable(),
				IsOwner:         cm.Membership.IsOwner,
			})
		}
	}

	return result, session, nil
}

/* -------------------------------------------------------------------------- */
/* Refresh and sign out                                                       */
/* -------------------------------------------------------------------------- */

// Refresh exchanges a refresh token for a new pair, rotating the session.
//
// Presenting an already-rotated token means the token was captured: the whole
// account's sessions are revoked rather than quietly issuing another pair.
func (s *Service) Refresh(ctx context.Context, refreshToken string, rc RequestContext) (*AuthResult, error) {
	hash := tokens.HashRefreshToken(refreshToken)

	session, err := s.store.SessionByRefreshHash(ctx, nil, hash)
	if err != nil {
		return nil, domain.ErrSessionNotFound
	}

	now := time.Now()
	if session.RevokedAt != nil {
		rotated, checkErr := s.store.WasRotated(ctx, nil, session.ID)
		if checkErr == nil && rotated {
			if _, revokeErr := s.store.RevokeAccountSessions(ctx, nil, session.AccountID, "refresh_token_replay"); revokeErr != nil {
				s.logger.Error("failed to revoke sessions after replay", slog.Any("error", revokeErr))
			}
			s.audit(ctx, store.AuthEvent{
				AccountID: session.AccountID, CompanyID: session.CompanyID,
				Event:     "session.refresh_replay_detected",
				IPAddress: rc.IPAddress, UserAgent: rc.UserAgent,
			})
			s.publish(ctx, events.SubjectSessionRevoked, map[string]any{
				"accountId": session.AccountID, "reason": "refresh_token_replay",
			}, events.PublishOptions{ActorID: session.AccountID})
		}
		return nil, domain.ErrSessionNotFound
	}

	if !session.Active(now) {
		return nil, domain.ErrSessionExpired
	}

	account, err := s.store.FindAccountByID(ctx, session.AccountID)
	if err != nil {
		return nil, err
	}
	if !account.Status.CanSignIn() {
		return nil, domain.ErrAccountInactive
	}

	// Re-resolve the tenant on every refresh: a subscription that lapsed or a
	// membership that was suspended must take effect without waiting for the
	// access token to expire.
	var membership *store.CompanyMembership
	if session.CompanyID != "" {
		company, err := s.store.FindCompanyByID(ctx, session.CompanyID)
		if err != nil {
			return nil, err
		}
		current, err := s.store.FindMembership(ctx, account.ID, company.ID)
		if err != nil {
			return nil, err
		}
		if current.Status != domain.MembershipActive {
			return nil, domain.ErrMembershipInactive
		}
		if !company.PortalAvailable() && !current.IsOwner {
			return nil, domain.ErrCompanyUnavailable
		}
		membership = &store.CompanyMembership{Membership: current, Company: company}
	}

	result, newSession, err := s.issueFor(ctx, account, membership, rc)
	if err != nil {
		return nil, err
	}

	// Link the spent session to its replacement so a later replay is detectable.
	if err := s.store.RotateSession(ctx, nil, session.ID, newSession.ID); err != nil {
		s.logger.Error("failed to rotate session", slog.Any("error", err))
	}

	return result, nil
}

// Logout ends the session behind a refresh token.
func (s *Service) Logout(ctx context.Context, refreshToken string, rc RequestContext) error {
	session, err := s.store.SessionByRefreshHash(ctx, nil, tokens.HashRefreshToken(refreshToken))
	if err != nil {
		// Signing out of a session that no longer exists is already the goal.
		return nil
	}

	if err := s.store.RevokeSession(ctx, session.ID, "signed_out"); err != nil {
		return err
	}

	s.audit(ctx, store.AuthEvent{
		AccountID: session.AccountID, CompanyID: session.CompanyID,
		Event: "session.signed_out", IPAddress: rc.IPAddress, UserAgent: rc.UserAgent,
	})
	return nil
}

// LogoutEverywhere ends every session for an account.
func (s *Service) LogoutEverywhere(ctx context.Context, accountID, reason string) error {
	count, err := s.store.RevokeAccountSessions(ctx, nil, accountID, reason)
	if err != nil {
		return err
	}

	s.audit(ctx, store.AuthEvent{
		AccountID: accountID, Event: "session.revoked_all",
		Metadata: map[string]any{"reason": reason, "count": count},
	})
	s.publish(ctx, events.SubjectSessionRevoked, map[string]any{
		"accountId": accountID, "reason": reason, "count": count,
	}, events.PublishOptions{ActorID: accountID})

	return nil
}

// SwitchCompany reissues tokens scoped to a different company.
func (s *Service) SwitchCompany(ctx context.Context, principal tenancy.Principal, companySlug string, rc RequestContext) (*AuthResult, error) {
	account, err := s.store.FindAccountByID(ctx, principal.Subject)
	if err != nil {
		return nil, err
	}
	if account.Realm != domain.RealmCompany {
		return nil, domain.ErrNoMembership
	}

	membership, err := s.resolveCompanyMembership(ctx, account.ID, companySlug)
	if err != nil {
		return nil, err
	}

	// The session being left behind is ended, so switching does not accumulate
	// live sessions across tenants.
	if principal.SessionID != "" {
		if err := s.store.RevokeSession(ctx, principal.SessionID, "switched_company"); err != nil {
			s.logger.Warn("failed to revoke session on switch", slog.Any("error", err))
		}
	}

	s.audit(ctx, store.AuthEvent{
		AccountID: account.ID, CompanyID: membership.Company.ID,
		Event: "session.company_switched", IPAddress: rc.IPAddress, UserAgent: rc.UserAgent,
	})

	result, _, err := s.issueFor(ctx, account, membership, rc)
	return result, err
}

/* -------------------------------------------------------------------------- */
/* Helpers                                                                    */
/* -------------------------------------------------------------------------- */

// PasswordPolicyError reports every way a password failed the policy.
type PasswordPolicyError struct {
	Problems []string
}

func (e *PasswordPolicyError) Error() string {
	return "password does not meet the policy: " + strings.Join(e.Problems, "; ")
}

func (s *Service) audit(ctx context.Context, event store.AuthEvent) {
	if err := s.store.RecordAuthEvent(context.WithoutCancel(ctx), event); err != nil {
		s.logger.Error("failed to record auth event",
			slog.String("event", event.Event), slog.Any("error", err))
	}
}

func (s *Service) publish(ctx context.Context, subject string, payload any, opts events.PublishOptions) {
	if s.bus == nil {
		return
	}
	if err := s.bus.Publish(context.WithoutCancel(ctx), subject, payload, opts); err != nil {
		s.logger.Error("failed to publish event",
			slog.String("subject", subject), slog.Any("error", err))
	}
}

func scopeForRealm(realm domain.Realm) rbac.Scope {
	switch realm {
	case domain.RealmPlatform:
		return rbac.ScopePlatform
	case domain.RealmCandidate:
		return rbac.ScopeCandidate
	default:
		return rbac.ScopeCompany
	}
}

func companyIDOf(m *store.CompanyMembership) string {
	if m == nil {
		return ""
	}
	return m.Company.ID
}
