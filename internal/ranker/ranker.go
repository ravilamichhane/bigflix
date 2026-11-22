package ranker

import (
	"bigflix/internal/database"
	"bigflix/internal/sources"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
)

type Candidate struct {
	Hash            string        `json:"hash"`
	Title           string        `json:"title"`
	Provider        string        `json:"provider"`
	Quality         string        `json:"quality"`
	SourceType      string        `json:"source_type"`
	Codec           string        `json:"codec"`
	Audio           string        `json:"audio"`
	Size            string        `json:"size"`
	SizeBytes       int64         `json:"size_bytes"`
	Seeds           int           `json:"seeds"`
	Peers           int           `json:"peers"`
	IsSeasonPack    bool          `json:"is_season_pack"`
	Score           float64       `json:"score"`
	AIRank          int           `json:"ai_rank"`
	Reason          string        `json:"reason"`
	BrowserFriendly bool          `json:"browser_friendly"`
	Languages       []string      `json:"languages,omitempty"`
	Parsed          ParsedRelease `json:"-"`
}

var (
	reTVMoviePack = regexp.MustCompile(`(?i)\bmovies?\s*\d`)
	reEditionWord = regexp.MustCompile(`(?i)\b(extended|unrated|uncut|remastered|repack|proper|imax|hybrid|directors?[\s._-]+cut|theatrical|special[\s._-]+edition)\b`)
)

func FormatBytes(n int64) string {
	if n <= 0 {
		return ""
	}
	if n >= 1_000_000_000 {
		return fmt.Sprintf("%.2f GB", float64(n)/1e9)
	}
	return fmt.Sprintf("%.0f MB", float64(n)/1e6)
}

func titleTokens(s string) []string {
	s = reEditionWord.ReplaceAllString(s, " ")
	cleaned := strings.ToLower(sources.CleanSearchTitle(s))
	cleaned = strings.ReplaceAll(cleaned, "'", "")
	cleaned = strings.ReplaceAll(cleaned, "-", " ")
	fields := strings.Fields(cleaned)
	var out []string
	for i, f := range fields {
		// Ignore leading articles ("the", "a", "an") and tiny filler connectors
		if i == 0 && (f == "the" || f == "a" || f == "an") {
			continue
		}
		if f == "and" || f == "of" {
			continue
		}
		out = append(out, f)
	}
	return out
}

// titleMatches performs strict bidirectional (recall + precision) comparison
// between the target TMDB title and the extracted torrent release title so
// unrelated movies sharing a single word (e.g. "A Toxic Love Story" vs "Toxic",
// "Boruto: Naruto Next Generations" vs "Naruto", "Send Help -TOXIC") are rejected.
func titleMatches(queryTitle, origTitle, extractedCandTitle string) bool {
	if strings.TrimSpace(extractedCandTitle) == "" {
		return false
	}
	// Support dual-titled releases separated by "/" or "|"
	segments := strings.FieldsFunc(extractedCandTitle, func(r rune) bool {
		return r == '/' || r == '|'
	})
	if len(segments) == 0 {
		segments = []string{extractedCandTitle}
	}
	targets := []string{queryTitle}
	if origTitle != "" && !strings.EqualFold(origTitle, queryTitle) {
		targets = append(targets, origTitle)
	}

	for _, target := range targets {
		qToks := titleTokens(target)
		if len(qToks) == 0 {
			continue
		}
		for _, seg := range segments {
			cToks := titleTokens(seg)
			if len(cToks) == 0 {
				continue
			}
			cSet := make(map[string]bool, len(cToks))
			for _, tok := range cToks {
				cSet[tok] = true
			}
			hits := 0
			for _, qt := range qToks {
				if cSet[qt] {
					hits++
				}
			}
			recall := float64(hits) / float64(len(qToks))
			precision := float64(hits) / float64(len(cToks))

			switch len(qToks) {
			case 1:
				// Single-word title (e.g. "Toxic", "Naruto", "Dune"): require exact title match!
				if recall == 1.0 && precision >= 0.75 {
					return true
				}
			case 2:
				// Two-word title: all target words present, at most 1 extra word
				if recall == 1.0 && precision >= 0.65 {
					return true
				}
			default:
				if recall >= 0.80 && precision >= 0.65 {
					return true
				}
			}
		}
	}
	return false
}

