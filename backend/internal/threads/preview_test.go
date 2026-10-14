package threads

import (
	"context"
	"os"
	"testing"
)

const sample = `<html><head>
<meta property="og:title" content="&#x627f;&#x661f;Ma (@chengxingma.hats) on Threads" />
<meta property="og:description" content="第一胎怕寶寶躺不舒服。&#10;你們都用哪台？" />
<meta property="og:image" content="https://instagram.ftpe20-1.fna.fbcdn.net/v/pic.jpg" />
<meta property="og:image" content="https://evil.example/pic.jpg" />
</head></html>`

func TestParse(t *testing.T) {
	got := Parse(sample)
	if got.Author != "承星Ma (@chengxingma.hats)" {
		t.Fatalf("author %q", got.Author)
	}
	if got.Body != "第一胎怕寶寶躺不舒服。 你們都用哪台？" {
		t.Fatalf("body %q", got.Body)
	}
	if got.ImageURL != "https://instagram.ftpe20-1.fna.fbcdn.net/v/pic.jpg" {
		t.Fatalf("image %q", got.ImageURL)
	}
}

func TestLiveShare(t *testing.T) {
	if os.Getenv("THREADS_LIVE") == "" {
		t.Skip()
	}
	got, err := Fetch(context.Background(), "https://www.threads.com/share/BAYwq7bx9V/")
	if err != nil {
		t.Fatal(err)
	}
	if got.Author == "" || got.Body == "" {
		t.Fatalf("empty preview author=%q body=%q", got.Author, got.Body)
	}
	if len(got.Image) == 0 {
		t.Fatal("missing image")
	}
}

func TestAllowedPage(t *testing.T) {
	if !AllowedPage("https://www.threads.com/share/abc/") {
		t.Fatal("share")
	}
	if !AllowedPage("https://threads.net/@name/post/abc") {
		t.Fatal("post")
	}
	if AllowedPage("https://example.com/share/abc") || AllowedPage("http://www.threads.com/share/abc") {
		t.Fatal("rejected host or scheme")
	}
}
