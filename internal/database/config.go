package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type TMDBConfig struct {
	ReadToken string `json:"read_token"`
}

type IndexerConfig struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	BaseURL string `json:"base_url"`
}

type TorznabConfig struct {
	Enabled bool   `json:"enabled"`
	URL     string `json:"url"`
	APIKey  string `json:"api_key"`
}

type SourcesConfig struct {
	Indexers []IndexerConfig `json:"indexers"`
	Torznab  TorznabConfig   `json:"torznab"`
}

type PlaybackPrefs struct {
	TargetQuality     string `json:"target_quality"`     // "1080p" | "720p" | "2160p"
	PreferDirectPlay  bool   `json:"prefer_direct_play"` // prefer H.264/AAC to save VPS CPU
	PreferredLanguage string `json:"preferred_language"` // "original" | "en" | etc.
	AutoEvict         bool   `json:"auto_evict"`         // auto-evict oldest un-kept downloads on budget
}

type AIConfig struct {
	Enabled    bool   `json:"enabled"`
	APIURL     string `json:"api_url"`
	Model      string `json:"model"`
	APIKey     string `json:"api_key"`
	TimeoutSec int    `json:"timeout_sec"`
}

type SubtitleProviderConfig struct {
	SubDLAPIKey         string `json:"subdl_api_key"`
	OpenSubtitlesAPIKey string `json:"opensubtitles_api_key"`
}

type WatchProgress struct {
	MediaKey     string  `json:"media_key"`
	MediaType    string  `json:"media_type"` // "movie" | "episode"
	TMDBID       int     `json:"tmdb_id"`
	Season       int     `json:"season"`
	Episode      int     `json:"episode"`
	Title        string  `json:"title"`
	EpisodeTitle string  `json:"episode_title"`
	Poster       string  `json:"poster"`
	Backdrop     string  `json:"backdrop"`
	LastHash     string  `json:"last_hash"`
	LastFileIdx  int     `json:"last_file_idx"`
	Position     float64 `json:"position"`
	Duration     float64 `json:"duration"`
	Completed    bool    `json:"completed"`
	UpdatedAt    int64   `json:"updated_at"`
}

func MediaKey(mediaType string, tmdbID, season, episode int) string {
	if mediaType == "episode" || mediaType == "tv" {
		return fmt.Sprintf("tv:%d:s%de%d", tmdbID, season, episode)
	}
	return fmt.Sprintf("movie:%d", tmdbID)
}

