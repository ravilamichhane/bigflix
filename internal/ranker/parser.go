package ranker

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type ParsedRelease struct {
	CandidateTitle   string // extracted title prefix (excluding fansub prefix, year, tech tags, release group)
	Resolution       string // "2160p" | "1440p" | "1080p" | "720p" | "480p" | ""
	Source           string // "REMUX" | "BluRay" | "WEB-DL" | "WEBRip" | "HDRip" | "HDTV" | "DVDRip" | "DVDScr" | "HDTS" | "CAM" | ""
	Codec            string // "h264" | "hevc" | "av1" | "xvid" | ""
	BitDepth         int    // 8 | 10
	HDR              string // "dv" | "hdr10" | "hdr" | ""
	Audio            string // "aac" | "ddp" | "ac3" | "dts" | "truehd" | ""
	Container        string // "mp4" | "mkv" | "ts" | "avi" | ""
	Languages        []string
	IsMultiAudio     bool
	IsDualAudio      bool
	IsDubbed         bool
	IsCAM            bool // true for CAM / HDTS / DVDScr pre-release rips
	IsSample         bool
	IsNonFeature     bool // true for Making Of, Behind The Scenes, Manga/Digital comics, OST, etc.
	IsDolbyVision    bool
	IsHardcodedSubs  bool
	Year             int
	Season           int
	Episode          int
	EndEpisode       int
	IsSeasonPack     bool
	IsCompleteSeries bool
	SeasonStart      int
	SeasonEnd        int
}

