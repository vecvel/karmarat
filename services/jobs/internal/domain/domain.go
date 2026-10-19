// Package domain holds the jobs service's entities and rules.
//
// Nothing here performs I/O: the transitions a requisition may make and the
// shape a valid application form must have are decided in one place, so the
// HTTP layer and a future event consumer cannot disagree about them.
package domain

import (
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// Status is the lifecycle of a requisition.
//
// `draft` is where a job starts and where a duplicate lands; `archived` is the
// soft delete. A job leaves `open` for `on_hold` while hiring pauses and for
// `closed` when the role is filled or abandoned.
type Status string

const (
	StatusDraft    Status = "draft"
	StatusOpen     Status = "open"
	StatusOnHold   Status = "on_hold"
	StatusClosed   Status = "closed"
	StatusArchived Status = "archived"
)

// Valid reports whether the status is one the service recognizes.
func (s Status) Valid() bool {
	switch s {
	case StatusDraft, StatusOpen, StatusOnHold, StatusClosed, StatusArchived:
		return true
	default:
		return false
	}
}

// Terminal reports whether the requisition has finished its life.
//
// A terminal job accepts no visibility change: re-listing a filled role would
// take applications nobody intends to read.
func (s Status) Terminal() bool {
	return s == StatusClosed || s == StatusArchived
}

// EmploymentType is the contractual shape of the role.
type EmploymentType string

const (
	EmploymentFullTime   EmploymentType = "full_time"
	EmploymentPartTime   EmploymentType = "part_time"
	EmploymentContract   EmploymentType = "contract"
	EmploymentTemporary  EmploymentType = "temporary"
	EmploymentInternship EmploymentType = "internship"
	EmploymentVolunteer  EmploymentType = "volunteer"
)

// Valid reports whether the employment type is recognized.
func (e EmploymentType) Valid() bool {
	switch e {
	case EmploymentFullTime, EmploymentPartTime, EmploymentContract,
		EmploymentTemporary, EmploymentInternship, EmploymentVolunteer:
		return true
	default:
		return false
	}
}

// Seniority is the experience band the role is pitched at.
type Seniority string

const (
	SeniorityIntern    Seniority = "intern"
	SeniorityEntry     Seniority = "entry"
	SeniorityJunior    Seniority = "junior"
	SeniorityMid       Seniority = "mid"
	SenioritySenior    Seniority = "senior"
	SeniorityLead      Seniority = "lead"
	SeniorityPrincipal Seniority = "principal"
	SeniorityDirector  Seniority = "director"
	SeniorityExecutive Seniority = "executive"
)

// Valid reports whether the seniority band is recognized.
func (s Seniority) Valid() bool {
	switch s {
	case SeniorityIntern, SeniorityEntry, SeniorityJunior, SeniorityMid, SenioritySenior,
		SeniorityLead, SeniorityPrincipal, SeniorityDirector, SeniorityExecutive:
		return true
	default:
		return false
	}
}

// WorkMode says where the work happens; it is what most candidates filter on.
type WorkMode string

const (
	WorkOnsite WorkMode = "onsite"
	WorkHybrid WorkMode = "hybrid"
	WorkRemote WorkMode = "remote"
)

// Valid reports whether the work mode is recognized.
func (w WorkMode) Valid() bool {
	switch w {
	case WorkOnsite, WorkHybrid, WorkRemote:
		return true
	default:
		return false
	}
}

// SalaryRange is stored in minor units so no rounding ever happens in float.
type SalaryRange struct {
	Min *int64 `json:"min,omitempty"`
	Max *int64 `json:"max,omitempty"`
	// Currency is an ISO 4217 alphabetic code, upper-cased on write.
	Currency string `json:"currency,omitempty"`
	// Public decides whether the band reaches a candidate. A band the company
	// keeps internal must never appear in a public response.
	Public bool `json:"isPublic"`
}

// Declared reports whether either bound was given.
func (s SalaryRange) Declared() bool { return s.Min != nil || s.Max != nil }

// Job is a requisition owned by one company.
type Job struct {
	ID        string
	CompanyID string
	Slug      string

	Title          string
	Department     string
	Locations      []string
	WorkMode       WorkMode
	EmploymentType EmploymentType
	Seniority      Seniority

	// Description and Requirements are markdown authored by a recruiter. They are
	// never rendered here; the portals sanitize at render time.
	Description  string
	Requirements string

	Salary    SalaryRange
	Headcount int

	// HiringManagerID and RecruiterID are identity account ids. They are internal:
	// a public response must not disclose who owns a requisition.
	HiringManagerID string
	RecruiterID     string
	InternalNotes   string

	Status           Status
	VisibleOnPortal  bool
	VisibleOnNetwork bool

	Form ApplicationForm

	OpenedAt  *time.Time
	ClosedAt  *time.Time
	CreatedBy string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// PubliclyListed reports whether the job may be served on any public surface.
func (j Job) PubliclyListed() bool {
	return j.Status == StatusOpen && (j.VisibleOnPortal || j.VisibleOnNetwork)
}

/* -------------------------------------------------------------------------- */
/* Errors                                                                     */
/* -------------------------------------------------------------------------- */

var (
	// ErrJobNotFound is returned for a missing job and for another tenant's job
	// alike — the caller must not be able to tell the two apart.
	ErrJobNotFound = errors.New("job requisition could not be found")
	// ErrSlugTaken means the company already uses the slug on another job.
	ErrSlugTaken = errors.New("another requisition in this company already uses that link")
	// ErrNetworkSlugTaken means the slug is held on the shared jobs board.
	ErrNetworkSlugTaken = errors.New("another company already publishes that link on the Karmarat jobs board; choose a different link before publishing")
	// ErrJobTerminal means the requisition has been closed or archived.
	ErrJobTerminal = errors.New("closed or archived requisition cannot be changed; duplicate it into a new draft instead")
	// ErrAlreadyClosed means close was asked for twice.
	ErrAlreadyClosed = errors.New("requisition is already closed")
)

// ValidationError carries one field-level failure out of the domain layer.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

// FieldErrors is a set of field-level failures, shaped for a 422 body.
type FieldErrors map[string][]string

// Add records a failure against a field.
func (f FieldErrors) Add(field, message string) {
	f[field] = append(f[field], message)
}

// Any reports whether anything failed.
func (f FieldErrors) Any() bool { return len(f) > 0 }

/* -------------------------------------------------------------------------- */
/* Validation                                                                 */
/* -------------------------------------------------------------------------- */

// Limits the service enforces on requisition content. They are generous enough
// for a real job description and small enough that one tenant cannot fill the
// table with a single row.
const (
	MaxTitleLength        = 200
	MaxDepartmentLength   = 120
	MaxLocations          = 20
	MaxLocationLength     = 160
	MaxDescriptionLength  = 50_000
	MaxRequirementsLength = 20_000
	MaxInternalNotes      = 10_000
	MaxHeadcount          = 10_000
	MaxSlugLength         = 90
)

var (
	slugPattern     = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)
	// Account ids are the prefixed ULIDs identity mints; anything else is a
	// caller guessing at the shape of another service's identifiers.
	accountIDPattern = regexp.MustCompile(`^[a-z]{2,8}_[0-9A-HJKMNP-TV-Z]{26}$`)
)

// JobDraft is the validated content of a requisition, shared by create and
// update so one set of rules covers both paths.
type JobDraft struct {
	Title           string
	Slug            string
	Department      string
	Locations       []string
	WorkMode        WorkMode
	EmploymentType  EmploymentType
	Seniority       Seniority
	Description     string
	Requirements    string
	Salary          SalaryRange
	Headcount       int
	HiringManagerID string
	RecruiterID     string
	InternalNotes   string
}

// Normalize trims and canonicalizes the draft in place.
//
// Normalizing before validating means a title of "  Senior Engineer  " is
// accepted and stored once, rather than accepted and stored twice differently.
func (d *JobDraft) Normalize() {
	d.Title = collapseSpaces(d.Title)
	d.Slug = strings.ToLower(strings.TrimSpace(d.Slug))
	d.Department = collapseSpaces(d.Department)
	d.Description = strings.TrimSpace(d.Description)
	d.Requirements = strings.TrimSpace(d.Requirements)
	d.InternalNotes = strings.TrimSpace(d.InternalNotes)
	d.HiringManagerID = strings.TrimSpace(d.HiringManagerID)
	d.RecruiterID = strings.TrimSpace(d.RecruiterID)
	d.Salary.Currency = strings.ToUpper(strings.TrimSpace(d.Salary.Currency))

	locations := make([]string, 0, len(d.Locations))
	seen := make(map[string]struct{}, len(d.Locations))
	for _, location := range d.Locations {
		location = collapseSpaces(location)
		if location == "" {
			continue
		}
		key := strings.ToLower(location)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		locations = append(locations, location)
	}
	d.Locations = locations

	if d.Headcount == 0 {
		d.Headcount = 1
	}
	if d.WorkMode == "" {
		d.WorkMode = WorkOnsite
	}
	if d.Slug == "" {
		d.Slug = Slugify(d.Title)
	}
}

// Validate reports every problem with the draft at once.
//
// Returning the whole set matters for a long requisition form: a recruiter who
// has written a page of description should not discover a second problem only
// after fixing the first.
func (d JobDraft) Validate() FieldErrors {
	errs := FieldErrors{}

	switch {
	case d.Title == "":
		errs.Add("title", "A job title is required.")
	case len([]rune(d.Title)) > MaxTitleLength:
		errs.Add("title", "A job title may be at most 200 characters.")
	}

	if d.Slug == "" {
		// Only reachable from a title that slugifies to nothing, e.g. "!!!".
		errs.Add("slug", "A job title must contain at least one letter or digit.")
	} else if len(d.Slug) > MaxSlugLength || !slugPattern.MatchString(d.Slug) {
		errs.Add("slug", "A link may contain lower-case letters, digits and single hyphens only.")
	}

	if len([]rune(d.Department)) > MaxDepartmentLength {
		errs.Add("department", "A department may be at most 120 characters.")
	}

	if len(d.Locations) > MaxLocations {
		errs.Add("locations", "A requisition may list at most 20 locations.")
	}
	for i, location := range d.Locations {
		if len([]rune(location)) > MaxLocationLength {
			errs.Add(indexedField("locations", i), "A location may be at most 160 characters.")
		}
	}
	if d.WorkMode != WorkRemote && len(d.Locations) == 0 {
		errs.Add("locations", "A role that is not remote needs at least one location.")
	}

	if !d.WorkMode.Valid() {
		errs.Add("workMode", "Work mode must be one of: onsite, hybrid, remote.")
	}
	if !d.EmploymentType.Valid() {
		errs.Add("employmentType",
			"Employment type must be one of: full_time, part_time, contract, temporary, internship, volunteer.")
	}
	if !d.Seniority.Valid() {
		errs.Add("seniority",
			"Seniority must be one of: intern, entry, junior, mid, senior, lead, principal, director, executive.")
	}

	if len([]rune(d.Description)) > MaxDescriptionLength {
		errs.Add("description", "A description may be at most 50,000 characters.")
	}
	if len([]rune(d.Requirements)) > MaxRequirementsLength {
		errs.Add("requirements", "Requirements may be at most 20,000 characters.")
	}
	if len([]rune(d.InternalNotes)) > MaxInternalNotes {
		errs.Add("internalNotes", "Internal notes may be at most 10,000 characters.")
	}

	if d.Headcount < 1 || d.Headcount > MaxHeadcount {
		errs.Add("headcount", "Headcount must be between 1 and 10,000.")
	}

	for field, value := range map[string]string{
		"hiringManagerId": d.HiringManagerID,
		"recruiterId":     d.RecruiterID,
	} {
		if value != "" && !accountIDPattern.MatchString(value) {
			errs.Add(field, "This does not look like a valid account identifier.")
		}
	}

	d.validateSalary(errs)

	return errs
}

func (d JobDraft) validateSalary(errs FieldErrors) {
	if !d.Salary.Declared() {
		// An undeclared band with a stray currency is a caller mistake worth
		// reporting rather than silently dropping.
		if d.Salary.Currency != "" && !currencyPattern.MatchString(d.Salary.Currency) {
			errs.Add("salary.currency", "Currency must be a three-letter ISO 4217 code, such as USD.")
		}
		return
	}

	if !currencyPattern.MatchString(d.Salary.Currency) {
		errs.Add("salary.currency", "A salary range needs a three-letter ISO 4217 currency, such as USD.")
	}
	if d.Salary.Min != nil && *d.Salary.Min < 0 {
		errs.Add("salary.min", "A salary cannot be negative.")
	}
	if d.Salary.Max != nil && *d.Salary.Max < 0 {
		errs.Add("salary.max", "A salary cannot be negative.")
	}
	if d.Salary.Min != nil && d.Salary.Max != nil && *d.Salary.Min > *d.Salary.Max {
		errs.Add("salary.max", "The top of the range must be at least the bottom of it.")
	}
}

/* -------------------------------------------------------------------------- */
/* Transitions                                                                */
/* -------------------------------------------------------------------------- */

// Visibility is a requested publication state.
type Visibility struct {
	Portal  bool
	Network bool
}

// Any reports whether the job would be listed anywhere.
func (v Visibility) Any() bool { return v.Portal || v.Network }

// PublishPermissions names the permissions a visibility change requires.
//
// Only a surface being *switched on* costs a permission: a recruiter who may not
// publish to the network must still be able to take a job down from it.
func PublishPermissions(current Job, requested Visibility) []string {
	var required []string
	if requested.Portal && !current.VisibleOnPortal {
		required = append(required, PermissionPublishPortal)
	}
	if requested.Network && !current.VisibleOnNetwork {
		required = append(required, PermissionPublishNetwork)
	}
	return required
}

// CheckPublish reports whether the requested visibility is a legal transition.
func CheckPublish(current Job, requested Visibility) error {
	if current.Status.Terminal() {
		return ErrJobTerminal
	}
	if requested.Any() && current.Status == StatusOnHold {
		return &ValidationError{
			Field:   "status",
			Message: "A requisition that is on hold must be reopened before it can be published.",
		}
	}
	return nil
}

// StatusAfterPublish is the status a publish leaves the job in.
//
// Publishing a draft is what opens it: a recruiter should not have to make two
// calls to put a job live, and a job that is visible but not `open` would be a
// state the public board has to special-case.
func StatusAfterPublish(current Job, requested Visibility) Status {
	if requested.Any() && current.Status == StatusDraft {
		return StatusOpen
	}
	return current.Status
}

// CheckClose reports whether the job may be closed.
func CheckClose(current Job) error {
	switch current.Status {
	case StatusArchived:
		return ErrJobTerminal
	case StatusClosed:
		return ErrAlreadyClosed
	default:
		return nil
	}
}

/* -------------------------------------------------------------------------- */
/* Slugs                                                                      */
/* -------------------------------------------------------------------------- */

// Slugify turns a title into a URL segment.
//
// Only ASCII letters and digits survive, because the slug ends up in a public
// URL that must be typable, quotable in an email, and stable across the
// normalization a browser applies to a pasted address.
//
// Accented letters are folded to their base letter rather than dropped:
// "Ingénieur Sénior" has to become "ingenieur-senior", not "ing-nieur-s-nior".
// Dropping them mangles ordinary European job titles, and a public careers URL
// is something a recruiter reads aloud.
func Slugify(title string) string {
	var b strings.Builder
	b.Grow(len(title))

	lastHyphen := true // leading hyphens are suppressed
	for _, r := range strings.ToLower(title) {
		folded := foldToASCII(r)

		switch {
		case folded != "":
			b.WriteString(folded)
			lastHyphen = false
		case !lastHyphen:
			b.WriteByte('-')
			lastHyphen = true
		}
	}

	slug := strings.Trim(b.String(), "-")
	if len(slug) > MaxSlugLength {
		slug = strings.Trim(slug[:MaxSlugLength], "-")
	}
	return slug
}

// asciiFolding maps the Latin-1 and Latin Extended-A letters we actually see in
// job titles onto their ASCII equivalents. A full Unicode normalization would
// pull in golang.org/x/text for a handful of letters; this covers the European
// languages the product serves and degrades to a hyphen for anything else.
var asciiFolding = map[rune]string{
	'à': "a", 'á': "a", 'â': "a", 'ã': "a", 'ä': "a", 'å': "a", 'ā': "a", 'ă': "a", 'ą': "a",
	'ç': "c", 'ć': "c", 'č': "c", 'ĉ': "c", 'ċ': "c",
	'ď': "d", 'đ': "d",
	'è': "e", 'é': "e", 'ê': "e", 'ë': "e", 'ē': "e", 'ĕ': "e", 'ė': "e", 'ę': "e", 'ě': "e",
	'ĝ': "g", 'ğ': "g", 'ġ': "g", 'ģ': "g",
	'ĥ': "h", 'ħ': "h",
	'ì': "i", 'í': "i", 'î': "i", 'ï': "i", 'ĩ': "i", 'ī': "i", 'ĭ': "i", 'į': "i", 'ı': "i",
	'ĵ': "j",
	'ķ': "k",
	'ĺ': "l", 'ļ': "l", 'ľ': "l", 'ł': "l",
	'ñ': "n", 'ń': "n", 'ņ': "n", 'ň': "n",
	'ò': "o", 'ó': "o", 'ô': "o", 'õ': "o", 'ö': "o", 'ø': "o", 'ō': "o", 'ŏ': "o", 'ő': "o",
	'ŕ': "r", 'ŗ': "r", 'ř': "r",
	'ś': "s", 'ŝ': "s", 'ş': "s", 'š': "s", 'ș': "s",
	'ţ': "t", 'ť': "t", 'ŧ': "t", 'ț': "t",
	'ù': "u", 'ú': "u", 'û': "u", 'ü': "u", 'ũ': "u", 'ū': "u", 'ŭ': "u", 'ů': "u", 'ű': "u", 'ų': "u",
	'ŵ': "w",
	'ý': "y", 'ÿ': "y", 'ŷ': "y",
	'ź': "z", 'ż': "z", 'ž': "z",
	// Ligatures and the German sharp s expand rather than fold.
	'æ': "ae", 'œ': "oe", 'ß': "ss", 'þ': "th", 'ð': "d",
}

// foldToASCII returns the ASCII text for a rune, or "" when it has none.
func foldToASCII(r rune) string {
	if r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
		return string(r)
	}
	return asciiFolding[r]
}

func collapseSpaces(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func indexedField(name string, index int) string {
	return name + "[" + itoa(index) + "]"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
