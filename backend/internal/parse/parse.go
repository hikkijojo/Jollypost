package parse

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

type InputError struct {
	Msg string
}

func (e InputError) Error() string { return e.Msg }

type Item struct {
	Code    string
	URL     string
	Product string
}

var (
	errNotCode = errors.New("not code")
	urlRe      = regexp.MustCompile(`https?://(?:www\.)?threads\.(?:com|net)/[^\s]+`)
	codeRe     = regexp.MustCompile(`^(\d{1,2})/(\d{1,2})[-－–—](\d+)$`)
)

func Parse(text string, year int) ([]Item, error) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	var acc builder
	var items []Item
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		display, err := NormalizeCode(line, year)
		if err == nil {
			if acc.url != "" {
				items = append(items, acc.item())
				acc = builder{}
			}
			acc.code = display
			continue
		}
		if !errors.Is(err, errNotCode) {
			return nil, InputError{Msg: fmt.Sprintf("第 %d 行的編號不正確", i+1)}
		}
		if u, rest, ok := extractURL(line); ok {
			if acc.url != "" {
				items = append(items, acc.item())
				acc = builder{}
			}
			acc.url = u
			if rest != "" {
				acc.parts = append(acc.parts, rest)
			}
			continue
		}
		acc.parts = append(acc.parts, line)
	}
	if acc.url != "" || acc.code != "" || len(acc.parts) > 0 {
		items = append(items, acc.item())
	}
	if len(items) == 0 {
		return nil, InputError{Msg: "請貼上老闆的訊息"}
	}
	for i, item := range items {
		if item.URL == "" {
			return nil, InputError{Msg: fmt.Sprintf("第 %d 則沒有 Threads 連結", i+1)}
		}
	}
	return items, nil
}

func NormalizeCode(raw string, year int) (string, error) {
	raw = strings.TrimSpace(raw)
	m := codeRe.FindStringSubmatch(raw)
	if m == nil {
		return "", errNotCode
	}
	month := atoi(m[1])
	day := atoi(m[2])
	n := atoi(m[3])
	if n < 1 || n > 999 {
		return "", InputError{Msg: "編號流水號要介於 1 到 999"}
	}
	stamp := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	if stamp.Month() != time.Month(month) || stamp.Day() != day {
		return "", InputError{Msg: "編號裡的日期不存在"}
	}
	return fmt.Sprintf("%d/%d-%d", month, day, n), nil
}

func NormalizeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "　", "")
	s = strings.TrimPrefix(s, "jolly")
	return s
}

type builder struct {
	code  string
	url   string
	parts []string
}

func (b builder) item() Item {
	return Item{
		Code:    b.code,
		URL:     b.url,
		Product: strings.TrimSpace(strings.Join(b.parts, " ")),
	}
}

func extractURL(line string) (string, string, bool) {
	loc := urlRe.FindStringIndex(line)
	if loc == nil {
		return "", "", false
	}
	raw := strings.TrimRight(line[loc[0]:loc[1]], ").,，。、")
	rest := strings.TrimSpace(line[:loc[0]] + " " + line[loc[1]:])
	return raw, rest, true
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	return n
}
