package ranker

import (
	"bigflix/internal/database"
	"bigflix/internal/sources"
	"strings"
	"testing"
)

func TestParseReleaseAndEpisodeFilenames(t *testing.T) {
	p := ParseRelease("Severance.S01E02.1080p.WEB-DL.H264.AAC-NTb.mkv")
	if p.Season != 1 || p.Episode != 2 || p.Resolution != "1080p" || p.Codec != "h264" || p.Audio != "aac" || p.Source != "WEB-DL" || p.IsSeasonPack {
		t.Fatalf("unexpected parse: %+v", p)
	}
	pack := ParseRelease("Breaking Bad Season 2 Complete 1080p BluRay x265 10bit")
	if !pack.IsSeasonPack || pack.Season != 2 || pack.Codec != "hevc" || pack.BitDepth != 10 {
		t.Fatalf("unexpected pack parse: %+v", pack)
	}

	cases := []struct {
		path string
		s, e int
		ok   bool
	}{
		{"Severance.S01/Severance.S01E03.1080p.mkv", 1, 3, true},
		{"Show/2x07 - Title.mp4", 2, 7, true},
		{"Season 1/Episode 05.mkv", 1, 5, true},
		{"[SubsPlease] Frieren - 12 (1080p).mkv", 0, 12, true},
		{"Severance.S01/Sample/sample-s01e01.mkv", 0, 0, false},
		{"Severance.S01/Extras/BehindTheScenes.mkv", 0, 0, false},
	}
	for _, tc := range cases {
		s, e, ok := ParseEpisodeFilename(tc.path)
		if ok != tc.ok || s != tc.s || e != tc.e {
			t.Fatalf("ParseEpisodeFilename(%q) = (%d,%d,%v), want (%d,%d,%v)", tc.path, s, e, ok, tc.s, tc.e, tc.ok)
		}
	}
}

func TestRankPrefers1080pDirectPlayAndFiltersJunk(t *testing.T) {
	q := sources.Query{
		MediaType:        "episode",
		Title:            "Severance",
		OriginalLanguage: "en",
		Season:           1,
		Episode:          2,
	}
	raw := []sources.RawTorrent{
		{Hash: strings.Repeat("1", 40), Title: "Severance.S01E02.1080p.CAM.x264", Provider: "apibay", Seeds: 500, SizeBytes: 1_200_000_000},
		{Hash: strings.Repeat("2", 40), Title: "Severance.S01E02.FRENCH.1080p.WEB-DL.x264", Provider: "knaben", Seeds: 200, SizeBytes: 1_400_000_000},
		{Hash: strings.Repeat("3", 40), Title: "Severance.S01E02.2160p.DV.HDR.WEB-DL.HEVC.Atmos", Provider: "knaben", Seeds: 90, SizeBytes: 5_200_000_000},
		{Hash: strings.Repeat("4", 40), Title: "Severance.S01E02.1080p.WEB-DL.x264.AAC", Provider: "eztv", Seeds: 65, SizeBytes: 1_600_000_000},
		{Hash: strings.Repeat("5", 40), Title: "Severance.S01E05.1080p.WEB-DL.x264.AAC", Provider: "eztv", Seeds: 300, SizeBytes: 1_600_000_000},
		{Hash: strings.Repeat("6", 40), Title: "Severance S01 Complete 1080p WEB-DL x264", Provider: "apibay", Seeds: 110, SizeBytes: 14_000_000_000},
	}

	ranked := Rank(q, raw, database.DefaultPlaybackPrefs())
	if len(ranked) != 4 {
		t.Fatalf("expected 4 valid candidates (CAM and E05 filtered), got %d: %+v", len(ranked), ranked)
	}
	if ranked[0].Hash != strings.Repeat("4", 40) {
		t.Fatalf("expected 1080p H.264 AAC episode first, got %s (%s, score=%.1f)", ranked[0].Title, ranked[0].Hash, ranked[0].Score)
	}
	// Foreign dub should rank below English H.264 / season pack
	if ranked[len(ranked)-1].Hash != strings.Repeat("2", 40) {
		t.Fatalf("expected foreign dub ranked last, got %s", ranked[len(ranked)-1].Title)
	}
}

