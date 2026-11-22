package web

import (
	"bigflix/internal/catalog"
	"bigflix/internal/database"
	"bigflix/internal/downloads"
	bigflixv1 "bigflix/internal/gen/bigflix/v1"
	"bigflix/internal/ranker"
	"bigflix/internal/sources"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
)

func catalogItemToProto(c catalog.CatalogItem) *bigflixv1.CatalogItem {
	return &bigflixv1.CatalogItem{
		TmdbId:        int32(c.TMDBID),
		MediaType:     c.MediaType,
		Title:         c.Title,
		OriginalTitle: c.OriginalTitle,
		Year:          int32(c.Year),
		ReleaseDate:   c.ReleaseDate,
		Rating:        c.Rating,
		Overview:      c.Overview,
		Poster:        c.Poster,
		Backdrop:      c.Backdrop,
		Language:      c.Language,
		Genres:        c.Genres,
		Runtime:       int32(c.Runtime),
		ImdbCode:      c.IMDb,
		YtsId:         int32(c.YTSID),
	}
}

func progressToProto(p database.WatchProgress) *bigflixv1.WatchProgressItem {
	return &bigflixv1.WatchProgressItem{
		MediaKey:      p.MediaKey,
		MediaType:     p.MediaType,
		TmdbId:        int32(p.TMDBID),
		SeasonNumber:  int32(p.Season),
		EpisodeNumber: int32(p.Episode),
		Title:         p.Title,
		EpisodeTitle:  p.EpisodeTitle,
		Poster:        p.Poster,
		Backdrop:      p.Backdrop,
		LastHash:      p.LastHash,
		LastFileIndex: int32(p.LastFileIdx),
		Position:      p.Position,
		Duration:      p.Duration,
		Completed:     p.Completed,
		UpdatedAt:     formatTime(time.Unix(p.UpdatedAt, 0)),
	}
}

func candidateToProto(c ranker.Candidate) *bigflixv1.SourceCandidate {
	return &bigflixv1.SourceCandidate{
		Hash:            c.Hash,
		Title:           c.Title,
		Provider:        c.Provider,
		Quality:         c.Quality,
		SourceType:      c.SourceType,
		Codec:           c.Codec,
		Audio:           c.Audio,
		Size:            c.Size,
		SizeBytes:       c.SizeBytes,
		Seeds:           int32(c.Seeds),
		Peers:           int32(c.Peers),
		IsSeasonPack:    c.IsSeasonPack,
		Score:           c.Score,
		AiRank:          int32(c.AIRank),
		Reason:          c.Reason,
		BrowserFriendly: c.BrowserFriendly,
		Languages:       c.Languages,
	}
}

func (a *App) BrowseCatalog(ctx context.Context, req *connect.Request[bigflixv1.BrowseCatalogRequest]) (*connect.Response[bigflixv1.BrowseCatalogResponse], error) {
	if a.service == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("catalog service unavailable"))
	}
	page := int(req.Msg.Page)
	if page == 0 {
		page = 1
	}
	if page < 1 || page > 500 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid page"))
	}
	feed, err := a.service.Browse(ctx, req.Msg.MediaType, req.Msg.Feed, req.Msg.Query, req.Msg.Genre, req.Msg.Sort, page)
	resp := &bigflixv1.BrowseCatalogResponse{
		Page:    int32(page),
		HasTmdb: a.service.HasTMDB(),
	}
	if err != nil {
		if errors.Is(err, catalog.ErrConfiguration) {
			resp.Heading = "Connect TMDB"
			resp.Subtitle = "Add your free TMDB Read Token in Settings to browse Movies & TV Series."
			resp.ErrorMessage = "TMDB is not configured yet. Open Settings to paste your TMDB Read Token, or browse the YTS movie fallback below."
			return connect.NewResponse(resp), nil
		}
		return nil, connect.NewError(connect.CodeUnavailable, errors.New(friendly(err)))
	}
	items := make([]*bigflixv1.CatalogItem, 0, len(feed.Items))
	for _, it := range feed.Items {
		items = append(items, catalogItemToProto(it))
	}
	limit := 20
	if req.Msg.Feed == "yts_fallback" {
		limit = 24
	}
	resp.Items = items
	resp.Count = int32(feed.Count)
	resp.HasPrev = page > 1
	resp.HasNext = page*limit < feed.Count && len(feed.Items) > 0
	resp.Heading = feed.Heading
	resp.Subtitle = feed.Subtitle
	resp.UpdatedAt = formatTime(feed.Updated)
	resp.Stale = feed.Stale
	resp.Notice = feed.Notice
	return connect.NewResponse(resp), nil
}