var (
	reRes2160    = regexp.MustCompile(`(?i)\b(2160p|4k|uhd)\b`)
	reRes1440    = regexp.MustCompile(`(?i)\b1440p\b`)
	reRes1080    = regexp.MustCompile(`(?i)\b1080[pi]\b`)
	reRes720     = regexp.MustCompile(`(?i)\b720p\b`)
	reRes480     = regexp.MustCompile(`(?i)\b(480p|576p|sd)\b`)
	reCAMOnly    = regexp.MustCompile(`(?i)\b(cam|hdcam|camrip|workprint)\b`)
	reHDTS       = regexp.MustCompile(`(?i)\b(hdts|hd-?ts|telesync|telecine|\btc\b|\bts\b)\b`)
	reDVDScr     = regexp.MustCompile(`(?i)\b(dvdscr|screener|r5)\b`)
	reRemux      = regexp.MustCompile(`(?i)\bremux\b`)
	reBluRay     = regexp.MustCompile(`(?i)\b(blu-?ray|bdrip|brrip|bdremux)\b`)
	reWebDL      = regexp.MustCompile(`(?i)\b(web-?dl|webdl|amzn|nf|dsnp|atvp|hmax)\b`)
	reWebRip     = regexp.MustCompile(`(?i)\b(web-?rip|webrip|\bweb\b)\b`)
	reHDRip      = regexp.MustCompile(`(?i)\b(hd-?rip|webhd|hc-?hdrip)\b`)
	reHDTV       = regexp.MustCompile(`(?i)\b(hdtv|hdtv-?rip|hd-?tv|pdtv|sdtv|dsr|dsrip|tvrip|satrip|dvbrip)\b`)
	reDVDRip     = regexp.MustCompile(`(?i)\b(dvd-?rip)\b`)
	reHEVC       = regexp.MustCompile(`(?i)(?:^|[^a-zA-Z])(x265|h\.?265|hevc)\b`)
	reH264       = regexp.MustCompile(`(?i)(?:^|[^a-zA-Z])(x264|h\.?264|avc)\b`)
	reAV1        = regexp.MustCompile(`(?i)\bav1\b`)
	reXvid       = regexp.MustCompile(`(?i)\b(xvid|divx)\b`)
	re10Bit      = regexp.MustCompile(`(?i)\b(10-?bit|hi10p?)\b`)
	reDV         = regexp.MustCompile(`(?i)\b(dv|dovi|dolby\.?vision)\b`)
	reHDR10      = regexp.MustCompile(`(?i)\bhdr10\+?\b`)
	reHDR        = regexp.MustCompile(`(?i)\bhdr\b`)
	reTrueHD     = regexp.MustCompile(`(?i)\b(truehd|atmos)\b`)
	reDTS        = regexp.MustCompile(`(?i)\b(dts-?hd|dts-?x|dts)\b`)
	reDDP        = regexp.MustCompile(`(?i)\b(ddp|dd\+|eac3|e-ac-3)\b`)
	reAC3        = regexp.MustCompile(`(?i)\b(ac3|dd5\.1|dd2\.0)\b`)
	reAAC        = regexp.MustCompile(`(?i)\baac\b`)
	reSample     = regexp.MustCompile(`(?i)\b(sample|trailer|featurette|deleted\.?scenes?)\b`)
	reNonFeature = regexp.MustCompile(`(?i)(\(\s*digital\s*\)|\b(making[\s._-]+of|behind[\s._-]+the[\s._-]+scenes?|the[\s._-]+real[\s._-]+story|soundtrack|\bost\b|manga|epub|cbz|cbr|pdf)\b)`)
	reHCSub      = regexp.MustCompile(`(?i)\b(hc|korsub|hardsub|hardcoded)\b`)
	reMulti      = regexp.MustCompile(`(?i)\b(multi\d*|dual[\s._-]?audio)\b`)
	reDualAudio  = regexp.MustCompile(`(?i)\b(dual[\s._-]?audio)\b`)
	reDubbed     = regexp.MustCompile(`(?i)\b(dub|dubbed|dublado)\b`)
	reYear       = regexp.MustCompile(`\b(19[4-9]\d|20[0-3]\d)\b`)
	reSxxExx     = regexp.MustCompile(`(?i)\bS(\d{1,2})[\s._-]*E(\d{1,3})(?:[\s._-]*E(\d{1,3}))?\b`)
	reNxNN       = regexp.MustCompile(`(?i)\b(\d{1,2})x(\d{1,3})\b`)
	reSRange     = regexp.MustCompile(`(?i)\bS(\d{1,2})\s*[-–]\s*S?(\d{1,2})\b`)
	reSOnly      = regexp.MustCompile(`(?i)\b(?:S(\d{1,2})|Season[\s._-]*(\d{1,2}))\b`)
	reComplete   = regexp.MustCompile(`(?i)\b(complete|full[\s._-]*series|all[\s._-]*seasons|box[\s._-]*set)\b`)
	reEpWord     = regexp.MustCompile(`(?i)\b(?:Episode|Ep\.?)[\s._-]*(\d{1,4})\b`)
	reBareE      = regexp.MustCompile(`(?i)(?:^|[\s._\[(-])E(\d{2,4})(?:[\s._\])v-]|$)`)
	reAnimeEp    = regexp.MustCompile(`(?:^|[\s._-])-\s*(\d{2,4})(?:v\d)?(?:[\s._\[\(]|$)`)
	reLeadNumEp  = regexp.MustCompile(`^\s*(\d{2,4})(?:v\d)?(?:[\s._-]+|$)`)
	reTrailNumEp = regexp.MustCompile(`[\s._]+(\d{2,4})(?:v\d)?\s*[\[\(]`)

	reLeadingBracket = regexp.MustCompile(`^\s*[\[\(][^\]\)]{1,45}[\]\)]\s*`)
	reTrailingGroup  = regexp.MustCompile(`-[A-Za-z0-9_.]{2,32}(\.[a-z0-9]{2,4})?$`)
	reTitleBoundary  = regexp.MustCompile(`(?i)(?:^|[\s._\(\[-])(` +
		`S\d{1,2}[\s._-]*E\d{1,3}|` +
		`S\d{1,2}\b|` +
		`Season[\s._-]*\d{1,2}\b|` +
		`\d{1,2}x\d{2,3}\b|` +
		`Ep\.?[\s._-]*\d{1,3}\b|` +
		`Episode[\s._-]*\d{1,3}\b|` +
		`2160p|1440p|1080[pi]|720p|480p|576p|\b4k\b|\buhd\b|` +
		`blu-?ray|bdrip|brrip|remux|web-?dl|webdl|web-?rip|webrip|\bweb\b|` +
		`hd-?rip|hdtv|hdtv-?rip|pdtv|sdtv|dsr|tvrip|satrip|dvbrip|dvd-?rip|dvdscr|screener|` +
		`hdcam|camrip|\bcam\b|hd-?ts|telesync|telecine|` +
		`x264|x265|h\.?264|h\.?265|hevc|av1|xvid|10-?bit|hdr10?|dovi|\bdv\b|` +
		`repack|proper|extended|unrated|uncut|remastered|imax|hybrid|directors?[\s._-]+cut|` +
		`complete|batch|box[\s._-]*\d+|vol\.?[\s._-]*\d+|` +
		`multi\d*|dual[\s._-]?audio|(?:english|eng|hindi|hin|tamil|telugu|kannada|malayalam|japanese|korean|spanish|french|german)[\s._-]+(?:dub|dubbed|audio)|dubbed|\bdub\b|vostfr|truefrench` +
		`)(?:[\s._\)\]-]|$)`)
	reTrailingLangTag = regexp.MustCompile(`(?:\s+(?:FRENCH|SPANISH|LATINO|GERMAN|ITALIAN|RUSSIAN|HINDI|TAMIL|TELUGU|KANNADA|MALAYALAM|BENGALI|KOREAN|JAPANESE|VOSTFR|TRUEFRENCH|MULTI\d*))+$`)
	reYearBoundary    = regexp.MustCompile(`(?:[\s._\(\[]+)(19[4-9]\d|20[0-3]\d)(?:[\s._\)\]]|$)`)
	reAnimeSep     = regexp.MustCompile(`\s+-\s+\d{1,3}(?:v\d)?(?:\s|\[|\(|$)`)

	langTokens = map[string]*regexp.Regexp{
		"en": regexp.MustCompile(`(?i)\b(english|eng)\b`),
		"fr": regexp.MustCompile(`(?i)\b(french|truefrench|vff|vfq|vf2|vostfr)\b`),
		"es": regexp.MustCompile(`(?i)\b(spanish|latino|castellano|espanol)\b`),
		"de": regexp.MustCompile(`(?i)\b(german|deutsch)\b`),
		"it": regexp.MustCompile(`(?i)\b(italian|ita)\b`),
		"ru": regexp.MustCompile(`(?i)\b(russian|rus)\b`),
		"pt": regexp.MustCompile(`(?i)\b(portuguese|dublado)\b`),
		"hi": regexp.MustCompile(`(?i)\b(hindi|hin)\b`),
		"ne": regexp.MustCompile(`(?i)\b(nepali|nep)\b`),
		"te": regexp.MustCompile(`(?i)\b(telugu|tel)\b`),
		"ta": regexp.MustCompile(`(?i)\b(tamil|tam)\b`),
		"kn": regexp.MustCompile(`(?i)\b(kannada|kan)\b`),
		"ml": regexp.MustCompile(`(?i)\b(malayalam|mal)\b`),
		"bn": regexp.MustCompile(`(?i)\b(bengali|bangla)\b`),
		"ar": regexp.MustCompile(`(?i)\b(arabic)\b`),
		"pl": regexp.MustCompile(`(?i)\b(polish|lektor)\b`),
		"tr": regexp.MustCompile(`(?i)\b(turkish)\b`),
		"ko": regexp.MustCompile(`(?i)\b(korean|kor)\b`),
		"ja": regexp.MustCompile(`(?i)\b(japanese|jpn)\b`),
		"zh": regexp.MustCompile(`(?i)\b(chinese|mandarin|cantonese)\b`),
		"uk": regexp.MustCompile(`(?i)\b(ukrainian|ukr)\b`),
	}

	langDisplayNames = map[string]string{
		"en": "English",
		"hi": "Hindi",
		"ne": "Nepali",
		"ta": "Tamil",
		"te": "Telugu",
		"kn": "Kannada",
		"ml": "Malayalam",
		"bn": "Bengali",
		"ja": "Japanese",
		"ko": "Korean",
		"zh": "Chinese",
		"es": "Spanish",
		"fr": "French",
		"de": "German",
		"it": "Italian",
		"pt": "Portuguese",
		"ru": "Russian",
		"ar": "Arabic",
		"pl": "Polish",
		"tr": "Turkish",
		"uk": "Ukrainian",
	}
)

