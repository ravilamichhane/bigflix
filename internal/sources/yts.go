package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

func (a *Aggregator) searchYTS(ctx context.Context, base string, q Query) ([]RawTorrent, error) {
	base = strings.TrimRight(base, "/") + "/"
	if q.IMDbID != "" {
		res, err := a.fetchYTS(ctx, base, q.IMDbID, q, true)
		if err != nil {
			return nil, err
		}
		if len(res) > 0 {
			return res, nil
		}
		// Fallback to title search if YTS hasn't indexed the IMDb code yet
		if titleTerm := CleanSearchTitle(q.Title); titleTerm != "" {
			if fallback, ferr := a.fetchYTS(ctx, base, titleTerm, q, false); ferr == nil {
				return fallback, nil
			}
		}
		return nil, nil
	}
	term := CleanSearchTitle(q.Title)
	if term == "" {
		return nil, nil
	}
	return a.fetchYTS(ctx, base, term, q, false)
}

func (a *Aggregator) fetchYTS(ctx context.Context, base, term string, q Query, imdbLookup bool) ([]RawTorrent, error) {
	v := url.Values{"query_term": {term}, "limit": {"10"}}
	b, err := doBoundedGet(ctx, a.publicClient, base+"list_movies.json?"+v.Encode(), 4<<20)
	if err != nil {
		return nil, err
	}
	var env struct {
		Status string `json:"status"`
		Data   struct {
			Movies []struct {
				Title    string `json:"title"`
				Year     int    `json:"year"`
				IMDbCode string `json:"imdb_code"`
				Torrents []struct {
					Hash    string `json:"hash"`
					Quality string `json:"quality"`
					Type    string `json:"type"`
					Codec   string `json:"video_codec"`
					Bytes   int64  `json:"size_bytes"`
					Seeds   int    `json:"seeds"`
					Peers   int    `json:"peers"`
					Audio   string `json:"audio_channels"`
				} `json:"torrents"`
			} `json:"movies"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &env); err != nil || env.Status != "ok" {
		return nil, fmt.Errorf("invalid YTS payload")
	}
	var out []RawTorrent
	for _, m := range env.Data.Movies {
		imdbMatched := q.IMDbID != "" && m.IMDbCode != "" && strings.EqualFold(m.IMDbCode, q.IMDbID)
		if q.IMDbID != "" && m.IMDbCode != "" && !imdbMatched {
			continue
		}
		if !imdbMatched && q.Year > 0 && m.Year > 0 && (m.Year < q.Year-1 || m.Year > q.Year+1) {
			continue
		}
		provider := "yts"
		if !imdbLookup && !imdbMatched {
			provider = "yts-search"
		}
		for _, t := range m.Torrents {
			title := fmt.Sprintf("%s (%d) %s %s %s YTS", m.Title, m.Year, t.Quality, t.Type, t.Codec)
			out = append(out, RawTorrent{
				Hash:       t.Hash,
				Title:      strings.Join(strings.Fields(title), " "),
				Provider:   provider,
				SizeBytes:  t.Bytes,
				Seeds:      t.Seeds,
				Peers:      t.Peers,
				Quality:    t.Quality,
				Codec:      t.Codec,
				SourceType: t.Type,
				Audio:      t.Audio,
			})
		}
	}
	return out, nil
}