func (a *App) GetMovieDetail(ctx context.Context, req *connect.Request[bigflixv1.GetMovieDetailRequest]) (*connect.Response[bigflixv1.GetMovieDetailResponse], error) {
	id := int(req.Msg.TmdbId)
	if id <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid movie TMDB id"))
	}
	item, at, stale, err := a.service.MovieDetail(ctx, id)
	if err != nil {
		if errors.Is(err, catalog.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("movie not found"))
		}
		if errors.Is(err, catalog.ErrConfiguration) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New(friendly(err)))
		}
		return nil, connect.NewError(connect.CodeUnavailable, errors.New(friendly(err)))
	}
	resp := &bigflixv1.GetMovieDetailResponse{
		Movie:     catalogItemToProto(item),
		UpdatedAt: formatTime(at),
		Stale:     stale,
	}
	if a.store != nil {
		if p, ok, err := a.store.GetProgress(ctx, database.MediaKey("movie", id, 0, 0)); err == nil && ok {
			resp.Progress = progressToProto(p)
		}
	}
	if a.engine != nil {
		if h, _, ok := a.engine.FindExistingMedia("movie", id, 0, 0); ok {
			resp.ActiveDownloadHash = h
		}
	}
	return connect.NewResponse(resp), nil
}

func (a *App) GetSeriesDetail(ctx context.Context, req *connect.Request[bigflixv1.GetSeriesDetailRequest]) (*connect.Response[bigflixv1.GetSeriesDetailResponse], error) {
	id := int(req.Msg.TmdbId)
	if id <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid series TMDB id"))
	}
	series, at, stale, err := a.service.SeriesDetail(ctx, id)
	if err != nil {
		if errors.Is(err, catalog.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("series not found"))
		}
		if errors.Is(err, catalog.ErrConfiguration) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New(friendly(err)))
		}
		return nil, connect.NewError(connect.CodeUnavailable, errors.New(friendly(err)))
	}
	seasons := make([]*bigflixv1.SeasonSummary, 0, len(series.Seasons))
	for _, sn := range series.Seasons {
		seasons = append(seasons, &bigflixv1.SeasonSummary{
			SeasonNumber: int32(sn.SeasonNumber),
			Name:         sn.Name,
			EpisodeCount: int32(sn.EpisodeCount),
			AirDate:      sn.AirDate,
			Poster:       sn.Poster,
			Overview:     sn.Overview,
		})
	}
	resp := &bigflixv1.GetSeriesDetailResponse{
		Series: &bigflixv1.SeriesDetail{
			TmdbId:        int32(series.TMDBID),
			ImdbCode:      series.IMDb,
			Title:         series.Title,
			OriginalTitle: series.OriginalTitle,
			FirstAirDate:  series.FirstAirDate,
			Year:          int32(series.Year),
			Rating:        series.Rating,
			Status:        series.Status,
			Genres:        series.Genres,
			Overview:      series.Overview,
			Poster:        series.Poster,
			Backdrop:      series.Backdrop,
			Language:      series.Language,
			SeasonCount:   int32(series.SeasonCount),
			EpisodeCount:  int32(series.EpisodeCount),
			Seasons:       seasons,
		},
		UpdatedAt: formatTime(at),
		Stale:     stale,
	}
	resp.NextUp = a.computeNextUp(ctx, series)
	return connect.NewResponse(resp), nil
}