// isExactTitleMatch returns true when the extracted release title's normalized
// tokens match the target title (or original title) 100% in both recall and precision.
func isExactTitleMatch(queryTitle, origTitle, extractedCandTitle string) bool {
	if strings.TrimSpace(extractedCandTitle) == "" {
		return false
	}
	segments := strings.FieldsFunc(extractedCandTitle, func(r rune) bool {
		return r == '/' || r == '|'
	})
	if len(segments) == 0 {
		segments = []string{extractedCandTitle}
	}
	targets := []string{queryTitle}
	if origTitle != "" && !strings.EqualFold(origTitle, queryTitle) {
		targets = append(targets, origTitle)
	}
	for _, target := range targets {
		qToks := titleTokens(target)
		if len(qToks) == 0 {
			continue
		}
		for _, seg := range segments {
			cToks := titleTokens(seg)
			if len(cToks) != len(qToks) {
				continue
			}
			same := true
			for i := range qToks {
				if qToks[i] != cToks[i] {
					same = false
					break
				}
			}
			if same {
				return true
			}
		}
	}
	return false
}

// IsVerifiedMatch reports whether a candidate is deterministically verified to
// match the requested movie/episode (either via exact IMDb-ID lookup on YTS/EZTV
// or via 100% exact title + year/episode match without a foreign-dub conflict).
// Small local AI models may re-order verified matches, but must not reject them.
func IsVerifiedMatch(q sources.Query, c Candidate, prefs database.PlaybackPrefs) bool {
	if (c.Provider == "yts" || c.Provider == "eztv") && q.IMDbID != "" {
		return true
	}
	p := c.Parsed
	if p.CandidateTitle == "" {
		p = ParseRelease(c.Title)
	}
	if p.IsSample || p.IsNonFeature {
		return false
	}
	wantedLang := strings.ToLower(strings.TrimSpace(prefs.PreferredLanguage))
	if wantedLang == "" || wantedLang == "original" {
		wantedLang = strings.ToLower(strings.TrimSpace(q.OriginalLanguage))
	}
	if len(p.Languages) > 0 && !p.IsMultiAudio {
		matchesWanted := false
		if wantedLang != "" {
			for _, l := range p.Languages {
				if l == wantedLang {
					matchesWanted = true
					break
				}
			}
		}
		if !matchesWanted {
			return false
		}
	}
	if !isExactTitleMatch(q.Title, q.OriginalTitle, p.CandidateTitle) {
		return false
	}
	if q.MediaType == "movie" {
		if p.Season > 0 && p.Episode > 0 {
			return false
		}
		if q.Year > 0 && p.Year > 0 && (p.Year < q.Year-1 || p.Year > q.Year+1) {
			return false
		}
		return (q.Year > 0 && p.Year >= q.Year-1 && p.Year <= q.Year+1) || p.Resolution != "" || p.Source != ""
	}
	if p.SeasonStart > 0 && p.SeasonEnd > 0 {
		return q.Season >= p.SeasonStart && q.Season <= p.SeasonEnd
	}
	if p.Season > 0 && p.Season != q.Season {
		return false
	}
	if p.IsSeasonPack {
		return p.Season == 0 || p.Season == q.Season
	}
	if p.Episode > 0 {
		endEp := p.EndEpisode
		if endEp == 0 {
			endEp = p.Episode
		}
		return q.Episode >= p.Episode && q.Episode <= endEp
	}
	return false
}

