package web

import (
	"bigflix/internal/catalog"
	"bigflix/internal/database"
	"bigflix/internal/downloads"
	bigflixv1 "bigflix/internal/gen/bigflix/v1"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

func TestScaledHeight(t *testing.T) {
	tests := []struct {
		name             string
		srcW, srcH, maxH int
		want             int
	}{
		{"4K 16:9 stays 2160p", 3840, 2160, 2160, 2160},
		{"4K 16:9 to 1440p", 3840, 2160, 1440, 1440},
		{"4K 16:9 to 1080p", 3840, 2160, 1080, 1080},
		{"4K 2.39:1 ultrawide to 1080p", 3840, 1600, 1080, 800},
		{"4K 16:9 to 720p", 3840, 2160, 720, 720},
		{"4K 2.39:1 ultrawide to 720p", 3840, 1600, 720, 532},
		{"1080p to 480p", 1920, 1080, 480, 480},
		{"1080p ultrawide does not upscale", 1920, 800, 1080, 800},
		{"720p does not upscale to 1080p", 1280, 720, 1080, 720},
		{"zero maxH preserves height", 3840, 2160, 0, 2160},
		{"unknown source falls back to maxH", 0, 0, 1080, 1080},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := scaledHeight(tc.srcW, tc.srcH, tc.maxH); got != tc.want {
				t.Fatalf("scaledHeight(%d, %d, %d) = %d, want %d", tc.srcW, tc.srcH, tc.maxH, got, tc.want)
			}
		})
	}
}

func TestEmbeddedSubtitleHelpers(t *testing.T) {
	if !isTextSubtitleCodec("subrip") || !isTextSubtitleCodec("ass") || !isTextSubtitleCodec("mov_text") {
		t.Fatal("expected text subtitle codecs to be accepted")
	}
	if isTextSubtitleCodec("hdmv_pgs_subtitle") || isTextSubtitleCodec("dvd_subtitle") {
		t.Fatal("expected bitmap subtitle codecs to be excluded")
	}
	if code, name := normalizeSubLang("eng"); code != "en" || name != "English" {
		t.Fatalf("normalizeSubLang(eng) = (%q, %q)", code, name)
	}
	if code, name := normalizeSubLang("fre"); code != "fr" || name != "French" {
		t.Fatalf("normalizeSubLang(fre) = (%q, %q)", code, name)
	}
	if got := buildEmbeddedSubLabel("English", "Dialogue", 2, false, true); got != "English · Dialogue · SDH" {
		t.Fatalf("unexpected label: %q", got)
	}
}

