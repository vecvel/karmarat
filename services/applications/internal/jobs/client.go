// Package jobs reads the requisition and application form a submission is
// checked against.
//
// The jobs service owns both. This service asks it rather than keeping a copy,
// because a form edited a second before a submission must be the form that
// submission is validated against — a projection that is briefly stale would
// accept answers to questions the company had already removed.
package jobs

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
	"github.com/karmarat/platform/services/applications/internal/domain"
)

// Job is the part of a requisition an application needs.
type Job struct {
	ID          string      `json:"id"`
	CompanyID   string      `json:"companyId"`
	CompanyName string      `json:"companyName"`
	Title       string      `json:"title"`
	Status      string      `json:"status"`
	Form        domain.Form `json:"form"`
	// AcceptingApplications is the jobs service's own answer to "may this be
	// applied to?", so the rule that decides it stays with the service that owns
	// the requisition's lifecycle.
	AcceptingApplications *bool `json:"acceptingApplications,omitempty"`
}

// Open reports whether the job may still be applied to.
func (j Job) Open() bool {
	if j.AcceptingApplications != nil {
		return *j.AcceptingApplications
	}
	return strings.EqualFold(j.Status, "open") || strings.EqualFold(j.Status, "published")
}

// Client calls the jobs service's internal API.
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

// New builds a jobs client.
func New(cfg Config) *Client {
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Second
	}
	return &Client{
		baseURL: strings.TrimSuffix(cfg.BaseURL, "/"),
		token:   cfg.Token,
		// The traced client keeps a slow apply request attributable to the hop
		// that was actually slow.
		http: observability.HTTPClient(cfg.Timeout),
	}
}

// ErrUnavailable means the jobs service could not be reached or answered with an
// error this service cannot interpret.
var ErrUnavailable = errors.New("the jobs service is unavailable")

// Fetch returns a job and its application form.
func (c *Client) Fetch(ctx context.Context, jobID string) (Job, error) {
	endpoint := fmt.Sprintf("%s/internal/jobs/%s", c.baseURL, url.PathEscape(jobID))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Job{}, fmt.Errorf("jobs: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		req.Header.Set("X-Internal-Token", c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return Job{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return Job{}, domain.ErrJobNotFound
	case http.StatusGone:
		return Job{}, domain.ErrJobNotAccepting
	default:
		return Job{}, fmt.Errorf("%w: status %d", ErrUnavailable, resp.StatusCode)
	}

	var job Job
	if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
		return Job{}, fmt.Errorf("%w: malformed response", ErrUnavailable)
	}
	if job.ID == "" || job.CompanyID == "" {
		return Job{}, fmt.Errorf("%w: response was missing the job identity", ErrUnavailable)
	}

	return job, nil
}
