package sources

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

func (a *Aggregator) searchNyaa(ctx context.Context, base string, q Query) ([]RawTorrent, error) {
	title := CleanSearchTitle(q.Title)
	if title == "" {
		return nil, nil
	}
	base = strings.TrimRight(base, "/")
	var queries []string
	if q.MediaType == "episode" {
		queries = append(queries, fmt.Sprintf("%s S%02dE%02d", title, q.Season, q.Episode))
		queries = append(queries, fmt.Sprintf("%s %02d", title, q.Episode))
		if q.OriginalTitle != "" && !strings.EqualFold(q.OriginalTitle, q.Title) {
			if orig := CleanSearchTitle(q.OriginalTitle); orig != "" {
				queries = append(queries, fmt.Sprintf("%s %02d", orig, q.Episode))
			}
		}
	} else {
		queries = append(queries, title)
	}

	var out []RawTorrent
	var lastErr error
	for _, term := range queries {
		v := url.Values{
			"page": {"rss"},
			"c":    {"1_2"}, // Anime - English-translated
			"f":    {"0"},
			"s":    {"seeders"},
			"o":    {"desc"},
			"q":    {term},
		}
		b, err := doBoundedGet(ctx, a.publicClient, base+"/?"+v.Encode(), 4<<20)
		if err != nil {
			lastErr = err
			continue
		}
		var rss struct {
			Items []struct {
				Title    string `xml:"title"`
				InfoHash string `xml:"infoHash"`
				Seeders  string `xml:"seeders"`
				Leechers string `xml:"leechers"`
				Size     string `xml:"size"`
			} `xml:"channel>item"`
		}
		if err := xml.Unmarshal(b, &rss); err != nil {
			lastErr = err
			continue
		}
		for _, item := range rss.Items {
			hash := ExtractHash(item.InfoHash)
			if hash == "" {
				continue
			}
			seeds, _ := strconv.Atoi(strings.TrimSpace(item.Seeders))
			peers, _ := strconv.Atoi(strings.TrimSpace(item.Leechers))
			out = append(out, RawTorrent{
				Hash:      hash,
				Title:     strings.TrimSpace(item.Title),
				Provider:  "nyaa",
				SizeBytes: ParseSizeBytes(item.Size),
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
