// Package candidates asks the service that owns a candidate whether they exist
// and whether they are open to being found.
//
// Messaging must not guess at this. A candidate's discoverability is a consent
// setting they can switch off at any moment, and a projection that is briefly
// stale would let a company open a thread seconds after the candidate closed the
// door. The authority is asked on the request path, every time.
//
// One thing it cannot answer: the per-company block list. The candidates
// service's internal profile endpoint exposes `discoverable` but not
// `hideFromCompanies`, so that half of the decision is served from this
// service's own projection of the visibility events. See the note on
// api.Eligibility.
package candidates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/karmarat/platform/packages/goshared/observability"
)

// Profile is the part of a candidate messaging needs.
type Profile struct {
	AccountID    string `json:"accountId"`
	ProfileID    string `json:"profileId"`
	FullName     string `json:"fullName"`
	Email        string `json:"email"`
	Headline     string `json:"headline"`
	Discoverable bool   `json:"discoverable"`
}

var (
	// ErrNotFound means no such candidate account exists.
	ErrNotFound = errors.New("candidates: profile not found")
	// ErrUnavailable means the candidates service could not be reached or
	// answered with something this service cannot interpret.
	//
	// It is distinct from ErrNotFound on purpose: "we could not ask" must fail
	// closed with a 503 the caller can retry, not look like "this person does
	// not exist".
	ErrUnavailable = errors.New("candidates: service unavailable")
)

// Client calls the candidates service's internal API.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// Config configures the client.
type Config struct {
	BaseURL string
	// Token is the shared secret guarding every service's /internal endpoints.
	Token   string
	Timeout time.Duration
}

// New builds a candidates client.
func New(cfg Config) *Client {
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Second
	}
	return &Client{
		baseURL: strings.TrimSuffix(cfg.BaseURL, "/"),
		token:   cfg.Token,
		// The traced client keeps a slow "open conversation" attributable to the
		// hop that was actually slow.
		http: observability.HTTPClient(cfg.Timeout),
	}
}

// Configured reports whether the client has somewhere to call.
func (c *Client) Configured() bool { return c.baseURL != "" }

// Fetch returns a candidate's internal profile.
func (c *Client) Fetch(ctx context.Context, accountID string) (Profile, error) {
	if !c.Configured() {
		return Profile{}, fmt.Errorf("%w: no base URL configured", ErrUnavailable)
	}
	if strings.TrimSpace(accountID) == "" {
		return Profile{}, ErrNotFound
	}

	endpoint := fmt.Sprintf("%s/internal/candidates/%s", c.baseURL, url.PathEscape(accountID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Profile{}, fmt.Errorf("candidates: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		req.Header.Set("X-Internal-Token", c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return Profile{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound, http.StatusGone:
		// Gone is a deleted profile. A caller has nothing different to do with
		// the distinction, and preserving it would confirm the account once
		// existed.
		return Profile{}, ErrNotFound
	default:
		return Profile{}, fmt.Errorf("%w: status %d", ErrUnavailable, resp.StatusCode)
	}

	var profile Profile
	if err := json.NewDecoder(resp.Body).Decode(&profile); err != nil {
		return Profile{}, fmt.Errorf("%w: malformed response", ErrUnavailable)
	}
	if profile.AccountID == "" {
		return Profile{}, fmt.Errorf("%w: response was missing the account identity", ErrUnavailable)
	}

	return profile, nil
}
