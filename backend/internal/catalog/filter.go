package catalog

import (
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"jobassistant/backend/internal/identity"
)

// Filter describes bounded, combinable conditions rather than a personal eligibility decision.
type Filter struct {
	Query                                                                                          string
	Company                                                                                        string
	Directions, Majors, Cities, Industries, Natures, Tiers, RecruitmentTypes, WrittenTests, States []string
	GraduationYear                                                                                 int
	DeadlineBefore                                                                                 string
	IncludeUnknown                                                                                 bool
	Sort                                                                                           string
	Limit, Offset                                                                                  int
	IncludeHidden                                                                                  bool
}

// ParseFilter validates query values before they become SQL parameters.
func ParseFilter(values url.Values) (Filter, error) {
	f := Filter{Query: strings.TrimSpace(values.Get("q")), Company: strings.TrimSpace(values.Get("company")), IncludeUnknown: true, Sort: "updated", Limit: 20}
	invalid := func(field string) (Filter, error) {
		return Filter{}, fmt.Errorf("%w: invalid %s filter", identity.ErrInvalidInput, field)
	}
	if utf8.RuneCountInString(f.Query) > 120 || utf8.RuneCountInString(f.Company) > 80 {
		return invalid("text")
	}
	fields := []struct {
		key    string
		target *[]string
	}{
		{"direction", &f.Directions}, {"major", &f.Majors}, {"city", &f.Cities}, {"industry", &f.Industries}, {"nature", &f.Natures}, {"tier", &f.Tiers}, {"recruitment_type", &f.RecruitmentTypes}, {"written_test", &f.WrittenTests}, {"status", &f.States},
	}
	for _, field := range fields {
		for _, raw := range values[field.key] {
			for _, value := range strings.Split(raw, ",") {
				value = strings.TrimSpace(value)
				if value == "" {
					continue
				}
				if utf8.RuneCountInString(value) > 80 {
					return invalid(field.key)
				}
				*field.target = append(*field.target, value)
			}
		}
		if len(*field.target) > 10 {
			return invalid(field.key)
		}
	}
	allowed := func(items []string, choices string) bool {
		for _, item := range items {
			if !slices.Contains(strings.Split(choices, "|"), item) {
				return false
			}
		}
		return true
	}
	if !allowed(f.WrittenTests, "yes|no|unknown") {
		return invalid("written_test")
	}
	if !allowed(f.States, "open|closed|unknown|all") {
		return invalid("status")
	}
	if !allowed(f.RecruitmentTypes, "campus|internship|early_career") {
		return invalid("recruitment_type")
	}
	if value := values.Get("include_unknown"); value != "" {
		v, err := strconv.ParseBool(value)
		if err != nil {
			return invalid("include_unknown")
		}
		f.IncludeUnknown = v
	}
	for _, field := range []struct {
		key      string
		target   *int
		min, max int
	}{{"limit", &f.Limit, 1, 100}, {"offset", &f.Offset, 0, 1000000}, {"graduation_year", &f.GraduationYear, 2000, 2100}} {
		if value := values.Get(field.key); value != "" {
			v, err := strconv.Atoi(value)
			if err != nil || v < field.min || v > field.max {
				return invalid(field.key)
			}
			*field.target = v
		}
	}
	if value := values.Get("deadline_before"); value != "" {
		if _, err := time.Parse("2006-01-02", value); err != nil {
			return invalid("deadline_before")
		}
		f.DeadlineBefore = value
	}
	if value := values.Get("sort"); value != "" {
		if value != "updated" && value != "deadline" {
			return invalid("sort")
		}
		f.Sort = value
	}
	return f, nil
}
