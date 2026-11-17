package catalog

import (
	"bigflix/internal/database"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Torrent struct {
	Hash    string `json:"hash"`
	Quality string `json:"quality"`
	Type    string `json:"type"`
	Codec   string `json:"video_codec"`
	Size    string `json:"size"`
	Bytes   int64  `json:"size_bytes"`
	Seeds   int    `json:"seeds"`
	Peers   int    `json:"peers"`
	Audio   string `json:"audio_channels"`
}
type Movie struct {
	ID          int       `json:"id"`
	IMDb        string    `json:"imdb_code"`
	Title       string    `json:"title"`
	Year        int       `json:"year"`
	Rating      float64   `json:"rating"`
	Runtime     int       `json:"runtime"`
	Genres      []string  `json:"genres"`
	Summary     string    `json:"summary"`
	Description string    `json:"description_full"`
	Poster      string    `json:"large_cover_image"`
	SmallPoster string    `json:"medium_cover_image"`
	Background  string    `json:"background_image_original"`
	Language    string    `json:"language"`
	Torrents    []Torrent `json:"torrents"`
	TMDBID      int       `json:"tmdb_id"`
	Release     string    `json:"release_date"`
}
type Feed struct {
	Movies  []Movie
	Count   int
	Page    int
	Updated time.Time
	Stale   bool
	Notice  string
}
type Service struct {
	store        *database.Store
	base         string
	tmdb         string
	token        string
	client       *http.Client
	slots        chan struct{}
	mu           sync.Mutex
	blockedUntil time.Time
}

var validHash = regexp.MustCompile(`(?i)^[a-f0-9]{40}$`)
var validIMDb = regexp.MustCompile(`^tt[0-9]{5,12}$`)
var ErrNotFound = errors.New("movie not found")
var ErrUnavailable = errors.New("catalog provider is temporarily unavailable")
var ErrConfiguration = errors.New("TMDB is not configured")

// PublicClient rejects private destinations on both initial requests and redirects.
func PublicClient() *http.Client {
	dialer := &net.Dialer{Timeout: 8 * time.Second}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		if len(ips) == 0 {
			return nil, errors.New("no addresses")
		}
		for _, ip := range ips {
			if !ip.IP.IsGlobalUnicast() || ip.IP.IsPrivate() || ip.IP.IsLoopback() || ip.IP.IsLinkLocalUnicast() {
				return nil, errors.New("private provider address rejected")
			}
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}
	return &http.Client{Transport: tr, Timeout: 15 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return errors.New("too many redirects")
		}
		if req.URL.Scheme != "https" || req.URL.User != nil {
			return errors.New("unsafe redirect")
		}
		if len(via) > 0 && req.URL.Host != via[0].URL.Host {
			req.Header.Del("Authorization")
		}
		return nil
	}}
}
func New(store *database.Store, base, token string) (*Service, error) {
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("YTS_API_URL must be an HTTPS base URL")
	}
	return &Service{store: store, base: strings.TrimRight(base, "/") + "/", tmdb: "https://api.themoviedb.org/3/", token: token, client: PublicClient(), slots: make(chan struct{}, 2)}, nil
}
func (s *Service) HasTMDB() bool { return s.token != "" }
func (s *Service) Store() *database.Store { return s.store }
func (s *Service) fetch(ctx context.Context, address string, ttl time.Duration, auth bool, validate func([]byte) error) ([]byte, time.Time, bool, error) {
	cached, at, cacheErr := s.store.Get(ctx, address)
	if cacheErr == nil && time.Since(at) < ttl && validate(cached) == nil {
		return cached, at, false, nil
	}
	fallback := func(err error) ([]byte, time.Time, bool, error) {
		if cacheErr == nil && validate(cached) == nil {
			return cached, at, true, nil
		}
		return nil, time.Time{}, false, err
	}
	s.mu.Lock()
	blocked := time.Now().Before(s.blockedUntil)
	s.mu.Unlock()
	if blocked {
		return fallback(ErrUnavailable)
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-ctx.Done():
		return nil, time.Time{}, false, ctx.Err()
	}
	req, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return fallback(err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Bigflix/0.1")
	if auth {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fallback(ErrUnavailable)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 429 {
		s.mu.Lock()
		s.blockedUntil = time.Now().Add(time.Minute)
		s.mu.Unlock()
	}
	if resp.StatusCode == 404 {
		return nil, time.Time{}, false, ErrNotFound
	}
	if resp.StatusCode != 200 {
		return fallback(ErrUnavailable)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024+1))
	if err != nil || len(b) > 4*1024*1024 {
		return fallback(ErrUnavailable)
	}
	if err = validate(b); err != nil {
		return fallback(ErrUnavailable)
	}
	at = time.Now()
	if err = s.store.Put(ctx, address, b, at); err != nil {
		return nil, time.Time{}, false, err
	}
	return b, at, false, nil
}

type ytsEnvelope struct {
	Status string `json:"status"`
	Data   struct {
		Count  int     `json:"movie_count"`
		Movies []Movie `json:"movies"`
		Movie  Movie   `json:"movie"`
	} `json:"data"`
}

func validateYTS(b []byte) error {
	var e ytsEnvelope
	if json.Unmarshal(b, &e) != nil || e.Status != "ok" {
		return ErrUnavailable
	}
	return nil
}
func clean(m Movie) Movie {
	m.Poster = safeImage(m.Poster)
	m.SmallPoster = safeImage(m.SmallPoster)
	m.Background = safeImage(m.Background)
	if m.Poster == "" {
		m.Poster = m.SmallPoster
	}
	seen := map[string]bool{}
	var ts []Torrent
	for _, t := range m.Torrents {
		t.Hash = strings.ToLower(t.Hash)
		if validHash.MatchString(t.Hash) && !seen[t.Hash] {
			ts = append(ts, t)
			seen[t.Hash] = true
		}
	}
	m.Torrents = ts
	if m.Description != "" {
		m.Summary = m.Description
	}
	return m
}
func safeImage(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return ""
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && (ip.IsPrivate() || !ip.IsGlobalUnicast() || ip.IsLoopback()) {
		return ""
	}
	if strings.EqualFold(u.Hostname(), "localhost") {
		return ""
	}
	return raw
}
func (s *Service) List(ctx context.Context, query, genre, sort string, page int) (Feed, error) {
	if page < 1 || page > 1000 {
		return Feed{}, errors.New("invalid page")
	}
	if len(query) > 200 {
		return Feed{}, errors.New("search is too long")
	}
	if sort != "date_added" && sort != "rating" && sort != "year" {
		sort = "date_added"
	}
	v := url.Values{"limit": {"24"}, "page": {strconv.Itoa(page)}, "sort_by": {sort}, "order_by": {"desc"}}
	if query != "" {
		v.Set("query_term", query)
	}
	if genre != "" {
		v.Set("genre", genre)
	}
	b, at, stale, err := s.fetch(ctx, s.base+"list_movies.json?"+v.Encode(), 15*time.Minute, false, validateYTS)
	if err != nil {
		return Feed{}, err
	}
	var e ytsEnvelope
	json.Unmarshal(b, &e)
	f := Feed{Count: e.Data.Count, Page: page, Updated: at, Stale: stale}
	for _, m := range e.Data.Movies {
		if m.ID > 0 {
			f.Movies = append(f.Movies, clean(m))
		}
	}
	return f, nil
}
func (s *Service) Detail(ctx context.Context, id int) (Movie, time.Time, bool, error) {
	if id <= 0 {
		return Movie{}, time.Time{}, false, ErrNotFound
	}
	b, at, stale, err := s.fetch(ctx, s.base+"movie_details.json?movie_id="+strconv.Itoa(id), 15*time.Minute, false, func(b []byte) error {
		if err := validateYTS(b); err != nil {
			return err
		}
		var e ytsEnvelope
		json.Unmarshal(b, &e)
		if e.Data.Movie.ID != id {
			return ErrNotFound
		}
		return nil
	})
	if err != nil {
		return Movie{}, at, stale, err
	}
	var e ytsEnvelope
	json.Unmarshal(b, &e)
	m := clean(e.Data.Movie)
	if m.ID != id {
		return m, at, stale, ErrNotFound
	}
	if err = s.store.Movie(ctx, m.ID, m.IMDb, m.Title, b); err != nil {
		return Movie{}, at, stale, err
	}
	return m, at, stale, nil
}
func validJSON(b []byte) error {
	if !json.Valid(b) {
		return ErrUnavailable
	}
	return nil
}
func (s *Service) Discover(ctx context.Context, feed string, page int) (Feed, error) {
	if !s.HasTMDB() {
		return Feed{}, ErrConfiguration
	}
	endpoint := "trending/movie/day"
	v := url.Values{"page": {strconv.Itoa(page)}}
	switch feed {
	case "week":
		endpoint = "trending/movie/week"
	case "new":
		endpoint = "discover/movie"
		v.Set("primary_release_date.gte", time.Now().UTC().AddDate(0, 0, -30).Format("2006-01-02"))
		v.Set("primary_release_date.lte", time.Now().UTC().Format("2006-01-02"))
		v.Set("sort_by", "popularity.desc")
		v.Set("include_adult", "false")
	}
	b, at, stale, err := s.fetch(ctx, s.tmdb+endpoint+"?"+v.Encode(), time.Hour, true, validJSON)
	if err != nil {
		return Feed{}, err
	}
	var e struct {
		Results []struct {
			ID       int     `json:"id"`
			Title    string  `json:"title"`
			Overview string  `json:"overview"`
			Poster   string  `json:"poster_path"`
			Release  string  `json:"release_date"`
			Rating   float64 `json:"vote_average"`
		}
		Count int `json:"total_results"`
	}
	if json.Unmarshal(b, &e) != nil {
		return Feed{}, ErrUnavailable
	}
	f := Feed{Page: page, Count: e.Count, Updated: at, Stale: stale}
	for _, m := range e.Results {
		year := 0
		if len(m.Release) >= 4 {
			year, _ = strconv.Atoi(m.Release[:4])
		}
		poster := ""
		if strings.HasPrefix(m.Poster, "/") {
			poster = "https://image.tmdb.org/t/p/w500" + m.Poster
		}
		f.Movies = append(f.Movies, Movie{TMDBID: m.ID, Title: m.Title, Summary: m.Overview, Poster: poster, Release: m.Release, Year: year, Rating: m.Rating})
	}
	return f, nil
}
func (s *Service) FindSources(ctx context.Context, id int) (Feed, error) {
	if !s.HasTMDB() {
		return Feed{}, ErrConfiguration
	}
	b, _, _, err := s.fetch(ctx, fmt.Sprintf("%smovie/%d/external_ids", s.tmdb, id), 24*time.Hour, true, validJSON)
	if err != nil {
		return Feed{}, err
	}
	var e struct {
		IMDb string `json:"imdb_id"`
	}
	json.Unmarshal(b, &e)
	if !validIMDb.MatchString(e.IMDb) {
		return Feed{Notice: "This title has no IMDb identifier. An automatic source match is unavailable."}, nil
	}
	f, err := s.List(ctx, e.IMDb, "", "date_added", 1)
	if err != nil {
		return f, err
	}
	var exact []Movie
	for _, m := range f.Movies {
		if m.IMDb == e.IMDb {
			exact = append(exact, m)
		}
	}
	f.Movies = exact
	f.Count = len(exact)
	f.Notice = "Sources matched by IMDb identifier. A listing does not guarantee available peers."
	return f, nil
}
