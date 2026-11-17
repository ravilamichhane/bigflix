package subtitles

import (
	"archive/zip"
	"bigflix/internal/database"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/net/html"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Service struct {
	DB     *database.Store
	Client *http.Client
	Base   string
}
type Result struct {
	ID              int    `json:"id"`
	Label           string `json:"label"`
	Language        string `json:"language"`
	Rating          int    `json:"rating"`
	Page            string `json:"page"`
	HearingImpaired bool   `json:"hearing_impaired"`
}

func New(db *database.Store) *Service {
	c := newBrowserClient()
	c.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 || r.URL.Scheme != "https" || r.URL.Host != "yifysubtitles.ch" || r.URL.User != nil {
			return errors.New("unsupported subtitle redirect")
		}
		return nil
	}
	return &Service{DB: db, Client: c, Base: "https://yifysubtitles.ch"}
}
func (s *Service) get(ctx context.Context, link string, referer ...string) ([]byte, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, errors.New("invalid subtitle address")
	}
	base, _ := url.Parse(s.Base)
	u = base.ResolveReference(u)
	if u.Scheme != "https" || u.Host != base.Host || u.User != nil {
		return nil, errors.New("unsupported subtitle address")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	if len(referer) > 0 {
		ref, err := url.Parse(referer[0])
		if err != nil {
			return nil, errors.New("invalid subtitle referrer")
		}
		ref = base.ResolveReference(ref)
		if ref.Scheme != "https" || ref.Host != base.Host || ref.User != nil {
			return nil, errors.New("unsupported subtitle referrer")
		}
		req.Header.Set("Referer", ref.String())
	}
	resp, err := s.Client.Do(req)
	if err != nil {
		return nil, errors.New("YIFY Subtitles is unreachable")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden && resp.Header.Get("Cf-Mitigated") == "challenge" {
		return nil, errors.New("YIFY requires browser verification for this download. Open the subtitle page, download the file there, then upload the extracted SRT/VTT in Upload file")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("YIFY Subtitles returned HTTP %d; retry later or upload a subtitle", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxSize+1))
	if len(b) > MaxSize {
		return nil, errors.New("subtitle response exceeds 2 MB")
	}
	return b, err
}
func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
func walk(n *html.Node, f func(*html.Node)) {
	f(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, f)
	}
}
func content(n *html.Node) string {
	var b strings.Builder
	walk(n, func(x *html.Node) {
		if x.Type == html.TextNode {
			b.WriteString(x.Data)
			b.WriteByte(' ')
		}
	})
	return strings.Join(strings.Fields(b.String()), " ")
}

var imdbPattern = regexp.MustCompile(`^tt[0-9]{7,10}$`)
var languages = map[string]string{"en": "english", "hi": "hindi", "ne": "nepali", "es": "spanish", "fr": "french", "de": "german", "pt-br": "brazilian portuguese", "ar": "arabic", "ja": "japanese", "ko": "korean", "zh-cn": "chinese"}