func envOr(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func DefaultSourcesConfig() SourcesConfig {
	torznabURL := envOr("TORZNAB_URL", "")
	torznabKey := envOr("TORZNAB_API_KEY", "")
	return SourcesConfig{
		Indexers: []IndexerConfig{
			{ID: "yts", Name: "YTS", Enabled: true, BaseURL: envOr("YTS_API_URL", "https://movies-api.accel.li/api/v2/")},
			{ID: "eztv", Name: "EZTV", Enabled: true, BaseURL: envOr("EZTV_API_URL", "https://eztvx.to/api/get-torrents")},
			{ID: "knaben", Name: "Knaben", Enabled: true, BaseURL: envOr("KNABEN_API_URL", "https://api.knaben.org/v1")},
			{ID: "apibay", Name: "APIBay", Enabled: true, BaseURL: envOr("APIBAY_API_URL", "https://apibay.org")},
			{ID: "nyaa", Name: "Nyaa", Enabled: true, BaseURL: envOr("NYAA_API_URL", "https://nyaa.si")},
		},
		Torznab: TorznabConfig{
			Enabled: torznabURL != "",
			URL:     torznabURL,
			APIKey:  torznabKey,
		},
	}
}

func DefaultPlaybackPrefs() PlaybackPrefs {
	return PlaybackPrefs{
		TargetQuality:     "1080p",
		PreferDirectPlay:  true,
		PreferredLanguage: "original",
		AutoEvict:         true,
	}
}

func DefaultAIConfig() AIConfig {
	apiURL := envOr("AI_API_URL", "")
	timeout := 30
	if raw := strings.TrimSpace(os.Getenv("AI_TIMEOUT_SEC")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 2 && n <= 60 {
			timeout = n
		}
	}
	return AIConfig{
		Enabled:    apiURL != "",
		APIURL:     apiURL,
		Model:      envOr("AI_MODEL", "Qwen3.5-2B"),
		APIKey:     envOr("AI_API_KEY", ""),
		TimeoutSec: timeout,
	}
}

func DefaultSubtitleProviderConfig() SubtitleProviderConfig {
	return SubtitleProviderConfig{
		SubDLAPIKey:         envOr("SUBDL_API_KEY", ""),
		OpenSubtitlesAPIKey: envOr("OPENSUBTITLES_API_KEY", ""),
	}
}

func (s *Store) GetConfig(ctx context.Context, key string, dst any) (bool, error) {
	var b []byte
	err := s.db.QueryRowContext(ctx, "SELECT payload FROM app_config WHERE key=?", key).Scan(&b)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(b, dst); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) PutConfig(ctx context.Context, key string, val any) error {
	b, err := json.Marshal(val)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO app_config VALUES(?,?) ON CONFLICT(key) DO UPDATE SET payload=excluded.payload", key, b)
	return err
}

func (s *Store) LoadTMDBToken(ctx context.Context, fallback string) string {
	var cfg TMDBConfig
	if ok, err := s.GetConfig(ctx, "tmdb", &cfg); err == nil && ok && strings.TrimSpace(cfg.ReadToken) != "" {
		return strings.TrimSpace(cfg.ReadToken)
	}
	return strings.TrimSpace(fallback)
}

func (s *Store) LoadSourcesConfig(ctx context.Context) SourcesConfig {
	def := DefaultSourcesConfig()
	var saved SourcesConfig
	ok, err := s.GetConfig(ctx, "sources", &saved)
	if err != nil || !ok {
		return def
	}
	byID := map[string]IndexerConfig{}
	for _, ix := range saved.Indexers {
		byID[ix.ID] = ix
	}
	merged := make([]IndexerConfig, 0, len(def.Indexers))
	for _, d := range def.Indexers {
		if ov, exists := byID[d.ID]; exists {
			if strings.TrimSpace(ov.BaseURL) == "" {
				ov.BaseURL = d.BaseURL
			}
			if ov.Name == "" {
				ov.Name = d.Name
			}
			merged = append(merged, ov)
		} else {
			merged = append(merged, d)
		}
	}
	saved.Indexers = merged
	return saved
}

func (s *Store) LoadPlaybackPrefs(ctx context.Context) PlaybackPrefs {
	prefs := DefaultPlaybackPrefs()
	if ok, err := s.GetConfig(ctx, "playback", &prefs); err != nil || !ok {
		return DefaultPlaybackPrefs()
	}
	switch prefs.TargetQuality {
	case "720p", "1080p", "2160p":
	default:
		prefs.TargetQuality = "1080p"
	}
	if strings.TrimSpace(prefs.PreferredLanguage) == "" {
		prefs.PreferredLanguage = "original"
	}
	return prefs
}

func (s *Store) LoadAIConfig(ctx context.Context) AIConfig {
	cfg := DefaultAIConfig()
	if ok, err := s.GetConfig(ctx, "ai", &cfg); err != nil || !ok {
		return DefaultAIConfig()
	}
	if cfg.TimeoutSec < 2 || cfg.TimeoutSec > 60 {
		cfg.TimeoutSec = 30
	}
	if strings.TrimSpace(cfg.Model) == "" {
		cfg.Model = "Qwen3.5-2B"
	}
	return cfg
}

func (s *Store) LoadSubtitleProviderConfig(ctx context.Context) SubtitleProviderConfig {
	def := DefaultSubtitleProviderConfig()
	var saved SubtitleProviderConfig
	if ok, err := s.GetConfig(ctx, "subtitles", &saved); err != nil || !ok {
		return def
	}
	if strings.TrimSpace(saved.SubDLAPIKey) == "" {
		saved.SubDLAPIKey = def.SubDLAPIKey
	}
	if strings.TrimSpace(saved.OpenSubtitlesAPIKey) == "" {
		saved.OpenSubtitlesAPIKey = def.OpenSubtitlesAPIKey
	}
	return saved
}