// matchNxNN matches TV episode notation like "1x05" while ignoring audio/codec
// collisions like "5.1x264" or "2.0x265" where x264/x265 is glued to channels.
func matchNxNN(s string) (season, episode int, ok bool) {
	for _, idx := range reNxNN.FindAllStringSubmatchIndex(s, -1) {
		if len(idx) < 6 {
			continue
		}
		start := idx[0]
		sStr := s[idx[2]:idx[3]]
		eStr := s[idx[4]:idx[5]]
		if eStr == "264" || eStr == "265" {
			continue
		}
		if start >= 2 && s[start-1] == '.' && s[start-2] >= '0' && s[start-2] <= '9' {
			continue
		}
		sn, err1 := strconv.Atoi(sStr)
		ep, err2 := strconv.Atoi(eStr)
		if err1 == nil && err2 == nil && sn > 0 && ep > 0 {
			return sn, ep, true
		}
	}
	return 0, 0, false
}

// ExtractReleaseTitle isolates the actual movie or show title from a noisy
// scene/P2P torrent name by stripping leading fansub/group tags, trailing
// release groups, and everything from the year / season / technical tags onward.
func ExtractReleaseTitle(raw string) string {
	title, _ := extractTitleAndMeta(raw)
	return title
}

func extractTitleAndMeta(raw string) (string, string) {
	s := strings.TrimSpace(raw)
	var leadingMeta []string
	// Strip leading [Group] / (Group) tags (up to 3 stacked prefixes)
	for range 3 {
		loc := reLeadingBracket.FindStringIndex(s)
		if loc == nil {
			break
		}
		prefix := strings.TrimSpace(s[loc[0]:loc[1]])
		next := strings.TrimSpace(s[loc[1]:])
		if next == "" {
			break
		}
		leadingMeta = append(leadingMeta, prefix)
		s = next
	}
	// Strip trailing -ReleaseGroup before tokenizing
	s = reTrailingGroup.ReplaceAllString(s, "")
	// Replace dots and underscores with spaces so boundary detection works uniformly
	norm := strings.NewReplacer(".", " ", "_", " ").Replace(s)

	cut := len(norm)
	if loc := reYearBoundary.FindStringIndex(norm); loc != nil && loc[0] > 0 && loc[0] < cut {
		cut = loc[0]
	}
	if loc := reTitleBoundary.FindStringIndex(norm); loc != nil && loc[0] > 0 && loc[0] < cut {
		cut = loc[0]
	}
	if loc := reAnimeSep.FindStringIndex(norm); loc != nil && loc[0] > 0 && loc[0] < cut {
		cut = loc[0]
	}
	title := strings.TrimSpace(norm[:cut])
	tailMeta := norm[cut:]
	if loc := reTrailingLangTag.FindStringIndex(title); loc != nil && loc[0] > 0 {
		tailMeta = title[loc[0]:] + " " + tailMeta
		title = strings.TrimSpace(title[:loc[0]])
	}
	// Trim trailing punctuation/brackets left before the boundary
	title = strings.TrimRight(title, " -–—:([/|")
	title = strings.TrimLeft(title, " -–—:)]/|")
	meta := strings.TrimSpace(strings.Join(leadingMeta, " ") + " " + tailMeta)
	return strings.Join(strings.Fields(title), " "), meta
}