func (s *Service) Search(ctx context.Context, imdb, title, language string) ([]Result, error) {
	name, ok := languages[language]
	if !ok {
		return nil, errors.New("unsupported subtitle language")
	}
	if !imdbPattern.MatchString(imdb) {
		return nil, errors.New("YIFY subtitle search requires this movie's IMDb ID; use a local subtitle upload for unmatched movies")
	}
	key := "yify-search:" + imdb + ":" + language
	if b, at, err := s.DB.Get(ctx, key); err == nil && time.Since(at) < 15*time.Minute {
		var out []Result
		if json.Unmarshal(b, &out) == nil {
			return out, nil
		}
	}
	b, err := s.get(ctx, "/movie-imdb/"+imdb)
	if err != nil {
		return nil, err
	}
	doc, err := html.Parse(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	out := []Result{}
	tableFound := false
	walk(doc, func(n *html.Node) {
		if n.Data == "th" && strings.EqualFold(content(n), "language") {
			tableFound = true
		}
		if n.Data != "tr" {
			return
		}
		id, _ := strconv.Atoi(attr(n, "data-id"))
		if id <= 0 {
			return
		}
		result := Result{ID: id, Language: language}
		lang := ""
		walk(n, func(x *html.Node) {
			switch attr(x, "class") {
			case "sub-lang":
				lang = strings.ToLower(content(x))
			case "rating-cell":
				result.Rating, _ = strconv.Atoi(content(x))
			}
			if x.Data == "a" && strings.HasPrefix(attr(x, "href"), "/subtitles/") {
				result.Page = attr(x, "href")
				result.Label = strings.TrimSpace(strings.TrimPrefix(content(x), "subtitle"))
			}
		})
		if lang != name || result.Page == "" {
			return
		}
		result.HearingImpaired = strings.Contains(strings.ToUpper(result.Label), "{HI}")
		out = append(out, result)
	})
	if !tableFound {
		return nil, errors.New("YIFY returned an unexpected page; retry later or upload a subtitle")
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Rating > out[j].Rating })
	if len(out) > 60 {
		out = out[:60]
	}
	for _, r := range out {
		v, _ := json.Marshal(r)
		if err := s.DB.Put(ctx, "yify-result:"+strconv.Itoa(r.ID)+":"+language, v, time.Now()); err != nil {
			return nil, err
		}
	}
	v, _ := json.Marshal(out)
	_ = s.DB.Put(ctx, key, v, time.Now())
	return out, nil
}
func DownloadKey(hash string, id int, language string) string {
	return "yify-download:" + hash + ":" + strconv.Itoa(id) + ":" + language
}
func (s *Service) Download(ctx context.Context, hash string, id int, label, language string) (database.Subtitle, error) {
	var empty database.Subtitle
	if id <= 0 || !ValidLanguage(language) {
		return empty, errors.New("invalid subtitle selection")
	}
	key := DownloadKey(hash, id, language)
	if b, _, err := s.DB.Get(ctx, key); err == nil {
		var sub database.Subtitle
		if json.Unmarshal(b, &sub) == nil {
			return sub, nil
		}
	}
	b, _, err := s.DB.Get(ctx, "yify-result:"+strconv.Itoa(id)+":"+language)
	if err != nil {
		return empty, errors.New("search again before selecting a subtitle")
	}
	var result Result
	if json.Unmarshal(b, &result) != nil {
		return empty, errors.New("invalid cached subtitle")
	}
	b, err = s.get(ctx, result.Page)
	if err != nil {
		return empty, err
	}
	doc, err := html.Parse(bytes.NewReader(b))
	if err != nil {
		return empty, err
	}
	link := ""
	walk(doc, func(n *html.Node) {
		if n.Data == "a" && strings.Contains(" "+attr(n, "class")+" ", " download-subtitle ") {
			link = attr(n, "href")
		}
	})
	if link == "" {
		return empty, errors.New("subtitle download link unavailable")
	}
	b, err = s.get(ctx, link, result.Page)
	if err != nil {
		return empty, err
	}
	raw, err := unpack(b)
	if err != nil {
		return empty, err
	}
	sub, err := Save(ctx, s.DB, hash, result.Label, language, raw)
	if err != nil {
		return empty, err
	}
	v, _ := json.Marshal(sub)
	_ = s.DB.Put(ctx, key, v, time.Now())
	return sub, nil
}
func unpack(b []byte) ([]byte, error) {
	z, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return nil, errors.New("provider did not return a valid subtitle ZIP")
	}
	if len(z.File) > 100 {
		return nil, errors.New("subtitle ZIP contains too many files")
	}
	for _, f := range z.File {
		ext := strings.ToLower(path.Ext(f.Name))
		if f.FileInfo().IsDir() || (ext != ".srt" && ext != ".vtt") {
			continue
		}
		if f.UncompressedSize64 > MaxSize {
			return nil, errors.New("unpacked subtitle exceeds 2 MB")
		}
		r, err := f.Open()
		if err != nil {
			return nil, err
		}
		raw, err := io.ReadAll(io.LimitReader(r, MaxSize+1))
		r.Close()
		if err != nil {
			return nil, err
		}
		if len(raw) > MaxSize {
			return nil, errors.New("unpacked subtitle exceeds 2 MB")
		}
		if _, err := Normalize(raw); err == nil {
			return raw, nil
		}
	}
	return nil, errors.New("ZIP contains no supported UTF-8 SRT or VTT subtitle")
}
