package ai

import (
	"bigflix/internal/database"
	"bigflix/internal/ranker"
	"bigflix/internal/sources"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestClassifierRanksAndGuardsAgainstHallucinations(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "ai.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if _, ok := req["response_format"]; !ok {
			t.Error("expected response_format json_schema")
		}
		// Model puts candidate 1 first, rejects 2, includes an out-of-range hallucinated id 99
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{
					"content": `{"ranked_ids":[1,99,0],"rejected":[{"id":2,"reason":"wrong language"}],"season_pack_ids":[1],"note":"Chose clean 1080p season pack"}`,
				}},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	_ = db.PutConfig(context.Background(), "ai", database.AIConfig{
		Enabled:    true,
		APIURL:     srv.URL + "/v1",
		Model:      "Qwen2.5-1.5B-Instruct",
		TimeoutSec: 5,
	})

	cl := NewClassifier(db)
	cl.SetClient(srv.Client())

	cands := []ranker.Candidate{
		{Hash: strings.Repeat("a", 40), Title: "Show S01E02 720p", Seeds: 20},
		{Hash: strings.Repeat("b", 40), Title: "Show S01 Complete 1080p x264", Seeds: 95},
		{Hash: strings.Repeat("c", 40), Title: "Show S01E02 FRENCH 1080p", Seeds: 120},
	}

	got, mode, note := cl.RankCandidates(context.Background(), sources.Query{MediaType: "episode", Title: "Show", Season: 1, Episode: 2}, database.DefaultPlaybackPrefs(), cands)
	if mode != "ai" || note != "Chose clean 1080p season pack" {
		t.Fatalf("expected ai mode, got mode=%q note=%q", mode, note)
	}
	if len(got) != 2 || got[0].Hash != strings.Repeat("b", 40) || !got[0].IsSeasonPack || got[1].Hash != strings.Repeat("a", 40) {
		t.Fatalf("unexpected AI ranking result: %+v", got)
	}
}

func TestClassifierHonorsAllRejectedResponse(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "ai_rej.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{
					"content": `{"ranked_ids":[],"rejected":[{"id":0,"reason":"wrong movie"},{"id":1,"reason":"release group mismatch"}],"season_pack_ids":[],"note":"No releases match Toxic (2026)"}`,
				}},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	_ = db.PutConfig(context.Background(), "ai", database.AIConfig{
		Enabled:    true,
		APIURL:     srv.URL + "/v1",
		Model:      "Qwen2.5-1.5B-Instruct",
		TimeoutSec: 5,
	})
	cl := NewClassifier(db)
	cl.SetClient(srv.Client())
	cands := []ranker.Candidate{
		{Hash: strings.Repeat("a", 40), Title: "A Toxic Love Story (2026)"},
		{Hash: strings.Repeat("b", 40), Title: "Send Help 2026 -TOXIC"},
	}
	got, mode, note := cl.RankCandidates(context.Background(), sources.Query{MediaType: "movie", Title: "Toxic", Year: 2026}, database.DefaultPlaybackPrefs(), cands)
	if mode != "ai" || len(got) != 0 || !strings.Contains(note, "Toxic") {
		t.Fatalf("expected AI all-rejection honored with 0 candidates, got mode=%q len=%d note=%q", mode, len(got), note)
	}
}

func TestClassifierProtectsVerifiedIMDbAndExactMatchesFromHallucinatedRejection(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "ai_verified.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate a small 1.5B model hallucinating rejections on valid Ranabaali releases
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{
					"content": `{"ranked_ids":[],"rejected":[{"id":0,"reason":"x265 not h264"},{"id":1,"reason":"non-latin original"},{"id":2,"reason":"2160p x265"}],"season_pack_ids":[],"note":"Rejected"}`,
				}},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	_ = db.PutConfig(context.Background(), "ai", database.AIConfig{
		Enabled:    true,
		APIURL:     srv.URL + "/v1",
		Model:      "Qwen2.5-1.5B-Instruct",
		TimeoutSec: 5,
	})
	cl := NewClassifier(db)
	cl.SetClient(srv.Client())

	q := sources.Query{
		MediaType:        "movie",
		TMDBID:           1389379,
		IMDbID:           "tt32324552",
		Title:            "Ranabaali",
		OriginalTitle:    "రణబాలి",
		Year:             2026,
		OriginalLanguage: "te",
	}
	cands := []ranker.Candidate{
		{Hash: strings.Repeat("1", 40), Provider: "apibay", Title: "Ranabaali (2026) 1080p Telugu HQ HDRip HEVC x265 DD+5.1 192Kbps AAC-ESub", Seeds: 6730},
		{Hash: strings.Repeat("2", 40), Provider: "apibay", Title: "Ranabaali 2026.1080p.NF.WEB-DL.Multi.DD+ 5.1x264-KIN", Seeds: 5},
		{Hash: strings.Repeat("3", 40), Provider: "yts", Title: "Ranabaali (2026) 2160p WEBRip 5.1 10Bit x265 -YTS", Seeds: 69},
	}

	got, _, _ := cl.RankCandidates(context.Background(), q, database.DefaultPlaybackPrefs(), cands)
	if len(got) != 3 {
		t.Fatalf("expected all 3 verified Ranabaali candidates protected from hallucinated AI rejection, got %d: %+v", len(got), got)
	}
}

