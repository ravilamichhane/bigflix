package sources

import (
	"bigflix/internal/catalog"
	"bigflix/internal/database"
	"context"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Query struct {
	MediaType        string // "movie" | "episode"
	TMDBID           int
	IMDbID           string
	Title            string
	OriginalTitle    string
	Year             int
	OriginalLanguage string
	Genres           []string
	Season           int
	Episode          int
	TotalSeasons     int
}

type RawTorrent struct {
	Hash         string `json:"hash"`
	Title        string `json:"title"`
	Provider     string `json:"provider"`
	SizeBytes    int64  `json:"size_bytes"`
	Seeds        int    `json:"seeds"`
	Peers        int    `json:"peers"`
	Quality      string `json:"quality,omitempty"`
	Codec        string `json:"codec,omitempty"`
	SourceType   string `json:"source_type,omitempty"`
	Audio        string `json:"audio,omitempty"`
	Season       int    `json:"season,omitempty"`
	Episode      int    `json:"episode,omitempty"`
	IsSeasonPack bool   `json:"is_season_pack,omitempty"`
}

type IndexerHealth struct {
	ID           string
	Name         string
	Enabled      bool
	BaseURL      string
	Health       string // "ok" | "degraded" | "idle" | "disabled"
	LastError    string
	AvgLatencyMs int
}

type SearchReport struct {
	Results          []RawTorrent `json:"results"`
	ProvidersQueried []string     `json:"providers_queried"`
	Warnings         []string     `json:"warnings,omitempty"`
	UpdatedAt        time.Time    `json:"updated_at"`
}

type healthEntry struct {
	status       string
	lastErr      string
	avgLatencyMs int
	failStreak   int
	cooldownEnd  time.Time
}

type Aggregator struct {
	store        *database.Store
	publicClient *http.Client
	localClient  *http.Client
	mu           sync.Mutex
	health       map[string]*healthEntry
}

var (
	hexHashRE    = regexp.MustCompile(`(?i)\b([a-f0-9]{40})\b`)
	magnetBtihRE = regexp.MustCompile(`(?i)xt=urn:btih:([a-z2-7]{32}|[a-f0-9]{40})`)
	sizeRE       = regexp.MustCompile(`(?i)([\d.]+)\s*(ti?b|gi?b|mi?b|ki?b|b)\b`)
	nonAlnumRE   = regexp.MustCompile(`[^\p{L}\p{N}\s'-]+`)
)

// LocalClient allows LAN/Docker hosts (for user-configured Torznab or local AI)
// while still enforcing HTTP/HTTPS scheme and bounded response reading.
func LocalClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	return &http.Client{
		Transport: tr,
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many redirects")
			}
			if (req.URL.Scheme != "http" && req.URL.Scheme != "https") || req.URL.User != nil {
				return errors.New("unsafe redirect")
			}
			return nil
		},
	}
}

func NewAggregator(store *database.Store) *Aggregator {
	return &Aggregator{
		store:        store,
		publicClient: catalog.PublicClient(),
		localClient:  LocalClient(10 * time.Second),
		health:       map[string]*healthEntry{},
	}
}

// SetClients is primarily for unit testing with httptest servers.
func (a *Aggregator) SetClients(publicClient, localClient *http.Client) {
	if publicClient != nil {
		a.publicClient = publicClient
	}
	if localClient != nil {
		a.localClient = localClient
	}
}

func ExtractHash(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) == 40 && hexHashRE.MatchString(raw) {
		return strings.ToLower(raw)
	}
	if m := magnetBtihRE.FindStringSubmatch(raw); len(m) == 2 {
		tok := m[1]
		if len(tok) == 40 {
			return strings.ToLower(tok)
		}
		if len(tok) == 32 {
			if decoded, err := base32.StdEncoding.DecodeString(strings.ToUpper(tok)); err == nil && len(decoded) == 20 {
				return hex.EncodeToString(decoded)
			}
		}
	}
	if m := hexHashRE.FindStringSubmatch(raw); len(m) == 2 {
		return strings.ToLower(m[1])
	}
	return ""
}