func (a *App) computeNextUp(ctx context.Context, series catalog.SeriesDetail) *bigflixv1.NextUpInfo {
	firstSeason := 1
	if len(series.Seasons) > 0 {
		firstSeason = series.Seasons[0].SeasonNumber
	}
	def := &bigflixv1.NextUpInfo{SeasonNumber: int32(firstSeason), EpisodeNumber: 1}
	if a.store == nil {
		return def
	}
	progress, err := a.store.ListSeriesProgress(ctx, series.TMDBID)
	if err != nil || len(progress) == 0 {
		return def
	}
	// Find most recently updated progress entry
	latest := progress[0]
	for _, p := range progress[1:] {
		if p.UpdatedAt > latest.UpdatedAt {
			latest = p
		}
	}
	if !latest.Completed && latest.Position > 5 {
		return &bigflixv1.NextUpInfo{
			SeasonNumber:  int32(latest.Season),
			EpisodeNumber: int32(latest.Episode),
			EpisodeTitle:  latest.EpisodeTitle,
			Position:      latest.Position,
			Duration:      latest.Duration,
			IsResume:      true,
		}
	}
	// Advance to next episode in same season or next season
	for i, sn := range series.Seasons {
		if sn.SeasonNumber == latest.Season {
			if latest.Episode < sn.EpisodeCount {
				return &bigflixv1.NextUpInfo{
					SeasonNumber:  int32(latest.Season),
					EpisodeNumber: int32(latest.Episode + 1),
				}
			}
			if i+1 < len(series.Seasons) && series.Seasons[i+1].SeasonNumber > 0 {
				return &bigflixv1.NextUpInfo{
					SeasonNumber:  int32(series.Seasons[i+1].SeasonNumber),
					EpisodeNumber: 1,
				}
			}
		}
	}
	return def
}

func (a *App) GetSeasonDetail(ctx context.Context, req *connect.Request[bigflixv1.GetSeasonDetailRequest]) (*connect.Response[bigflixv1.GetSeasonDetailResponse], error) {
	id := int(req.Msg.TmdbId)
	seasonNum := int(req.Msg.SeasonNumber)
	if id <= 0 || seasonNum < 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid series or season"))
	}
	detail, at, stale, err := a.service.SeasonDetail(ctx, id, seasonNum)
	if err != nil {
		if errors.Is(err, catalog.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("season not found"))
		}
		return nil, connect.NewError(connect.CodeUnavailable, errors.New(friendly(err)))
	}

	progByEp := map[int]database.WatchProgress{}
	if a.store != nil {
		if list, err := a.store.ListSeriesProgress(ctx, id); err == nil {
			for _, p := range list {
				if p.Season == seasonNum {
					progByEp[p.Episode] = p
				}
			}
		}
	}
	dlMap := map[[2]int]struct {
		Hash      string
		FileIndex int
	}{}
	if a.engine != nil {
		dlMap = a.engine.FindSeriesDownloads(id)
	}

	eps := make([]*bigflixv1.Episode, 0, len(detail.Episodes))
	for _, ep := range detail.Episodes {
		item := &bigflixv1.Episode{
			SeasonNumber:      int32(ep.SeasonNumber),
			EpisodeNumber:     int32(ep.EpisodeNumber),
			Name:              ep.Name,
			Overview:          ep.Overview,
			AirDate:           ep.AirDate,
			Runtime:           int32(ep.Runtime),
			Still:             ep.Still,
			Rating:            ep.Rating,
			DownloadFileIndex: -1,
		}
		if p, ok := progByEp[ep.EpisodeNumber]; ok {
			item.Position = p.Position
			item.Duration = p.Duration
			item.Completed = p.Completed
		}
		if dl, ok := dlMap[[2]int{ep.SeasonNumber, ep.EpisodeNumber}]; ok {
			item.DownloadHash = dl.Hash
			item.DownloadFileIndex = int32(dl.FileIndex)
		}
		eps = append(eps, item)
	}

	return connect.NewResponse(&bigflixv1.GetSeasonDetailResponse{
		Season: &bigflixv1.SeasonSummary{
			SeasonNumber: int32(detail.Season.SeasonNumber),
			Name:         detail.Season.Name,
			EpisodeCount: int32(detail.Season.EpisodeCount),
			AirDate:      detail.Season.AirDate,
			Poster:       detail.Season.Poster,
			Overview:     detail.Season.Overview,
		},
		Episodes:  eps,
		UpdatedAt: formatTime(at),
		Stale:     stale,
	}), nil
}