func Rank(q sources.Query, raw []sources.RawTorrent, prefs database.PlaybackPrefs) []Candidate {
	if prefs.TargetQuality == "" {
		prefs = database.DefaultPlaybackPrefs()
	}

	hasAnySeeded := false
	for _, r := range raw {
		if r.Seeds > 0 {
			hasAnySeeded = true
			break
		}
	}

	type parsedItem struct {
		raw sources.RawTorrent
		p   ParsedRelease
	}

	// Pass 1: filter structurally invalid / wrong-title / wrong-episode releases
	var matched []parsedItem
	hasCleanRelease := false
	for _, r := range raw {
		p := ParseRelease(r.Title)
		if p.Resolution == "" && r.Quality != "" {
			p.Resolution = r.Quality
		}
		if p.Codec == "" && r.Codec != "" {
			lc := strings.ToLower(r.Codec)
			switch {
			case strings.Contains(lc, "264") || strings.Contains(lc, "avc"):
				p.Codec = "h264"
			case strings.Contains(lc, "265") || strings.Contains(lc, "hevc"):
				p.Codec = "hevc"
			case strings.Contains(lc, "av1"):
				p.Codec = "av1"
			default:
				p.Codec = lc
			}
		}
		if p.Source == "" && r.SourceType != "" {
			p.Source = r.SourceType
		}
		if p.Audio == "" && r.Audio != "" {
			p.Audio = r.Audio
		}
		if p.Season == 0 && r.Season > 0 {
			p.Season = r.Season
		}
		if p.Episode == 0 && r.Episode > 0 {
			p.Episode = r.Episode
		}
		if r.IsSeasonPack {
			p.IsSeasonPack = true
		}

		if hasAnySeeded && r.Seeds <= 0 {
			continue
		}
		if p.IsSample {
			continue
		}
		// Reject Making Of, Behind The Scenes, manga/digital comics, OST unless part of actual title
		if p.IsNonFeature && !reNonFeature.MatchString(q.Title) {
			continue
		}

		// Strict title matching for non-IMDb-exact providers (note: knaben/yts is NOT direct IMDb-exact yts!)
		imdbExactProvider := (r.Provider == "yts" || r.Provider == "eztv") && q.IMDbID != ""
		if !imdbExactProvider {
			if !titleMatches(q.Title, q.OriginalTitle, p.CandidateTitle) {
				continue
			}
		}

		if q.MediaType == "movie" {
			if p.Season > 0 && p.Episode > 0 {
				continue
			}
			if p.IsSeasonPack {
				continue
			}
			if q.Year > 0 && p.Year > 0 && (p.Year < q.Year-1 || p.Year > q.Year+1) && !imdbExactProvider {
				continue
			}
			if r.SizeBytes > 0 && r.SizeBytes < 80<<20 {
				continue
			}
		} else {
			// Reject anime movie compilation packs when searching for TV episodes
			if reTVMoviePack.MatchString(r.Title) {
				continue
			}
			if p.SeasonStart > 0 && p.SeasonEnd > 0 {
				if q.Season < p.SeasonStart || q.Season > p.SeasonEnd {
					continue
				}
			} else if p.Season > 0 && p.Season != q.Season {
				continue
			}
			if p.Episode > 0 && !p.IsSeasonPack {
				endEp := p.EndEpisode
				if endEp == 0 {
					endEp = p.Episode
				}
				if q.Episode < p.Episode || q.Episode > endEp {
					continue
				}
			}
			if r.SizeBytes > 0 && r.SizeBytes < 25<<20 {
				continue
			}
		}

		if !p.IsCAM {
			hasCleanRelease = true
		}
		matched = append(matched, parsedItem{raw: r, p: p})
	}

	// Pass 2: score matched releases; drop CAM/HDTS/DVDScr whenever clean digital/HDTV/BluRay sources exist,
	// but keep them as clearly labeled fallbacks when nothing cleaner is released yet.
	var out []Candidate
	for _, item := range matched {
		r := item.raw
		p := item.p
		if hasCleanRelease && p.IsCAM {
			continue
		}

		score := 0.0
		var notes []string

		// 1. Seed health
		if r.Seeds > 0 {
			score += math.Min(36.0, math.Log2(float64(r.Seeds)+1)*5.5)
			if r.Seeds >= 15 {
				score += 6
			} else if r.Seeds <= 2 {
				score -= 10
			}
		} else {
			score -= 25
		}

		// 2. Resolution match
		score += resolutionScore(p.Resolution, prefs.TargetQuality)

		// 3. Codec priority (prioritize HEVC/x265 for better quality-per-byte & faster streaming)
		//    and browser / 4 vCPU VPS direct-play friendliness.
		browserFriendly := isBrowserFriendly(p, r.Provider)
		if p.Codec == "hevc" && !p.IsDolbyVision {
			score += 14
		} else if p.Codec == "h264" {
			score += 4
		}
		if prefs.PreferDirectPlay {
			if browserFriendly {
				score += 14
				notes = append(notes, "direct-play friendly")
			} else {
				score -= 8
			}
			if p.IsDolbyVision {
				score -= 18
			}
			if p.Codec == "av1" {
				score -= 10
			}
			if p.Source == "REMUX" {
				score -= 18
			}
		}

		// 4. Source quality (HDTV & HDRip are first-class alongside WEB-DL / BluRay)
		switch p.Source {
		case "WEB-DL", "BluRay":
			score += 8
		case "WEBRip", "HDRip":
			score += 7
		case "HDTV":
			score += 6
			notes = append(notes, "HDTV")
		case "DVDRip":
			score += 3
		case "DVDScr":
			score -= 12
			notes = append(notes, "pre-release DVDScr")
		case "HDTS":
			score -= 22
			notes = append(notes, "pre-release HDTS")
		case "CAM":
			score -= 30
			notes = append(notes, "pre-release CAM")
		}

		// 5. Audio compatibility
		switch p.Audio {
		case "aac":
			score += 5
		case "ddp", "ac3":
			score += 2
		case "dts", "truehd":
			if prefs.PreferDirectPlay {
				score -= 4
			}
		}

		// 6. Language / dub check & human-readable dub badges
		explicitPref := strings.ToLower(strings.TrimSpace(prefs.PreferredLanguage))
		if explicitPref == "original" {
			explicitPref = ""
		}
		wantedLang := explicitPref
		if wantedLang == "" {
			wantedLang = strings.ToLower(q.OriginalLanguage)
		}
		if len(p.Languages) > 0 {
			matchesWanted := false
			for _, l := range p.Languages {
				if l == wantedLang {
					matchesWanted = true
				}
			}
			if matchesWanted {
				if explicitPref != "" {
					score += 24
				} else {
					score += 8
				}
			} else if !p.IsMultiAudio && wantedLang != "" {
				score -= 95
				notes = append(notes, "foreign dub tag")
			}
		}
		if p.IsMultiAudio {
			score += 4
		}

		langBadges := formatLanguageBadges(p)

		if p.IsHardcodedSubs {
			score -= 14
		}

		// 7. Size efficiency: prefer smaller, faster-streaming files when quality & peers are comparable
		if r.SizeBytes > 0 {
			score += sizeEfficiencyScore(q.MediaType, p.Resolution, p.IsSeasonPack, r.SizeBytes)
		}

		// 8. Episode exact match vs season pack
		if q.MediaType == "episode" {
			if p.IsSeasonPack {
				if p.IsCompleteSeries {
					score -= 6
				} else {
					score -= 2
				}
				notes = append(notes, "season pack")
			} else if p.Season == q.Season && p.Episode == q.Episode {
				score += 6
			}
		}

		if r.Provider == "yts" && !p.IsDolbyVision && prefs.PreferDirectPlay {
			score += 6
		}

		qualityLabel := p.Resolution
		if qualityLabel == "" {
			qualityLabel = "SD"
		}
		if p.Resolution != "" {
			notes = append([]string{p.Resolution}, notes...)
		}
		if p.Codec != "" {
			notes = append(notes, strings.ToUpper(p.Codec))
		}
		if r.Seeds > 0 {
			notes = append(notes, fmt.Sprintf("%d seeds", r.Seeds))
		}

		out = append(out, Candidate{
			Hash:            r.Hash,
			Title:           r.Title,
			Provider:        r.Provider,
			Quality:         qualityLabel,
			SourceType:      p.Source,
			Codec:           p.Codec,
			Audio:           p.Audio,
			Size:            FormatBytes(r.SizeBytes),
			SizeBytes:       r.SizeBytes,
			Seeds:           r.Seeds,
			Peers:           r.Peers,
			IsSeasonPack:    p.IsSeasonPack,
			Score:           math.Round(score*10) / 10,
			Reason:          strings.Join(notes, " · "),
			BrowserFriendly: browserFriendly,
			Languages:       langBadges,
			Parsed:          p,
		})
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if out[i].Seeds != out[j].Seeds {
			return out[i].Seeds > out[j].Seeds
		}
		if out[i].SizeBytes > 0 && out[j].SizeBytes > 0 && out[i].SizeBytes != out[j].SizeBytes {
			return out[i].SizeBytes < out[j].SizeBytes
		}
		return false
	})
	return out
}

