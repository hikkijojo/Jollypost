package week

import (
	"fmt"
	"time"
)

// Range 是結算篇章。篇章從上一個 26 日到這個 25 日，結算日是區間結束後的 26 日。
// offset 0 是今天所在的篇章，-1 是上一篇。
func Range(now time.Time, offset int) (start, end time.Time, label string) {
	loc := now.Location()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	if day.Day() >= 26 {
		start = time.Date(day.Year(), day.Month(), 26, 0, 0, 0, 0, loc)
	} else {
		start = time.Date(day.Year(), day.Month(), 26, 0, 0, 0, 0, loc).AddDate(0, -1, 0)
	}
	start = start.AddDate(0, offset, 0)
	end = start.AddDate(0, 1, 0)
	last := end.AddDate(0, 0, -1)
	label = fmt.Sprintf("%d/%d–%d/%d · %d/%d 結算", int(start.Month()), start.Day(), int(last.Month()), last.Day(), int(end.Month()), end.Day())
	return start, end, label
}
