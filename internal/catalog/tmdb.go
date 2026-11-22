package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type CatalogItem struct {
	TMDBID        int
	MediaType     string // "movie" | "tv"
	Title         string
	OriginalTitle string
	Year          int
	ReleaseDate   string
	Rating        float64
	Overview      string
	Poster        string
	Backdrop      string
	Language      string
	Genres        []string
	Runtime       int
	IMDb          string
	YTSID         int
}

type CatalogFeed struct {
	Items    []CatalogItem
	Count    int
	Page     int
	Updated  time.Time
	Stale    bool
	Notice   string
	Heading  string
	Subtitle string
}

type SeasonSummary struct {
	SeasonNumber int
	Name         string
	EpisodeCount int
	AirDate      string
	Poster       string
	Overview     string
}

type SeriesDetail struct {
	TMDBID        int
	IMDb          string
	Title         string
	OriginalTitle string
	FirstAirDate  string
	Year          int
	Rating        float64
	Status        string
	Genres        []string
	Overview      string
	Poster        string
	Backdrop      string
	Language      string
	SeasonCount   int
	EpisodeCount  int
	Seasons       []SeasonSummary
}

type EpisodeDetail struct {
	SeasonNumber  int
	EpisodeNumber int
	Name          string
	Overview      string
	AirDate       string
	Runtime       int
	Still         string
	Rating        float64
}

type SeasonDetail struct {
	Season   SeasonSummary
	Episodes []EpisodeDetail
}

var movieGenreIDs = map[string]string{
	"Action":      "28",
	"Adventure":   "12",
	"Animation":   "16",
	"Anime":       "16",
	"Comedy":      "35",
	"Crime":       "80",
	"Documentary": "99",
	"Drama":       "18",
	"Family":      "10751",
	"Fantasy":     "14",
	"History":     "36",
	"Horror":      "27",
	"Mystery":     "9648",
	"Romance":     "10749",
	"Sci-Fi":      "878",
	"Thriller":    "53",
	"War":         "10752",
	"Western":     "37",
}

var tvGenreIDs = map[string]string{
	"Action":      "10759",
	"Adventure":   "10759",
	"Animation":   "16",
	"Anime":       "16",
	"Comedy":      "35",
	"Crime":       "80",
	"Documentary": "99",
	"Drama":       "18",
	"Family":      "10751",
	"Fantasy":     "10765",
	"Kids":        "10762",
	"Mystery":     "9648",
	"Reality":     "10764",
	"Sci-Fi":      "10765",
	"War":         "10768",
	"Western":     "37",
}

func tmdbPoster(path string) string {
	if strings.HasPrefix(path, "/") {
		return "https://image.tmdb.org/t/p/w500" + path
	}
	return ""
}

func tmdbBackdrop(path string) string {
	if strings.HasPrefix(path, "/") {
		return "https://image.tmdb.org/t/p/w1280" + path
	}
	return ""
}

func tmdbStill(path string) string {
	if strings.HasPrefix(path, "/") {
		return "https://image.tmdb.org/t/p/w500" + path
	}
	return ""
}

func parseYear(date string) int {
	if len(date) >= 4 {
		y, _ := strconv.Atoi(date[:4])
		return y
	}
	return 0
}

type tmdbRawItem struct {
	ID               int     `json:"id"`
	MediaType        string  `json:"media_type"`
	Title            string  `json:"title"`
	OriginalTitle    string  `json:"original_title"`
	Name             string  `json:"name"`
	OriginalName     string  `json:"original_name"`
	Overview         string  `json:"overview"`
	PosterPath       string  `json:"poster_path"`
	BackdropPath     string  `json:"backdrop_path"`
	ReleaseDate      string  `json:"release_date"`
	FirstAirDate     string  `json:"first_air_date"`
	VoteAverage      float64 `json:"vote_average"`
	OriginalLanguage string  `json:"original_language"`
}