// sizeEfficiencyScore rewards compact releases within each quality tier so that
// among releases with comparable peer counts and the same resolution, smaller
// files rank higher for faster startup and lower VPS disk/bandwidth usage.
func sizeEfficiencyScore(mediaType, res string, isSeasonPack bool, sizeBytes int64) float64 {
	gb := float64(sizeBytes) / 1e9
	if gb <= 0 {
		return 0
	}
	if isSeasonPack {
		// Season packs vary widely (single season 8-15 GB vs complete 500 GB boxset).
		// Gently favor smaller packs when quality and seeds are equal.
		if gb <= 12.0 {
			return 4.0
		}
		return math.Max(-10.0, 4.0-2.5*math.Log2(gb/12.0))
	}
	if mediaType == "movie" {
		ideal := 2.0 // 1080p default
		switch res {
		case "480p":
			ideal = 0.8
		case "720p":
			ideal = 1.2
		case "1440p":
			ideal = 4.5
		case "2160p":
			ideal = 8.0
		}
		if gb <= ideal {
			return 10.0
		}
		return math.Max(-18.0, 10.0-4.0*math.Log2(gb/ideal))
	}
	// Single TV episode
	ideal := 0.85 // 1080p default
	switch res {
	case "480p":
		ideal = 0.25
	case "720p":
		ideal = 0.45
	case "1440p":
		ideal = 1.6
	case "2160p":
		ideal = 3.0
	}
	if gb <= ideal {
		return 10.0
	}
	return math.Max(-14.0, 10.0-4.0*math.Log2(gb/ideal))
}

