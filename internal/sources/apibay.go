package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

func (a *Aggregator) searchAPIBay(ctx context.Context, base string, q Query) ([]RawTorrent, error) {
	title := CleanSearchTitle(q.Title)
	if title == "" {
		return nil, nil
	}
	base = strings.TrimRight(base, "/")
	var queries []string
	cat := "200" // Video
	if q.MediaType == "episode" {
		queries = append(queries, fmt.Sprintf("%s S%02dE%02d", title, q.Season, q.Episode))
		queries = append(queries, fmt.Sprintf("%s S%02d", title, q.Season))
	} else {
		if q.Year > 0 {
			queries = append(queries, fmt.Sprintf("%s %d", title, q.Year))
		} else {
			queries = append(queries, title)
		}
	}

	var out []RawTorrent
	var lastErr error
	for _, term := range queries {
		v := url.Values{"q": {term}, "cat": {cat}}
		b, err := doBoundedGet(ctx, a.publicClient, base+"/q.php?"+v.Encode(), 4<<20)
		if err != nil {
			lastErr = err
			continue
		}
		var rows []struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			InfoHash string `json:"info_hash"`
			Leechers string `json:"leechers"`
			Seeders  string `json:"seeders"`
			Size     string `json:"size"`
		}
		if err := json.Unmarshal(b, &rows); err != nil {
			lastErr = err
			continue
		}
		for _, r := range rows {
			if r.ID == "" || r.ID == "0" {
				continue
			}
			hash := ExtractHash(r.InfoHash)
			if hash == "" {
				continue
			}
			seeds, _ := strconv.Atoi(r.Seeders)
			peers, _ := strconv.Atoi(r.Leechers)
			size, _ := strconv.ParseInt(r.Size, 10, 64)
			out = append(out, RawTorrent{
				Hash:      hash,
				Title:     strings.TrimSpace(r.Name),
				Provider:  "apibay",
				SizeBytes: size,
				Seeds:     seeds,
				Peers:     peers,
			})
		}
	}
	if len(out) == 0 && lastErr != nil {
		return nil, lastErr
	}
	return out, nil
}