func ParseRelease(title string) ParsedRelease {
	var p ParsedRelease
	var meta string
	p.CandidateTitle, meta = extractTitleAndMeta(title)
	lower := strings.ToLower(title)

	switch {
	case reRes2160.MatchString(title):
		p.Resolution = "2160p"
	case reRes1440.MatchString(title):
		p.Resolution = "1440p"
	case reRes1080.MatchString(title):
		p.Resolution = "1080p"
	case reRes720.MatchString(title):
		p.Resolution = "720p"
	case reRes480.MatchString(title):
		p.Resolution = "480p"
	}

	p.IsSample = reSample.MatchString(title)
	p.IsNonFeature = reNonFeature.MatchString(title)
	p.IsHardcodedSubs = reHCSub.MatchString(title)
	p.IsMultiAudio = reMulti.MatchString(title)
	p.IsDualAudio = reDualAudio.MatchString(title)
	p.IsDubbed = reDubbed.MatchString(title)

	switch {
	case reCAMOnly.MatchString(title):
		p.Source = "CAM"
		p.IsCAM = true
	case reHDTS.MatchString(title):
		p.Source = "HDTS"
		p.IsCAM = true
	case reDVDScr.MatchString(title):
		p.Source = "DVDScr"
		p.IsCAM = true
	case reRemux.MatchString(title):
		p.Source = "REMUX"
	case reBluRay.MatchString(title):
		p.Source = "BluRay"
	case reWebDL.MatchString(title):
		p.Source = "WEB-DL"
	case reWebRip.MatchString(title):
		p.Source = "WEBRip"
	case reHDRip.MatchString(title):
		p.Source = "HDRip"
	case reHDTV.MatchString(title):
		p.Source = "HDTV"
	case reDVDRip.MatchString(title):
		p.Source = "DVDRip"
		if p.Resolution == "" {
			p.Resolution = "480p"
		}
	}

	switch {
	case reAV1.MatchString(title):
		p.Codec = "av1"
	case reHEVC.MatchString(title):
		p.Codec = "hevc"
	case reH264.MatchString(title):
		p.Codec = "h264"
	case reXvid.MatchString(title):
		p.Codec = "xvid"
	}
	if re10Bit.MatchString(title) {
		p.BitDepth = 10
	} else {
		p.BitDepth = 8
	}

	if reDV.MatchString(title) {
		p.IsDolbyVision = true
		p.HDR = "dv"
	} else if reHDR10.MatchString(title) {
		p.HDR = "hdr10"
	} else if reHDR.MatchString(title) {
		p.HDR = "hdr"
	}

	switch {
	case reTrueHD.MatchString(title):
		p.Audio = "truehd"
	case reDTS.MatchString(title):
		p.Audio = "dts"
	case reDDP.MatchString(title):
		p.Audio = "ddp"
	case reAC3.MatchString(title):
		p.Audio = "ac3"
	case reAAC.MatchString(title):
		p.Audio = "aac"
	}

	switch {
	case strings.HasSuffix(lower, ".mp4") || strings.Contains(lower, " mp4") || strings.Contains(lower, ".mp4"):
		p.Container = "mp4"
	case strings.HasSuffix(lower, ".mkv") || strings.Contains(lower, " mkv") || strings.Contains(lower, ".mkv"):
		p.Container = "mkv"
	case strings.HasSuffix(lower, ".ts") || strings.HasSuffix(lower, ".m2ts"):
		p.Container = "ts"
	case strings.HasSuffix(lower, ".avi"):
		p.Container = "avi"
	}

	langScan := meta
	if langScan == "" {
		langScan = title
	}
	for code, re := range langTokens {
		if re.MatchString(langScan) {
			p.Languages = append(p.Languages, code)
		}
	}
	if len(p.Languages) >= 2 {
		p.IsMultiAudio = true
	}

	if matches := reYear.FindAllString(title, -1); len(matches) > 0 {
		for i := len(matches) - 1; i >= 0; i-- {
			if y, err := strconv.Atoi(matches[i]); err == nil && y >= 1940 && y <= 2035 {
				p.Year = y
				break
			}
		}
	}

	// Episode / Season parsing
	if m := reSxxExx.FindStringSubmatch(title); len(m) >= 3 {
		p.Season, _ = strconv.Atoi(m[1])
		p.Episode, _ = strconv.Atoi(m[2])
		if len(m) >= 4 && m[3] != "" {
			p.EndEpisode, _ = strconv.Atoi(m[3])
		}
	} else if s, e, ok := matchNxNN(title); ok {
		p.Season = s
		p.Episode = e
	} else if m := reSRange.FindStringSubmatch(title); len(m) >= 3 {
		p.SeasonStart, _ = strconv.Atoi(m[1])
		p.SeasonEnd, _ = strconv.Atoi(m[2])
		p.IsSeasonPack = true
		p.IsCompleteSeries = true
	} else if m := reSOnly.FindStringSubmatch(title); len(m) >= 3 {
		sNum := m[1]
		if sNum == "" {
			sNum = m[2]
		}
		p.Season, _ = strconv.Atoi(sNum)
		if p.Season > 0 {
			if em := reEpWord.FindStringSubmatch(title); len(em) >= 2 {
				p.Episode, _ = strconv.Atoi(em[1])
			} else {
				p.IsSeasonPack = true
			}
		}
	} else if reComplete.MatchString(title) {
		p.IsSeasonPack = true
		p.IsCompleteSeries = true
	}
	return p
}

