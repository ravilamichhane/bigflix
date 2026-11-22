package subtitles

import (
	"archive/zip"
	"bigflix/internal/database"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestSubDLTVSearchAndDownload(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "subdl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	f, _ := zw.Create("ep2.srt")
	_, _ = f.Write([]byte("1\n00:00:01,000 --> 00:00:04,000\nWelcome to Lumon\n"))
	zw.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/subtitles":
			if r.URL.Query().Get("season_number") != "1" || r.URL.Query().Get("episode_number") != "2" {
				t.Errorf("missing season/episode query: %s", r.URL.RawQuery)
			}
			fmt.Fprint(w, `{"status":true,"subtitles":[{"release_name":"Severance.S01E02.1080p.WEB-DL","url":"/subtitle/ep2.zip","hi":false}]}`)
		case "/subtitle/ep2.zip":
			_, _ = w.Write(archive.Bytes())
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	origAPI, origDL := subdlAPIBase, subdlDownloadBase
	subdlAPIBase = srv.URL + "/api/v1/subtitles"
	subdlDownloadBase = srv.URL
	t.Cleanup(func() {
		subdlAPIBase = origAPI
		subdlDownloadBase = origDL
	})

	_ = db.PutConfig(context.Background(), "subtitles", database.SubtitleProviderConfig{SubDLAPIKey: "test-key"})
	svc := New(db)
	svc.Client = srv.Client()

	res, provider, err := svc.SearchMedia(context.Background(), SearchQuery{
		IMDbID:    "tt11280740",
		MediaType: "episode",
		Title:     "Severance",
		Season:    1,
		Episode:   2,
		Language:  "en",
	})
	if err != nil || provider != "subdl" || len(res) != 1 {
		t.Fatalf("SearchMedia SubDL failed: provider=%q res=%+v err=%v", provider, res, err)
	}

	sub, err := svc.DownloadMedia(context.Background(), "packhash", 1, res[0].DownloadKey, 0, res[0].Label, "en")
	if err != nil {
		t.Fatalf("DownloadMedia SubDL failed: %v", err)
	}
	vtt, err := db.SubtitleForFile(context.Background(), "packhash", 1, sub.ID)
	if err != nil || !strings.Contains(string(vtt), "Welcome to Lumon") {
		t.Fatalf("unexpected VTT stored for fileIndex 1: %q err=%v", vtt, err)
	}
	// Ensure fileIndex 0 doesn't see fileIndex 1's subtitle
	list0, _ := db.SubtitlesForFile(context.Background(), "packhash", 0)
	if len(list0) != 0 {
		t.Fatalf("file 0 leaked file 1 subtitle: %+v", list0)
	}
}

func TestOpenSubtitlesTVSearchAndDownload(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "os.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/subtitles":
			if r.Header.Get("Api-Key") != "os-key" {
				http.Error(w, "no key", 401)
				return
			}
			fmt.Fprint(w, `{"data":[{"attributes":{"release":"Show.S01E03.1080p","ratings":8.5,"hearing_impaired":true,"files":[{"file_id":4242,"file_name":"show.srt"}]}}]}`)
		case "/download":
			fmt.Fprintf(w, `{"link":"%s/file/4242.srt"}`, srvURL)
		case "/file/4242.srt":
			fmt.Fprint(w, "1\n00:00:02,000 --> 00:00:05,000\nOpenSubtitles line\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	srvURL = srv.URL

	origOS := openSubtitlesAPIBase
	openSubtitlesAPIBase = srv.URL
	t.Cleanup(func() { openSubtitlesAPIBase = origOS })

	_ = db.PutConfig(context.Background(), "subtitles", database.SubtitleProviderConfig{OpenSubtitlesAPIKey: "os-key"})
	svc := New(db)
	svc.Client = srv.Client()

	res, provider, err := svc.SearchMedia(context.Background(), SearchQuery{
		TMDBID:    95396,
		MediaType: "episode",
		Title:     "Severance",
		Season:    1,
		Episode:   3,
		Language:  "en",
	})
	if err != nil || provider != "opensubtitles" || len(res) != 1 {
		t.Fatalf("SearchMedia OpenSubtitles failed: provider=%q res=%+v err=%v", provider, res, err)
	}
	sub, err := svc.DownloadMedia(context.Background(), "packhash", 2, res[0].DownloadKey, 0, res[0].Label, "en")
	if err != nil {
		t.Fatalf("DownloadMedia OpenSubtitles failed: %v", err)
	}
	vtt, err := db.SubtitleForFile(context.Background(), "packhash", 2, sub.ID)
	if err != nil || !strings.Contains(string(vtt), "OpenSubtitles line") {
		t.Fatalf("unexpected OpenSubtitles VTT: %q err=%v", vtt, err)
	}
}
