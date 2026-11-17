package web

import (
	"bigflix/internal/catalog"
	"bigflix/internal/database"
	"bigflix/internal/downloads"
	bigflixv1 "bigflix/internal/gen/bigflix/v1"
	"bigflix/internal/gen/bigflix/v1/bigflixv1connect"
	"context"
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

func bearerClient(baseURL string, httpClient *http.Client, token *atomic.Value) bigflixv1connect.BigflixServiceClient {
	interceptor := connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if t, _ := token.Load().(string); t != "" {
				req.Header().Set("Authorization", "Bearer "+t)
			}
			return next(ctx, req)
		}
	})
	streamInterceptor := &clientStreamAuth{token: token}
	return bigflixv1connect.NewBigflixServiceClient(
		httpClient,
		baseURL,
		connect.WithInterceptors(interceptor, streamInterceptor),
	)
}

type clientStreamAuth struct {
	token *atomic.Value
}

func (c *clientStreamAuth) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return next
}

func (c *clientStreamAuth) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		conn := next(ctx, spec)
		if t, _ := c.token.Load().(string); t != "" {
			conn.RequestHeader().Set("Authorization", "Bearer "+t)
		}
		return conn
	}
}

func (c *clientStreamAuth) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}

func TestSPAShellAndHeaders(t *testing.T) {
	t.Setenv("AUTH_TOKEN", "test-secret")
	db, err := database.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	base := "https://example.com/api/v2/"
	s, _ := catalog.New(db, base, "")
	h, err := New(s)
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"/", "/downloads", "/settings", "/movie/1", "/watch/" + strings.Repeat("a", 40)} {
		r := httptest.NewRequest("GET", path, nil)
		r.Host = "localhost:3000"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `<div id="root"></div>`) {
			t.Fatalf("%s -> status %d body %s", path, w.Code, w.Body.String())
		}
		if got := w.Header().Get("Referrer-Policy"); got != "same-origin" {
			t.Fatalf("unexpected Referrer-Policy: %q", got)
		}
	}

	r := httptest.NewRequest("GET", "/assets/nonexistent.js", nil)
	r.Host = "localhost:3000"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatalf("missing asset got %d", w.Code)
	}

	r = httptest.NewRequest("POST", "/bigflix.v1.BigflixService/GetStatus", strings.NewReader("{}"))
	r.Host = "localhost:3000"
	r.Header.Set("Origin", "https://evil.example")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("cross-origin request got %d", w.Code)
	}
}

