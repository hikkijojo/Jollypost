package week

import (
	"fmt"
	"time"
)

func Range(now time.Time, offset int) (start, end time.Time, label string) {
	loc := now.Location()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	sinceMonday := (int(day.Weekday()) + 6) % 7
	start = day.AddDate(0, 0, -sinceMonday+offset*7)
	end = start.AddDate(0, 0, 7)
	last := end.AddDate(0, 0, -1)
	label = fmt.Sprintf("%d/%d–%d/%d", int(start.Month()), start.Day(), int(last.Month()), last.Day())
	return start, end, label
}
