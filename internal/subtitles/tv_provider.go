package subtitles

import (
	"bigflix/internal/database"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type SearchQuery struct {
	IMDbID    string
	TMDBID    int
	MediaType string // "movie" | "episode"
	Title     string
	Season    int
	Episode   int
	Language  string
}

type MediaResult struct {
	ID              int    `json:"id"`
	Label           string `json:"label"`
	Language        string `json:"language"`
	Rating          int    `json:"rating"`
	Page            string `json:"page"`
	HearingImpaired bool   `json:"hearing_impaired"`
	Provider        string `json:"provider"`
	DownloadKey     string `json:"download_key"`
}

// Optional base URL overrides (used in tests; default to official HTTPS endpoints).
var (
	subdlAPIBase         = "https://api.subdl.com/api/v1/subtitles"
	subdlDownloadBase    = "https://dl.subdl.com"
	openSubtitlesAPIBase = "https://api.opensubtitles.com/api/v1"
)

func (s *Service) apiClient() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (s *Service) ActiveProviderName(ctx context.Context, mediaType string) string {
	if s.DB != nil {
		cfg := s.DB.LoadSubtitleProviderConfig(ctx)
		if strings.TrimSpace(cfg.SubDLAPIKey) != "" {
			return "SubDL"
		}
		if strings.TrimSpace(cfg.OpenSubtitlesAPIKey) != "" {
			return "OpenSubtitles"
		}
	}
	if mediaType == "episode" {
		return "None (add SubDL or OpenSubtitles key in Settings)"
	}
	return "YIFY Subtitles"
}

func subdlLangCode(lang string) string {
	switch strings.ToLower(lang) {
	case "pt-br":
		return "BR_PT"
	case "zh-cn":
		return "ZH"
	default:
		return strings.ToUpper(lang)
	}
}

func openSubtitlesLangCode(lang string) string {
	switch strings.ToLower(lang) {
	case "zh-cn":
		return "zh-cn"
	default:
		return strings.ToLower(lang)
	}
}

func (s *Service) SearchMedia(ctx context.Context, q SearchQuery) ([]MediaResult, string, error) {
	if !ValidLanguage(q.Language) {
		return nil, "", errors.New("unsupported subtitle language")
	}
	var cfg database.SubtitleProviderConfig
	if s.DB != nil {
		cfg = s.DB.LoadSubtitleProviderConfig(ctx)
	}

	if strings.TrimSpace(cfg.SubDLAPIKey) != "" {
		res, err := s.searchSubDL(ctx, strings.TrimSpace(cfg.SubDLAPIKey), q)
		if err == nil {
			return res, "subdl", nil
		}
		if strings.TrimSpace(cfg.OpenSubtitlesAPIKey) == "" && q.MediaType == "episode" {
			return nil, "subdl", err
		}
	}
	if strings.TrimSpace(cfg.OpenSubtitlesAPIKey) != "" {
		res, err := s.searchOpenSubtitles(ctx, strings.TrimSpace(cfg.OpenSubtitlesAPIKey), q)
		if err == nil {
			return res, "opensubtitles", nil
		}
		if q.MediaType == "episode" {
			return nil, "opensubtitles", err
		}
	}
	if q.MediaType == "episode" {
		return nil, "", errors.New("online TV subtitle search needs a free SubDL or OpenSubtitles API key in Settings — or use Built-in / Torrent subtitles / Upload")
	}

	// Keyless YIFY fallback for movies
	yifyRes, err := s.Search(ctx, q.IMDbID, q.Title, q.Language)
	if err != nil {
		return nil, "yify", err
	}
	out := make([]MediaResult, 0, len(yifyRes))
	for _, r := range yifyRes {
		out = append(out, MediaResult{
			ID:              r.ID,
			Label:           r.Label,
			Language:        r.Language,
			Rating:          r.Rating,
			Page:            r.Page,
			HearingImpaired: r.HearingImpaired,
			Provider:        "yify",
			DownloadKey:     "yify:" + strconv.Itoa(r.ID),
		})
	}
	return out, "yify", nil
}

type cachedSubDLItem struct {
	URL   string `json:"url"`
	Label string `json:"label"`
}

func (s *Service) searchSubDL(ctx context.Context, apiKey string, q SearchQuery) ([]MediaResult, error) {
	u, err := url.Parse(subdlAPIBase)
	if err != nil {
		return nil, err
	}
	v := u.Query()
	v.Set("api_key", apiKey)
	v.Set("languages", subdlLangCode(q.Language))
	v.Set("subs_per_page", "30")
	if q.MediaType == "episode" {
		v.Set("type", "tv")
		if q.Season > 0 {
			v.Set("season_number", strconv.Itoa(q.Season))
		}
		if q.Episode > 0 {
			v.Set("episode_number", strconv.Itoa(q.Episode))
		}
	} else {
		v.Set("type", "movie")
	}
	switch {
	case imdbPattern.MatchString(q.IMDbID):
		v.Set("imdb_id", q.IMDbID)
	case q.TMDBID > 0:
		v.Set("tmdb_id", strconv.Itoa(q.TMDBID))
	default:
		v.Set("film_name", q.Title)
	}
	u.RawQuery = v.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Bigflix/1.0")
	resp, err := s.apiClient().Do(req)
	if err != nil {
		return nil, errors.New("SubDL is unreachable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("SubDL returned HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	var payload struct {
		Status    bool `json:"status"`
		Subtitles []struct {
			ReleaseName string `json:"release_name"`
			Name        string `json:"name"`
			URL         string `json:"url"`
			HI          bool   `json:"hi"`
		} `json:"subtitles"`
	}
	if err := json.Unmarshal(b, &payload); err != nil || !payload.Status {
		return nil, errors.New("SubDL returned an invalid response")
	}
	out := make([]MediaResult, 0, len(payload.Subtitles))
	for i, item := range payload.Subtitles {
		if !strings.HasPrefix(item.URL, "/") {
			continue
		}
		label := strings.TrimSpace(item.ReleaseName)
		if label == "" {
			label = strings.TrimSpace(item.Name)
		}
		if label == "" {
			label = fmt.Sprintf("SubDL #%d", i+1)
		}
		sum := sha256.Sum256([]byte(item.URL))
		token := hex.EncodeToString(sum[:8])
		key := "subdl:" + token
		if s.DB != nil {
			cached, _ := json.Marshal(cachedSubDLItem{URL: item.URL, Label: label})
			_ = s.DB.Put(ctx, "sub-item:"+key, cached, time.Now())
		}
		out = append(out, MediaResult{
			ID:              i + 1,
			Label:           label,
			Language:        q.Language,
			HearingImpaired: item.HI,
			Provider:        "subdl",
			DownloadKey:     key,
		})
	}
	return out, nil
}

type cachedOSItem struct {
	FileID int    `json:"file_id"`
	Label  string `json:"label"`
}

func (s *Service) searchOpenSubtitles(ctx context.Context, apiKey string, q SearchQuery) ([]MediaResult, error) {
	u, err := url.Parse(strings.TrimRight(openSubtitlesAPIBase, "/") + "/subtitles")
	if err != nil {
		return nil, err
	}
	v := u.Query()
	v.Set("languages", openSubtitlesLangCode(q.Language))
	if q.MediaType == "episode" {
		v.Set("type", "episode")
		if q.Season > 0 {
			v.Set("season_number", strconv.Itoa(q.Season))
		}
		if q.Episode > 0 {
			v.Set("episode_number", strconv.Itoa(q.Episode))
		}
		if q.TMDBID > 0 {
			v.Set("parent_tmdb_id", strconv.Itoa(q.TMDBID))
		} else if imdbPattern.MatchString(q.IMDbID) {
			v.Set("parent_imdb_id", strings.TrimPrefix(q.IMDbID, "tt"))
		} else {
			v.Set("query", q.Title)
		}
	} else {
		v.Set("type", "movie")
		if imdbPattern.MatchString(q.IMDbID) {
			v.Set("imdb_id", strings.TrimPrefix(q.IMDbID, "tt"))
		} else if q.TMDBID > 0 {
			v.Set("tmdb_id", strconv.Itoa(q.TMDBID))
		} else {
			v.Set("query", q.Title)
		}
	}
	u.RawQuery = v.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Api-Key", apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Bigflix/1.0")
	resp, err := s.apiClient().Do(req)
	if err != nil {
		return nil, errors.New("OpenSubtitles is unreachable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenSubtitles returned HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	var payload struct {
		Data []struct {
			Attributes struct {
				Release         string  `json:"release"`
				Ratings         float64 `json:"ratings"`
				HearingImpaired bool    `json:"hearing_impaired"`
				Files           []struct {
					FileID   int    `json:"file_id"`
					FileName string `json:"file_name"`
				} `json:"files"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &payload); err != nil {
		return nil, errors.New("OpenSubtitles returned an invalid response")
	}
	out := make([]MediaResult, 0, len(payload.Data))
	for _, row := range payload.Data {
		if len(row.Attributes.Files) == 0 || row.Attributes.Files[0].FileID <= 0 {
			continue
		}
		fid := row.Attributes.Files[0].FileID
		label := strings.TrimSpace(row.Attributes.Release)
		if label == "" {
			label = strings.TrimSpace(row.Attributes.Files[0].FileName)
		}
		key := "opensubtitles:" + strconv.Itoa(fid)
		if s.DB != nil {
			cached, _ := json.Marshal(cachedOSItem{FileID: fid, Label: label})
			_ = s.DB.Put(ctx, "sub-item:"+key, cached, time.Now())
		}
		out = append(out, MediaResult{
			ID:              fid,
			Label:           label,
			Language:        q.Language,
			Rating:          int(row.Attributes.Ratings),
			HearingImpaired: row.Attributes.HearingImpaired,
			Provider:        "opensubtitles",
			DownloadKey:     key,
		})
	}
	return out, nil
}

func (s *Service) DownloadMedia(ctx context.Context, hash string, fileIndex int, downloadKey string, yifyID int, label, language string) (database.Subtitle, error) {
	if !ValidLanguage(language) {
		return database.Subtitle{}, errors.New("invalid language")
	}
	switch {
	case strings.HasPrefix(downloadKey, "subdl:"):
		return s.downloadSubDL(ctx, hash, fileIndex, downloadKey, language)
	case strings.HasPrefix(downloadKey, "opensubtitles:"):
		return s.downloadOpenSubtitles(ctx, hash, fileIndex, downloadKey, language)
	case strings.HasPrefix(downloadKey, "yify:"):
		id, _ := strconv.Atoi(strings.TrimPrefix(downloadKey, "yify:"))
		return s.downloadYIFYForFile(ctx, hash, fileIndex, id, label, language)
	default:
		if yifyID > 0 {
			return s.downloadYIFYForFile(ctx, hash, fileIndex, yifyID, label, language)
		}
		return database.Subtitle{}, errors.New("invalid subtitle download key")
	}
}

func (s *Service) downloadSubDL(ctx context.Context, hash string, fileIndex int, downloadKey, language string) (database.Subtitle, error) {
	var empty database.Subtitle
	b, _, err := s.DB.Get(ctx, "sub-item:"+downloadKey)
	if err != nil {
		return empty, errors.New("search again before selecting a subtitle")
	}
	var item cachedSubDLItem
	if json.Unmarshal(b, &item) != nil || !strings.HasPrefix(item.URL, "/") {
		return empty, errors.New("invalid cached SubDL subtitle")
	}
	dlURL := strings.TrimRight(subdlDownloadBase, "/") + item.URL
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, dlURL, nil)
	if err != nil {
		return empty, err
	}
	req.Header.Set("User-Agent", "Bigflix/1.0")
	resp, err := s.apiClient().Do(req)
	if err != nil {
		return empty, errors.New("SubDL download unreachable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return empty, fmt.Errorf("SubDL download returned HTTP %d", resp.StatusCode)
	}
	archive, err := io.ReadAll(io.LimitReader(resp.Body, MaxSize+1))
	if err != nil || len(archive) > MaxSize {
		return empty, errors.New("subtitle archive exceeds 2 MB")
	}
	raw, err := unpack(archive)
	if err != nil {
		return empty, err
	}
	return SaveForFile(ctx, s.DB, hash, fileIndex, item.Label, language, raw)
}

func (s *Service) downloadOpenSubtitles(ctx context.Context, hash string, fileIndex int, downloadKey, language string) (database.Subtitle, error) {
	var empty database.Subtitle
	cfg := s.DB.LoadSubtitleProviderConfig(ctx)
	if strings.TrimSpace(cfg.OpenSubtitlesAPIKey) == "" {
		return empty, errors.New("OpenSubtitles API key not configured")
	}
	b, _, err := s.DB.Get(ctx, "sub-item:"+downloadKey)
	if err != nil {
		return empty, errors.New("search again before selecting a subtitle")
	}
	var item cachedOSItem
	if json.Unmarshal(b, &item) != nil || item.FileID <= 0 {
		return empty, errors.New("invalid cached OpenSubtitles entry")
	}
	reqBody, _ := json.Marshal(map[string]int{"file_id": item.FileID})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(openSubtitlesAPIBase, "/")+"/download", bytes.NewReader(reqBody))
	if err != nil {
		return empty, err
	}
	req.Header.Set("Api-Key", strings.TrimSpace(cfg.OpenSubtitlesAPIKey))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Bigflix/1.0")
	resp, err := s.apiClient().Do(req)
	if err != nil {
		return empty, errors.New("OpenSubtitles download failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return empty, fmt.Errorf("OpenSubtitles download returned HTTP %d", resp.StatusCode)
	}
	var linkResp struct {
		Link string `json:"link"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&linkResp); err != nil || linkResp.Link == "" {
		return empty, errors.New("OpenSubtitles did not return a download link")
	}
	dlReq, err := http.NewRequestWithContext(ctx, http.MethodGet, linkResp.Link, nil)
	if err != nil {
		return empty, err
	}
	dlReq.Header.Set("User-Agent", "Bigflix/1.0")
	dlResp, err := s.apiClient().Do(dlReq)
	if err != nil {
		return empty, errors.New("failed fetching subtitle file")
	}
	defer dlResp.Body.Close()
	if dlResp.StatusCode != http.StatusOK {
		return empty, fmt.Errorf("subtitle file returned HTTP %d", dlResp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(dlResp.Body, MaxSize+1))
	if err != nil || len(raw) > MaxSize {
		return empty, errors.New("subtitle exceeds 2 MB")
	}
	return SaveForFile(ctx, s.DB, hash, fileIndex, item.Label, language, raw)
}

func (s *Service) downloadYIFYForFile(ctx context.Context, hash string, fileIndex, id int, label, language string) (database.Subtitle, error) {
	sub, err := s.Download(ctx, hash, id, label, language)
	if err != nil || fileIndex < 0 {
		return sub, err
	}
	// Copy to the episode's fileIndex scope as well
	if vtt, vErr := s.DB.Subtitle(ctx, hash, sub.ID); vErr == nil {
		_ = s.DB.SaveSubtitleForFile(ctx, hash, fileIndex, sub, vtt)
	}
	return sub, nil
}