func TestConnectRPCAuthAndWorkflows(t *testing.T) {
	t.Setenv("AUTH_TOKEN", "initial-token")
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
	cfg.NoDefaultPortForwarding = true
	engine, err := downloads.Open(db, downloads.Options{MediaDir: filepath.Join(root, "media"), ClientConfig: cfg})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()

	base := "https://example.com/api/v2/"
	hash := strings.Repeat("a", 40)
	_ = db.Put(context.Background(), base+"list_movies.json?limit=24&order_by=desc&page=1&sort_by=date_added",
		[]byte(`{"status":"ok","data":{"movie_count":1,"movies":[{"id":1,"title":"Test Movie","year":2024}]}}`), time.Now())
	_ = db.Put(context.Background(), base+"movie_details.json?movie_id=1",
		[]byte(`{"status":"ok","data":{"movie":{"id":1,"title":"Test Movie","torrents":[{"hash":"`+hash+`","quality":"1080p"}]}}}`), time.Now())
	service, _ := catalog.New(db, base, "")

	var restarts atomic.Int32
	handler, err := NewWithRestart(service, engine, func() { restarts.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	var tok atomic.Value
	tok.Store("")
	client := bearerClient(srv.URL, srv.Client(), &tok)

	// Unauthenticated ConnectRPC calls must fail with CodeUnauthenticated.
	if _, err := client.GetStatus(context.Background(), connect.NewRequest(&bigflixv1.GetStatusRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("expected CodeUnauthenticated with empty token, got %v", err)
	}
	if _, err := client.ListMovies(context.Background(), connect.NewRequest(&bigflixv1.ListMoviesRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("expected CodeUnauthenticated on ListMovies, got %v", err)
	}
	streamUnauth, err := client.WatchDownload(context.Background(), connect.NewRequest(&bigflixv1.WatchDownloadRequest{Hash: hash}))
	if err == nil && streamUnauth.Receive() {
		t.Fatal("expected unauthenticated stream to fail")
	} else if err == nil && connect.CodeOf(streamUnauth.Err()) != connect.CodeUnauthenticated {
		t.Fatalf("expected CodeUnauthenticated on stream, got %v", streamUnauth.Err())
	}

	// Wrong token must fail.
	tok.Store("wrong-token")
	if _, err := client.GetStatus(context.Background(), connect.NewRequest(&bigflixv1.GetStatusRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("expected CodeUnauthenticated with wrong token, got %v", err)
	}

	// Media endpoints must reject unauthenticated requests with 401.
	for _, mediaPath := range []string{"/media/" + hash, "/media/" + hash + "/remux"} {
		resp, err := srv.Client().Get(srv.URL + mediaPath)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s without token got %d", mediaPath, resp.StatusCode)
		}
	}

	// Valid token unlocks ConnectRPC and media endpoints.
	tok.Store("initial-token")
	statusRes, err := client.GetStatus(context.Background(), connect.NewRequest(&bigflixv1.GetStatusRequest{}))
	if err != nil || statusRes.Msg.Status != "ok" {
		t.Fatalf("GetStatus failed: %v", err)
	}

	// Authenticated media request with query token passes auth (404 since hash not yet started).
	resp, err := srv.Client().Get(srv.URL + "/media/" + hash + "?token=initial-token")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 after auth, got %d", resp.StatusCode)
	}

	// Catalog listing & TMDB message.
	moviesRes, err := client.ListMovies(context.Background(), connect.NewRequest(&bigflixv1.ListMoviesRequest{}))
	if err != nil || len(moviesRes.Msg.Movies) != 1 || moviesRes.Msg.Movies[0].Title != "Test Movie" {
		t.Fatalf("ListMovies unexpected: %+v err=%v", moviesRes, err)
	}
	tmdbRes, err := client.ListMovies(context.Background(), connect.NewRequest(&bigflixv1.ListMoviesRequest{Feed: "day"}))
	if err != nil || !strings.Contains(tmdbRes.Msg.ErrorMessage, "Connect TMDB") {
		t.Fatalf("expected TMDB configuration message, got %+v err=%v", tmdbRes, err)
	}
	if _, err := client.ListMovies(context.Background(), connect.NewRequest(&bigflixv1.ListMoviesRequest{Page: -2})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("expected CodeInvalidArgument for invalid page, got %v", err)
	}

	// GetMovie & StartDownload validation.
	movieRes, err := client.GetMovie(context.Background(), connect.NewRequest(&bigflixv1.GetMovieRequest{Id: 1}))
	if err != nil || len(movieRes.Msg.Movie.Torrents) != 1 {
		t.Fatalf("GetMovie failed: %v", err)
	}
	if _, err := client.StartDownload(context.Background(), connect.NewRequest(&bigflixv1.StartDownloadRequest{
		MovieId: 1,
		Hash:    strings.Repeat("b", 40),
	})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("expected CodeInvalidArgument for unlisted source, got %v", err)
	}
	if _, err := client.StartDownload(context.Background(), connect.NewRequest(&bigflixv1.StartDownloadRequest{
		MovieId: 1,
		Hash:    hash,
	})); err != nil {
		t.Fatalf("StartDownload failed: %v", err)
	}
	if _, err := client.StartDownload(context.Background(), connect.NewRequest(&bigflixv1.StartDownloadRequest{
		MovieId: 1,
		Hash:    hash,
	})); err != nil {
		t.Fatalf("duplicate StartDownload failed: %v", err)
	}
	dlList, err := client.ListDownloads(context.Background(), connect.NewRequest(&bigflixv1.ListDownloadsRequest{}))
	if err != nil || len(dlList.Msg.Downloads) != 1 {
		t.Fatalf("expected 1 download, got %v err=%v", dlList, err)
	}

	// WatchDownload server stream yields initial status.
	wCtx, wCancel := context.WithTimeout(context.Background(), 3*time.Second)
	stream, err := client.WatchDownload(wCtx, connect.NewRequest(&bigflixv1.WatchDownloadRequest{Hash: hash}))
	if err != nil {
		t.Fatalf("WatchDownload failed: %v", err)
	}
	if !stream.Receive() || stream.Msg().Download.Hash != hash {
		t.Fatalf("WatchDownload did not receive status: err=%v", stream.Err())
	}
	wCancel()
	_ = stream.Close()

	// Subtitle upload & retrieval via ConnectRPC.
	addSubRes, err := client.AddSubtitle(context.Background(), connect.NewRequest(&bigflixv1.AddSubtitleRequest{
		Hash:     hash,
		Action:   "upload",
		Language: "en",
		Filename: "example.srt",
		Data:     []byte("1\n00:00:01,000 --> 00:00:03,000\nCaption test\n"),
	}))
	if err != nil || addSubRes.Msg.Track == nil {
		t.Fatalf("AddSubtitle failed: %v", err)
	}
	subRes, err := client.GetSubtitle(context.Background(), connect.NewRequest(&bigflixv1.GetSubtitleRequest{
		Hash: hash,
		Id:   addSubRes.Msg.Track.Id,
	}))
	if err != nil || !strings.Contains(subRes.Msg.Vtt, "00:00:01.000") {
		t.Fatalf("GetSubtitle failed: %v vtt=%q", err, subRes.Msg.Vtt)
	}

	// DeleteDownload removes job and DB state.
	if _, err := client.DeleteDownload(context.Background(), connect.NewRequest(&bigflixv1.DeleteDownloadRequest{Hash: hash})); err != nil {
		t.Fatalf("DeleteDownload failed: %v", err)
	}
	if _, err := client.GetDownload(context.Background(), connect.NewRequest(&bigflixv1.GetDownloadRequest{Hash: hash})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("expected CodeNotFound after delete, got %v", err)
	}

	// Network settings validation and persistence.
	for _, tc := range []struct {
		mode     string
		port     int32
		wantCode connect.Code
		wantPort int
	}{
		{"fixed", 70000, connect.CodeInvalidArgument, 51413},
		{"fixed", 0, connect.CodeInvalidArgument, 51413},
		{"fixed", 51414, 0, 51414},
		{"dynamic", 99999, 0, 0},
	} {
		_, err := client.SaveNetworkSettings(context.Background(), connect.NewRequest(&bigflixv1.SaveNetworkSettingsRequest{
			Mode: tc.mode,
			Port: tc.port,
		}))
		if tc.wantCode != 0 {
			if connect.CodeOf(err) != tc.wantCode {
				t.Fatalf("mode=%s port=%d expected %v, got %v", tc.mode, tc.port, tc.wantCode, err)
			}
		} else if err != nil {
			t.Fatalf("mode=%s port=%d failed: %v", tc.mode, tc.port, err)
		}
		savedPort, err := db.TorrentPort(context.Background(), 51413)
		if err != nil || savedPort != tc.wantPort {
			t.Fatalf("persisted port %d (want %d): %v", savedPort, tc.wantPort, err)
		}
	}
	deadline := time.Now().Add(time.Second)
	for restarts.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if restarts.Load() != 2 {
		t.Fatalf("expected 2 restarts, got %d", restarts.Load())
	}

	// Token rotation invalidates old token and persists in SQLite.
	if _, err := client.UpdateAuthToken(context.Background(), connect.NewRequest(&bigflixv1.UpdateAuthTokenRequest{NewToken: "rotated-secret"})); err != nil {
		t.Fatalf("UpdateAuthToken failed: %v", err)
	}
	if _, err := client.GetStatus(context.Background(), connect.NewRequest(&bigflixv1.GetStatusRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("expected old token rejected after rotation, got %v", err)
	}
	tok.Store("rotated-secret")
	if _, err := client.GetStatus(context.Background(), connect.NewRequest(&bigflixv1.GetStatusRequest{})); err != nil {
		t.Fatalf("expected rotated token accepted, got %v", err)
	}
	persisted, err := db.AuthToken(context.Background(), "fallback")
	if err != nil || persisted != "rotated-secret" {
		t.Fatalf("expected persisted token rotated-secret, got %q err=%v", persisted, err)
	}
}

func TestFrontendDevProxy(t *testing.T) {
	devHits := 0
	devServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		devHits++
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("vite-dev:" + r.URL.Path))
	}))
	defer devServer.Close()

	db, err := database.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service, _ := catalog.New(db, "https://example.com/api/v2/", "")

	t.Setenv("FRONTEND_URL", "not-a-url")
	if _, err := New(service); err == nil {
		t.Fatal("expected error for invalid FRONTEND_URL")
	}

	t.Setenv("AUTH_TOKEN", "dev-secret")
	t.Setenv("FRONTEND_URL", devServer.URL)
	h, err := New(service)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL + "/downloads")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 64)
	n, _ := resp.Body.Read(buf)
	if resp.StatusCode != 200 || string(buf[:n]) != "vite-dev:/downloads" {
		t.Fatalf("expected proxied vite response, got status=%d body=%q", resp.StatusCode, string(buf[:n]))
	}
	if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "'unsafe-inline'") {
		t.Fatalf("expected relaxed dev CSP, got %q", resp.Header.Get("Content-Security-Policy"))
	}
	if devHits != 1 {
		t.Fatalf("expected 1 dev server hit, got %d", devHits)
	}

	// ConnectRPC and media routes must still go to the Go backend with auth enforcement.
	var tok atomic.Value
	tok.Store("")
	c := bearerClient(srv.URL, srv.Client(), &tok)
	if _, err := c.GetStatus(context.Background(), connect.NewRequest(&bigflixv1.GetStatusRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("expected CodeUnauthenticated in proxy mode, got %v", err)
	}
	tok.Store("dev-secret")
	if res, err := c.GetStatus(context.Background(), connect.NewRequest(&bigflixv1.GetStatusRequest{})); err != nil || res.Msg.Status != "ok" {
		t.Fatalf("expected authenticated GetStatus ok in proxy mode, got res=%v err=%v", res, err)
	}
	mediaResp, err := srv.Client().Get(srv.URL + "/media/" + strings.Repeat("a", 40))
	if err != nil {
		t.Fatal(err)
	}
	mediaResp.Body.Close()
	if mediaResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated media in proxy mode, got %d", mediaResp.StatusCode)
	}
	if devHits != 1 {
		t.Fatalf("ConnectRPC/media hit dev server unexpectedly: devHits=%d", devHits)
	}
}