func toCatalogItem(r tmdbRawItem, defaultType string) (CatalogItem, bool) {
	mt := r.MediaType
	if mt == "" {
		mt = defaultType
	}
	if mt != "movie" && mt != "tv" {
		return CatalogItem{}, false
	}
	title := r.Title
	orig := r.OriginalTitle
	date := r.ReleaseDate
	if mt == "tv" {
		if r.Name != "" {
			title = r.Name
		}
		if r.OriginalName != "" {
			orig = r.OriginalName
		}
		if r.FirstAirDate != "" {
			date = r.FirstAirDate
		}
	}
	if title == "" {
		return CatalogItem{}, false
	}
	return CatalogItem{
		TMDBID:        r.ID,
		MediaType:     mt,
		Title:         title,
		OriginalTitle: orig,
		Year:          parseYear(date),
		ReleaseDate:   date,
		Rating:        r.VoteAverage,
		Overview:      r.Overview,
		Poster:        tmdbPoster(r.PosterPath),
		Backdrop:      tmdbBackdrop(r.BackdropPath),
		Language:      r.OriginalLanguage,
	}, true
}

func (s *Service) Browse(ctx context.Context, mediaType, feed, query, genre, sortBy string, page int) (CatalogFeed, error) {
	if page < 1 || page > 500 {
		return CatalogFeed{}, errors.New("invalid page")
	}
	query = strings.TrimSpace(query)
	if len(query) > 200 {
		return CatalogFeed{}, errors.New("search is too long")
	}
	if mediaType == "" {
		mediaType = "all"
	}
	if mediaType != "all" && mediaType != "movie" && mediaType != "tv" {
		return CatalogFeed{}, errors.New("invalid media type")
	}

	// Emergency YTS movie fallback when TMDB is not configured yet.
	if feed == "yts_fallback" {
		ytsFeed, err := s.List(ctx, query, genre, sortBy, page)
		if err != nil {
			return CatalogFeed{}, err
		}
		out := CatalogFeed{
			Count:    ytsFeed.Count,
			Page:     ytsFeed.Page,
			Updated:  ytsFeed.Updated,
			Stale:    ytsFeed.Stale,
			Notice:   "Showing YTS movie fallback. Add a TMDB Read Token in Settings to unlock TV series, full search, and multi-source playback.",
			Heading:  "YTS Movie Fallback",
			Subtitle: "Configure TMDB in Settings for Movies + TV Series discovery.",
		}
		for _, m := range ytsFeed.Movies {
			out.Items = append(out.Items, CatalogItem{
				MediaType: "movie",
				Title:     m.Title,
				Year:      m.Year,
				Rating:    m.Rating,
				Overview:  m.Summary,
				Poster:    m.Poster,
				Backdrop:  m.Background,
				Language:  m.Language,
				Genres:    m.Genres,
				Runtime:   m.Runtime,
				IMDb:      m.IMDb,
				YTSID:     m.ID,
			})
		}
		return out, nil
	}

	if !s.HasTMDB() {
		return CatalogFeed{}, ErrConfiguration
	}

	// Direct IMDb ID lookup via TMDB /find/{imdb_id}
	if query != "" && validIMDb.MatchString(query) {
		return s.findByIMDb(ctx, query, mediaType)
	}

	// Search query
	if query != "" {
		endpoint := "search/multi"
		defaultType := ""
		heading := "Search results"
		subtitle := "Movies & TV series from TMDB."
		switch mediaType {
		case "movie":
			endpoint = "search/movie"
			defaultType = "movie"
			subtitle = "Movies from TMDB."
		case "tv":
			endpoint = "search/tv"
			defaultType = "tv"
			subtitle = "TV series from TMDB."
		}
		v := url.Values{"query": {query}, "page": {strconv.Itoa(page)}, "include_adult": {"false"}}
		return s.fetchTMDBList(ctx, endpoint, v, 15*time.Minute, defaultType, page, heading, subtitle)
	}

	// Discover / Browse feeds
	if feed == "" {
		if mediaType == "all" {
			feed = "trending"
		} else {
			feed = "popular"
		}
	}

	if mediaType == "all" {
		switch feed {
		case "week":
			v := url.Values{"page": {strconv.Itoa(page)}}
			return s.fetchTMDBList(ctx, "trending/all/week", v, time.Hour, "", page, "Trending this week", "Popular movies & TV series worldwide, powered by TMDB.")
		case "new":
			return s.browseMixedNew(ctx, page)
		case "popular":
			v := url.Values{"page": {strconv.Itoa(page)}}
			return s.fetchTMDBList(ctx, "trending/all/week", v, time.Hour, "", page, "Popular now", "Movies & TV series people are watching right now.")
		default:
			v := url.Values{"page": {strconv.Itoa(page)}}
			return s.fetchTMDBList(ctx, "trending/all/day", v, time.Hour, "", page, "Discover", "Trending movies & TV series today, powered by TMDB.")
		}
	}

	if mediaType == "movie" {
		if genre != "" || sortBy != "" || feed == "popular" || feed == "top_rated" || feed == "new" {
			v := url.Values{"page": {strconv.Itoa(page)}, "include_adult": {"false"}}
			heading := "Movies"
			subtitle := "Browse and stream any movie with one click."
			if feed == "new" {
				v.Set("primary_release_date.gte", time.Now().UTC().AddDate(0, 0, -45).Format("2006-01-02"))
				v.Set("primary_release_date.lte", time.Now().UTC().Format("2006-01-02"))
				v.Set("sort_by", "popularity.desc")
				heading = "New movies"
				subtitle = "Recently released movies on TMDB."
			} else if feed == "top_rated" || sortBy == "rating" {
				v.Set("sort_by", "vote_average.desc")
				v.Set("vote_count.gte", "300")
				heading = "Top rated movies"
			} else if sortBy == "year" {
				v.Set("sort_by", "primary_release_date.desc")
				v.Set("primary_release_date.lte", time.Now().UTC().Format("2006-01-02"))
				v.Set("vote_count.gte", "50")
			} else {
				v.Set("sort_by", "popularity.desc")
			}
			if genre != "" {
				if id, ok := movieGenreIDs[genre]; ok {
					v.Set("with_genres", id)
				}
				if strings.EqualFold(genre, "Anime") {
					v.Set("with_original_language", "ja")
				}
			}
			return s.fetchTMDBList(ctx, "discover/movie", v, time.Hour, "movie", page, heading, subtitle)
		}
		endpoint := "trending/movie/day"
		heading := "Trending movies"
		if feed == "week" {
			endpoint = "trending/movie/week"
			heading = "Trending movies this week"
		}
		return s.fetchTMDBList(ctx, endpoint, url.Values{"page": {strconv.Itoa(page)}}, time.Hour, "movie", page, heading, "Worldwide movie discovery, powered by TMDB.")
	}

	// mediaType == "tv"
	if genre != "" || sortBy != "" || feed == "popular" || feed == "top_rated" {
		v := url.Values{"page": {strconv.Itoa(page)}, "include_adult": {"false"}}
		heading := "TV Series"
		subtitle := "Browse shows, seasons, and episodes — click Play with zero torrent hunting."
		if feed == "top_rated" || sortBy == "rating" {
			v.Set("sort_by", "vote_average.desc")
			v.Set("vote_count.gte", "200")
			heading = "Top rated TV series"
		} else if sortBy == "year" {
			v.Set("sort_by", "first_air_date.desc")
			v.Set("first_air_date.lte", time.Now().UTC().Format("2006-01-02"))
			v.Set("vote_count.gte", "30")
		} else {
			v.Set("sort_by", "popularity.desc")
		}
		if genre != "" {
			if id, ok := tvGenreIDs[genre]; ok {
				v.Set("with_genres", id)
			}
			if strings.EqualFold(genre, "Anime") {
				v.Set("with_original_language", "ja")
			}
		}
		return s.fetchTMDBList(ctx, "discover/tv", v, time.Hour, "tv", page, heading, subtitle)
	}
	switch feed {
	case "new", "on_the_air":
		return s.fetchTMDBList(ctx, "tv/on_the_air", url.Values{"page": {strconv.Itoa(page)}}, time.Hour, "tv", page, "On the air", "TV series airing new episodes now.")
	case "week":
		return s.fetchTMDBList(ctx, "trending/tv/week", url.Values{"page": {strconv.Itoa(page)}}, time.Hour, "tv", page, "Trending TV this week", "Worldwide TV series discovery, powered by TMDB.")
	default:
		return s.fetchTMDBList(ctx, "trending/tv/day", url.Values{"page": {strconv.Itoa(page)}}, time.Hour, "tv", page, "Trending TV series", "Worldwide TV series discovery, powered by TMDB.")
	}
}

