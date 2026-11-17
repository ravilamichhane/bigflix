package subtitles

import (
	"archive/zip"
	"bigflix/internal/database"
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
		bad               bool
	}{
		{"srt", "\ufeff1\r\n00:00:01,250 --> 00:00:03,500\r\nHello <script>alert(1)</script>\r\n", "00:00:01.250 --> 00:00:03.500\nHello &lt;script&gt;alert(1)&lt;/script&gt;", false},
		{"vtt", "WEBVTT\n\n00:01.000 --> 00:03.000 align:start\nHello\n", "00:00:01.000 --> 00:00:03.000", false},
		{"empty", "WEBVTT\n", "", true},
		{"reversed", "1\n00:00:04,000 --> 00:00:01,000\nHi", "", true},
		{"bad time", "1\n00:65:04,000 --> 00:66:01,000\nHi", "", true},
		{"binary", string([]byte{0xff, 0xfe}), "", true},
		{"oversize", strings.Repeat("x", MaxSize+1), "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, err := Normalize([]byte(tc.input))
			if (err != nil) != tc.bad {
				t.Fatalf("error: %v", err)
			}
			if !tc.bad && (!strings.HasPrefix(string(b), "WEBVTT\n") || !strings.Contains(string(b), tc.want)) {
				t.Fatalf("output: %s", b)
			}
		})
	}
}

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestYIFYSearchDownloadAndCache(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	f, _ := zw.Create("nested/example.srt")
	f.Write([]byte("1\n00:00:01,000 --> 00:00:03,000\nHello\n"))
	zw.Close()
	calls := 0
	s := New(db)
	s.Client = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls++
		var body []byte
		if r.Header.Get("Authorization") != "" || r.Header.Get("Api-Key") != "" {
			t.Fatal("unexpected credentials")
		}
		switch r.URL.Path {
		case "/movie-imdb/tt1234567":
			body = []byte(`<table><thead><tr><th>language</th></tr></thead><tbody><tr data-id="123"><td class="rating-cell">5</td><td><span class="sub-lang">English</span></td><td><a href="/subtitles/example-123"><span>subtitle</span> Example.1080p</a></td></tr><tr data-id="124"><td><span class="sub-lang">French</span></td><td><a href="/subtitles/example-124">Example</a></td></tr></tbody></table>`)
		case "/subtitles/example-123":
			body = []byte(`<a class="btn-icon download-subtitle" href="/subtitle/example.zip">Download</a>`)
		case "/subtitle/example.zip":
			if r.Header.Get("Referer") != "https://yifysubtitles.ch/subtitles/example-123" {
				t.Fatal("missing subtitle page referrer")
			}
			body = archive.Bytes()
		default:
			t.Fatalf("unexpected URL %s", r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header)}, nil
	})}
	ctx := context.Background()
	results, err := s.Search(ctx, "tt1234567", "Example", "en")
	if err != nil || len(results) != 1 || results[0].Rating != 5 {
		t.Fatalf("search: %v %v", results, err)
	}
	s.Search(ctx, "tt1234567", "Example", "en")
	if calls != 1 {
		t.Fatal("search cache missed")
	}
	sub, err := s.Download(ctx, "hash", 123, "untrusted label", "en")
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.Download(ctx, "hash", 123, "", "en")
	if err != nil || sub.ID != again.ID || calls != 3 {
		t.Fatal("download cache missed")
	}
	if sub.Label != "Example.1080p" {
		t.Fatal("trusted release label not used")
	}
	b, err := db.Subtitle(ctx, "hash", sub.ID)
	if err != nil || !strings.Contains(string(b), "Hello") {
		t.Fatal("missing cached VTT")
	}
	if _, err = s.Search(ctx, "", "Example", "en"); err == nil {
		t.Fatal("missing IMDb accepted")
	}
	if _, err = s.get(ctx, "https://evil.example/file.zip"); err == nil {
		t.Fatal("external download accepted")
	}
	if _, err = s.Download(ctx, "hash", 999, "", "en"); err == nil {
		t.Fatal("unsearched ID accepted")
	}
}

// Opt-in: exercises the real provider without modifying the user's library.
func TestYIFYLiveDownload(t *testing.T) {
	if os.Getenv("BIGFLIX_TEST_YIFY_LIVE") != "1" {
		t.Skip("live provider check is opt-in")
	}
	db, err := database.Open(filepath.Join(t.TempDir(), "subtitles.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db)
	defer s.Client.CloseIdleConnections()
	ctx := context.Background()
	results, err := s.Search(ctx, "tt1318514", "Rise of the Planet of the Apes", "en")
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range results {
		if result.ID != 480027 {
			continue
		}
		sub, err := s.Download(ctx, "live-test", result.ID, result.Label, "en")
		if err != nil {
			t.Fatal(err)
		}
		vtt, err := db.Subtitle(ctx, "live-test", sub.ID)
		if err != nil || !bytes.HasPrefix(vtt, []byte("WEBVTT")) || !bytes.Contains(vtt, []byte(" --> ")) {
			t.Fatalf("invalid cached subtitle: %v", err)
		}
		t.Logf("Downloaded, converted and cached %d bytes of WebVTT", len(vtt))
		return
	}
	t.Fatal("reference subtitle missing from provider listing")
}
func TestYIFYZipBounds(t *testing.T) {
	if _, err := unpack([]byte("<html>challenge</html>")); err == nil {
		t.Fatal("HTML accepted as ZIP")
	}
	for _, data := range []string{"not a subtitle", strings.Repeat("x", MaxSize+1)} {
		var b bytes.Buffer
		w := zip.NewWriter(&b)
		f, _ := w.Create("test.srt")
		f.Write([]byte(data))
		w.Close()
		if _, err := unpack(b.Bytes()); err == nil {
			t.Fatal("invalid or oversized subtitle accepted")
		}
	}
}

func TestYIFYChallengeError(t *testing.T) {
	s := New(nil)
	s.Client = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 403, Header: http.Header{"Cf-Mitigated": []string{"challenge"}}, Body: io.NopCloser(strings.NewReader("challenge"))}, nil
	})}
	_, err := s.get(context.Background(), "/subtitle/example.zip")
	if err == nil || !strings.Contains(err.Error(), "browser verification") {
		t.Fatalf("missing actionable error: %v", err)
	}
}
