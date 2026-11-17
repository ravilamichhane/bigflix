package web

import (
	"bigflix/internal/catalog"
	"bigflix/internal/database"
	"bigflix/internal/downloads"
	"bigflix/internal/gen/bigflix/v1/bigflixv1connect"
	"context"
	"crypto/subtle"
	"embed"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
)

//go:embed all:dist
var assets embed.FS

type App struct {
	restart func()
	engine  *downloads.Manager
	service *catalog.Service
	store   *database.Store
	tokenMu sync.RWMutex
	token   string
}

func New(service *catalog.Service, engines ...*downloads.Manager) (http.Handler, error) {
	var engine *downloads.Manager
	if len(engines) > 0 {
		engine = engines[0]
	}
	return NewWithRestart(service, engine, nil)
}

func NewWithRestart(service *catalog.Service, engine *downloads.Manager, restart func()) (http.Handler, error) {
	var store *database.Store
	if engine != nil && engine.Store() != nil {
		store = engine.Store()
	} else if service != nil && service.Store() != nil {
		store = service.Store()
	}
	defaultToken := strings.TrimSpace(os.Getenv("AUTH_TOKEN"))
	if defaultToken == "" {
		defaultToken = "bigflix"
	}
	token := defaultToken
	if store != nil {
		if saved, err := store.AuthToken(context.Background(), defaultToken); err == nil && saved != "" {
			token = saved
		}
	}
	a := &App{
		service: service,
		engine:  engine,
		restart: restart,
		store:   store,
		token:   token,
	}
	mux := http.NewServeMux()
	rpcPath, rpcHandler := bigflixv1connect.NewBigflixServiceHandler(
		a,
		connect.WithInterceptors(a.authInterceptor()),
	)
	mux.Handle(rpcPath, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/WatchDownload") || strings.HasSuffix(r.URL.Path, "/ProbeMedia") {
			controller := http.NewResponseController(w)
			_ = controller.SetWriteDeadline(time.Time{})
		}
		rpcHandler.ServeHTTP(w, r)
	}))
	a.downloadRoutes(mux)
	a.remuxRoutes(mux)

	csp := "default-src 'self'; img-src 'self' https: data:; style-src 'self' 'unsafe-inline'; font-src 'self' data:; media-src 'self' blob:; connect-src 'self' blob:; script-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'"
	frontendURL := strings.TrimSpace(os.Getenv("FRONTEND_URL"))
	if frontendURL == "" {
		frontendURL = strings.TrimSpace(os.Getenv("FRONTEND_DEV_URL"))
	}
	if frontendURL != "" {
		target, err := url.Parse(frontendURL)
		if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" {
			return nil, errors.New("FRONTEND_URL must be an http or https URL")
		}
		proxy := httputil.NewSingleHostReverseProxy(target)
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
				controller := http.NewResponseController(w)
				_ = controller.SetReadDeadline(time.Time{})
				_ = controller.SetWriteDeadline(time.Time{})
			}
			proxy.ServeHTTP(w, r)
		})
		csp = "default-src 'self'; img-src 'self' https: data:; style-src 'self' 'unsafe-inline'; font-src 'self' data:; media-src 'self' blob:; connect-src 'self' blob: ws: wss: http: https:; script-src 'self' 'unsafe-inline' 'unsafe-eval'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'"
	} else {
		dist, err := fs.Sub(assets, "dist")
		if err != nil {
			return nil, err
		}
		indexHTML, err := fs.ReadFile(dist, "index.html")
		if err != nil {
			return nil, err
		}
		fileServer := http.FileServer(http.FS(dist))
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				http.NotFound(w, r)
				return
			}
			clean := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
			if clean != "" {
				if info, err := fs.Stat(dist, clean); err == nil && !info.IsDir() {
					fileServer.ServeHTTP(w, r)
					return
				}
				if strings.HasPrefix(clean, "assets/") || strings.HasPrefix(clean, "vendor/") {
					http.NotFound(w, r)
					return
				}
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			if r.Method == http.MethodGet {
				_, _ = w.Write(indexHTML)
			}
		})
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o := r.Header.Get("Origin"); o != "" && o != "http://"+r.Host && o != "https://"+r.Host {
			http.Error(w, "Cross-origin request rejected", http.StatusForbidden)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", csp)
		w.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	}), nil
}

func (a *App) getToken() string {
	a.tokenMu.RLock()
	defer a.tokenMu.RUnlock()
	return a.token
}

func (a *App) setToken(token string) {
	a.tokenMu.Lock()
	a.token = token
	a.tokenMu.Unlock()
}

func (a *App) validToken(candidate string) bool {
	if candidate == "" {
		return false
	}
	expected := a.getToken()
	return expected != "" && subtle.ConstantTimeCompare([]byte(candidate), []byte(expected)) == 1
}

func extractHeaderToken(h http.Header) string {
	if auth := h.Get("Authorization"); auth != "" {
		if len(auth) > 7 && strings.EqualFold(auth[:7], "Bearer ") {
			return strings.TrimSpace(auth[7:])
		}
	}
	return strings.TrimSpace(h.Get("X-Bigflix-Token"))
}

func (a *App) authInterceptor() connect.Interceptor {
	return &tokenAuthInterceptor{app: a}
}

type tokenAuthInterceptor struct {
	app *App
}

func (i *tokenAuthInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if !i.app.validToken(extractHeaderToken(req.Header())) {
			return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
		}
		return next(ctx, req)
	}
}

func (i *tokenAuthInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (i *tokenAuthInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		if !i.app.validToken(extractHeaderToken(conn.RequestHeader())) {
			return connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
		}
		return next(ctx, conn)
	}
}

func (a *App) requireMediaAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := extractHeaderToken(r.Header)
		if token == "" {
			token = strings.TrimSpace(r.URL.Query().Get("token"))
		}
		if token == "" {
			if c, err := r.Cookie("bigflix_token"); err == nil {
				token = strings.TrimSpace(c.Value)
			}
		}
		if !a.validToken(token) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func friendly(err error) string {
	if errors.Is(err, catalog.ErrConfiguration) {
		return "Connect TMDB to browse worldwide trends and new releases. Your YTS movie catalog is already available."
	}
	if errors.Is(err, catalog.ErrNotFound) {
		return "This movie could not be found."
	}
	return "The catalog provider could not be reached. Please try again shortly."
}