func (s *Service) browseMixedNew(ctx context.Context, page int) (CatalogFeed, error) {
	mv := url.Values{
		"page":                     {strconv.Itoa(page)},
		"primary_release_date.gte": {time.Now().UTC().AddDate(0, 0, -45).Format("2006-01-02")},
		"primary_release_date.lte": {time.Now().UTC().Format("2006-01-02")},
		"sort_by":                  {"popularity.desc"},
		"include_adult":            {"false"},
	}
	movies, mErr := s.fetchTMDBList(ctx, "discover/movie", mv, time.Hour, "movie", page, "", "")
	tv, tErr := s.fetchTMDBList(ctx, "tv/on_the_air", url.Values{"page": {strconv.Itoa(page)}}, time.Hour, "tv", page, "", "")
	if mErr != nil && tErr != nil {
		return CatalogFeed{}, mErr
	}
	out := CatalogFeed{
		Page:     page,
		Heading:  "New releases & On the air",
		Subtitle: "Recent movies and currently airing TV series from TMDB.",
		Updated:  movies.Updated,
		Stale:    movies.Stale || tv.Stale,
	}
	if out.Updated.IsZero() {
		out.Updated = tv.Updated
	}
	maxLen := max(len(movies.Items), len(tv.Items))
	for i := 0; i < maxLen; i++ {
		if i < len(movies.Items) {
			out.Items = append(out.Items, movies.Items[i])
		}
		if i < len(tv.Items) {
			out.Items = append(out.Items, tv.Items[i])
		}
	}
	if len(out.Items) > 24 {
		out.Items = out.Items[:24]
	}
	out.Count = movies.Count + tv.Count
	return out, nil
}