func (a *App) ListContinueWatching(ctx context.Context, req *connect.Request[bigflixv1.ListContinueWatchingRequest]) (*connect.Response[bigflixv1.ListContinueWatchingResponse], error) {
	if a.store == nil {
		return connect.NewResponse(&bigflixv1.ListContinueWatchingResponse{}), nil
	}
	limit := int(req.Msg.Limit)
	if limit <= 0 {
		limit = 16
	}
	list, err := a.store.ListContinueWatching(ctx, limit)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("could not load continue watching"))
	}
	items := make([]*bigflixv1.WatchProgressItem, 0, len(list))
	for _, p := range list {
		items = append(items, progressToProto(p))
	}
	return connect.NewResponse(&bigflixv1.ListContinueWatchingResponse{Items: items}), nil
}

type resolvedPayload struct {
	Candidates       []ranker.Candidate `json:"candidates"`
	RankedBy         string             `json:"ranked_by"`
	AINote           string             `json:"ai_note"`
	ProvidersQueried []string           `json:"providers_queried"`
	Warnings         []string           `json:"warnings,omitempty"`
	UpdatedAt        time.Time          `json:"updated_at"`
}

type mediaMeta struct {
	Query        sources.Query
	Title        string
	EpisodeTitle string
	Poster       string
	Backdrop     string
}

func (a *App) loadMediaMeta(ctx context.Context, mediaType string, tmdbID, season, episode int) (mediaMeta, error) {
	if a.service == nil {
		return mediaMeta{}, errors.New("catalog unavailable")
	}
	if mediaType == "episode" || mediaType == "tv" {
		if season < 0 || episode <= 0 {
			return mediaMeta{}, errors.New("valid season and episode are required")
		}
		ser, _, _, err := a.service.SeriesDetail(ctx, tmdbID)
		if err != nil {
			return mediaMeta{}, err
		}
		epTitle := fmt.Sprintf("Episode %d", episode)
		if sd, _, _, err := a.service.SeasonDetail(ctx, tmdbID, season); err == nil {
			for _, ep := range sd.Episodes {
				if ep.EpisodeNumber == episode && ep.Name != "" {
					epTitle = ep.Name
					break
				}
			}
		}
		return mediaMeta{
			Query: sources.Query{
				MediaType:        "episode",
				TMDBID:           ser.TMDBID,
				IMDbID:           ser.IMDb,
				Title:            ser.Title,
				OriginalTitle:    ser.OriginalTitle,
				Year:             ser.Year,
				OriginalLanguage: ser.Language,
				Genres:           ser.Genres,
				Season:           season,
				Episode:          episode,
				TotalSeasons:     ser.SeasonCount,
			},
			Title:        ser.Title,
			EpisodeTitle: epTitle,
			Poster:       ser.Poster,
			Backdrop:     ser.Backdrop,
		}, nil
	}
	mov, _, _, err := a.service.MovieDetail(ctx, tmdbID)
	if err != nil {
		return mediaMeta{}, err
	}
	return mediaMeta{
		Query: sources.Query{
			MediaType:        "movie",
			TMDBID:           mov.TMDBID,
			IMDbID:           mov.IMDb,
			Title:            mov.Title,
			OriginalTitle:    mov.OriginalTitle,
			Year:             mov.Year,
			OriginalLanguage: mov.Language,
			Genres:           mov.Genres,
		},
		Title:    mov.Title,
		Poster:   mov.Poster,
		Backdrop: mov.Backdrop,
	}, nil
}

