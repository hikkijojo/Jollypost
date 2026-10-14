package parse

import "testing"

func TestParseFormats(t *testing.T) {
	text := "10/11-3\nhttps://www.threads.com/share/_1QiARHx8/\n\n10/11-4\nhttps://www.threads.com/share/BBcAk7AWcJ/\n"
	items, err := Parse(text, 2026)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("len=%d", len(items))
	}
	if items[0].Code != "10/11-3" || items[0].URL != "https://www.threads.com/share/_1QiARHx8/" {
		t.Fatalf("first=%+v", items[0])
	}
	if items[1].Code != "10/11-4" || items[1].Product != "" {
		t.Fatalf("second=%+v", items[1])
	}

	items, err = Parse("https://www.threads.com/share/_tmf3gfLm/\nJolly 床邊床\n", 2026)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Code != "" || items[0].Product != "Jolly 床邊床" {
		t.Fatalf("%+v", items)
	}
}

func TestParseNoBlankAndDash(t *testing.T) {
	text := "10/03－4\nhttps://www.threads.net/share/abc/\n10/11-5\nhttps://threads.com/share/def/"
	items, err := Parse(text, 2026)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("len=%d", len(items))
	}
	if items[0].Code != "10/3-4" {
		t.Fatalf("code=%s", items[0].Code)
	}
	if items[1].URL != "https://threads.com/share/def/" {
		t.Fatalf("url=%s", items[1].URL)
	}
}

func TestParseMissingURL(t *testing.T) {
	_, err := Parse("只有產品名稱", 2026)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestNormalizeName(t *testing.T) {
	if NormalizeName("Jolly 床邊床") != "床邊床" {
		t.Fatal(NormalizeName("Jolly 床邊床"))
	}
	if NormalizeName("Jolly pad") != "pad" {
		t.Fatal(NormalizeName("Jolly pad"))
	}
}