func TestClassifierExtractsJSONWithThinkTagsAndReasoningContent(t *testing.T) {
	// Case 1: <think>...</think> prefix before JSON in message.content
	if got, ok := extractJSONObject("<think>\n\n</think>\n{\"ranked_ids\":[0],\"rejected\":[],\"season_pack_ids\":[],\"note\":\"ok\"}"); !ok || !strings.HasPrefix(got, "{") {
		t.Fatalf("expected JSON extracted after <think> block, got ok=%v %q", ok, got)
	}

	// Case 2: empty message.content with valid JSON placed in reasoning_content (llama.cpp Qwen3.5 quirk)
	db, err := database.Open(filepath.Join(t.TempDir(), "ai_think.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{
				{
					"finish_reason": "stop",
					"message": map[string]any{
						"content":           "",
						"reasoning_content": `{"ranked_ids":[1,0],"rejected":[],"season_pack_ids":[],"note":"Picked 1080p"}`,
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	_ = db.PutConfig(context.Background(), "ai", database.AIConfig{
		Enabled:    true,
		APIURL:     srv.URL + "/v1",
		Model:      "Qwen3.5-0.8B",
		TimeoutSec: 5,
	})
	cl := NewClassifier(db)
	cl.SetClient(srv.Client())

	cands := []ranker.Candidate{
		{Hash: strings.Repeat("a", 40), Title: "Dune (2021) 720p WEB-DL x264", Seeds: 20},
		{Hash: strings.Repeat("b", 40), Title: "Dune (2021) 1080p BluRay x264", Seeds: 80},
	}
	got, mode, note := cl.RankCandidates(context.Background(), sources.Query{MediaType: "movie", Title: "Dune", Year: 2021}, database.DefaultPlaybackPrefs(), cands)
	if mode != "ai" || note != "Picked 1080p" || got[0].Hash != strings.Repeat("b", 40) {
		t.Fatalf("expected JSON recovered from reasoning_content, got mode=%q note=%q top=%+v", mode, note, got[0])
	}
}

func TestClassifierGracefulFallbackWhenOffline(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "ai2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "offline", 503)
	}))
	defer srv.Close()

	_ = db.PutConfig(context.Background(), "ai", database.AIConfig{
		Enabled:    true,
		APIURL:     srv.URL + "/v1",
		Model:      "Qwen2.5-1.5B-Instruct",
		TimeoutSec: 2,
	})

	cl := NewClassifier(db)
	cl.SetClient(srv.Client())
	cands := []ranker.Candidate{
		{Hash: strings.Repeat("a", 40), Title: "First"},
		{Hash: strings.Repeat("b", 40), Title: "Second"},
	}
	got, mode, _ := cl.RankCandidates(context.Background(), sources.Query{MediaType: "movie", Title: "Movie"}, database.DefaultPlaybackPrefs(), cands)
	if mode != "deterministic" || len(got) != 2 || got[0].Hash != strings.Repeat("a", 40) {
		t.Fatalf("expected deterministic fallback, got mode=%q cands=%+v", mode, got)
	}
}

func TestMapSeasonPackFiles(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "ai3.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{
					"content": `{"mappings":[{"file_index":0,"season":1,"episode":1},{"file_index":1,"season":1,"episode":2},{"file_index":99,"season":1,"episode":3}]}`,
				}},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	_ = db.PutConfig(context.Background(), "ai", database.AIConfig{Enabled: true, APIURL: srv.URL + "/v1", Model: "Qwen2.5-1.5B-Instruct", TimeoutSec: 5})
	cl := NewClassifier(db)
	cl.SetClient(srv.Client())

	mapped, err := cl.MapSeasonPackFiles(context.Background(), "Frieren", 1, []FileEntry{
		{Index: 0, Path: "01.mkv", Size: 500_000_000},
		{Index: 1, Path: "02.mkv", Size: 500_000_000},
	})
	if err != nil || len(mapped) != 2 || mapped[1].FileIndex != 1 || mapped[1].Episode != 2 {
		t.Fatalf("unexpected season pack mapping: %+v err=%v", mapped, err)
	}
}