func (a *App) resolveInternal(ctx context.Context, meta mediaMeta, forceRefresh bool) (resolvedPayload, error) {
	prefs := database.DefaultPlaybackPrefs()
	aiEnabled := false
	if a.store != nil {
		prefs = a.store.LoadPlaybackPrefs(ctx)
		aiCfg := a.store.LoadAIConfig(ctx)
		aiEnabled = aiCfg.Enabled && strings.TrimSpace(aiCfg.APIURL) != ""
	}
	mediaKey := database.MediaKey(meta.Query.MediaType, meta.Query.TMDBID, meta.Query.Season, meta.Query.Episode)
	cacheKey := fmt.Sprintf("resolved:v6:%s:%s:%v:%s:%v", mediaKey, prefs.TargetQuality, prefs.PreferDirectPlay, prefs.PreferredLanguage, aiEnabled)

	if !forceRefresh && a.store != nil {
		if b, at, err := a.store.Get(ctx, cacheKey); err == nil && time.Since(at) < 6*time.Hour {
			var cached resolvedPayload
			if json.Unmarshal(b, &cached) == nil && len(cached.Candidates) > 0 {
				return cached, nil
			}
		}
	}

	report, err := a.aggregator.Search(ctx, meta.Query, forceRefresh)
	if err != nil {
		return resolvedPayload{}, err
	}
	ranked := ranker.Rank(meta.Query, report.Results, prefs)
	rankedBy := "deterministic"
	aiNote := ""
	if a.classifier != nil && len(ranked) > 0 {
		ranked, rankedBy, aiNote = a.classifier.RankCandidates(ctx, meta.Query, prefs, ranked)
	}
	payload := resolvedPayload{
		Candidates:       ranked,
		RankedBy:         rankedBy,
		AINote:           aiNote,
		ProvidersQueried: report.ProvidersQueried,
		Warnings:         report.Warnings,
		UpdatedAt:        time.Now().UTC(),
	}
	if a.store != nil && len(ranked) > 0 {
		if b, err := json.Marshal(payload); err == nil {
			_ = a.store.Put(ctx, cacheKey, b, payload.UpdatedAt)
		}
	}
	return payload, nil
}

func (a *App) ResolveSources(ctx context.Context, req *connect.Request[bigflixv1.ResolveSourcesRequest]) (*connect.Response[bigflixv1.ResolveSourcesResponse], error) {
	if req.Msg.TmdbId <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid TMDB ID"))
	}
	meta, err := a.loadMediaMeta(ctx, req.Msg.MediaType, int(req.Msg.TmdbId), int(req.Msg.SeasonNumber), int(req.Msg.EpisodeNumber))
	if err != nil {
		if errors.Is(err, catalog.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		if errors.Is(err, catalog.ErrConfiguration) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New(friendly(err)))
		}
		return nil, connect.NewError(connect.CodeUnavailable, errors.New(friendly(err)))
	}
	resolved, err := a.resolveInternal(ctx, meta, req.Msg.ForceRefresh)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	out := make([]*bigflixv1.SourceCandidate, 0, len(resolved.Candidates))
	for _, c := range resolved.Candidates {
		out = append(out, candidateToProto(c))
	}
	return connect.NewResponse(&bigflixv1.ResolveSourcesResponse{
		Candidates:       out,
		RankedBy:         resolved.RankedBy,
		AiNote:           resolved.AINote,
		UpdatedAt:        formatTime(resolved.UpdatedAt),
		ProvidersQueried: resolved.ProvidersQueried,
		Warnings:         resolved.Warnings,
	}), nil
}