func ParseSizeBytes(raw string) int64 {
	raw = strings.TrimSpace(raw)
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 {
		return n
	}
	m := sizeRE.FindStringSubmatch(raw)
	if len(m) != 3 {
		return 0
	}
	val, err := strconv.ParseFloat(m[1], 64)
	if err != nil || val <= 0 {
		return 0
	}
	unit := strings.ToLower(m[2])
	switch unit {
	case "tib", "tb":
		return int64(val * 1024 * 1024 * 1024 * 1024)
	case "gib", "gb":
		return int64(val * 1024 * 1024 * 1024)
	case "mib", "mb":
		return int64(val * 1024 * 1024)
	case "kib", "kb":
		return int64(val * 1024)
	default:
		return int64(val)
	}
}

func CleanSearchTitle(title string) string {
	clean := nonAlnumRE.ReplaceAllString(title, " ")
	return strings.Join(strings.Fields(clean), " ")
}

func isAnimeQuery(q Query) bool {
	if strings.EqualFold(q.OriginalLanguage, "ja") {
		return true
	}
	for _, g := range q.Genres {
		if strings.EqualFold(g, "Animation") || strings.EqualFold(g, "Anime") {
			return true
		}
	}
	return false
}

func (a *Aggregator) recordResult(id string, latency time.Duration, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	h := a.health[id]
	if h == nil {
		h = &healthEntry{status: "idle"}
		a.health[id] = h
	}
	ms := int(latency.Milliseconds())
	if h.avgLatencyMs == 0 {
		h.avgLatencyMs = ms
	} else {
		h.avgLatencyMs = (h.avgLatencyMs*3 + ms) / 4
	}
	if err != nil {
		h.failStreak++
		h.lastErr = err.Error()
		h.status = "degraded"
		if h.failStreak >= 3 {
			h.cooldownEnd = time.Now().Add(60 * time.Second)
		}
	} else {
		h.failStreak = 0
		h.lastErr = ""
		h.status = "ok"
		h.cooldownEnd = time.Time{}
	}
}

func (a *Aggregator) inCooldown(id string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if h := a.health[id]; h != nil && !h.cooldownEnd.IsZero() && time.Now().Before(h.cooldownEnd) {
		return true
	}
	return false
}

func (a *Aggregator) ResetHealth() {
	a.mu.Lock()
	a.health = map[string]*healthEntry{}
	a.mu.Unlock()
}

func (a *Aggregator) HealthSnapshot(ctx context.Context) []IndexerHealth {
	cfg := database.DefaultSourcesConfig()
	if a.store != nil {
		cfg = a.store.LoadSourcesConfig(ctx)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]IndexerHealth, 0, len(cfg.Indexers))
	for _, ix := range cfg.Indexers {
		st := IndexerHealth{
			ID:      ix.ID,
			Name:    ix.Name,
			Enabled: ix.Enabled,
			BaseURL: ix.BaseURL,
			Health:  "idle",
		}
		if !ix.Enabled {
			st.Health = "disabled"
		} else if h := a.health[ix.ID]; h != nil {
			st.Health = h.status
			st.LastError = h.lastErr
			st.AvgLatencyMs = h.avgLatencyMs
		}
		out = append(out, st)
	}
	return out
}

