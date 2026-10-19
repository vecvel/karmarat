package api

import (
	"time"

	"github.com/karmarat/platform/services/jobs/internal/domain"
)

// salaryView is the internal shape of a band: the `isPublic` flag travels with
// it so a recruiter can see at a glance what a candidate would.
type salaryView struct {
	Min      *int64 `json:"min,omitempty"`
	Max      *int64 `json:"max,omitempty"`
	Currency string `json:"currency,omitempty"`
	IsPublic bool   `json:"isPublic"`
}

type visibilityView struct {
	Portal  bool `json:"portal"`
	Network bool `json:"network"`
}

// jobView is the full requisition, for a principal inside the company.
type jobView struct {
	ID             string                `json:"id"`
	CompanyID      string                `json:"companyId"`
	Slug           string                `json:"slug"`
	Title          string                `json:"title"`
	Department     string                `json:"department"`
	Locations      []string              `json:"locations"`
	WorkMode       domain.WorkMode       `json:"workMode"`
	EmploymentType domain.EmploymentType `json:"employmentType"`
	Seniority      domain.Seniority      `json:"seniority"`
	Description    string                `json:"description"`
	Requirements   string                `json:"requirements"`
	Salary         salaryView            `json:"salary"`
	Headcount      int                   `json:"headcount"`

	HiringManagerID string `json:"hiringManagerId,omitempty"`
	RecruiterID     string `json:"recruiterId,omitempty"`
	InternalNotes   string `json:"internalNotes,omitempty"`

	Status     domain.Status  `json:"status"`
	Visibility visibilityView `json:"visibility"`

	FormVersion int `json:"formVersion"`

	OpenedAt  *time.Time `json:"openedAt,omitempty"`
	ClosedAt  *time.Time `json:"closedAt,omitempty"`
	CreatedBy string     `json:"createdBy,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
}

// jobSummary is a row in the requisition list.
//
// The description, the requirements and the form are left out: a list of 25
// requisitions would otherwise carry a megabyte of markdown nobody is reading
// on that screen.
type jobSummary struct {
	ID             string                `json:"id"`
	Slug           string                `json:"slug"`
	Title          string                `json:"title"`
	Department     string                `json:"department"`
	Locations      []string              `json:"locations"`
	WorkMode       domain.WorkMode       `json:"workMode"`
	EmploymentType domain.EmploymentType `json:"employmentType"`
	Seniority      domain.Seniority      `json:"seniority"`
	Salary         salaryView            `json:"salary"`
	Headcount      int                   `json:"headcount"`
	Status         domain.Status         `json:"status"`
	Visibility     visibilityView        `json:"visibility"`
	OpenedAt       *time.Time            `json:"openedAt,omitempty"`
	ClosedAt       *time.Time            `json:"closedAt,omitempty"`
	UpdatedAt      time.Time             `json:"updatedAt"`
}

func toJobView(job domain.Job) jobView {
	return jobView{
		ID:              job.ID,
		CompanyID:       job.CompanyID,
		Slug:            job.Slug,
		Title:           job.Title,
		Department:      job.Department,
		Locations:       job.Locations,
		WorkMode:        job.WorkMode,
		EmploymentType:  job.EmploymentType,
		Seniority:       job.Seniority,
		Description:     job.Description,
		Requirements:    job.Requirements,
		Salary:          toSalaryView(job.Salary),
		Headcount:       job.Headcount,
		HiringManagerID: job.HiringManagerID,
		RecruiterID:     job.RecruiterID,
		InternalNotes:   job.InternalNotes,
		Status:          job.Status,
		Visibility:      visibilityView{Portal: job.VisibleOnPortal, Network: job.VisibleOnNetwork},
		FormVersion:     job.Form.Version,
		OpenedAt:        job.OpenedAt,
		ClosedAt:        job.ClosedAt,
		CreatedBy:       job.CreatedBy,
		CreatedAt:       job.CreatedAt,
		UpdatedAt:       job.UpdatedAt,
	}
}

func toJobSummary(job domain.Job) jobSummary {
	return jobSummary{
		ID:             job.ID,
		Slug:           job.Slug,
		Title:          job.Title,
		Department:     job.Department,
		Locations:      job.Locations,
		WorkMode:       job.WorkMode,
		EmploymentType: job.EmploymentType,
		Seniority:      job.Seniority,
		Salary:         toSalaryView(job.Salary),
		Headcount:      job.Headcount,
		Status:         job.Status,
		Visibility:     visibilityView{Portal: job.VisibleOnPortal, Network: job.VisibleOnNetwork},
		OpenedAt:       job.OpenedAt,
		ClosedAt:       job.ClosedAt,
		UpdatedAt:      job.UpdatedAt,
	}
}

func toSalaryView(salary domain.SalaryRange) salaryView {
	return salaryView{
		Min:      salary.Min,
		Max:      salary.Max,
		Currency: salary.Currency,
		IsPublic: salary.Public,
	}
}