func (s *Service) findByIMDb(ctx context.Context, imdbID, mediaType string) (CatalogFeed, error) {
	b, at, stale, err := s.fetch(ctx, s.tmdb+"find/"+imdbID+"?external_source=imdb_id", 6*time.Hour, true, validJSON)
	if err != nil {
		return CatalogFeed{}, err
	}
	var e struct {
		MovieResults []tmdbRawItem `json:"movie_results"`
		TVResults    []tmdbRawItem `json:"tv_results"`
	}
	if json.Unmarshal(b, &e) != nil {
		return CatalogFeed{}, ErrUnavailable
	}
	out := CatalogFeed{
		Page:     1,
		Updated:  at,
		Stale:    stale,
		Heading:  "IMDb Match",
		Subtitle: "Exact title matched by IMDb ID.",
	}
	if mediaType == "all" || mediaType == "movie" {
		for _, r := range e.MovieResults {
			if item, ok := toCatalogItem(r, "movie"); ok {
				item.IMDb = imdbID
				out.Items = append(out.Items, item)
			}
		}
	}
	if mediaType == "all" || mediaType == "tv" {
		for _, r := range e.TVResults {
			if item, ok := toCatalogItem(r, "tv"); ok {
				item.IMDb = imdbID
				out.Items = append(out.Items, item)
			}
		}
	}
	out.Count = len(out.Items)
	return out, nil
}

func (s *Service) fetchTMDBList(ctx context.Context, endpoint string, v url.Values, ttl time.Duration, defaultType string, page int, heading, subtitle string) (CatalogFeed, error) {
	b, at, stale, err := s.fetch(ctx, s.tmdb+endpoint+"?"+v.Encode(), ttl, true, validJSON)
	if err != nil {
		return CatalogFeed{}, err
	}
	var e struct {
		Results      []tmdbRawItem `json:"results"`
		TotalResults int           `json:"total_results"`
	}
	if json.Unmarshal(b, &e) != nil {
		return CatalogFeed{}, ErrUnavailable
	}
	out := CatalogFeed{
		Count:    e.TotalResults,
		Page:     page,
		Updated:  at,
		Stale:    stale,
		Heading:  heading,
		Subtitle: subtitle,
	}
	for _, r := range e.Results {
		if item, ok := toCatalogItem(r, defaultType); ok {
			out.Items = append(out.Items, item)
		}
	}
	return out, nil
}