func TestStrictTitleMatchingRejectsFalsePositives(t *testing.T) {
	// Real-world case 1: Movie "Toxic" (2026) must NOT match "A Toxic Love Story", "-TOXIC" groups, or manga
	toxicQ := sources.Query{MediaType: "movie", Title: "Toxic", Year: 2026, OriginalLanguage: "kn"}
	toxicRaw := []sources.RawTorrent{
		{Hash: strings.Repeat("1", 40), Title: "A Toxic Love Story (2026) 1080p WEBRip 5.1 x264 -YTS", Provider: "knaben/yts", Seeds: 42, SizeBytes: 1_800_000_000},
		{Hash: strings.Repeat("2", 40), Title: "Send.Help.2026.2160p.AMZN.WEB-DL.DDP5.1.DV.HDR.H.265-ToxicOfficeSurvivalMode", Provider: "knaben/tpb", Seeds: 5, SizeBytes: 17_000_000_000},
		{Hash: strings.Repeat("3", 40), Title: "Fils.de.personne.2026.FRENCH.HDR.2160p.WEB.EAC3.5.1.H265-TOXIC", Provider: "apibay", Seeds: 10, SizeBytes: 17_000_000_000},
		{Hash: strings.Repeat("4", 40), Title: `After Getting the Crap Skill "Poison Immunity," I Ate the Toxic Meat 001-005 (2026) (Digital) (Oak)`, Provider: "knaben/nyaa.si", Seeds: 5, SizeBytes: 212_000_000},
		{Hash: strings.Repeat("5", 40), Title: "Toxic.2026.1080p.HDRip.x264.AAC", Provider: "apibay", Seeds: 18, SizeBytes: 2_100_000_000},
	}
	gotToxic := Rank(toxicQ, toxicRaw, database.DefaultPlaybackPrefs())
	if len(gotToxic) != 1 || gotToxic[0].Hash != strings.Repeat("5", 40) || gotToxic[0].SourceType != "HDRip" {
		t.Fatalf("expected only real Toxic (2026) HDRip, got %+v", gotToxic)
	}

	// Real-world case 2: "The Odyssey" (2026) must reject "The Real Story" and "Making Of"
	odyQ := sources.Query{MediaType: "movie", Title: "The Odyssey", Year: 2026, OriginalLanguage: "en"}
	odyRaw := []sources.RawTorrent{
		{Hash: strings.Repeat("1", 40), Title: "The Odyssey - The Real Story (2026) 1080p WEBRip x264 -YTS", Provider: "knaben/yts", Seeds: 100, SizeBytes: 1_500_000_000},
		{Hash: strings.Repeat("2", 40), Title: "The Odyssey The Making Of An Epic (2026) 1080p WEBRip 5.1-LAMA", Provider: "knaben/tpb", Seeds: 112, SizeBytes: 1_400_000_000},
		{Hash: strings.Repeat("3", 40), Title: "The.Odyssey.2026.BluRay.1080p.DDP.5.1.x264-hallowed", Provider: "apibay", Seeds: 491, SizeBytes: 4_500_000_000},
	}
	gotOdy := Rank(odyQ, odyRaw, database.DefaultPlaybackPrefs())
	if len(gotOdy) != 1 || gotOdy[0].Hash != strings.Repeat("3", 40) {
		t.Fatalf("expected only real The Odyssey movie, got %+v", gotOdy)
	}

	// Real-world case 3: "Naruto" S01E02 must reject fansub [Naruto-Kun.Hu], Boruto, Shippuden, Spin-Off, Movies pack
	narutoQ := sources.Query{MediaType: "episode", Title: "Naruto", OriginalLanguage: "ja", Season: 1, Episode: 2}
	narutoRaw := []sources.RawTorrent{
		{Hash: strings.Repeat("1", 40), Title: "[Naruto-Kun.Hu] One-Room TA S01E02 [1080p].mkv", Provider: "knaben/nyaa.si", Seeds: 6, SizeBytes: 400_000_000},
		{Hash: strings.Repeat("2", 40), Title: "NARUTO.Spin-Off.Rock.Lee.and.His.Ninja.Pals.S01.1080p.CR.WEB-DL.AAC2.0.H.264-Kitsune", Provider: "knaben/nyaa.si", Seeds: 9, SizeBytes: 6_000_000_000},
		{Hash: strings.Repeat("3", 40), Title: "[JP-EN] Boruto : Naruto Next Generations S01 COMPLETE 1080p BDRip x265", Provider: "knaben/nyaa.si", Seeds: 21, SizeBytes: 15_000_000_000},
		{Hash: strings.Repeat("4", 40), Title: "Naruto Shippuden S01-18 (1-393) English Dubbed", Provider: "knaben/tpb", Seeds: 34, SizeBytes: 50_000_000_000},
		{Hash: strings.Repeat("5", 40), Title: "[Judas] Naruto - Movies 01-03 [BD 1080p][HEVC]", Provider: "nyaa", Seeds: 10, SizeBytes: 5_000_000_000},
		{Hash: strings.Repeat("6", 40), Title: "Naruto.S01E02.1080p.HDTV.x264.AAC", Provider: "apibay", Seeds: 15, SizeBytes: 450_000_000},
	}
	gotNaruto := Rank(narutoQ, narutoRaw, database.DefaultPlaybackPrefs())
	if len(gotNaruto) != 1 || gotNaruto[0].Hash != strings.Repeat("6", 40) || gotNaruto[0].SourceType != "HDTV" {
		t.Fatalf("expected only real Naruto S01E02 HDTV, got %+v", gotNaruto)
	}
}

