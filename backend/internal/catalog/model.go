// Package catalog stores official job evidence and searchable public opportunities.
package catalog

import "time"

// Location preserves the original place alongside a normalized prefecture or overseas city.
type Location struct {
	Code     string `json:"code"`
	Kind     string `json:"kind"`
	Country  string `json:"country"`
	Province string `json:"province"`
	City     string `json:"city"`
	Raw      string `json:"raw"`
}

// DepartmentGroup keeps a department and its applicable places together.
type DepartmentGroup struct {
	Code      string     `json:"code"`
	Name      string     `json:"name"`
	Locations []Location `json:"locations"`
}

// MajorRequirement distinguishes explicit, related, preferred, unrestricted and unknown requirements.
type MajorRequirement struct {
	Kind     string   `json:"kind"`
	Tags     []string `json:"tags"`
	Evidence string   `json:"evidence"`
}

// GraduationRule is an applicable date range, never a parent menu's stale display year.
type GraduationRule struct {
	Region    string `json:"region"`
	Education string `json:"education"`
	From      string `json:"from"`
	Through   string `json:"through"`
	Evidence  string `json:"evidence"`
}

// Variant is one coherent job intention; its conditions and cities cannot be mixed with others.
type Variant struct {
	Key              string            `json:"key"`
	Name             string            `json:"name"`
	Direction        string            `json:"direction"`
	Responsibilities string            `json:"responsibilities"`
	Requirements     string            `json:"requirements"`
	Preferred        string            `json:"preferred"`
	Major            MajorRequirement  `json:"major"`
	Locations        []Location        `json:"locations"`
	Departments      []DepartmentGroup `json:"departments"`
}

// Document is stable, evidence-backed job content used for version comparison.
type Document struct {
	ExternalID              string           `json:"external_id"`
	CompanyCode             string           `json:"company_code"`
	SourceCode              string           `json:"source_code"`
	CampaignKey             string           `json:"campaign_key"`
	CampaignName            string           `json:"campaign_name"`
	Title                   string           `json:"title"`
	RecruitmentType         string           `json:"recruitment_type"`
	Publisher               string           `json:"publisher"`
	PublicStatus            string           `json:"public_status"`
	StatusEvidence          string           `json:"status_evidence"`
	WrittenTest             string           `json:"written_test"`
	WrittenTestEvidence     string           `json:"written_test_evidence"`
	SourceURL               string           `json:"source_url"`
	ApplyURL                string           `json:"apply_url"`
	ApplyLevel              string           `json:"apply_level"`
	LoginRequired           bool             `json:"login_required"`
	DeadlineDate            string           `json:"deadline_date"`
	DeadlineAt              *time.Time       `json:"deadline_at"`
	DeadlineTimezone        string           `json:"deadline_timezone"`
	GraduationRules         []GraduationRule `json:"graduation_rules"`
	GraduationPreference    string           `json:"graduation_preference"`
	StudentRequired         string           `json:"student_required"`
	MinimumEducation        string           `json:"minimum_education"`
	MinimumInternshipMonths *int             `json:"minimum_internship_months"`
	DaysPerWeek             *int             `json:"days_per_week"`
	CampaignEvidence        string           `json:"campaign_evidence"`
	Variants                []Variant        `json:"variants"`
}

// Job combines content with live publication and verification metadata.
type Job struct {
	Document
	ID                 string    `json:"id"`
	CompanyName        string    `json:"company_name"`
	Industry           string    `json:"industry"`
	Nature             string    `json:"nature"`
	Tiers              []string  `json:"tiers"`
	CompanyBasis       string    `json:"company_basis"`
	Version            int64     `json:"version"`
	Published          bool      `json:"published"`
	VerificationStatus string    `json:"verification_status"`
	VerifiedAt         time.Time `json:"verified_at"`
	UpdatedAt          time.Time `json:"updated_at"`
	ReviewNote         string    `json:"review_note"`
}

// Company contains administrator-maintained classifications with their attribution.
type Company struct {
	Code     string   `json:"code"`
	Name     string   `json:"name"`
	Industry string   `json:"industry"`
	Nature   string   `json:"nature"`
	Tiers    []string `json:"tiers"`
	Basis    string   `json:"basis"`
}

// Source reports scope, control state and the latest observed collection result.
type Source struct {
	Code            string     `json:"code"`
	CompanyCode     string     `json:"company_code"`
	Name            string     `json:"name"`
	OfficialURL     string     `json:"official_url"`
	Coverage        string     `json:"coverage"`
	Enabled         bool       `json:"enabled"`
	LastStatus      string     `json:"last_status"`
	LastError       string     `json:"last_error"`
	LastCheckedAt   *time.Time `json:"last_checked_at"`
	LastSucceededAt *time.Time `json:"last_succeeded_at"`
	JobCount        int        `json:"job_count"`
}

// DictionaryEntry is an extensible filter choice, independent of an industry-specific table.
type DictionaryEntry struct {
	Kind string `json:"kind"`
	Code string `json:"code"`
	Name string `json:"name"`
}

// Page contains a bounded job result and its full filtered count.
type Page struct {
	Items  []Job `json:"items"`
	Total  int   `json:"total"`
	Limit  int   `json:"limit"`
	Offset int   `json:"offset"`
}
