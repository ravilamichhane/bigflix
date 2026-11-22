package sources

import (
	"bigflix/internal/database"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestAggregatorAllIndexersDedupAndPartialFailure(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	h1 := strings.Repeat("1", 40)
	h2 := strings.Repeat("2", 40)
	h3 := strings.Repeat("3", 40)
	h4 := strings.Repeat("4", 40)
	h5 := strings.Repeat("5", 40)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/eztv"):
			fmt.Fprintf(w, `{"torrents_count":2,"torrents":[
				{"hash":"%s","title":"Severance.S01E02.1080p.WEB.h264-EZTV","season":"1","episode":"2","seeds":45,"peers":10,"size_bytes":"1500000000"},
				{"hash":"%s","title":"Severance.S01E09.1080p.WEB.h264","season":"1","episode":"9","seeds":80,"peers":20,"size_bytes":"1500000000"}
			]}`, h1, strings.Repeat("9", 40))
		case strings.HasPrefix(r.URL.Path, "/knaben"):
			fmt.Fprintf(w, `{"hits":[
				{"title":"Severance.S01E02.1080p.WEB.h264-EZTV","hash":"%s","bytes":1500000000,"seeders":60,"peers":12,"cachedOrigin":"1337x"},
				{"title":"Severance S01 Complete 1080p WEB-DL x264","hash":"%s","bytes":12000000000,"seeders":120,"peers":30,"cachedOrigin":"TPB"}
			]}`, strings.ToUpper(h1), h2)
		case strings.HasPrefix(r.URL.Path, "/apibay"):
			fmt.Fprintf(w, `[{"id":"100","name":"Severance.S01E02.720p.WEB.x264","info_hash":"%s","seeders":"25","leechers":"5","size":"800000000"}]`, h3)
		case strings.HasPrefix(r.URL.Path, "/nyaa"):
			fmt.Fprintf(w, `<?xml version="1.0"?><rss xmlns:nyaa="https://nyaa.si/xmlns/nyaa"><channel><item><title>[Sub] Severance S01E02 1080p</title><nyaa:infoHash>%s</nyaa:infoHash><nyaa:seeders>35</nyaa:seeders><nyaa:leechers>4</nyaa:leechers><nyaa:size>1.4 GiB</nyaa:size></item></channel></rss>`, h4)
		case strings.HasPrefix(r.URL.Path, "/torznab"):
			fmt.Fprintf(w, `<?xml version="1.0"?><rss xmlns:torznab="http://torznab.com/schemas/2015/feed"><channel><item><title>Severance S01E02 2160p WEB-DL</title><size>4200000000</size><torznab:attr name="infohash" value="%s"/><torznab:attr name="seeders" value="18"/><torznab:attr name="peers" value="3"/></item></channel></rss>`, h5)
		case strings.HasPrefix(r.URL.Path, "/yts"):
			http.Error(w, "down", 503)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cfg := database.SourcesConfig{
		Indexers: []database.IndexerConfig{
			{ID: "yts", Name: "YTS", Enabled: true, BaseURL: srv.URL + "/yts/"},
			{ID: "eztv", Name: "EZTV", Enabled: true, BaseURL: srv.URL + "/eztv"},
			{ID: "knaben", Name: "Knaben", Enabled: true, BaseURL: srv.URL + "/knaben"},
			{ID: "apibay", Name: "APIBay", Enabled: true, BaseURL: srv.URL + "/apibay"},
			{ID: "nyaa", Name: "Nyaa", Enabled: true, BaseURL: srv.URL + "/nyaa"},
		},
		Torznab: database.TorznabConfig{Enabled: true, URL: srv.URL + "/torznab", APIKey: "secret"},
	}
	if err := db.PutConfig(context.Background(), "sources", cfg); err != nil {
		t.Fatal(err)
	}

	agg := NewAggregator(db)
	agg.SetClients(srv.Client(), srv.Client())

	report, err := agg.Search(context.Background(), Query{
		MediaType:        "episode",
		TMDBID:           95396,
		IMDbID:           "tt11280740",
		Title:            "Severance",
		OriginalLanguage: "ja", // triggers nyaa too
		Season:           1,
		Episode:          2,
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 5 {
		t.Fatalf("expected 5 deduplicated torrents (h1..h5), got %d: %+v", len(report.Results), report.Results)
	}
	// Verify h1 deduplicated across EZTV (45 seeds) and Knaben (60 seeds) keeping max seeds.
	for _, r := range report.Results {
		if r.Hash == h1 && r.Seeds != 60 {
			t.Fatalf("expected merged max seeds=60 for h1, got %d", r.Seeds)
		}
		if r.Hash == h4 && r.SizeBytes == 0 {
			t.Fatalf("expected parsed GiB size for nyaa item, got 0")
		}
	}
}

func TestExtractHashAndSizeParsing(t *testing.T) {
	hexH := "0123456789abcdef0123456789abcdef01234567"
	if got := ExtractHash("magnet:?xt=urn:btih:" + strings.ToUpper(hexH) + "&dn=test"); got != hexH {
		t.Fatalf("hex magnet extract failed: %q", got)
	}
	// Base32 encoded 20-byte hash
	b32 := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	if got := ExtractHash("magnet:?xt=urn:btih:" + b32); len(got) != 40 || got != strings.Repeat("0", 40) {
		t.Fatalf("base32 magnet extract failed: %q", got)
	}
	if got := ParseSizeBytes("1.5 GiB"); got < 1_500_000_000 || got > 1_700_000_000 {
		t.Fatalf("ParseSizeBytes(1.5 GiB) = %d", got)
	}
}