func (s *Service) MovieDetail(ctx context.Context, tmdbID int) (CatalogItem, time.Time, bool, error) {
	if tmdbID <= 0 {
		return CatalogItem{}, time.Time{}, false, ErrNotFound
	}
	if !s.HasTMDB() {
		return CatalogItem{}, time.Time{}, false, ErrConfiguration
	}
	addr := fmt.Sprintf("%smovie/%d?append_to_response=external_ids", s.tmdb, tmdbID)
	b, at, stale, err := s.fetch(ctx, addr, 24*time.Hour, true, validJSON)
	if err != nil {
		return CatalogItem{}, at, stale, err
	}
	var raw struct {
		ID               int     `json:"id"`
		Title            string  `json:"title"`
		OriginalTitle    string  `json:"original_title"`
		Overview         string  `json:"overview"`
		PosterPath       string  `json:"poster_path"`
		BackdropPath     string  `json:"backdrop_path"`
		ReleaseDate      string  `json:"release_date"`
		VoteAverage      float64 `json:"vote_average"`
		Runtime          int     `json:"runtime"`
		OriginalLanguage string  `json:"original_language"`
		IMDbID           string  `json:"imdb_id"`
		Genres           []struct {
			Name string `json:"name"`
		} `json:"genres"`
		ExternalIDs struct {
			IMDbID string `json:"imdb_id"`
		} `json:"external_ids"`
	}
	if json.Unmarshal(b, &raw) != nil || raw.ID == 0 {
		return CatalogItem{}, at, stale, ErrNotFound
	}
	imdb := raw.IMDbID
	if imdb == "" {
		imdb = raw.ExternalIDs.IMDbID
	}
	if !validIMDb.MatchString(imdb) {
		imdb = ""
	}
	genres := make([]string, 0, len(raw.Genres))
	for _, g := range raw.Genres {
		if g.Name != "" {
			genres = append(genres, g.Name)
		}
	}
	return CatalogItem{
		TMDBID:        raw.ID,
		MediaType:     "movie",
		Title:         raw.Title,
		OriginalTitle: raw.OriginalTitle,
		Year:          parseYear(raw.ReleaseDate),
		ReleaseDate:   raw.ReleaseDate,
		Rating:        raw.VoteAverage,
		Overview:      raw.Overview,
		Poster:        tmdbPoster(raw.PosterPath),
		Backdrop:      tmdbBackdrop(raw.BackdropPath),
		Language:      raw.OriginalLanguage,
		Genres:        genres,
		Runtime:       raw.Runtime,
		IMDb:          imdb,
	}, at, stale, nil
}

func (s *Service) SeriesDetail(ctx context.Context, tmdbID int) (SeriesDetail, time.Time, bool, error) {
	if tmdbID <= 0 {
		return SeriesDetail{}, time.Time{}, false, ErrNotFound
	}
	if !s.HasTMDB() {
		return SeriesDetail{}, time.Time{}, false, ErrConfiguration
	}
	addr := fmt.Sprintf("%stv/%d?append_to_response=external_ids", s.tmdb, tmdbID)
	b, at, stale, err := s.fetch(ctx, addr, 12*time.Hour, true, validJSON)
	if err != nil {
		return SeriesDetail{}, at, stale, err
	}
	var raw struct {
		ID               int     `json:"id"`
		Name             string  `json:"name"`
		OriginalName     string  `json:"original_name"`
		Overview         string  `json:"overview"`
		PosterPath       string  `json:"poster_path"`
		BackdropPath     string  `json:"backdrop_path"`
		FirstAirDate     string  `json:"first_air_date"`
		VoteAverage      float64 `json:"vote_average"`
		Status           string  `json:"status"`
		OriginalLanguage string  `json:"original_language"`
		NumberOfSeasons  int     `json:"number_of_seasons"`
		NumberOfEpisodes int     `json:"number_of_episodes"`
		Genres           []struct {
			Name string `json:"name"`
		} `json:"genres"`
		Seasons []struct {
			SeasonNumber int    `json:"season_number"`
			Name         string `json:"name"`
			EpisodeCount int    `json:"episode_count"`
			AirDate      string `json:"air_date"`
			PosterPath   string `json:"poster_path"`
			Overview     string `json:"overview"`
		} `json:"seasons"`
		ExternalIDs struct {
			IMDbID string `json:"imdb_id"`
		} `json:"external_ids"`
	}
	if json.Unmarshal(b, &raw) != nil || raw.ID == 0 {
		return SeriesDetail{}, at, stale, ErrNotFound
	}
	imdb := raw.ExternalIDs.IMDbID
	if !validIMDb.MatchString(imdb) {
		imdb = ""
	}
	genres := make([]string, 0, len(raw.Genres))
	for _, g := range raw.Genres {
		if g.Name != "" {
			genres = append(genres, g.Name)
		}
	}
	seasons := make([]SeasonSummary, 0, len(raw.Seasons))
	for _, sn := range raw.Seasons {
		if sn.EpisodeCount <= 0 {
			continue
		}
		seasons = append(seasons, SeasonSummary{
			SeasonNumber: sn.SeasonNumber,
			Name:         sn.Name,
			EpisodeCount: sn.EpisodeCount,
			AirDate:      sn.AirDate,
			Poster:       tmdbPoster(sn.PosterPath),
			Overview:     sn.Overview,
		})
	}
	// Put regular seasons (1..N) first, Specials (0) at the end.
	sort.SliceStable(seasons, func(i, j int) bool {
		if seasons[i].SeasonNumber == 0 {
			return false
		}
		if seasons[j].SeasonNumber == 0 {
			return true
		}
		return seasons[i].SeasonNumber < seasons[j].SeasonNumber
	})
	return SeriesDetail{
		TMDBID:        raw.ID,
		IMDb:          imdb,
		Title:         raw.Name,
		OriginalTitle: raw.OriginalName,
		FirstAirDate:  raw.FirstAirDate,
		Year:          parseYear(raw.FirstAirDate),
		Rating:        raw.VoteAverage,
		Status:        raw.Status,
		Genres:        genres,
		Overview:      raw.Overview,
		Poster:        tmdbPoster(raw.PosterPath),
		Backdrop:      tmdbBackdrop(raw.BackdropPath),
		Language:      raw.OriginalLanguage,
		SeasonCount:   raw.NumberOfSeasons,
		EpisodeCount:  raw.NumberOfEpisodes,
		Seasons:       seasons,
	}, at, stale, nil
}

