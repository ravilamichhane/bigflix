package downloads

import (
	"bigflix/internal/database"
	"bytes"
	"crypto/rand"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

func TestSeasonPackAutoMapsSelectsOnlyTargetEpisodeAndSwitches(t *testing.T) {
	root := t.TempDir()
	packDir := filepath.Join(root, "source", "Severance.S01.1080p")
	if err := os.MkdirAll(packDir, 0700); err != nil {
		t.Fatal(err)
	}
	epData := make([][]byte, 3)
	names := []string{"Severance.S01E01.mp4", "Severance.S01E02.mp4", "Severance.S01E03.mp4"}
	for i, name := range names {
		epData[i] = make([]byte, 2<<20)
		if _, err := rand.Read(epData[i]); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(packDir, name), epData[i], 0600); err != nil {
			t.Fatal(err)
		}
	}

	info := metainfo.Info{PieceLength: 128 << 10}
	if err := info.BuildFromFilePath(packDir); err != nil {
		t.Fatal(err)
	}
	b, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	mi := &metainfo.MetaInfo{InfoBytes: b}

	seedCfg := isolatedConfig()
	seedCfg.DataDir = filepath.Join(root, "source")
	seedCfg.Seed = true
	seed, err := torrent.NewClient(seedCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer seed.Close()
	st, err := seed.AddTorrent(mi)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.VerifyData(); err != nil {
		t.Fatal(err)
	}

	db, err := database.Open(filepath.Join(root, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mgr, err := Open(db, Options{MediaDir: filepath.Join(root, "cache"), ClientConfig: isolatedConfig()})
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	status, err := mgr.AddMetaMedia(mi, MediaRequest{
		Title:        "Severance",
		MediaType:    "episode",
		TMDBID:       95396,
		Season:       1,
		Episode:      2,
		EpisodeTitle: "Half Loop",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.ConnectPeer(status.Hash, seed.ListenAddrs()[0]); err != nil {
		t.Fatal(err)
	}

	eventually(t, func() bool {
		cur, err := mgr.Get(status.Hash)
		return err == nil && cur.FileIndex >= 0 && len(cur.EpisodeFiles) == 3 && cur.Size == int64(len(epData[1]))
	})

	cur, _ := mgr.Get(status.Hash)
	if !strings.HasSuffix(cur.Files[cur.FileIndex].Name, "Severance.S01E02.mp4") {
		t.Fatalf("expected S01E02 auto-selected, got %s", cur.Files[cur.FileIndex].Name)
	}

	// Stream bytes via HTTP and verify exact S01E02 content
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mgr.Stream(w, r, status.Hash)
	}))
	defer srv.Close()

	req, _ := http.NewRequest("GET", srv.URL, nil)
	req.Header.Set("Range", "bytes=0-32767")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	got2, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Equal(got2, epData[1][:32768]) {
		t.Fatal("S01E02 streamed bytes mismatch")
	}

	// Switch to S01E03 on the same season pack via AddMedia (reusing torrent!)
	switched, err := mgr.AddMedia(MediaRequest{
		Hash:         status.Hash,
		Title:        "Severance",
		MediaType:    "episode",
		TMDBID:       95396,
		Season:       1,
		Episode:      3,
		EpisodeTitle: "In Perpetuity",
	})
	if err != nil {
		t.Fatal(err)
	}
	if switched.Episode != 3 || switched.FileIndex == cur.FileIndex {
		t.Fatalf("expected switched episode 3 and different file index, got %+v", switched)
	}

	req3, _ := http.NewRequest("GET", srv.URL, nil)
	req3.Header.Set("Range", "bytes=0-32767")
	resp3, err := srv.Client().Do(req3)
	if err != nil {
		t.Fatal(err)
	}
	got3, _ := io.ReadAll(resp3.Body)
	resp3.Body.Close()
	if !bytes.Equal(got3, epData[2][:32768]) {
		t.Fatal("S01E03 streamed bytes mismatch after switching season-pack episode")
	}
}

func TestAutoFailoverOnStalledSource(t *testing.T) {
	root := t.TempDir()
	db, err := database.Open(filepath.Join(root, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mgr, err := Open(db, Options{
		MediaDir:        filepath.Join(root, "cache"),
		ClientConfig:    isolatedConfig(),
		FailoverTimeout: 450 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	deadHash := strings.Repeat("d", 40)
	backupHash := strings.Repeat("e", 40)

	_, err = mgr.AddMedia(MediaRequest{
		Hash:              deadHash,
		Title:             "Severance",
		MediaType:         "episode",
		TMDBID:            95396,
		Season:            1,
		Episode:           1,
		ActiveSourceLabel: "Primary 1080p",
		Fallbacks: []FallbackCandidate{
			{Hash: backupHash, Label: "Backup 1080p"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	eventually(t, func() bool {
		st, err := mgr.Get(deadHash)
		return err == nil && st.ReplacedByHash == backupHash && st.State == "failover"
	})

	backup, err := mgr.Get(backupHash)
	if err != nil {
		t.Fatalf("expected backup download created on failover: %v", err)
	}
	if backup.FailoverAttempt != 2 || backup.FailoverTotal != 2 || backup.TMDBID != 95396 || backup.Episode != 1 {
		t.Fatalf("unexpected failover backup metadata: %+v", backup)
	}
}

func TestLRUEvictionProtectsKeptAndActiveReaders(t *testing.T) {
	root := t.TempDir()
	db, err := database.Open(filepath.Join(root, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	makeMeta := func(name string, size int) *metainfo.MetaInfo {
		p := filepath.Join(root, name)
		f, _ := os.Create(p)
		_ = f.Truncate(int64(size))
		f.Close()
		info := metainfo.Info{PieceLength: 128 << 10}
		if err := info.BuildFromFilePath(p); err != nil {
			t.Fatal(err)
		}
		b, _ := bencode.Marshal(info)
		return &metainfo.MetaInfo{InfoBytes: b}
	}

	// Tight 5 MiB budget: two 2 MiB items fit, third 2 MiB item must evict oldest un-kept one!
	mgr, err := Open(db, Options{
		MediaDir:     filepath.Join(root, "cache"),
		MaxBytes:     5 << 20,
		ClientConfig: isolatedConfig(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	kept, err := mgr.AddMeta(makeMeta("kept.mp4", 2<<20), "Kept Movie")
	if err != nil {
		t.Fatal(err)
	}
	_ = mgr.Update(kept.Hash, "keep", 0, 0)
	eventually(t, func() bool { s, _ := mgr.Get(kept.Hash); return s.Size > 0 })

	oldCache, err := mgr.AddMeta(makeMeta("old.mp4", 2<<20), "Old Cached Episode")
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { s, _ := mgr.Get(oldCache.Hash); return s.Size > 0 })

	// Age oldCache so it's clearly the oldest LRU candidate
	mgr.mu.Lock()
	mgr.jobs[oldCache.Hash].record.LastAccessed = time.Now().Add(-2 * time.Hour)
	mgr.mu.Unlock()

	newest, err := mgr.AddMeta(makeMeta("new.mp4", 2<<20), "New Episode")
	if err != nil {
		t.Fatal(err)
	}

	eventually(t, func() bool {
		_, errOld := mgr.Get(oldCache.Hash)
		sNew, errNew := mgr.Get(newest.Hash)
		return errOld == ErrNotFound && errNew == nil && sNew.Error == "" && sNew.Size > 0
	})
	// Kept item must still exist!
	if _, err := mgr.Get(kept.Hash); err != nil {
		t.Fatalf("kept download was wrongly evicted: %v", err)
	}
}
