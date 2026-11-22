package catalog

import (
	"bigflix/internal/database"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testService(t *testing.T, h http.HandlerFunc) *Service {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	s, err := New(db, "https://example.com/api/v2/", "")
	if err != nil {
		t.Fatal(err)
	}
	s.base = server.URL + "/"
	s.client = server.Client()
	return s
}

const fixture = `{"status":"ok","data":{"movie_count":1,"movies":[{"id":12,"title":"Example","imdb_code":"tt1234567","large_cover_image":"javascript:alert(1)","torrents":[{"hash":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","quality":"1080p"},{"hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","quality":"1080p"},{"hash":"bad"}]}]}}`

func TestListNormalizesAndCaches(t *testing.T) {
	calls := 0
	s := testService(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("query_term") != "a & b" {
			t.Error("query not encoded")
		}
		fmt.Fprint(w, fixture)
	})
	for range 2 {
		f, err := s.List(context.Background(), "a & b", "", "date_added", 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(f.Movies) != 1 || f.Movies[0].Poster != "" || len(f.Movies[0].Torrents) != 1 {
			t.Fatalf("normalization: %+v", f)
		}
	}
	if calls != 1 {
		t.Fatalf("expected cache hit: calls=%d", calls)
	}
}
func TestStaleFallbackDoesNotReplaceGoodData(t *testing.T) {
	s := testService(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"status":"error"}`) })
	key := s.base + "list_movies.json?limit=24&order_by=desc&page=1&sort_by=date_added"
	s.store.Put(context.Background(), key, []byte(fixture), time.Now().Add(-time.Hour))
	f, err := s.List(context.Background(), "", "", "", 1)
	if err != nil || !f.Stale || len(f.Movies) != 1 {
		t.Fatalf("stale fallback: %+v %v", f, err)
	}
	b, _, _ := s.store.Get(context.Background(), key)
	if string(b) != fixture {
		t.Fatal("failed response overwrote snapshot")
	}
}
func TestNoCacheProviderError(t *testing.T) {
	s := testService(t, func(w http.ResponseWriter, r *http.Request) { http.Error(w, "failed", 503) })
	_, err := s.List(context.Background(), "", "", "", 1)
	if err == nil {
		t.Fatal("expected unavailable")
	}
}
func TestThrottleBackoff(t *testing.T) {
	calls := 0
	s := testService(t, func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(429) })
	for range 2 {
		s.List(context.Background(), "", "", "", 1)
	}
	if calls != 1 {
		t.Fatal("throttled provider was immediately retried")
	}
}
func TestIMDbMatchesOnly(t *testing.T) {
	s := testService(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "external_ids") {
			fmt.Fprint(w, `{"imdb_id":"tt1234567"}`)
			return
		}
		fmt.Fprint(w, `{"status":"ok","data":{"movie_count":2,"movies":[{"id":1,"title":"Exact","imdb_code":"tt1234567"},{"id":2,"title":"Wrong","imdb_code":"tt7654321"}]}}`)
	})
	s.tmdb = s.base
	s.token = "test"
	f, err := s.FindSources(context.Background(), 5)
	if err != nil || len(f.Movies) != 1 || f.Movies[0].ID != 1 {
		t.Fatalf("unsafe match: %+v %v", f, err)
	}
}
func TestTMDBRequiresConfiguration(t *testing.T) {
	s := testService(t, func(w http.ResponseWriter, r *http.Request) { t.Fatal("must not fetch without credential") })
	_, err := s.Discover(context.Background(), "day", 1)
	if err != ErrConfiguration {
		t.Fatal(err)
	}
}
func TestPublicClientBlocksLoopback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("private service reached") }))
	defer server.Close()
	_, err := PublicClient().Get(server.URL)
	if err == nil {
		t.Fatal("private destination allowed")
	}
}

func TestTMDBBrowseSeriesSeasonAndDynamicToken(t *testing.T) {
	s := testService(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer live-db-token" {
			http.Error(w, "unauthorized", 401)
			return
		}
		switch r.URL.Path {
		case "/trending/all/day":
			fmt.Fprint(w, `{"total_results":3,"results":[
				{"id":10,"media_type":"movie","title":"Arrival","release_date":"2016-11-11","vote_average":7.9,"poster_path":"/arr.jpg"},
				{"id":20,"media_type":"tv","name":"Severance","first_air_date":"2022-02-18","vote_average":8.4,"poster_path":"/sev.jpg"},
				{"id":30,"media_type":"person","name":"Actor"}
			]}`)
		case "/tv/20":
			fmt.Fprint(w, `{"id":20,"name":"Severance","first_air_date":"2022-02-18","status":"Returning Series","number_of_seasons":2,"number_of_episodes":19,"external_ids":{"imdb_id":"tt11280740"},"seasons":[{"season_number":0,"name":"Specials","episode_count":1},{"season_number":2,"name":"Season 2","episode_count":10},{"season_number":1,"name":"Season 1","episode_count":9}]}`)
		case "/tv/20/season/1":
			fmt.Fprint(w, `{"season_number":1,"name":"Season 1","episodes":[{"season_number":1,"episode_number":1,"name":"Good News About Hell","runtime":57,"still_path":"/ep1.jpg"}]}`)
		case "/find/tt11280740":
			fmt.Fprint(w, `{"tv_results":[{"id":20,"name":"Severance","first_air_date":"2022-02-18"}]}`)
		default:
			http.NotFound(w, r)
		}
	})
	s.tmdb = s.base
	if s.HasTMDB() {
		t.Fatal("expected TMDB unconfigured initially")
	}
	if err := s.SetTMDBToken(context.Background(), "live-db-token"); err != nil {
		t.Fatal(err)
	}
	if !s.HasTMDB() {
		t.Fatal("expected TMDB configured after SetTMDBToken")
	}

	feed, err := s.Browse(context.Background(), "all", "trending", "", "", "", 1)
	if err != nil || len(feed.Items) != 2 {
		t.Fatalf("expected 2 non-person items, got %+v err=%v", feed, err)
	}
	if feed.Items[0].MediaType != "movie" || feed.Items[1].MediaType != "tv" || feed.Items[1].Year != 2022 {
		t.Fatalf("unexpected browse items: %+v", feed.Items)
	}

	series, _, _, err := s.SeriesDetail(context.Background(), 20)
	if err != nil || series.IMDb != "tt11280740" || len(series.Seasons) != 3 || series.Seasons[0].SeasonNumber != 1 || series.Seasons[2].SeasonNumber != 0 {
		t.Fatalf("unexpected series detail: %+v err=%v", series, err)
	}

	season, _, _, err := s.SeasonDetail(context.Background(), 20, 1)
	if err != nil || len(season.Episodes) != 1 || season.Episodes[0].Name != "Good News About Hell" {
		t.Fatalf("unexpected season detail: %+v err=%v", season, err)
	}

	found, err := s.Browse(context.Background(), "all", "", "tt11280740", "", "", 1)
	if err != nil || len(found.Items) != 1 || found.Items[0].TMDBID != 20 {
		t.Fatalf("unexpected IMDb lookup: %+v err=%v", found, err)
	}
}