func (s *Service) SeasonDetail(ctx context.Context, tmdbID, seasonNum int) (SeasonDetail, time.Time, bool, error) {
	if tmdbID <= 0 || seasonNum < 0 {
		return SeasonDetail{}, time.Time{}, false, ErrNotFound
	}
	if !s.HasTMDB() {
		return SeasonDetail{}, time.Time{}, false, ErrConfiguration
	}
	addr := fmt.Sprintf("%stv/%d/season/%d", s.tmdb, tmdbID, seasonNum)
	b, at, stale, err := s.fetch(ctx, addr, 6*time.Hour, true, validJSON)
	if err != nil {
		return SeasonDetail{}, at, stale, err
	}
	var raw struct {
		SeasonNumber int    `json:"season_number"`
		Name         string `json:"name"`
		AirDate      string `json:"air_date"`
		PosterPath   string `json:"poster_path"`
		Overview     string `json:"overview"`
		Episodes     []struct {
			SeasonNumber  int     `json:"season_number"`
			EpisodeNumber int     `json:"episode_number"`
			Name          string  `json:"name"`
			Overview      string  `json:"overview"`
			AirDate       string  `json:"air_date"`
			Runtime       int     `json:"runtime"`
			StillPath     string  `json:"still_path"`
			VoteAverage   float64 `json:"vote_average"`
		} `json:"episodes"`
	}
	if json.Unmarshal(b, &raw) != nil {
		return SeasonDetail{}, at, stale, ErrUnavailable
	}
	out := SeasonDetail{
		Season: SeasonSummary{
			SeasonNumber: raw.SeasonNumber,
			Name:         raw.Name,
			EpisodeCount: len(raw.Episodes),
			AirDate:      raw.AirDate,
			Poster:       tmdbPoster(raw.PosterPath),
			Overview:     raw.Overview,
		},
		Episodes: make([]EpisodeDetail, 0, len(raw.Episodes)),
	}
	for _, ep := range raw.Episodes {
		out.Episodes = append(out.Episodes, EpisodeDetail{
			SeasonNumber:  ep.SeasonNumber,
			EpisodeNumber: ep.EpisodeNumber,
			Name:          ep.Name,
			Overview:      ep.Overview,
			AirDate:       ep.AirDate,
			Runtime:       ep.Runtime,
			Still:         tmdbStill(ep.StillPath),
			Rating:        ep.VoteAverage,
		})
	}
	return out, at, stale, nil
}
