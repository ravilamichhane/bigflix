package downloads

import (
	"bigflix/internal/database"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"golang.org/x/time/rate"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func isolatedConfig() *torrent.ClientConfig {
	c := torrent.NewDefaultClientConfig()
	c.SetListenAddr("127.0.0.1:0")
	c.NoDHT = true
	c.DisableTrackers = true
	c.DisablePEX = true
	c.DisableUTP = true
	c.DisableIPv6 = true
	c.DisableWebtorrent = true
	c.DisableWebseeds = true
	c.NoDefaultPortForwarding = true
	return c
}
func eventually(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("condition did not become true")
}
func TestStreamIncompleteSeekPauseAndRestart(t *testing.T) {
	testStreaming(t, false)
}
func TestStreamWithSelectedInterface(t *testing.T) {
	testStreaming(t, true)
}
func testStreaming(t *testing.T, selectedInterface bool) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	os.MkdirAll(source, 0700)
	payload := make([]byte, 32<<20)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	video := filepath.Join(source, "sample.mp4")
	if err := os.WriteFile(video, payload, 0600); err != nil {
		t.Fatal(err)
	}
	info := metainfo.Info{PieceLength: 256 << 10}
	if err := info.BuildFromFilePath(video); err != nil {
		t.Fatal(err)
	}
	b, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	mi := &metainfo.MetaInfo{InfoBytes: b}
	cfg := isolatedConfig()
	cfg.DataDir = source
	cfg.Seed = true
	cfg.UploadRateLimiter = rate.NewLimiter(rate.Limit(2<<20), 256<<10)
	seed, err := torrent.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer seed.Close()
	st, err := seed.AddTorrent(mi)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.VerifyData(); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(filepath.Join(root, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	opts := Options{MediaDir: filepath.Join(root, "cache"), ClientConfig: isolatedConfig()}
	if selectedInterface {
		name := loopbackInterface(t)
		b, _ := json.Marshal([]InterfaceBinding{{Name: name, Port: 0}})
		if err := db.SaveNetworkConfig(context.Background(), b); err != nil {
			t.Fatal(err)
		}
		opts.ClientConfig = nil
	}
	m, err := Open(db, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { m.Close() }()
	s, err := m.Add(mi.HashInfoBytes().HexString(), "Test video", "", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	hash := s.Hash
	m.mu.Lock()
	m.jobs[hash].t.AddPeers([]torrent.PeerInfo{{Addr: seed.ListenAddrs()[0], Trusted: true}})
	m.mu.Unlock()
	eventually(t, func() bool { s, _ := m.Get(hash); return s.Size > 0 })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { m.Stream(w, r, hash) }))
	defer server.Close()
	get := func(rng string) []byte {
		t.Helper()
		req, _ := http.NewRequest("GET", server.URL, nil)
		req.Header.Set("Range", rng)
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 206 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("range status %d: %s", resp.StatusCode, body)
		}
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	first := get("bytes=0-65535")
	if !bytes.Equal(first, payload[:65536]) {
		t.Fatal("unverified or incorrect initial bytes")
	}
	s, _ = m.Get(hash)
	if s.Completed >= s.Size {
		t.Fatal("test did not prove streaming before completion")
	}
	t.Logf("initial video bytes served at %.1f%% download completion", s.Progress*100)
	offset := 20 << 20
	if !bytes.Equal(get("bytes=20971520-21037055"), payload[offset:offset+65536]) {
		t.Fatal("seek bytes differ")
	}
	if !bytes.Equal(get("bytes=-1024"), payload[len(payload)-1024:]) {
		t.Fatal("suffix bytes differ")
	}
	req, _ := http.NewRequest("HEAD", server.URL, nil)
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.ContentLength != int64(len(payload)) {
		t.Fatal("HEAD length wrong")
	}
	req, _ = http.NewRequest("GET", server.URL, nil)
	req.Header.Set("Range", "bytes=999999999-")
	resp, err = server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 416 {
		t.Fatal("invalid range accepted")
	}
	if err = m.Update(hash, "pause", 0, 0); err != nil {
		t.Fatal(err)
	}
	_, _, _, err = m.Reader(context.Background(), hash)
	if err != ErrPaused {
		t.Fatalf("paused reader: %v", err)
	}
	if err = m.Update(hash, "keep", 0, 0); err != nil {
		t.Fatal(err)
	}
	m.Close()
	opts.ClientConfig = isolatedConfig()
	m, err = Open(db, opts)
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { s, _ := m.Get(hash); return s.Size > 0 && s.Completed > 0 })
	s, _ = m.Get(hash)
	if !s.Paused || !s.Keep {
		t.Fatal("retention/pause state not restored")
	}
	if err = m.Update(hash, "resume", 0, 0); err != nil {
		t.Fatal(err)
	}
	// Seeder need not be reconnected: cached verified pieces must remain readable.
	if !bytes.Equal(get("bytes=0-65535"), payload[:65536]) {
		t.Fatal("restart lost cached bytes")
	}
	t.Logf("restart reused cached data with no peer connection")
}
func TestRangeValidation(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		a, b int64
		bad  bool
	}{{"", 0, 99, false}, {"bytes=10-20", 10, 20, false}, {"bytes=90-", 90, 99, false}, {"bytes=-10", 90, 99, false}, {"bytes=0-999", 0, 99, false}, {"bytes=100-", 0, 0, true}, {"bytes=-0", 0, 0, true}, {"bytes=2-1", 0, 0, true}, {"bytes=0-1,3-4", 0, 0, true}} {
		a, b, _, err := ParseRange(tc.raw, 100)
		if (err != nil) != tc.bad || (!tc.bad && (a != tc.a || b != tc.b)) {
			t.Fatalf("%s -> %d %d %v", tc.raw, a, b, err)
		}
	}
}