func TestEmbeddedSubtitlesListAndExtract(t *testing.T) {
	if downloads.FFmpeg == "" {
		t.Skip("ffmpeg not installed")
	}
	t.Setenv("AUTH_TOKEN", "sub-secret")
	root := t.TempDir()
	srcDir := filepath.Join(root, "src")
	if err := os.MkdirAll(srcDir, 0700); err != nil {
		t.Fatal(err)
	}

	enSRT := filepath.Join(root, "en.srt")
	frSRT := filepath.Join(root, "fr.srt")
	if err := os.WriteFile(enSRT, []byte("1\n00:00:00,100 --> 00:00:00,900\nHello built-in subtitles!\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(frSRT, []byte("1\n00:00:00,100 --> 00:00:00,900\nBonjour sous-titres!\n"), 0600); err != nil {
		t.Fatal(err)
	}

	mkv := filepath.Join(srcDir, "movie.mkv")
	cmd := exec.Command(
		downloads.FFmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=320x180:duration=1:rate=24",
		"-i", enSRT, "-i", frSRT,
		"-map", "0:v", "-map", "1:0", "-map", "2:0",
		"-c:v", "libx264", "-preset", "ultrafast",
		"-c:s", "srt",
		"-metadata:s:s:0", "language=eng", "-metadata:s:s:0", "title=Full",
		"-disposition:s:0", "default",
		"-metadata:s:s:1", "language=fre", "-metadata:s:s:1", "title=French",
		mkv,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg mkv build failed: %v\n%s", err, out)
	}

	info := metainfo.Info{PieceLength: 64 << 10}
	if err := info.BuildFromFilePath(mkv); err != nil {
		t.Fatal(err)
	}
	b, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	mi := &metainfo.MetaInfo{InfoBytes: b}

	seedCfg := torrent.NewDefaultClientConfig()
	seedCfg.SetListenAddr("127.0.0.1:0")
	seedCfg.DataDir = srcDir
	seedCfg.Seed = true
	seedCfg.NoDHT = true
	seedCfg.DisableTrackers = true
	seedCfg.DisablePEX = true
	seedCfg.DisableUTP = true
	seedCfg.DisableIPv6 = true
	seedCfg.DisableWebtorrent = true
	seedCfg.DisableWebseeds = true
	seedCfg.NoDefaultPortForwarding = true
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

	dlCfg := torrent.NewDefaultClientConfig()
	dlCfg.SetListenAddr("127.0.0.1:0")
	dlCfg.NoDHT = true
	dlCfg.DisableTrackers = true
	dlCfg.DisablePEX = true
	dlCfg.DisableUTP = true
	dlCfg.DisableIPv6 = true
	dlCfg.DisableWebtorrent = true
	dlCfg.DisableWebseeds = true
	dlCfg.NoDefaultPortForwarding = true

	mgr, err := downloads.Open(db, downloads.Options{MediaDir: filepath.Join(root, "cache"), ClientConfig: dlCfg})
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	status, err := mgr.AddMeta(mi, "Embedded Sub Test")
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.ConnectPeer(status.Hash, seed.ListenAddrs()[0]); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		cur, err := mgr.Get(status.Hash)
		if err == nil && cur.Size > 0 && cur.Completed >= cur.Size {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	svc, err := catalog.New(db, "https://example.com/api/v2/", "")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(svc, mgr)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	var tok atomic.Value
	tok.Store("sub-secret")
	client := bearerClient(srv.URL, srv.Client(), &tok)

	ctx := context.Background()
	listResp, err := client.ListSubtitles(ctx, connect.NewRequest(&bigflixv1.ListSubtitlesRequest{
		Hash: status.Hash,
	}))
	if err != nil {
		t.Fatalf("ListSubtitles failed: %v", err)
	}
	if len(listResp.Msg.Embedded) != 2 {
		t.Fatalf("expected 2 embedded subtitles, got %d: %+v", len(listResp.Msg.Embedded), listResp.Msg.Embedded)
	}
	en := listResp.Msg.Embedded[0]
	if en.Language != "en" || !en.IsDefault || en.Codec != "subrip" {
		t.Fatalf("unexpected first embedded track: %+v", en)
	}

	addResp, err := client.AddSubtitle(ctx, connect.NewRequest(&bigflixv1.AddSubtitleRequest{
		Hash:     status.Hash,
		Action:   "embedded",
		Language: "en",
		Index:    en.StreamIndex,
	}))
	if err != nil {
		t.Fatalf("AddSubtitle(embedded) failed: %v", err)
	}
	if addResp.Msg.Track == nil || addResp.Msg.Track.Id == "" {
		t.Fatalf("expected saved track, got %+v", addResp.Msg)
	}

	getResp, err := client.GetSubtitle(ctx, connect.NewRequest(&bigflixv1.GetSubtitleRequest{
		Hash: status.Hash,
		Id:   addResp.Msg.Track.Id,
	}))
	if err != nil {
		t.Fatalf("GetSubtitle failed: %v", err)
	}
	if !strings.Contains(getResp.Msg.Vtt, "Hello built-in subtitles!") {
		t.Fatalf("extracted VTT missing expected cue:\n%s", getResp.Msg.Vtt)
	}

	// Verify live progressive embedded subtitle streaming endpoint (works on incomplete downloads too)
	liveURL := fmt.Sprintf("%s/media/%s/subtitle/%d?t=0&token=sub-secret", srv.URL, status.Hash, en.StreamIndex)
	liveResp, err := srv.Client().Get(liveURL)
	if err != nil {
		t.Fatalf("live subtitle GET failed: %v", err)
	}
	liveBody, _ := io.ReadAll(liveResp.Body)
	liveResp.Body.Close()
	if liveResp.StatusCode != http.StatusOK || !strings.Contains(liveResp.Header.Get("Content-Type"), "text/vtt") || !strings.Contains(string(liveBody), "Hello built-in subtitles!") {
		t.Fatalf("live subtitle stream unexpected: status=%d ct=%q body=%q", liveResp.StatusCode, liveResp.Header.Get("Content-Type"), string(liveBody))
	}
}

func TestEmbeddedAudioProbeAndTrackSelection(t *testing.T) {
	if downloads.FFmpeg == "" {
		t.Skip("ffmpeg not installed")
	}
	t.Setenv("AUTH_TOKEN", "audio-secret")
	root := t.TempDir()
	srcDir := filepath.Join(root, "src")
	if err := os.MkdirAll(srcDir, 0700); err != nil {
		t.Fatal(err)
	}

	mkv := filepath.Join(srcDir, "dual-audio.mkv")
	cmd := exec.Command(
		downloads.FFmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=320x180:duration=1:rate=24",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1",
		"-f", "lavfi", "-i", "sine=frequency=880:duration=1",
		"-map", "0:v", "-map", "1:a", "-map", "2:a",
		"-c:v", "libx264", "-preset", "ultrafast",
		"-c:a", "aac", "-ac", "2",
		"-metadata:s:a:0", "language=eng", "-metadata:s:a:0", "title=Original",
		"-disposition:a:0", "default",
		"-metadata:s:a:1", "language=hin", "-metadata:s:a:1", "title=Hindi Dub",
		"-disposition:a:1", "0",
		mkv,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg dual-audio mkv build failed: %v\n%s", err, out)
	}

	info := metainfo.Info{PieceLength: 64 << 10}
	if err := info.BuildFromFilePath(mkv); err != nil {
		t.Fatal(err)
	}
	b, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	mi := &metainfo.MetaInfo{InfoBytes: b}

	seedCfg := torrent.NewDefaultClientConfig()
	seedCfg.SetListenAddr("127.0.0.1:0")
	seedCfg.DataDir = srcDir
	seedCfg.Seed = true
	seedCfg.NoDHT = true
	seedCfg.DisableTrackers = true
	seedCfg.DisablePEX = true
	seedCfg.DisableUTP = true
	seedCfg.DisableIPv6 = true
	seedCfg.DisableWebtorrent = true
	seedCfg.DisableWebseeds = true
	seedCfg.NoDefaultPortForwarding = true
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

	dlCfg := torrent.NewDefaultClientConfig()
	dlCfg.SetListenAddr("127.0.0.1:0")
	dlCfg.NoDHT = true
	dlCfg.DisableTrackers = true
	dlCfg.DisablePEX = true
	dlCfg.DisableUTP = true
	dlCfg.DisableIPv6 = true
	dlCfg.DisableWebtorrent = true
	dlCfg.DisableWebseeds = true
	dlCfg.NoDefaultPortForwarding = true

	mgr, err := downloads.Open(db, downloads.Options{MediaDir: filepath.Join(root, "cache"), ClientConfig: dlCfg})
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	status, err := mgr.AddMeta(mi, "Dual Audio Test")
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.ConnectPeer(status.Hash, seed.ListenAddrs()[0]); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		cur, err := mgr.Get(status.Hash)
		if err == nil && cur.Size > 0 && cur.Completed >= cur.Size {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	svc, err := catalog.New(db, "https://example.com/api/v2/", "")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(svc, mgr)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	var tok atomic.Value
	tok.Store("audio-secret")
	client := bearerClient(srv.URL, srv.Client(), &tok)

	probeResp, err := client.ProbeMedia(context.Background(), connect.NewRequest(&bigflixv1.ProbeMediaRequest{
		Hash: status.Hash,
	}))
	if err != nil {
		t.Fatalf("ProbeMedia failed: %v", err)
	}
	tracks := probeResp.Msg.AudioTracks
	if len(tracks) != 2 {
		t.Fatalf("expected 2 embedded audio tracks, got %d: %+v", len(tracks), tracks)
	}
	if tracks[0].Language != "en" || !tracks[0].IsDefault || tracks[0].Channels != 2 || !strings.Contains(tracks[0].Label, "English") {
		t.Fatalf("unexpected track 0: %+v", tracks[0])
	}
	if tracks[1].Language != "hi" || tracks[1].IsDefault || tracks[1].Channels != 2 || !strings.Contains(tracks[1].Label, "Hindi") {
		t.Fatalf("unexpected track 1: %+v", tracks[1])
	}

	// Verify selecting the Hindi embedded audio stream via ?a=<streamIndex> streams valid fMP4 bytes
	remuxURL := fmt.Sprintf("%s/media/%s/remux?mode=copy&a=%d&token=audio-secret", srv.URL, status.Hash, tracks[1].StreamIndex)
	resp, err := srv.Client().Get(remuxURL)
	if err != nil {
		t.Fatalf("remux with ?a= failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "video/mp4" || len(body) < 1024 || !strings.Contains(string(body[:min(len(body), 64)]), "ftyp") {
		t.Fatalf("unexpected remux audio response: status=%d ct=%q bytes=%d", resp.StatusCode, resp.Header.Get("Content-Type"), len(body))
	}
}
