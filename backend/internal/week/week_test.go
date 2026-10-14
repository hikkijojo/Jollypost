package week

import (
	"testing"
	"time"
)

func TestRangeBeforeSettlement(t *testing.T) {
	loc := taipei(t)
	now := time.Date(2026, 10, 14, 21, 30, 0, 0, loc)
	start, end, label := Range(now, 0)
	if !start.Equal(time.Date(2026, 9, 26, 0, 0, 0, 0, loc)) {
		t.Fatalf("start=%s", start)
	}
	if !end.Equal(time.Date(2026, 10, 26, 0, 0, 0, 0, loc)) {
		t.Fatalf("end=%s", end)
	}
	if label != "9/26–10/25 · 10/26 結算" {
		t.Fatalf("label=%s", label)
	}
	prev, prevEnd, prevLabel := Range(now, -1)
	if !prev.Equal(time.Date(2026, 8, 26, 0, 0, 0, 0, loc)) || !prevEnd.Equal(time.Date(2026, 9, 26, 0, 0, 0, 0, loc)) {
		t.Fatalf("prev=%s %s", prev, prevEnd)
	}
	if prevLabel != "8/26–9/25 · 9/26 結算" {
		t.Fatalf("prev label=%s", prevLabel)
	}
}

func TestRangeOnSettlementDay(t *testing.T) {
	loc := taipei(t)
	now := time.Date(2026, 10, 26, 9, 0, 0, 0, loc)
	start, end, label := Range(now, 0)
	if !start.Equal(time.Date(2026, 10, 26, 0, 0, 0, 0, loc)) || !end.Equal(time.Date(2026, 11, 26, 0, 0, 0, 0, loc)) {
		t.Fatalf("start=%s end=%s", start, end)
	}
	if label != "10/26–11/25 · 11/26 結算" {
		t.Fatalf("label=%s", label)
	}
}

func TestRangeAcrossYear(t *testing.T) {
	loc := taipei(t)
	now := time.Date(2027, 1, 10, 8, 0, 0, 0, loc)
	start, end, label := Range(now, 0)
	if !start.Equal(time.Date(2026, 12, 26, 0, 0, 0, 0, loc)) || !end.Equal(time.Date(2027, 1, 26, 0, 0, 0, 0, loc)) {
		t.Fatalf("start=%s end=%s", start, end)
	}
	if label != "12/26–1/25 · 1/26 結算" {
		t.Fatalf("label=%s", label)
	}
}

func taipei(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}
