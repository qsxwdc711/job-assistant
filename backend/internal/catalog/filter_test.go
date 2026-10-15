package catalog

import (
	"net/url"
	"testing"
)

func TestFilterInputBoundaries(t *testing.T) {
	for _, query := range []string{"written_test=false", "status=open%7Cclosed", "recruitment_type=campus%7Cinternship", "limit=101", "offset=-1", "graduation_year=tomorrow", "deadline_before=2026-13-40", "include_unknown=perhaps", "sort=password"} {
		values, _ := url.ParseQuery(query)
		if _, err := ParseFilter(values); err == nil {
			t.Errorf("invalid filter accepted: %s", query)
		}
	}
	values, _ := url.ParseQuery("direction=backend&direction=data&city=深圳市&graduation_year=2027")
	f, err := ParseFilter(values)
	if err != nil || len(f.Directions) != 2 || !f.IncludeUnknown || f.Limit != 20 {
		t.Fatalf("combined filters: %#v %v", f, err)
	}
}