func TestHDTVSupportAndPreReleaseFallback(t *testing.T) {
	// HDTV / PDTV / HDTVRip should be first-class
	tvQ := sources.Query{MediaType: "episode", Title: "The Wire", Season: 1, Episode: 1, OriginalLanguage: "en"}
	tvRaw := []sources.RawTorrent{
		{Hash: strings.Repeat("1", 40), Title: "The.Wire.S01E01.720p.HDTV.x264-KILLERS", Provider: "eztv", Seeds: 40, SizeBytes: 900_000_000},
		{Hash: strings.Repeat("2", 40), Title: "The.Wire.S01E01.1080p.HDTVRip.H264.AAC", Provider: "apibay", Seeds: 55, SizeBytes: 1_500_000_000},
	}
	gotTV := Rank(tvQ, tvRaw, database.DefaultPlaybackPrefs())
	if len(gotTV) != 2 || gotTV[0].SourceType != "HDTV" || gotTV[1].SourceType != "HDTV" {
		t.Fatalf("expected both HDTV releases supported, got %+v", gotTV)
	}

	// Pre-release CAM/HDTS/DVDScr should be kept ONLY when no clean release exists, ranked DVDScr > HDTS > CAM
	newQ := sources.Query{MediaType: "movie", Title: "Ramayana Rise of a Legend", Year: 2026, OriginalLanguage: "hi"}
	newRaw := []sources.RawTorrent{
		{Hash: strings.Repeat("a", 40), Title: "Ramayana.Rise.of.a.Legend.2026.CAM.1080p.HI-CinemaCity", Provider: "apibay", Seeds: 20, SizeBytes: 1_800_000_000},
		{Hash: strings.Repeat("b", 40), Title: "Ramayana.Rise.of.a.Legend.2026.1080p.TELESYNC.MULTi5.x264-EaZy", Provider: "apibay", Seeds: 25, SizeBytes: 2_100_000_000},
		{Hash: strings.Repeat("c", 40), Title: "Ramayana Rise Of A Legend (2026) 1080p Hindi DVDScr x264 AAC", Provider: "apibay", Seeds: 50, SizeBytes: 2_200_000_000},
	}
	gotPre := Rank(newQ, newRaw, database.DefaultPlaybackPrefs())
	if len(gotPre) != 3 || gotPre[0].SourceType != "DVDScr" || gotPre[1].SourceType != "HDTS" || gotPre[2].SourceType != "CAM" {
		t.Fatalf("expected DVDScr > HDTS > CAM fallback order, got %+v", gotPre)
	}
}

