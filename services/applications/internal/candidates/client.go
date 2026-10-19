// Package candidates reads the applicant's display name from the service that
// owns it.
//
// The gateway's principal carries an email but no name, and inventing one from
// a request body would mean a candidate could put any name on an application.
// The candidates service is the only authority, so it is asked.
//
// Unlike the jobs lookup, this one is best-effort: a recruiter seeing an email
// instead of a full name is a cosmetic problem, while refusing an application
// because a profile service blipped loses the candidate. Applying is the most
// important write in the product and it should degrade, not fail.
package candidates

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/karmarat/platform/packages/goshared/observability"
)

// Profile is the part of a candidate an application needs.
type Profile struct {
	AccountID string `json:"accountId"`
	FullName  string `json:"fullName"`
	Email     string `json:"email"`
}

// Client calls the candidates service's internal API.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// Config configures the client.
type Config struct {
	BaseURL string
	Token   string
	Timeout time.Duration
}

// New builds a candidates client. A client with no base URL is inert and always
// reports "not found", which is what lets the service run before candidates is
// deployed.
func New(cfg Config) *Client {
	if cfg.Timeout == 0 {
		// Deliberately shorter than the jobs timeout: this lookup is optional, so
		// it should never be the reason an apply request feels slow.
		cfg.Timeout = 2 * time.Second
	}
	return &Client{
		baseURL: strings.TrimSuffix(cfg.BaseURL, "/"),
		token:   cfg.Token,
		http:    observability.HTTPClient(cfg.Timeout),
	}
}

// DisplayName returns the candidate's name, falling back to the supplied email.
//
// It never returns an error: every failure path degrades to the fallback, and
// the caller has nothing useful to do with the distinction.
func (c *Client) DisplayName(ctx context.Context, accountID, fallbackEmail string) string {
	profile, err := c.fetch(ctx, accountID)
	if err != nil || strings.TrimSpace(profile.FullName) == "" {
		return fallbackEmail
	}
	return strings.TrimSpace(profile.FullName)
}

func (c *Client) fetch(ctx context.Context, accountID string) (Profile, error) {
	if c.baseURL == "" || accountID == "" {
		return Profile{}, fmt.Errorf("candidates: not configured")
	}

	endpoint := fmt.Sprintf("%s/internal/candidates/%s", c.baseURL, url.PathEscape(accountID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Profile{}, err
	}
	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		req.Header.Set("X-Internal-Token", c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return Profile{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return Profile{}, fmt.Errorf("candidates: status %d", resp.StatusCode)
	}

	var profile Profile
	if err := json.NewDecoder(resp.Body).Decode(&profile); err != nil {
		return Profile{}, err
	}
	return profile, nil
}
