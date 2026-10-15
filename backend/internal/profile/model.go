// Package profile owns private resumes, confirmed candidate versions and their access policy.
package profile

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"jobassistant/backend/internal/identity"
)

const MaxFileBytes = 10 << 20
const MaxTextBytes = 192 << 10

var (
	ErrInvalidFile          = errors.New("unsupported, encrypted, scanned or invalid resume file")
	ErrExtractorUnavailable = errors.New("local PDF extractor unavailable")
	ErrUnconfigured         = errors.New("model service not configured")
	ErrInvalidOutput        = errors.New("model output or evidence invalid")
	ErrStale                = errors.New("resource version changed")
)

// Evidence is a verbatim citation from the redacted resume, never an invented fact.
type Evidence struct {
	Field string `json:"field"`
	Quote string `json:"quote"`
}

// Candidate contains confirmed job-relevant facts; nil means the user has not confirmed a value.
type Candidate struct {
	Education        string     `json:"education"`
	EducationRegion  string     `json:"education_region"`
	GraduationDate   string     `json:"graduation_date"`
	MajorCodes       []string   `json:"major_codes"`
	Student          *bool      `json:"student"`
	ExperienceMonths *int       `json:"experience_months"`
	InternshipMonths *int       `json:"internship_months"`
	DaysPerWeek      *int       `json:"days_per_week"`
	Skills           []string   `json:"skills"`
	Evidence         []Evidence `json:"evidence"`
}

// Parser is implemented by the configured external model adapter.
type Parser interface {
	Parse(context.Context, string) (Candidate, error)
	Version() string
	Configured() bool
}

// Scope comes from the authenticated route, never an uploaded document.
type Scope struct {
	ActorID, OwnerID string
	Admin            bool
}

// ValidateCandidate bounds user edits and requires verifiable citations on model-generated drafts.
func ValidateCandidate(c Candidate, source string, requireEvidence bool) error {
	contains := func(value, choices string) bool {
		for _, choice := range strings.Split(choices, "|") {
			if value == choice {
				return true
			}
		}
		return false
	}
	if !contains(c.Education, "|unknown|associate|bachelor|master|doctorate") || !contains(c.EducationRegion, "|unknown|domestic|overseas") || len(c.MajorCodes) > 12 || len(c.Skills) > 40 || len(c.Evidence) > 60 {
		return identity.ErrInvalidInput
	}
	if c.GraduationDate != "" {
		if _, err := time.Parse("2006-01-02", c.GraduationDate); err != nil {
			return identity.ErrInvalidInput
		}
	}
	for _, item := range []struct {
		v   *int
		max int
	}{{c.ExperienceMonths, 1200}, {c.InternshipMonths, 60}, {c.DaysPerWeek, 7}} {
		if item.v != nil && (*item.v < 0 || *item.v > item.max) {
			return identity.ErrInvalidInput
		}
	}
	for _, skill := range c.Skills {
		if strings.TrimSpace(skill) == "" || utf8.RuneCountInString(skill) > 80 {
			return identity.ErrInvalidInput
		}
		if requireEvidence && !strings.Contains(strings.ToLower(source), strings.ToLower(skill)) {
			return ErrInvalidOutput
		}
	}
	for _, code := range c.MajorCodes {
		if !contains(code, "computer_science|software_engineering|communications|electronics|mathematics|information_technology|artificial_intelligence") {
			return identity.ErrInvalidInput
		}
	}
	fields := map[string]bool{}
	for _, evidence := range c.Evidence {
		if len(evidence.Quote) > 2000 || !contains(evidence.Field, "education|education_region|graduation_date|major_codes|student|experience_months|internship_months|days_per_week|skills") {
			return ErrInvalidOutput
		}
		if requireEvidence && (strings.TrimSpace(evidence.Quote) == "" || !strings.Contains(source, evidence.Quote)) {
			return ErrInvalidOutput
		}
		fields[evidence.Field] = true
	}
	if requireEvidence {
		facts := map[string]bool{"education": c.Education != "" && c.Education != "unknown", "education_region": c.EducationRegion != "" && c.EducationRegion != "unknown", "graduation_date": c.GraduationDate != "", "major_codes": len(c.MajorCodes) > 0, "student": c.Student != nil, "experience_months": c.ExperienceMonths != nil, "internship_months": c.InternshipMonths != nil, "days_per_week": c.DaysPerWeek != nil, "skills": len(c.Skills) > 0}
		for field, present := range facts {
			if present && !fields[field] {
				return ErrInvalidOutput
			}
		}
	}
	return nil
}