func TestSequentialWindowAndCanceledReader(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "sample.mp4")
	f, err := os.OpenFile(file, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(32 << 20); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	info := metainfo.Info{PieceLength: 256 << 10}
	if err := info.BuildFromFilePath(file); err != nil {
		t.Fatal(err)
	}
	b, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	m, err := Open(db, Options{MediaDir: filepath.Join(root, "cache"), ClientConfig: isolatedConfig()})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	s, err := m.AddMeta(&metainfo.MetaInfo{InfoBytes: b}, "test")
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { s, _ := m.Get(s.Hash); return s.Size > 0 })
	m.mu.Lock()
	j := m.jobs[s.Hash]
	if len(j.priorities) == 0 || len(j.priorities) >= j.t.NumPieces() {
		m.mu.Unlock()
		t.Fatal("background scheduler requested entire file")
	}
	if j.t.PieceState(16).Priority != torrent.PiecePriorityNormal {
		m.mu.Unlock()
		t.Fatal("background pipeline is too small to use fast peers")
	}
	if j.t.PieceState(50).Priority != torrent.PiecePriorityNone {
		m.mu.Unlock()
		t.Fatal("background scheduler must remain bounded")
	}
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	r, _, release, err := m.Reader(ctx, s.Hash)
	if err != nil {
		t.Fatal(err)
	}
	r.Seek(8<<20, io.SeekStart)
	_, err = r.Read(make([]byte, 128))
	release()
	if err == nil {
		t.Fatal("missing pieces returned data")
	}
	s, _ = m.Get(s.Hash)
	if s.Readers != 0 {
		t.Fatal("canceled reader leaked its lease")
	}
}

func TestDeleteDownloadCleansDiskAndDB(t *testing.T) {
	root := t.TempDir()
	mediaDir := filepath.Join(root, "cache")
	file := filepath.Join(root, "sample.mp4")
	if err := os.WriteFile(file, make([]byte, 1<<20), 0600); err != nil {
		t.Fatal(err)
	}
	info := metainfo.Info{PieceLength: 256 << 10}
	if err := info.BuildFromFilePath(file); err != nil {
		t.Fatal(err)
	}
	b, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(filepath.Join(root, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	m, err := Open(db, Options{MediaDir: mediaDir, ClientConfig: isolatedConfig()})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	s, err := m.AddMeta(&metainfo.MetaInfo{InfoBytes: b}, "Delete test")
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { st, _ := m.Get(s.Hash); return st.Size > 0 })

	mediaPath := filepath.Join(mediaDir, s.Hash)
	if err := os.MkdirAll(mediaPath, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mediaPath, "dummy.media"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveSubtitle(context.Background(), s.Hash, database.Subtitle{ID: "sub1", Label: "Test", Language: "en"}, []byte("WEBVTT\n\n")); err != nil {
		t.Fatal(err)
	}

	if err := m.Delete(s.Hash); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if _, err := m.Get(s.Hash); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
	if len(m.List()) != 0 {
		t.Fatalf("expected 0 jobs after delete, got %d", len(m.List()))
	}
	if _, err := os.Stat(mediaPath); !os.IsNotExist(err) {
		t.Fatalf("expected media dir removed, got err=%v", err)
	}
	recs, err := db.Downloads(context.Background())
	if err != nil || len(recs) != 0 {
		t.Fatalf("expected DB downloads cleared, got len=%d err=%v", len(recs), err)
	}
	subs, err := db.Subtitles(context.Background(), s.Hash)
	if err != nil || len(subs) != 0 {
		t.Fatalf("expected DB subtitles cleared, got len=%d err=%v", len(subs), err)
	}
	if err := m.Delete(s.Hash); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound on second delete, got %v", err)
	}
}
