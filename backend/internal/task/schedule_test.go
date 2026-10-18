package task

import (
	"testing"
	"time"
)

func TestDayTimesUseShanghaiDateAndCutoff(t *testing.T) {
	now, err := time.Parse(time.RFC3339, "2026-10-16T17:30:00Z")
	if err != nil {
		t.Fatal(err)
	}
	day, start, cutoff := DayTimes(now)
	if day != "2026-10-17" || start.Format(time.RFC3339) != "2026-10-17T08:00:00+08:00" || cutoff.Format(time.RFC3339) != "2026-10-17T09:07:00+08:00" || !now.Before(start) {
		t.Fatalf("Shanghai schedule: day=%s start=%s cutoff=%s", day, start, cutoff)
	}
}
