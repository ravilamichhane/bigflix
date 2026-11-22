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

type EpisodePickResult struct {
	Found     bool   `json:"found"`
	FileIndex int    `json:"file_index"`
	Reason    string `json:"reason"`
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
		"chat_template_kwargs": map[string]any{
			"enable_thinking": false,
		},
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
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(rawResp, &completion); err != nil || len(completion.Choices) == 0 {
		return nil, errors.New("AI server returned an unexpected response")
	}
	choice := completion.Choices[0]
	if extracted, ok := extractJSONObject(choice.Message.Content); ok {
		return []byte(extracted), nil
	}
	// Some llama.cpp builds emit schema output into reasoning_content when
	// thinking templates interact with json_schema; recover valid JSON if present.
	if extracted, ok := extractJSONObject(choice.Message.ReasoningContent); ok {
		return []byte(extracted), nil
	}
	preview := strings.TrimSpace(choice.Message.Content)
	if preview == "" {
		preview = strings.TrimSpace(choice.Message.ReasoningContent)
	}
	if len(preview) > 140 {
		preview = preview[:140] + "…"
	}
	return nil, fmt.Errorf("AI server returned non-JSON output (finish_reason=%s, preview=%q)", choice.FinishReason, preview)
}

func extractJSONObject(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", false
	}
	// Strip <think>...</think> blocks (or unclosed <think> prefix) if emitted by Qwen3/3.5
	for {
		lower := strings.ToLower(s)
		start := strings.Index(lower, "<think>")
		if start < 0 {
			break
		}
		end := strings.Index(lower[start:], "</think>")
		if end >= 0 {
			s = strings.TrimSpace(s[:start] + s[start+end+len("</think>"):])
		} else {
			s = strings.TrimSpace(s[:start])
			break
		}
	}
	// Strip markdown fences if an OpenAI-compatible server wrapped JSON
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```JSON")
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSuffix(s, "```")
		s = strings.TrimSpace(s)
	}
	if json.Valid([]byte(s)) {
		return s, true
	}
	// Recover the outermost JSON object if surrounded by brief prose
	first := strings.IndexByte(s, '{')
	last := strings.LastIndexByte(s, '}')
	if first >= 0 && last > first {
		candidate := strings.TrimSpace(s[first : last+1])
		if json.Valid([]byte(candidate)) {
			return candidate, true
		}
	}
	return "", false
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
Reject ONLY clear wrong titles (e.g. target only in release group), samples/extras/manga, wrong season/episode, or conflicting single-language dubs. Keep note <= 10 words. Return JSON only.`

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

	raw, err := c.callJSONSchema(ctx, cfg, systemPrompt, sb.String(), "torrent_ranking", schema, 320)
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

// PickEpisodeFile asks the local CPU model to inspect a season pack's video
// files and pick the exact file_index matching the requested TV episode
// (using series title, season, episode number, and TMDB episode title).
// Because it returns a single compact JSON object (~30 tokens), it runs fast
// even on 0.8B CPU models and never truncates on large packs.
func (c *Classifier) PickEpisodeFile(ctx context.Context, seriesTitle string, season, episode int, episodeTitle string, files []FileEntry) (EpisodePickResult, error) {
	if len(files) == 0 {
		return EpisodePickResult{}, errors.New("no video files provided")
	}
	cfg := c.loadConfig(ctx)
	if !cfg.Enabled || strings.TrimSpace(cfg.APIURL) == "" {
		return EpisodePickResult{}, errors.New("AI classifier not configured")
	}
	release, ok := c.acquire(ctx, 6*time.Second)
	if !ok {
		return EpisodePickResult{}, errors.New("AI classifier busy")
	}
	defer release()

	limit := min(40, len(files))
	var sb strings.Builder
	if strings.TrimSpace(episodeTitle) != "" {
		fmt.Fprintf(&sb, "Target TV Episode: %q S%02dE%02d (%q)\nFiles:\n", seriesTitle, season, episode, episodeTitle)
	} else {
		fmt.Fprintf(&sb, "Target TV Episode: %q S%02dE%02d\nFiles:\n", seriesTitle, season, episode)
	}
	validIdx := map[int]bool{}
	for i := 0; i < limit; i++ {
		f := files[i]
		validIdx[f.Index] = true
		fmt.Fprintf(&sb, "[%d] %s %q\n", f.Index, ranker.FormatBytes(f.Size), f.Path)
	}

	systemPrompt := `Pick the exact file_index in the list matching the target TV series season, episode number, and episode title.
Rules:
- Check folder names (e.g. "Season 2") AND filenames (S02E01, 2x01, or absolute anime numbering matching the episode title).
- Never pick samples, trailers, extras, bonus features, openings/endings (NCOP/NCED), or OVAs.
- If the target episode is NOT in the list (e.g. wrong season pack), return {"found":false,"file_index":-1,"reason":"wrong season or missing"}.
- Keep reason <= 10 words. Return JSON only.`

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"found":      map[string]any{"type": "boolean"},
			"file_index": map[string]any{"type": "integer"},
			"reason":     map[string]any{"type": "string"},
		},
		"required":             []string{"found", "file_index", "reason"},
		"additionalProperties": false,
	}

	raw, err := c.callJSONSchema(ctx, cfg, systemPrompt, sb.String(), "pick_episode_file", schema, 160)
	if err != nil {
		return EpisodePickResult{}, err
	}
	var out EpisodePickResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return EpisodePickResult{}, err
	}
	if out.Found {
		if !validIdx[out.FileIndex] {
			return EpisodePickResult{}, fmt.Errorf("AI returned invalid file_index %d", out.FileIndex)
		}
	} else {
		out.FileIndex = -1
	}
	out.Reason = strings.TrimSpace(out.Reason)
	return out, nil
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
		model = "Qwen3.5-0.8B"
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
