package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestLegacySubtitlesMigrationAndFileIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.Exec(`
		CREATE TABLE schema_version(version INTEGER PRIMARY KEY);
		INSERT INTO schema_version VALUES(1);
		CREATE TABLE subtitles(hash TEXT NOT NULL,id TEXT NOT NULL,label TEXT NOT NULL,language TEXT NOT NULL,vtt BLOB NOT NULL,PRIMARY KEY(hash,id));
		INSERT INTO subtitles VALUES('abc','s1','Legacy Movie','en','WEBVTT');
	`)
	if err != nil {
		t.Fatal(err)
	}
	raw.Close()

	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open legacy DB failed: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	legacyVtt, err := store.Subtitle(ctx, "abc", "s1")
	if err != nil || string(legacyVtt) != "WEBVTT" {
		t.Fatalf("legacy subtitle lost: %q err=%v", legacyVtt, err)
	}

	// File-scoped subtitles for a season pack stay isolated.
	if err := store.SaveSubtitleForFile(ctx, "pack", 0, Subtitle{ID: "ep1", Label: "Ep 1 EN", Language: "en"}, []byte("WEBVTT EP1")); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSubtitleForFile(ctx, "pack", 1, Subtitle{ID: "ep2", Label: "Ep 2 EN", Language: "en"}, []byte("WEBVTT EP2")); err != nil {
		t.Fatal(err)
	}
	list0, err := store.SubtitlesForFile(ctx, "pack", 0)
	if err != nil || len(list0) != 1 || list0[0].ID != "ep1" {
		t.Fatalf("file 0 subtitles wrong: %+v err=%v", list0, err)
	}
	list1, err := store.SubtitlesForFile(ctx, "pack", 1)
	if err != nil || len(list1) != 1 || list1[0].ID != "ep2" {
		t.Fatalf("file 1 subtitles wrong: %+v err=%v", list1, err)
	}
}

func TestConfigAndWatchProgress(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	t.Setenv("AI_API_URL", "http://llama-server:8080/v1")
	aiCfg := store.LoadAIConfig(ctx)
	if !aiCfg.Enabled || aiCfg.APIURL != "http://llama-server:8080/v1" {
		t.Fatalf("expected env AI config, got %+v", aiCfg)
	}

	if err := store.PutConfig(ctx, "tmdb", TMDBConfig{ReadToken: "db-token"}); err != nil {
		t.Fatal(err)
	}
	if got := store.LoadTMDBToken(ctx, "env-token"); got != "db-token" {
		t.Fatalf("expected db-token, got %q", got)
	}

	now := time.Now().Unix()
	_ = store.UpsertProgress(ctx, WatchProgress{
		MediaType: "movie", TMDBID: 100, Title: "Dune", Position: 600, Duration: 9000, UpdatedAt: now - 10,
	})
	_ = store.UpsertProgress(ctx, WatchProgress{
		MediaType: "movie", TMDBID: 101, Title: "Finished Movie", Position: 5900, Duration: 6000, UpdatedAt: now - 5,
	})
	_ = store.UpsertProgress(ctx, WatchProgress{
		MediaType: "episode", TMDBID: 200, Season: 1, Episode: 1, Title: "Severance", EpisodeTitle: "Good News", Position: 2800, Duration: 3000, UpdatedAt: now - 20,
	})
	_ = store.UpsertProgress(ctx, WatchProgress{
		MediaType: "episode", TMDBID: 200, Season: 1, Episode: 2, Title: "Severance", EpisodeTitle: "Half Loop", Position: 400, Duration: 3000, UpdatedAt: now,
	})

	finished, ok, err := store.GetProgress(ctx, MediaKey("movie", 101, 0, 0))
	if err != nil || !ok || !finished.Completed {
		t.Fatalf("expected finished movie completed=true: %+v ok=%v err=%v", finished, ok, err)
	}

	cw, err := store.ListContinueWatching(ctx, 10)
	if err != nil || len(cw) != 2 {
		t.Fatalf("expected 2 continue watching items (deduped series + unfinished movie), got %d: %+v err=%v", len(cw), cw, err)
	}
	if cw[0].TMDBID != 200 || cw[0].Episode != 2 || cw[1].TMDBID != 100 {
		t.Fatalf("unexpected continue watching order: %+v", cw)
	}
}