func TestSizeRankingPrefersSmallerAtSameQualityAndPeers(t *testing.T) {
	// Movies: same quality (1080p), same codec (x264), same seeds (50) -> smaller size ranks higher
	mq := sources.Query{MediaType: "movie", Title: "Arrival", Year: 2016, OriginalLanguage: "en"}
	mRaw := []sources.RawTorrent{
		{Hash: strings.Repeat("1", 40), Title: "Arrival.2016.1080p.BluRay.x264.AAC", Provider: "apibay", Seeds: 50, SizeBytes: 12_000_000_000},
		{Hash: strings.Repeat("2", 40), Title: "Arrival.2016.1080p.BluRay.x264.AAC", Provider: "apibay", Seeds: 50, SizeBytes: 1_900_000_000},
		{Hash: strings.Repeat("3", 40), Title: "Arrival.2016.1080p.BluRay.x264.AAC", Provider: "apibay", Seeds: 50, SizeBytes: 5_200_000_000},
	}
	gotM := Rank(mq, mRaw, database.DefaultPlaybackPrefs())
	if len(gotM) != 3 || gotM[0].SizeBytes != 1_900_000_000 || gotM[1].SizeBytes != 5_200_000_000 || gotM[2].SizeBytes != 12_000_000_000 {
		t.Fatalf("expected movie size order 1.9G > 5.2G > 12G, got %+v", gotM)
	}

	// Episodes: same quality & seeds -> smaller episode wins
	eq := sources.Query{MediaType: "episode", Title: "Severance", Season: 1, Episode: 2, OriginalLanguage: "en"}
	eRaw := []sources.RawTorrent{
		{Hash: strings.Repeat("a", 40), Title: "Severance.S01E02.1080p.WEB-DL.x264.AAC", Provider: "eztv", Seeds: 40, SizeBytes: 3_200_000_000},
		{Hash: strings.Repeat("b", 40), Title: "Severance.S01E02.1080p.WEB-DL.x264.AAC", Provider: "eztv", Seeds: 40, SizeBytes: 750_000_000},
	}
	gotE := Rank(eq, eRaw, database.DefaultPlaybackPrefs())
	if len(gotE) != 2 || gotE[0].SizeBytes != 750_000_000 {
		t.Fatalf("expected smaller episode first, got %+v", gotE)
	}

	// Huge anime pack filenames
	for _, tc := range []struct {
		path string
		ep   int
	}{
		{"Naruto/001 - Enter Naruto Uzumaki.mkv", 1},
		{"[Judas] Naruto 042 [1080p].mkv", 42},
		{"Season 1/E105.mkv", 105},
	} {
		_, ep, ok := ParseEpisodeFilename(tc.path)
		if !ok || ep != tc.ep {
			t.Fatalf("ParseEpisodeFilename(%q) = ep %d ok=%v, want %d", tc.path, ep, ok, tc.ep)
		}
	}
}

func TestDubbedAndDualAudioBadgesAndRanking(t *testing.T) {
	q := sources.Query{MediaType: "movie", Title: "Inception", Year: 2010, OriginalLanguage: "en"}
	raw := []sources.RawTorrent{
		{Hash: strings.Repeat("1", 40), Title: "Inception.2010.1080p.BluRay.x264.AAC-YIFY", Provider: "yts", Seeds: 120, SizeBytes: 2_100_000_000},
		{Hash: strings.Repeat("2", 40), Title: "Inception 2010 1080p BluRay Dual Audio [Hindi-English] x264 AAC", Provider: "apibay", Seeds: 60, SizeBytes: 2_200_000_000},
		{Hash: strings.Repeat("3", 40), Title: "Inception 2010 1080p Hindi Dubbed WEB-DL x264 AAC", Provider: "apibay", Seeds: 50, SizeBytes: 1_900_000_000},
	}

	// Default ("original" -> English): Dual Audio English+Hindi is NOT penalized, and badges show clearly
	gotDefault := Rank(q, raw, database.DefaultPlaybackPrefs())
	if len(gotDefault) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(gotDefault))
	}
	byHash := map[string]Candidate{}
	for _, c := range gotDefault {
		byHash[c.Hash] = c
	}
	dual := byHash[strings.Repeat("2", 40)]
	if strings.Join(dual.Languages, ",") != "English,Hindi,Dual Audio" {
		t.Fatalf("expected [English Hindi Dual Audio], got %v", dual.Languages)
	}
	if strings.Contains(dual.Reason, "foreign dub") {
		t.Fatalf("dual audio Hindi+English should not be penalized as foreign dub: %+v", dual)
	}
	hindiOnly := byHash[strings.Repeat("3", 40)]
	if strings.Join(hindiOnly.Languages, ",") != "Hindi Dub" {
		t.Fatalf("expected [Hindi Dub], got %v", hindiOnly.Languages)
	}

	// Explicit PreferredLanguage="hi" boosts Hindi / Dual Audio releases to the top
	hiPrefs := database.DefaultPlaybackPrefs()
	hiPrefs.PreferredLanguage = "hi"
	gotHindi := Rank(q, raw, hiPrefs)
	if gotHindi[0].Hash != strings.Repeat("2", 40) && gotHindi[0].Hash != strings.Repeat("3", 40) {
		t.Fatalf("expected a Hindi audio release ranked #1 when PreferredLanguage=hi, got %+v", gotHindi)
	}

	// Movie titles containing language words ("The French Dispatch") must NOT get false dub tags
	frTitleQ := sources.Query{MediaType: "movie", Title: "The French Dispatch", Year: 2021, OriginalLanguage: "en"}
	frTitleRaw := []sources.RawTorrent{
		{Hash: strings.Repeat("f", 40), Title: "The.French.Dispatch.2021.1080p.BluRay.x264.AAC", Provider: "apibay", Seeds: 80, SizeBytes: 2_000_000_000},
	}
	gotFrTitle := Rank(frTitleQ, frTitleRaw, database.DefaultPlaybackPrefs())
	if len(gotFrTitle) != 1 {
		t.Fatalf("expected The French Dispatch to match, got %+v", gotFrTitle)
	}
	if len(gotFrTitle[0].Languages) != 0 || strings.Contains(gotFrTitle[0].Reason, "foreign dub") {
		t.Fatalf("movie title word 'French' should not trigger a dub tag: %+v", gotFrTitle[0])
	}
}