// ParseEpisodeFilename maps a video file path inside a torrent (e.g., season pack)
// to (season, episode). Returns season=0 when only an episode number is present
// (common in single-season anime packs).
func ParseEpisodeFilename(path string) (season, episode int, ok bool) {
	clean := filepath.ToSlash(path)
	lower := strings.ToLower(clean)
	if strings.Contains(lower, "/extras/") || strings.Contains(lower, "/specials/") || strings.Contains(lower, "/featurettes/") || reSample.MatchString(filepath.Base(clean)) {
		return 0, 0, false
	}
	base := filepath.Base(clean)
	if m := reSxxExx.FindStringSubmatch(base); len(m) >= 3 {
		s, _ := strconv.Atoi(m[1])
		e, _ := strconv.Atoi(m[2])
		return s, e, e > 0
	}
	if s, e, ok := matchNxNN(base); ok {
		return s, e, e > 0
	}
	parentSeason := 0
	if m := reSOnly.FindStringSubmatch(clean); len(m) >= 3 {
		raw := m[1]
		if raw == "" {
			raw = m[2]
		}
		parentSeason, _ = strconv.Atoi(raw)
	}
	if m := reEpWord.FindStringSubmatch(base); len(m) >= 2 {
		e, _ := strconv.Atoi(m[1])
		if e > 0 {
			return parentSeason, e, true
		}
	}
	if m := reBareE.FindStringSubmatch(base); len(m) >= 2 {
		e, _ := strconv.Atoi(m[1])
		if e > 0 {
			return parentSeason, e, true
		}
	}
	if m := reAnimeEp.FindStringSubmatch(base); len(m) >= 2 {
		e, _ := strconv.Atoi(m[1])
		if e > 0 && e != 480 && e != 720 && e != 1080 && e != 2160 {
			return parentSeason, e, true
		}
	}
	// Strip leading [Group] prefix before checking bare leading/trailing episode numbers
	noGroup := strings.TrimSuffix(base, filepath.Ext(base))
	for range 2 {
		next := reLeadingBracket.ReplaceAllString(noGroup, "")
		if next == noGroup {
			break
		}
		noGroup = strings.TrimSpace(next)
	}
	if m := reLeadNumEp.FindStringSubmatch(noGroup); len(m) >= 2 {
		e, _ := strconv.Atoi(m[1])
		if e > 0 && e < 2000 && e != 480 && e != 720 && e != 1080 {
			return parentSeason, e, true
		}
	}
	if m := reTrailNumEp.FindStringSubmatch(noGroup); len(m) >= 2 {
		e, _ := strconv.Atoi(m[1])
		if e > 0 && e < 1900 && e != 480 && e != 720 && e != 1080 {
			return parentSeason, e, true
		}
	}
	return 0, 0, false
}
