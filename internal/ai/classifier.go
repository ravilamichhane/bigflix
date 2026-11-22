package ai

import (
	"bigflix/internal/database"
	"bigflix/internal/ranker"
	"bigflix/internal/sources"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Classifier struct {
	store  *database.Store
	client *http.Client
	slots  chan struct{} // concurrency 1 on 4 vCPU VPS so AI never stacks against FFmpeg
}

type FileEntry struct {
	Index int    `json:"index"`
	Path  string `json:"path"`
	Size  int64  `json:"size"`
}

type FileEpisodeMapping struct {
	FileIndex int `json:"file_index"`
	Season    int `json:"season"`
	Episode   int `json:"episode"`
}

func NewClassifier(store *database.Store) *Classifier {
	return &Classifier{
		store:  store,
		client: sources.LocalClient(65 * time.Second),
		slots:  make(chan struct{}, 1),
	}
}

func (c *Classifier) SetClient(cl *http.Client) {
	if cl != nil {
		c.client = cl
	}
}

func (c *Classifier) loadConfig(ctx context.Context) database.AIConfig {
	if c.store == nil {
		return database.DefaultAIConfig()
	}
	return c.store.LoadAIConfig(ctx)
}

func (c *Classifier) acquire(ctx context.Context, wait time.Duration) (func(), bool) {
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case c.slots <- struct{}{}:
		return func() { <-c.slots }, true
	case <-t.C:
		return nil, false
	case <-ctx.Done():
		return nil, false
	}
}

func chatEndpoint(apiURL string) (string, error) {
	clean := strings.TrimRight(strings.TrimSpace(apiURL), "/")
	u, err := url.Parse(clean)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return "", errors.New("AI API URL must be a valid http or https URL")
	}
	if strings.HasSuffix(clean, "/chat/completions") {
		return clean, nil
	}
	return clean + "/chat/completions", nil
}

func (c *Classifier) callJSONSchema(ctx context.Context, cfg database.AIConfig, systemPrompt, userPrompt, schemaName string, schema map[string]any, maxTokens int) ([]byte, error) {
	endpoint, err := chatEndpoint(cfg.APIURL)
	if err != nil {
		return nil, err
	}
	timeout := time.Duration(cfg.TimeoutSec) * time.Second
	if timeout < 2*time.Second {
		timeout = 25 * time.Second
	}
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	body := map[string]any{
		"model":        cfg.Model,
		"temperature":  0.0,
		"max_tokens":   maxTokens,
		"cache_prompt": true,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
		"response_format": map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   schemaName,
				"strict": true,
				"schema": schema,
			},
		},
	}
	rawBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(rctx, http.MethodPost, endpoint, bytes.NewReader(rawBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if strings.TrimSpace(cfg.APIKey) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(cfg.APIKey))
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("AI server HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(errBody)))
	}
	rawResp, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var completion struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(rawResp, &completion); err != nil || len(completion.Choices) == 0 {
		return nil, errors.New("AI server returned an unexpected response")
	}
	content := strings.TrimSpace(completion.Choices[0].Message.Content)
	// Strip markdown fences if an OpenAI-compatible server wrapped JSON
	if strings.HasPrefix(content, "```") {
		content = strings.TrimPrefix(content, "```json")
		content = strings.TrimPrefix(content, "```")
		content = strings.TrimSuffix(content, "```")
		content = strings.TrimSpace(content)
	}
	if !json.Valid([]byte(content)) {
		return nil, errors.New("AI server returned non-JSON output")
	}
	return []byte(content), nil
}

