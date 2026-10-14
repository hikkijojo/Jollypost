package week

import (
	"testing"
	"time"
)

func TestRangeWednesday(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 14, 21, 30, 0, 0, loc)
	start, end, label := Range(now, 0)
	if start.Day() != 12 || start.Month() != 10 {
		t.Fatalf("start=%s", start)
	}
	if !end.Equal(start.AddDate(0, 0, 7)) {
		t.Fatalf("end=%s", end)
	}
	if label != "10/12–10/18" {
		t.Fatalf("label=%s", label)
	}
	prev, _, prevLabel := Range(now, -1)
	if prev.Day() != 5 || prevLabel != "10/5–10/11" {
		t.Fatalf("prev=%s %s", prev, prevLabel)
	}
}