func (a *Aggregator) Search(ctx context.Context, q Query, forceRefresh bool) (SearchReport, error) {
	cfg := database.DefaultSourcesConfig()
	if a.store != nil {
		cfg = a.store.LoadSourcesConfig(ctx)
	}
	cacheKey := fmt.Sprintf("sources:v2:%s:%d:s%de%d", q.MediaType, q.TMDBID, q.Season, q.Episode)
	if !forceRefresh && a.store != nil && q.TMDBID > 0 {
		if b, at, err := a.store.Get(ctx, cacheKey); err == nil && time.Since(at) < 15*time.Minute {
			var cached SearchReport
			if json.Unmarshal(b, &cached) == nil && len(cached.Results) > 0 {
				return cached, nil
			}
		}
	}

	type jobSpec struct {
		id  string
		run func(context.Context) ([]RawTorrent, error)
	}
	var jobs []jobSpec
	anime := isAnimeQuery(q)

	for _, ix := range cfg.Indexers {
		if !ix.Enabled || strings.TrimSpace(ix.BaseURL) == "" {
			continue
		}
		if a.inCooldown(ix.ID) {
			continue
		}
		base := strings.TrimSpace(ix.BaseURL)
		switch ix.ID {
		case "yts":
			if q.MediaType == "movie" {
				jobs = append(jobs, jobSpec{id: "yts", run: func(c context.Context) ([]RawTorrent, error) {
					return a.searchYTS(c, base, q)
				}})
			}
		case "eztv":
			if q.MediaType == "episode" && q.IMDbID != "" {
				jobs = append(jobs, jobSpec{id: "eztv", run: func(c context.Context) ([]RawTorrent, error) {
					return a.searchEZTV(c, base, q)
				}})
			}
		case "knaben":
			jobs = append(jobs, jobSpec{id: "knaben", run: func(c context.Context) ([]RawTorrent, error) {
				return a.searchKnaben(c, base, q)
			}})
		case "apibay":
			jobs = append(jobs, jobSpec{id: "apibay", run: func(c context.Context) ([]RawTorrent, error) {
				return a.searchAPIBay(c, base, q)
			}})
		case "nyaa":
			if anime {
				jobs = append(jobs, jobSpec{id: "nyaa", run: func(c context.Context) ([]RawTorrent, error) {
					return a.searchNyaa(c, base, q)
				}})
			}
		}
	}

	if cfg.Torznab.Enabled && strings.TrimSpace(cfg.Torznab.URL) != "" && !a.inCooldown("torznab") {
		tCfg := cfg.Torznab
		jobs = append(jobs, jobSpec{id: "torznab", run: func(c context.Context) ([]RawTorrent, error) {
			return a.searchTorznab(c, tCfg, q)
		}})
	}

	type outcome struct {
		id      string
		results []RawTorrent
		err     error
	}
	ch := make(chan outcome, len(jobs))
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		go func(spec jobSpec) {
			defer wg.Done()
			jCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()
			start := time.Now()
			res, err := spec.run(jCtx)
			a.recordResult(spec.id, time.Since(start), err)
			ch <- outcome{id: spec.id, results: res, err: err}
		}(j)
	}
	wg.Wait()
	close(ch)

	report := SearchReport{UpdatedAt: time.Now().UTC()}
	byHash := map[string]RawTorrent{}
	for out := range ch {
		report.ProvidersQueried = append(report.ProvidersQueried, out.id)
		if out.err != nil {
			report.Warnings = append(report.Warnings, fmt.Sprintf("%s: %v", out.id, out.err))
			continue
		}
		for _, r := range out.results {
			h := ExtractHash(r.Hash)
			if h == "" || strings.TrimSpace(r.Title) == "" {
				continue
			}
			r.Hash = h
			if prev, exists := byHash[h]; exists {
				if r.Seeds > prev.Seeds {
					prev.Seeds = r.Seeds
				}
				if r.Peers > prev.Peers {
					prev.Peers = r.Peers
				}
				if prev.SizeBytes == 0 && r.SizeBytes > 0 {
					prev.SizeBytes = r.SizeBytes
				}
				if prev.Quality == "" && r.Quality != "" {
					prev.Quality = r.Quality
				}
				if prev.Codec == "" && r.Codec != "" {
					prev.Codec = r.Codec
				}
				if prev.SourceType == "" && r.SourceType != "" {
					prev.SourceType = r.SourceType
				}
				if prev.Audio == "" && r.Audio != "" {
					prev.Audio = r.Audio
				}
				if r.Provider == "yts" {
					prev.Provider = "yts"
				}
				byHash[h] = prev
			} else {
				byHash[h] = r
			}
		}
	}

	report.Results = make([]RawTorrent, 0, len(byHash))
	for _, r := range byHash {
		report.Results = append(report.Results, r)
	}

	if a.store != nil && q.TMDBID > 0 && len(report.Results) > 0 {
		if b, err := json.Marshal(report); err == nil {
			_ = a.store.Put(ctx, cacheKey, b, report.UpdatedAt)
		}
	}
	return report, nil
}

func doBoundedGet(ctx context.Context, client *http.Client, rawURL string, maxBytes int64) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return nil, errors.New("invalid indexer URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json, application/xml, application/rss+xml, text/xml;q=0.9, */*;q=0.5")
	req.Header.Set("User-Agent", "Bigflix/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > maxBytes {
		return nil, errors.New("response too large")
	}
	return b, nil
}