// RankCandidates runs the local CPU AI classifier over the top <=12 pre-filtered
// candidates. Returns (rankedCandidates, "ai"|"deterministic", aiNote).
// Always degrades gracefully to the deterministic order if AI is disabled, busy, or fails.
func (c *Classifier) RankCandidates(ctx context.Context, q sources.Query, prefs database.PlaybackPrefs, candidates []ranker.Candidate) ([]ranker.Candidate, string, string) {
	if len(candidates) == 0 {
		return candidates, "deterministic", ""
	}
	cfg := c.loadConfig(ctx)
	if !cfg.Enabled || strings.TrimSpace(cfg.APIURL) == "" {
		return candidates, "deterministic", ""
	}

	release, ok := c.acquire(ctx, 3*time.Second)
	if !ok {
		slog.Info("local AI busy; falling back to deterministic ranker", "title", q.Title)
		return candidates, "deterministic", "AI classifier busy — used fast deterministic ranking."
	}
	defer release()

	topN := min(8, len(candidates))
	var sb strings.Builder
	if q.MediaType == "episode" {
		fmt.Fprintf(&sb, "Target TV: %q (orig: %q, lang: %s) S%02dE%02d\n",
			q.Title, q.OriginalTitle, q.OriginalLanguage, q.Season, q.Episode)
	} else {
		fmt.Fprintf(&sb, "Target Movie: %q (orig: %q, year: %d, lang: %s)\n",
			q.Title, q.OriginalTitle, q.Year, q.OriginalLanguage)
	}
	fmt.Fprintf(&sb, "Prefs: quality=%s h264=%v lang=%s\nCandidates:\n",
		prefs.TargetQuality, prefs.PreferDirectPlay, prefs.PreferredLanguage)
	for i := 0; i < topN; i++ {
		cand := candidates[i]
		imdbVerified := (cand.Provider == "yts" || cand.Provider == "eztv") && q.IMDbID != ""
		fmt.Fprintf(&sb, "[%d] prov=%s imdb=%v seeds=%d size=%s src=%s pack=%v h264=%v %q\n",
			i, cand.Provider, imdbVerified, cand.Seeds, cand.Size, cand.SourceType, cand.IsSeasonPack, cand.BrowserFriendly, cand.Title)
	}

	systemPrompt := `Rank pre-filtered torrent IDs best-to-worst in ranked_ids for a 4-vCPU streaming server.
Prefer matching quality, healthy seeds, smaller size at equal quality, and h264=true when h264 pref is true.
Never reject imdb=true or matching titles for x265/2160p/720p/HDRip/HDTV/missing lang — just rank lower. Match non-Latin orig against romanized target title.
Reject ONLY clear wrong titles (e.g. target only in release group), samples/extras/manga, wrong season/episode, or conflicting single-language dubs. Return JSON only.`

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"ranked_ids": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "integer"},
			},
			"rejected": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":     map[string]any{"type": "integer"},
						"reason": map[string]any{"type": "string"},
					},
					"required":             []string{"id", "reason"},
					"additionalProperties": false,
				},
			},
			"season_pack_ids": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "integer"},
			},
			"note": map[string]any{"type": "string"},
		},
		"required":             []string{"ranked_ids", "rejected", "season_pack_ids", "note"},
		"additionalProperties": false,
	}

	raw, err := c.callJSONSchema(ctx, cfg, systemPrompt, sb.String(), "torrent_ranking", schema, 180)
	if err != nil {
		slog.Warn("AI ranking failed; falling back to deterministic", "error", err)
		return candidates, "deterministic", ""
	}

	var parsed struct {
		RankedIDs []int `json:"ranked_ids"`
		Rejected  []struct {
			ID     int    `json:"id"`
			Reason string `json:"reason"`
		} `json:"rejected"`
		SeasonPackIDs []int  `json:"season_pack_ids"`
		Note          string `json:"note"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return candidates, "deterministic", ""
	}

	packSet := map[int]bool{}
	for _, id := range parsed.SeasonPackIDs {
		if id >= 0 && id < topN {
			packSet[id] = true
		}
	}
	rejectedSet := map[int]bool{}
	for _, rej := range parsed.Rejected {
		if rej.ID >= 0 && rej.ID < topN {
			// Guard against small local 1.5B models hallucinating rejections on
			// IMDb-verified (YTS/EZTV) or exact title+year matches.
			if ranker.IsVerifiedMatch(q, candidates[rej.ID], prefs) {
				slog.Info("ignoring AI rejection of verified match", "title", q.Title, "candidate", candidates[rej.ID].Title, "reason", rej.Reason)
				continue
			}
			rejectedSet[rej.ID] = true
		}
	}

	seen := map[int]bool{}
	var reordered []ranker.Candidate
	rank := 1
	for _, id := range parsed.RankedIDs {
		if id < 0 || id >= topN || seen[id] || rejectedSet[id] {
			continue
		}
		seen[id] = true
		cand := candidates[id]
		cand.AIRank = rank
		if packSet[id] {
			cand.IsSeasonPack = true
		}
		reordered = append(reordered, cand)
		rank++
	}
	if len(reordered) == 0 {
		// If the AI explicitly rejected the candidates as wrong titles/mismatches,
		// honor that rejection rather than serving wrong movies!
		if len(rejectedSet) > 0 {
			var remaining []ranker.Candidate
			for i := 0; i < topN; i++ {
				if !rejectedSet[i] {
					remaining = append(remaining, candidates[i])
				}
			}
			note := strings.TrimSpace(parsed.Note)
			if note == "" {
				note = "AI rejected non-matching releases."
			}
			return remaining, "ai", note
		}
		return candidates, "deterministic", ""
	}
	// Append non-rejected topN leftovers + remaining tail candidates as extra backups.
	for i := 0; i < topN; i++ {
		if !seen[i] && !rejectedSet[i] {
			reordered = append(reordered, candidates[i])
		}
	}
	if len(candidates) > topN {
		reordered = append(reordered, candidates[topN:]...)
	}
	return reordered, "ai", strings.TrimSpace(parsed.Note)
}

// MapSeasonPackFiles asks the local CPU model to map ambiguous torrent file paths
// to (season, episode) numbers. Only called when regex filename parsing fails.
func (c *Classifier) MapSeasonPackFiles(ctx context.Context, seriesTitle string, targetSeason int, files []FileEntry) ([]FileEpisodeMapping, error) {
	cfg := c.loadConfig(ctx)
	if !cfg.Enabled || strings.TrimSpace(cfg.APIURL) == "" {
		return nil, errors.New("AI classifier not configured")
	}
	release, ok := c.acquire(ctx, 4*time.Second)
	if !ok {
		return nil, errors.New("AI classifier busy")
	}
	defer release()

	limit := min(60, len(files))
	var sb strings.Builder
	fmt.Fprintf(&sb, "TV Series: %q, Target Season: %d\nVideo files inside torrent:\n", seriesTitle, targetSeason)
	validIdx := map[int]bool{}
	for i := 0; i < limit; i++ {
		f := files[i]
		validIdx[f.Index] = true
		fmt.Fprintf(&sb, "file_index=%d size=%s path=%q\n", f.Index, ranker.FormatBytes(f.Size), f.Path)
	}

	systemPrompt := `Map each TV episode video file to its integer season and episode number. Ignore samples, trailers, and extras (omit them). Respond strictly as JSON.`
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"mappings": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"file_index": map[string]any{"type": "integer"},
						"season":     map[string]any{"type": "integer"},
						"episode":    map[string]any{"type": "integer"},
					},
					"required":             []string{"file_index", "season", "episode"},
					"additionalProperties": false,
				},
			},
		},
		"required":             []string{"mappings"},
		"additionalProperties": false,
	}

	raw, err := c.callJSONSchema(ctx, cfg, systemPrompt, sb.String(), "season_pack_map", schema, 512)
	if err != nil {
		return nil, err
	}
	var out struct {
		Mappings []FileEpisodeMapping `json:"mappings"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	var clean []FileEpisodeMapping
	for _, m := range out.Mappings {
		if validIdx[m.FileIndex] && m.Episode > 0 {
			if m.Season <= 0 {
				m.Season = targetSeason
			}
			clean = append(clean, m)
		}
	}
	return clean, nil
}

func (c *Classifier) TestConnection(ctx context.Context, apiURL, model, apiKey string) (int, string, error) {
	if strings.TrimSpace(apiURL) == "" {
		return 0, "", errors.New("enter an AI API URL (e.g. http://llama-server:8080/v1)")
	}
	if strings.TrimSpace(model) == "" {
		model = "Qwen3.5-2B"
	}
	cfg := database.AIConfig{
		Enabled:    true,
		APIURL:     apiURL,
		Model:      model,
		APIKey:     apiKey,
		TimeoutSec: 30,
	}
	start := time.Now()
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"ok": map[string]any{"type": "boolean"},
		},
		"required":             []string{"ok"},
		"additionalProperties": false,
	}
	raw, err := c.callJSONSchema(ctx, cfg, "Return {\"ok\":true}.", "ping", "ping", schema, 32)
	latency := int(time.Since(start).Milliseconds())
	if err != nil {
		return latency, "", err
	}
	return latency, fmt.Sprintf("Connected to %s (%dms, JSON schema verified: %s)", model, latency, string(raw)), nil
}
