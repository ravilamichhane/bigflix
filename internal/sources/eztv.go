package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

func (a *Aggregator) searchEZTV(ctx context.Context, base string, q Query) ([]RawTorrent, error) {
	imdbDigits := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(q.IMDbID)), "tt")
	if imdbDigits == "" {
		return nil, nil
	}
	var out []RawTorrent
	for page := 1; page <= 3; page++ {
		u, err := url.Parse(base)
		if err != nil {
			return nil, err
		}
		v := u.Query()
		v.Set("imdb_id", imdbDigits)
		v.Set("limit", "100")
		v.Set("page", strconv.Itoa(page))
		u.RawQuery = v.Encode()

		b, err := doBoundedGet(ctx, a.publicClient, u.String(), 4<<20)
		if err != nil {
			if len(out) > 0 {
				return out, nil
			}
			return nil, err
		}
		var resp struct {
			TorrentsCount int `json:"torrents_count"`
			Torrents      []struct {
				Hash      string      `json:"hash"`
				MagnetURL string      `json:"magnet_url"`
				Title     string      `json:"title"`
				Filename  string      `json:"filename"`
				Season    json.Number `json:"season"`
				Episode   json.Number `json:"episode"`
				Seeds     int         `json:"seeds"`
				Peers     int         `json:"peers"`
				SizeBytes json.Number `json:"size_bytes"`
			} `json:"torrents"`
		}
		if err := json.Unmarshal(b, &resp); err != nil {
			return out, fmt.Errorf("invalid EZTV payload")
		}
		if len(resp.Torrents) == 0 {
			break
		}
		for _, item := range resp.Torrents {
			season, _ := strconv.Atoi(item.Season.String())
			episode, _ := strconv.Atoi(item.Episode.String())
			isPack := season == q.Season && episode == 0
			if season != q.Season || (episode != q.Episode && !isPack) {
				continue
			}
			hash := ExtractHash(item.Hash)
			if hash == "" {
				hash = ExtractHash(item.MagnetURL)
			}
			size, _ := strconv.ParseInt(item.SizeBytes.String(), 10, 64)
			title := strings.TrimSpace(item.Title)
			if title == "" {
				title = strings.TrimSpace(item.Filename)
			}
			out = append(out, RawTorrent{
				Hash:         hash,
				Title:        title,
				Provider:     "eztv",
				SizeBytes:    size,
				Seeds:        item.Seeds,
				Peers:        item.Peers,
				Season:       season,
				Episode:      episode,
				IsSeasonPack: isPack,
			})
		}
		if page*100 >= resp.TorrentsCount || len(resp.Torrents) < 100 {
			break
		}
	}
	return out, nil
}