func TestRanabaaliRegressionAndCodecChannelCollision(t *testing.T) {
	q := sources.Query{
		MediaType:        "movie",
		TMDBID:           1389379,
		IMDbID:           "tt32324552",
		Title:            "Ranabaali",
		OriginalTitle:    "రణబాలి",
		Year:             2026,
		OriginalLanguage: "te",
	}
	raw := []sources.RawTorrent{
		{
			Hash: "6cac38c86c90d02ecb54f5ab01460b7887b748b5", Title: "Ranabaali (2026) 2160p WEBRip 5.1 10Bit x265 -YTS",
			Provider: "yts", SizeBytes: 8407398482, Seeds: 69, Peers: 61, Quality: "2160p", Codec: "x265", SourceType: "web", Audio: "5.1",
		},
		{
			Hash: "089b02671fdf039418bf34b16a00651d3f8d0170", Title: "Ranabaali 2026.1080p.NF.WEB-DL.Multi.DD+ 5.1x264-KIN",
			Provider: "apibay", SizeBytes: 6257944398, Seeds: 5, Peers: 26,
		},
		{
			Hash: "be07b2e0b45488ef883859efd58af9bc6cec9b9b", Title: "Ranabaali (2026) 1080p Telugu HQ HDRip HEVC x265 DD+5.1 192Kbps AAC-ESub",
			Provider: "apibay", SizeBytes: 1615333456, Seeds: 6730, Peers: 667,
		},
		{
			Hash: "c64726a0c6b96f60ac23b1c5536e67151939708b", Title: "Ranabaali (2026) 1080p WEBRip x265 DDP 5.1 [ Hin, Tel, Kan ] ESub",
			Provider: "knaben/1337x", SizeBytes: 2400000000, Seeds: 0, Peers: 0,
		},
	}

	prefs := database.DefaultPlaybackPrefs()
	got := Rank(q, raw, prefs)
	if len(got) != 3 {
		t.Fatalf("expected all 3 seeded Ranabaali releases (including 5.1x264 and YTS 2160p), got %d: %+v", len(got), got)
	}
	foundKin := false
	for _, c := range got {
		if !IsVerifiedMatch(q, c, prefs) {
			t.Fatalf("expected %q (%s) to be a verified match", c.Title, c.Provider)
		}
		if c.Hash == "089b02671fdf039418bf34b16a00651d3f8d0170" {
			foundKin = true
			if c.Codec != "h264" || c.Parsed.Season != 0 || c.Parsed.Episode != 0 {
				t.Fatalf("expected 5.1x264 parsed as h264 movie (not 1x264 TV ep), got codec=%q s=%d e=%d", c.Codec, c.Parsed.Season, c.Parsed.Episode)
			}
		}
	}
	if !foundKin {
		t.Fatal("expected 5.1x264 WEB-DL release in ranked output")
	}
}