func resolutionScore(res, target string) float64 {
	switch target {
	case "2160p":
		switch res {
		case "2160p":
			return 26
		case "1440p":
			return 20
		case "1080p":
			return 16
		case "720p":
			return 6
		case "480p":
			return -8
		}
	case "720p":
		switch res {
		case "720p":
			return 24
		case "1080p":
			return 18
		case "480p":
			return 4
		case "2160p":
			return -6
		}
	default: // "1080p"
		switch res {
		case "1080p":
			return 26
		case "720p":
			return 14
		case "1440p":
			return 10
		case "2160p":
			return 2
		case "480p":
			return -10
		}
	}
	return 0
}

func isBrowserFriendly(p ParsedRelease, provider string) bool {
	if p.IsDolbyVision || p.Codec == "av1" || p.Codec == "xvid" || p.Source == "REMUX" {
		return false
	}
	if p.Codec == "hevc" {
		return true
	}
	if p.Codec == "h264" && p.BitDepth <= 8 {
		return true
	}
	if provider == "yts" {
		return true
	}
	if p.Container == "mp4" {
		return true
	}
	return false
}

func formatLanguageBadges(p ParsedRelease) []string {
	var out []string
	seen := map[string]bool{}
	codes := append([]string(nil), p.Languages...)
	sort.Strings(codes)
	for _, c := range codes {
		name := langDisplayNames[c]
		if name == "" {
			name = strings.ToUpper(c)
		}
		if p.IsDubbed && len(codes) == 1 && !p.IsMultiAudio {
			name += " Dub"
		}
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	if p.IsDualAudio {
		out = append(out, "Dual Audio")
	} else if p.IsMultiAudio && len(codes) < 2 {
		out = append(out, "Multi Audio")
	} else if p.IsDubbed && len(codes) == 0 {
		out = append(out, "Dubbed")
	}
	return out
}
