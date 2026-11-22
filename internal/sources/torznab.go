package sources

import (
	"bigflix/internal/database"
	"context"
	"encoding/xml"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

func (a *Aggregator) searchTorznab(ctx context.Context, cfg database.TorznabConfig, q Query) ([]RawTorrent, error) {
	u, err := url.Parse(strings.TrimSpace(cfg.URL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("invalid Torznab URL")
	}
	v := u.Query()
	if cfg.APIKey != "" {
		v.Set("apikey", cfg.APIKey)
	}
	if q.MediaType == "episode" {
		v.Set("t", "tvsearch")
		if q.IMDbID != "" {
			v.Set("imdbid", q.IMDbID)
		} else {
			v.Set("q", CleanSearchTitle(q.Title))
		}
		if q.Season > 0 {
			v.Set("season", strconv.Itoa(q.Season))
		}
		if q.Episode > 0 {
			v.Set("ep", strconv.Itoa(q.Episode))
		}
	} else {
		v.Set("t", "movie")
		if q.IMDbID != "" {
			v.Set("imdbid", q.IMDbID)
		} else {
			v.Set("q", CleanSearchTitle(q.Title))
		}
	}
	u.RawQuery = v.Encode()

	b, err := doBoundedGet(ctx, a.localClient, u.String(), 4<<20)
	if err != nil {
		return nil, err
	}
	var feed struct {
		Items []struct {
			Title     string `xml:"title"`
			Size      int64  `xml:"size"`
			Link      string `xml:"link"`
			Enclosure struct {
				URL    string `xml:"url,attr"`
				Length int64  `xml:"length,attr"`
			} `xml:"enclosure"`
			Attrs []struct {
				Name  string `xml:"name,attr"`
				Value string `xml:"value,attr"`
			} `xml:"attr"`
		} `xml:"channel>item"`
	}
	if err := xml.Unmarshal(b, &feed); err != nil {
		return nil, fmt.Errorf("invalid Torznab XML")
	}
	var out []RawTorrent
	for _, item := range feed.Items {
		var hash string
		var seeds, peers int
		size := item.Size
		if size == 0 {
			size = item.Enclosure.Length
		}
		for _, attr := range item.Attrs {
			switch strings.ToLower(attr.Name) {
			case "infohash":
				hash = ExtractHash(attr.Value)
			case "magneturl":
				if hash == "" {
					hash = ExtractHash(attr.Value)
				}
			case "seeders":
				seeds, _ = strconv.Atoi(attr.Value)
			case "peers":
				peers, _ = strconv.Atoi(attr.Value)
			case "size":
				if size == 0 {
					size, _ = strconv.ParseInt(attr.Value, 10, 64)
				}
			}
		}
		if hash == "" {
			hash = ExtractHash(item.Link)
		}
		if hash == "" {
			hash = ExtractHash(item.Enclosure.URL)
		}
		if hash == "" {
			continue
		}
		out = append(out, RawTorrent{
			Hash:      hash,
			Title:     strings.TrimSpace(item.Title),
			Provider:  "torznab",
			SizeBytes: size,
			Seeds:     seeds,
			Peers:     peers,
		})
	}
	return out, nil
}