func (a *App) StartPlayback(ctx context.Context, req *connect.Request[bigflixv1.StartPlaybackRequest]) (*connect.Response[bigflixv1.StartPlaybackResponse], error) {
	if a.engine == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("download engine unavailable"))
	}
	if req.Msg.TmdbId <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid TMDB ID"))
	}
	mediaType := req.Msg.MediaType
	if mediaType == "tv" {
		mediaType = "episode"
	}
	if mediaType != "movie" && mediaType != "episode" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("media_type must be movie or episode"))
	}

	meta, err := a.loadMediaMeta(ctx, mediaType, int(req.Msg.TmdbId), int(req.Msg.SeasonNumber), int(req.Msg.EpisodeNumber))
	if err != nil {
		if errors.Is(err, catalog.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, connect.NewError(connect.CodeUnavailable, errors.New(friendly(err)))
	}

	override := strings.ToLower(strings.TrimSpace(req.Msg.OverrideHash))

	// Fast path: reuse an existing download or season pack that already has this movie/episode!
	if override == "" {
		if existingHash, existingFileIdx, ok := a.engine.FindExistingMedia(mediaType, meta.Query.TMDBID, meta.Query.Season, meta.Query.Episode); ok {
			st, addErr := a.engine.AddMedia(downloads.MediaRequest{
				Hash:         existingHash,
				Title:        meta.Title,
				Poster:       meta.Poster,
				Backdrop:     meta.Backdrop,
				MediaType:    mediaType,
				TMDBID:       meta.Query.TMDBID,
				IMDbID:       meta.Query.IMDbID,
				Season:       meta.Query.Season,
				Episode:      meta.Query.Episode,
				EpisodeTitle: meta.EpisodeTitle,
				Keep:         req.Msg.Keep,
			})
			if addErr == nil {
				a.scheduleNextEpisodePrewarm(meta)
				fIdx := st.FileIndex
				if fIdx < 0 {
					fIdx = existingFileIdx
				}
				return connect.NewResponse(&bigflixv1.StartPlaybackResponse{
					Download:       statusToProto(st),
					FileIndex:      int32(fIdx),
					CandidateCount: 1,
					RankedBy:       "cached",
				}), nil
			}
		}
	}

	resolved, err := a.resolveInternal(ctx, meta, false)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	if len(resolved.Candidates) == 0 {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no playable torrent sources were found across enabled indexers"))
	}

	chosenIdx := 0
	if override != "" {
		found := false
		for i, c := range resolved.Candidates {
			if strings.EqualFold(c.Hash, override) {
				chosenIdx = i
				found = true
				break
			}
		}
		if !found {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("selected source is not in the verified candidates for this title"))
		}
	}

	chosen := resolved.Candidates[chosenIdx]
	var fallbacks []downloads.FallbackCandidate
	for i, c := range resolved.Candidates {
		if i == chosenIdx {
			continue
		}
		fallbacks = append(fallbacks, downloads.FallbackCandidate{
			Hash:         c.Hash,
			Label:        sourceLabel(c),
			IsSeasonPack: c.IsSeasonPack,
		})
		if len(fallbacks) >= 5 {
			break
		}
	}

	st, err := a.engine.AddMedia(downloads.MediaRequest{
		Hash:              chosen.Hash,
		Title:             meta.Title,
		Poster:            meta.Poster,
		Backdrop:          meta.Backdrop,
		MediaType:         mediaType,
		TMDBID:            meta.Query.TMDBID,
		IMDbID:            meta.Query.IMDbID,
		Season:            meta.Query.Season,
		Episode:           meta.Query.Episode,
		EpisodeTitle:      meta.EpisodeTitle,
		ActiveSourceLabel: sourceLabel(chosen),
		Keep:              req.Msg.Keep,
		Fallbacks:         fallbacks,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	if a.store != nil {
		_ = a.store.UpsertProgress(ctx, database.WatchProgress{
			MediaType:    mediaType,
			TMDBID:       meta.Query.TMDBID,
			Season:       meta.Query.Season,
			Episode:      meta.Query.Episode,
			Title:        meta.Title,
			EpisodeTitle: meta.EpisodeTitle,
			Poster:       meta.Poster,
			Backdrop:     meta.Backdrop,
			LastHash:     st.Hash,
			LastFileIdx:  st.FileIndex,
			Position:     st.Position,
		})
	}
	a.scheduleNextEpisodePrewarm(meta)

	return connect.NewResponse(&bigflixv1.StartPlaybackResponse{
		Download:       statusToProto(st),
		FileIndex:      int32(st.FileIndex),
		CandidateCount: int32(len(resolved.Candidates)),
		RankedBy:       resolved.RankedBy,
	}), nil
}

func (a *App) SwitchSource(ctx context.Context, req *connect.Request[bigflixv1.SwitchSourceRequest]) (*connect.Response[bigflixv1.SwitchSourceResponse], error) {
	if a.engine == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("download engine unavailable"))
	}
	curHash := strings.ToLower(strings.TrimSpace(req.Msg.CurrentHash))
	nextHash := strings.ToLower(strings.TrimSpace(req.Msg.NextHash))
	cur, err := a.engine.Get(curHash)
	if err != nil {
		return nil, downloadErr(err)
	}
	if cur.TMDBID <= 0 {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("cannot switch sources for legacy download without TMDB ID"))
	}
	mt := cur.MediaType
	if mt == "" {
		mt = "movie"
	}
	startRes, err := a.StartPlayback(ctx, connect.NewRequest(&bigflixv1.StartPlaybackRequest{
		MediaType:     mt,
		TmdbId:        int32(cur.TMDBID),
		SeasonNumber:  int32(cur.Season),
		EpisodeNumber: int32(cur.Episode),
		Keep:          req.Msg.Keep || cur.Keep,
		OverrideHash:  nextHash,
	}))
	if err != nil {
		return nil, err
	}
	if curHash != nextHash && !cur.Keep {
		_ = a.engine.Update(curHash, "pause", 0, 0)
	}
	return connect.NewResponse(&bigflixv1.SwitchSourceResponse{
		Download: startRes.Msg.Download,
	}), nil
}

