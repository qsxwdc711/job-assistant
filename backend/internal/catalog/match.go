package catalog

import (
	"slices"
	"strconv"
	"strings"
	"time"
)

// MatchingVariants applies the board's filters to an immutable public job snapshot.
// A direction, major and place must all match inside the same variant.
func MatchingVariants(job Job, f Filter, now time.Time) []Variant {
	contains := func(values []string, value string) bool { return len(values) == 0 || slices.Contains(values, value) }
	if !job.Published || !strings.Contains(strings.ToLower(job.CompanyName), strings.ToLower(f.Company)) || !contains(f.Industries, job.Industry) || !contains(f.Natures, job.Nature) || !contains(f.RecruitmentTypes, job.RecruitmentType) || !contains(f.WrittenTests, job.WrittenTest) {
		return nil
	}
	if len(f.Tiers) > 0 && !slices.ContainsFunc(job.Tiers, func(value string) bool { return slices.Contains(f.Tiers, value) }) {
		return nil
	}
	state := job.PublicStatus
	if job.DeadlineAt != nil && !job.DeadlineAt.After(now) {
		state = "closed"
	}
	states := f.States
	if len(states) == 0 {
		states = []string{"open", "unknown"}
	}
	if !slices.Contains(states, "all") && !slices.Contains(states, state) {
		return nil
	}
	if f.DeadlineBefore != "" && (job.DeadlineDate == "" || job.DeadlineDate > f.DeadlineBefore) {
		return nil
	}
	if f.GraduationYear != 0 {
		year := strconv.Itoa(f.GraduationYear)
		match := len(job.GraduationRules) == 0 && f.IncludeUnknown
		for _, rule := range job.GraduationRules {
			if len(rule.From) >= 4 && len(rule.Through) >= 4 && rule.From[:4] <= year && rule.Through[:4] >= year {
				match = true
			}
		}
		if !match {
			return nil
		}
	}
	queryMatch := strings.Contains(strings.ToLower(job.Title), strings.ToLower(f.Query))
	for _, v := range job.Variants {
		if strings.Contains(strings.ToLower(strings.Join([]string{v.Name, v.Responsibilities, v.Requirements, v.Preferred}, " ")), strings.ToLower(f.Query)) {
			queryMatch = true
		}
	}
	if !queryMatch {
		return nil
	}
	var result []Variant
	for _, v := range job.Variants {
		if !contains(f.Directions, v.Direction) {
			continue
		}
		if len(f.Majors) > 0 && v.Major.Kind != "unrestricted" && v.Major.Kind != "preferred" && !(v.Major.Kind == "unknown" && f.IncludeUnknown) && !slices.ContainsFunc(v.Major.Tags, func(value string) bool { return slices.Contains(f.Majors, value) }) {
			continue
		}
		if len(f.Cities) > 0 && !slices.ContainsFunc(v.Locations, func(place Location) bool {
			return slices.Contains(f.Cities, place.City) || slices.Contains(f.Cities, place.Code) || slices.Contains(f.Cities, place.Kind)
		}) {
			continue
		}
		result = append(result, v)
	}
	return result
}
