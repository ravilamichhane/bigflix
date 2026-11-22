package web

import (
	"bigflix/internal/catalog"
	"bigflix/internal/database"
	"bigflix/internal/downloads"
	bigflixv1 "bigflix/internal/gen/bigflix/v1"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anacrolix/torrent"
)

func TestTVSeriesCatalogAutoPlaySettingsAndAI(t *testing.T) {
	t.Setenv("AUTH_TOKEN", "tv-secret")
	root := t.TempDir()
	db, err := database.Open(filepath.Join(root, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cfg := torrent.NewDefaultClientConfig()
	cfg.SetListenAddr("127.0.0.1:0")
	cfg.NoDHT = true
	cfg.DisableTrackers = true
	cfg.DisablePEX = true
	cfg.DisableUTP = true
	cfg.DisableIPv6 = true
	cfg.DisableWebtorrent = true
	cfg.DisableWebseeds = true
	cfg.NoDefaultPortForwarding = true
	engine, err := downloads.Open(db, downloads.Options{MediaDir: filepath.Join(root, "media"), ClientConfig: cfg})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()

	// Pre-populate TMDB snapshots so catalog calls don't require external network
	tmdbBase := "https://api.themoviedb.org/3/"
	_ = db.Put(context.Background(), tmdbBase+"tv/95396?append_to_response=external_ids",
		[]byte(`{"id":95396,"name":"Severance","first_air_date":"2022-02-18","status":"Returning Series","number_of_seasons":2,"number_of_episodes":19,"original_language":"en","external_ids":{"imdb_id":"tt11280740"},"seasons":[{"season_number":1,"name":"Season 1","episode_count":9},{"season_number":2,"name":"Season 2","episode_count":10}]}`),
		time.Now())
	_ = db.Put(context.Background(), tmdbBase+"tv/95396/season/1",
		[]byte(`{"season_number":1,"name":"Season 1","episodes":[{"season_number":1,"episode_number":1,"name":"Good News About Hell","runtime":57},{"season_number":1,"episode_number":2,"name":"Half Loop","runtime":55}]}`),
		time.Now())
	_ = db.Put(context.Background(), tmdbBase+"trending/all/day?page=1",
		[]byte(`{"total_results":1,"results":[{"id":95396,"media_type":"tv","name":"Severance","first_air_date":"2022-02-18","vote_average":8.4}]}`),
		time.Now())

	// Pre-populate source aggregator snapshot for S01E02 (2 candidates)
	hBest := strings.Repeat("1", 40)
	hBackup := strings.Repeat("2", 40)
	_ = db.Put(context.Background(), "sources:v2:episode:95396:s1e2",
		[]byte(fmt.Sprintf(`{"results":[{"hash":"%s","title":"Severance.S01E02.1080p.WEB-DL.x264.AAC","provider":"eztv","size_bytes":1600000000,"seeds":75,"peers":12},{"hash":"%s","title":"Severance.S01E02.720p.WEB.x264","provider":"apibay","size_bytes":800000000,"seeds":30,"peers":5}],"providers_queried":["eztv","apibay"]}`, hBest, hBackup)),
		time.Now())

	// Mock local llama-server for TestAIConnection and ranking
	aiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		content := `{"ok":true}`
		if rf, ok := body["response_format"].(map[string]any); ok {
			if js, ok := rf["json_schema"].(map[string]any); ok && js["name"] == "torrent_ranking" {
				content = `{"ranked_ids":[0,1],"rejected":[],"season_pack_ids":[],"note":"Picked clean 1080p H.264 AAC release"}`
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": content}}},
		})
	}))
	defer aiServer.Close()

	service, err := catalog.New(db, "https://example.com/api/v2/", "")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(service, engine)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	var tok atomic.Value
	tok.Store("tv-secret")
	client := bearerClient(srv.URL, srv.Client(), &tok)
	ctx := context.Background()

	// 1. Before TMDB token is set, BrowseCatalog returns friendly onboarding message
	unconf, err := client.BrowseCatalog(ctx, connect.NewRequest(&bigflixv1.BrowseCatalogRequest{MediaType: "all"}))
	if err != nil || unconf.Msg.HasTmdb || unconf.Msg.ErrorMessage == "" {
		t.Fatalf("expected unconfigured TMDB guidance, got %+v err=%v", unconf, err)
	}

	// 2. Save TMDB token, playback prefs, and local AI server URL via SaveAppSettings
	tmdbTok := "test-tmdb-token"
	saveRes, err := client.SaveAppSettings(ctx, connect.NewRequest(&bigflixv1.SaveAppSettingsRequest{
		TmdbReadToken: &tmdbTok,
		Playback: &bigflixv1.PlaybackPreferences{
			TargetQuality:     "1080p",
			PreferDirectPlay:  true,
			PreferredLanguage: "original",
			AutoEvict:         true,
		},
		Ai: &bigflixv1.AISetting{
			Enabled:    true,
			ApiUrl:     aiServer.URL + "/v1",
			Model:      "Qwen2.5-1.5B-Instruct",
			TimeoutSec: 5,
		},
	}))
	if err != nil || !saveRes.Msg.HasTmdb {
		t.Fatalf("SaveAppSettings failed: %+v err=%v", saveRes, err)
	}

	// 3. TestAIConnection verifies our mock llama-server
	testAI, err := client.TestAIConnection(ctx, connect.NewRequest(&bigflixv1.TestAIConnectionRequest{}))
	if err != nil || !testAI.Msg.Ok {
		t.Fatalf("TestAIConnection failed: %+v err=%v", testAI, err)
	}

	// 4. BrowseCatalog, GetSeriesDetail, GetSeasonDetail work with TMDB
	browse, err := client.BrowseCatalog(ctx, connect.NewRequest(&bigflixv1.BrowseCatalogRequest{MediaType: "all", Feed: "trending"}))
	if err != nil || len(browse.Msg.Items) != 1 || browse.Msg.Items[0].MediaType != "tv" {
		t.Fatalf("BrowseCatalog unexpected: %+v err=%v", browse, err)
	}
	serRes, err := client.GetSeriesDetail(ctx, connect.NewRequest(&bigflixv1.GetSeriesDetailRequest{TmdbId: 95396}))
	if err != nil || serRes.Msg.Series.Title != "Severance" || serRes.Msg.NextUp.EpisodeNumber != 1 {
		t.Fatalf("GetSeriesDetail unexpected: %+v err=%v", serRes, err)
	}
	snRes, err := client.GetSeasonDetail(ctx, connect.NewRequest(&bigflixv1.GetSeasonDetailRequest{TmdbId: 95396, SeasonNumber: 1}))
	if err != nil || len(snRes.Msg.Episodes) != 2 {
		t.Fatalf("GetSeasonDetail unexpected: %+v err=%v", snRes, err)
	}

	// 5. ResolveSources ranks candidates using mock local AI
	resolved, err := client.ResolveSources(ctx, connect.NewRequest(&bigflixv1.ResolveSourcesRequest{
		MediaType:     "episode",
		TmdbId:        95396,
		SeasonNumber:  1,
		EpisodeNumber: 2,
	}))
	if err != nil || resolved.Msg.RankedBy != "ai" || len(resolved.Msg.Candidates) != 2 || resolved.Msg.Candidates[0].Hash != hBest {
		t.Fatalf("ResolveSources unexpected: %+v err=%v", resolved, err)
	}

	// 6. StartPlayback rejects unverified override hash, succeeds with 1-click auto-pick
	if _, err := client.StartPlayback(ctx, connect.NewRequest(&bigflixv1.StartPlaybackRequest{
		MediaType:     "episode",
		TmdbId:        95396,
		SeasonNumber:  1,
		EpisodeNumber: 2,
		OverrideHash:  strings.Repeat("f", 40),
	})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("expected CodeInvalidArgument for unlisted override hash, got %v", err)
	}

	playRes, err := client.StartPlayback(ctx, connect.NewRequest(&bigflixv1.StartPlaybackRequest{
		MediaType:     "episode",
		TmdbId:        95396,
		SeasonNumber:  1,
		EpisodeNumber: 2,
	}))
	if err != nil || playRes.Msg.Download.Hash != hBest || playRes.Msg.Download.FailoverTotal != 2 || playRes.Msg.Download.EpisodeNumber != 2 {
		t.Fatalf("StartPlayback unexpected: %+v err=%v", playRes, err)
	}

	// 7. Progress updates populate Continue Watching and update Series NextUp
	if _, err := client.UpdateDownload(ctx, connect.NewRequest(&bigflixv1.UpdateDownloadRequest{
		Hash:     hBest,
		Action:   "progress",
		Position: 1200,
		Duration: 3300,
	})); err != nil {
		t.Fatalf("UpdateDownload progress failed: %v", err)
	}
	cw, err := client.ListContinueWatching(ctx, connect.NewRequest(&bigflixv1.ListContinueWatchingRequest{}))
	if err != nil || len(cw.Msg.Items) != 1 || cw.Msg.Items[0].EpisodeNumber != 2 {
		t.Fatalf("ListContinueWatching unexpected: %+v err=%v", cw, err)
	}
	serRes2, err := client.GetSeriesDetail(ctx, connect.NewRequest(&bigflixv1.GetSeriesDetailRequest{TmdbId: 95396}))
	if err != nil || !serRes2.Msg.NextUp.IsResume || serRes2.Msg.NextUp.EpisodeNumber != 2 {
		t.Fatalf("expected NextUp resume S01E02, got %+v err=%v", serRes2, err)
	}
}