func sourceLabel(c ranker.Candidate) string {
	parts := []string{}
	if c.Quality != "" {
		parts = append(parts, c.Quality)
	}
	if c.Codec != "" {
		parts = append(parts, strings.ToUpper(c.Codec))
	}
	if c.Size != "" {
		parts = append(parts, c.Size)
	}
	if c.IsSeasonPack {
		parts = append(parts, "Season Pack")
	}
	if c.Provider != "" {
		parts = append(parts, strings.ToUpper(c.Provider))
	}
	return strings.Join(parts, " · ")
}

// Pre-resolves and caches the next TV episode in the background after initial playback settles
// so clicking Next Episode starts instantly without waiting for indexers or CPU AI.
func (a *App) scheduleNextEpisodePrewarm(meta mediaMeta) {
	if meta.Query.MediaType != "episode" || meta.Query.TMDBID <= 0 {
		return
	}
	nextSeason := meta.Query.Season
	nextEp := meta.Query.Episode + 1
	tmdbID := meta.Query.TMDBID
	go func() {
		time.Sleep(12 * time.Second)
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		nextMeta, err := a.loadMediaMeta(ctx, "episode", tmdbID, nextSeason, nextEp)
		if err != nil {
			return
		}
		_, _ = a.resolveInternal(ctx, nextMeta, false)
	}()
}
