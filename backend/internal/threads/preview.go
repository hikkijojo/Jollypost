package threads

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const maxPage = 768 << 10
const maxImage = 2 << 20

type Preview struct {
	Author   string
	Body     string
	Image    []byte
	ImageURL string
}

var (
	metaTag = regexp.MustCompile(`(?is)<meta\b[^>]*>`)
	attrRe  = regexp.MustCompile(`(?is)\b(property|name|content)\s*=\s*("([^"]*)"|'([^']*)')`)
)

func AllowedPage(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return false
	}
	return hostOK(u.Hostname(), "threads.com", "threads.net")
}

func Fetch(ctx context.Context, pageURL string) (Preview, error) {
	if !AllowedPage(pageURL) {
		return Preview{}, fmt.Errorf("not a threads url")
	}
	client := &http.Client{Timeout: 12 * time.Second, CheckRedirect: limitRedirects(5)}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return Preview{}, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1")
	req.Header.Set("Accept", "text/html")
	res, err := client.Do(req)
	if err != nil {
		return Preview{}, err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return Preview{}, fmt.Errorf("threads status %d", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, maxPage))
	if err != nil {
		return Preview{}, err
	}
	preview := Parse(string(body))
	if preview.ImageURL == "" {
		return preview, nil
	}
	image, err := downloadImage(ctx, client, preview.ImageURL)
	if err != nil {
		preview.ImageURL = ""
		return preview, nil
	}
	preview.Image = image
	return preview, nil
}

func Parse(page string) Preview {
	var out Preview
	for _, tag := range metaTag.FindAllString(page, -1) {
		property, content := metaAttr(tag)
		if content == "" {
			continue
		}
		switch strings.ToLower(property) {
		case "og:title":
			if out.Author == "" {
				out.Author = cleanAuthor(content)
			}
		case "og:description", "description":
			if out.Body == "" {
				out.Body = cleanBody(content)
			}
		case "og:image":
			if out.ImageURL == "" && allowedImage(content) {
				out.ImageURL = content
			}
		}
	}
	return out
}

func metaAttr(tag string) (property, content string) {
	for _, match := range attrRe.FindAllStringSubmatch(tag, -1) {
		value := match[3]
		if value == "" {
			value = match[4]
		}
		value = strings.TrimSpace(html.UnescapeString(value))
		switch strings.ToLower(match[1]) {
		case "property", "name":
			property = value
		case "content":
			content = value
		}
	}
	return property, content
}

func cleanAuthor(title string) string {
	title = strings.TrimSpace(title)
	title = strings.TrimSuffix(title, " on Threads")
	return trimRunes(title, 120)
}

func cleanBody(body string) string {
	body = strings.TrimSpace(body)
	body = strings.Join(strings.Fields(body), " ")
	return trimRunes(body, 500)
}

func trimRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func allowedImage(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" {
		return false
	}
	return hostOK(u.Hostname(), "fbcdn.net", "cdninstagram.com", "threads.com", "threads.net", "instagram.com")
}

func hostOK(host string, suffixes ...string) bool {
	host = strings.ToLower(host)
	for _, suffix := range suffixes {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}

func limitRedirects(max int) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) >= max {
			return fmt.Errorf("too many redirects")
		}
		if !AllowedPage(req.URL.String()) && !allowedImage(req.URL.String()) {
			return fmt.Errorf("redirect left threads")
		}
		return nil
	}
}

func downloadImage(ctx context.Context, client *http.Client, raw string) ([]byte, error) {
	if !allowedImage(raw) {
		return nil, fmt.Errorf("image host")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Accept", "image/*")
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("image status %d", res.StatusCode)
	}
	kind := res.Header.Get("Content-Type")
	if kind != "" && !strings.HasPrefix(strings.ToLower(kind), "image/") {
		return nil, fmt.Errorf("not an image")
	}
	buf, err := io.ReadAll(io.LimitReader(res.Body, maxImage+1))
	if err != nil {
		return nil, err
	}
	if len(buf) == 0 || len(buf) > maxImage {
		return nil, fmt.Errorf("image size")
	}
	return buf, nil
}
