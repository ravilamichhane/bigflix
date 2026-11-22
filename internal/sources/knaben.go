package sources

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func (a *Aggregator) searchKnaben(ctx context.Context, base string, q Query) ([]RawTorrent, error) {
	title := CleanSearchTitle(q.Title)
	if title == "" {
		return nil, nil
	}
	var queries []string
	var categories []int
	anime := isAnimeQuery(q)
	if q.MediaType == "episode" {
		// Search both the exact episode and season pack
		queries = append(queries, fmt.Sprintf("%s S%02dE%02d", title, q.Season, q.Episode))
		queries = append(queries, fmt.Sprintf("%s S%02d", title, q.Season))
		categories = []int{2000000} // TV (includes HD, SD, UHD, HDTV, Boxsets)
		if anime {
			categories = append(categories, 6001000, 6002000) // Anime Subbed/Dubbed (excludes Manga/Literature)
		}
	} else {
		if q.Year > 0 {
			queries = append(queries, fmt.Sprintf("%s %d", title, q.Year))
		} else {
			queries = append(queries, title)
		}
		categories = []int{3000000} // Movies
		if anime {
			categories = append(categories, 6001000, 6002000)
		}
	}

	var out []RawTorrent
	var lastErr error
	for _, term := range queries {
		body, _ := json.Marshal(map[string]any{
			"query":       term,
			"size":        30,
			"order_by":    "seeders",
			"order_direction": "desc",
			"hide_unsafe": true,
			"hide_xxx":    true,
			"categories":  categories,
		})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/"), bytes.NewReader(body))
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "Bigflix/1.0")
		resp, err := a.publicClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		b, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20+1))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || readErr != nil || len(b) > 4<<20 {
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			continue
		}
		var payload struct {
			Hits []struct {
				Title      string `json:"title"`
				Hash       string `json:"hash"`
				MagnetURL  string `json:"magnetUrl"`
				Bytes      int64  `json:"bytes"`
				Seeders    int    `json:"seeders"`
				Peers      int    `json:"peers"`
				CachedOrigin string `json:"cachedOrigin"`
			} `json:"hits"`
		}
		if err := json.Unmarshal(b, &payload); err != nil {
			lastErr = err
			continue
		}
		for _, h := range payload.Hits {
			hash := ExtractHash(h.Hash)
			if hash == "" {
				hash = ExtractHash(h.MagnetURL)
			}
			if hash == "" {
				continue
			}
			prov := "knaben"
			if h.CachedOrigin != "" {
				prov = "knaben/" + strings.ToLower(strings.TrimSpace(h.CachedOrigin))
			}
			out = append(out, RawTorrent{
				Hash:      hash,
				Title:     strings.TrimSpace(h.Title),
				Provider:  prov,
				SizeBytes: h.Bytes,
				Seeds:     h.Seeders,
				Peers:     h.Peers,
			})
		}
	}
	if len(out) == 0 && lastErr != nil {
		return nil, lastErr
	}
	return out, nil
}
