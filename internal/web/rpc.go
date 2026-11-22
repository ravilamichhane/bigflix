package web

import (
	"bigflix/internal/catalog"
	"bigflix/internal/downloads"
	bigflixv1 "bigflix/internal/gen/bigflix/v1"
	"context"
	"errors"
	"strings"
	"time"

	"connectrpc.com/connect"
)

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func movieToProto(m catalog.Movie) *bigflixv1.Movie {
	torrents := make([]*bigflixv1.Torrent, 0, len(m.Torrents))
	for _, t := range m.Torrents {
		torrents = append(torrents, &bigflixv1.Torrent{
			Hash:          t.Hash,
			Quality:       t.Quality,
			Type:          t.Type,
			VideoCodec:    t.Codec,
			Size:          t.Size,
			SizeBytes:     t.Bytes,
			Seeds:         int32(t.Seeds),
			Peers:         int32(t.Peers),
			AudioChannels: t.Audio,
		})
	}
	return &bigflixv1.Movie{
		Id:              int32(m.ID),
		ImdbCode:        m.IMDb,
		Title:           m.Title,
		Year:            int32(m.Year),
		Rating:          m.Rating,
		Runtime:         int32(m.Runtime),
		Genres:          m.Genres,
		Summary:         m.Summary,
		DescriptionFull: m.Description,
		Poster:          m.Poster,
		SmallPoster:     m.SmallPoster,
		Background:      m.Background,
		Language:        m.Language,
		Torrents:        torrents,
		TmdbId:          int32(m.TMDBID),
		ReleaseDate:     m.Release,
	}
}

func statusToProto(s downloads.Status) *bigflixv1.DownloadStatus {
	files := make([]*bigflixv1.VideoFile, 0, len(s.Files))
	for _, f := range s.Files {
		files = append(files, &bigflixv1.VideoFile{
			Index:    int32(f.Index),
			Name:     f.Name,
			Size:     f.Size,
			Playable: f.Playable,
		})
	}
	epFiles := make([]*bigflixv1.EpisodeFileMap, 0, len(s.EpisodeFiles))
	for _, ef := range s.EpisodeFiles {
		epFiles = append(epFiles, &bigflixv1.EpisodeFileMap{
			FileIndex:     int32(ef.FileIndex),
			SeasonNumber:  int32(ef.Season),
			EpisodeNumber: int32(ef.Episode),
			Name:          ef.Name,
			Size:          ef.Size,
		})
	}
	return &bigflixv1.DownloadStatus{
		Hash:              s.Hash,
		Title:             s.Title,
		Poster:            s.Poster,
		MovieId:           int32(s.MovieID),
		FileIndex:         int32(s.FileIndex),
		Keep:              s.Keep,
		Paused:            s.Paused,
		Created:           formatTime(s.Created),
		Position:          s.Position,
		State:             s.State,
		Error:             s.Error,
		Files:             files,
		Size:              s.Size,
		Completed:         s.Completed,
		Buffered:          s.Buffered,
		Speed:             s.Speed,
		Peers:             int32(s.Peers),
		Discovering:       s.Discovering,
		Readers:           int32(s.Readers),
		Progress:          s.Progress,
		Playable:          s.Playable,
		Direct:            s.Direct,
		MediaType:         s.MediaType,
		TmdbId:            int32(s.TMDBID),
		ImdbId:            s.IMDbID,
		SeasonNumber:      int32(s.Season),
		EpisodeNumber:     int32(s.Episode),
		EpisodeTitle:      s.EpisodeTitle,
		ActiveSourceLabel: s.ActiveSourceLabel,
		ReplacedByHash:    s.ReplacedByHash,
		FailoverAttempt:   int32(s.FailoverAttempt),
		FailoverTotal:     int32(s.FailoverTotal),
		FailoverNote:      s.FailoverNote,
		EpisodeFiles:      epFiles,
	}
}

func (a *App) GetStatus(_ context.Context, _ *connect.Request[bigflixv1.GetStatusRequest]) (*connect.Response[bigflixv1.GetStatusResponse], error) {
	return connect.NewResponse(&bigflixv1.GetStatusResponse{
		Status:    "ok",
		Milestone: "playback",
		HasTmdb:   a.service != nil && a.service.HasTMDB(),
	}), nil
}

func (a *App) UpdateAuthToken(ctx context.Context, req *connect.Request[bigflixv1.UpdateAuthTokenRequest]) (*connect.Response[bigflixv1.UpdateAuthTokenResponse], error) {
	next := strings.TrimSpace(req.Msg.NewToken)
	if len(next) < 4 || len(next) > 256 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("token must be 4 to 256 characters"))
	}
	if a.store != nil {
		if err := a.store.SetAuthToken(ctx, next); err != nil {
			return nil, connect.NewError(connect.CodeInternal, errors.New("unable to persist auth token"))
		}
	}
	a.setToken(next)
	return connect.NewResponse(&bigflixv1.UpdateAuthTokenResponse{}), nil
}

func (a *App) ListMovies(ctx context.Context, req *connect.Request[bigflixv1.ListMoviesRequest]) (*connect.Response[bigflixv1.ListMoviesResponse], error) {
	page := int(req.Msg.Page)
	if page == 0 {
		page = 1
	}
	if page < 1 || page > 500 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid page"))
	}
	query := strings.TrimSpace(req.Msg.Query)
	if len(query) > 200 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("search is too long"))
	}
	tab := req.Msg.Feed
	if tab == "" {
		tab = "library"
	}
	heading := "Find your next great watch."
	subtitle := "Movies worth staying in for. Your library starts here."
	var (
		feed catalog.Feed
		err  error
	)
	switch tab {
	case "library":
		feed, err = a.service.List(ctx, query, req.Msg.Genre, req.Msg.Sort, page)
	case "day", "week", "new":
		heading = map[string]string{
			"day":  "Trending today.",
			"week": "This week’s conversation.",
			"new":  "Fresh on the scene.",
		}[tab]
		subtitle = "Worldwide movie discovery, powered by TMDB."
		if tab == "new" {
			subtitle = "Movies with a primary release date in the last 30 days. Availability varies by country."
		}
		feed, err = a.service.Discover(ctx, tab, page)
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("unknown feed"))
	}
	if query != "" && tab == "library" {
		heading = "Search results"
		subtitle = "Matches from the YTS movie catalog."
	}
	resp := &bigflixv1.ListMoviesResponse{
		Page:     int32(page),
		Heading:  heading,
		Subtitle: subtitle,
		HasTmdb:  a.service.HasTMDB(),
	}
	if err != nil {
		if errors.Is(err, catalog.ErrConfiguration) {
			resp.ErrorMessage = friendly(err)
			return connect.NewResponse(resp), nil
		}
		return nil, connect.NewError(connect.CodeUnavailable, errors.New(friendly(err)))
	}
	movies := make([]*bigflixv1.Movie, 0, len(feed.Movies))
	for _, m := range feed.Movies {
		movies = append(movies, movieToProto(m))
	}
	limit := 24
	if tab != "library" {
		limit = 20
	}
	resp.Movies = movies
	resp.Count = int32(feed.Count)
	resp.HasPrev = page > 1
	resp.HasNext = page*limit < feed.Count && len(feed.Movies) > 0
	resp.UpdatedAt = formatTime(feed.Updated)
	resp.Stale = feed.Stale
	resp.Notice = feed.Notice
	return connect.NewResponse(resp), nil
}

func (a *App) GetMovie(ctx context.Context, req *connect.Request[bigflixv1.GetMovieRequest]) (*connect.Response[bigflixv1.GetMovieResponse], error) {
	id := int(req.Msg.Id)
	if id <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid movie id"))
	}
	m, at, stale, err := a.service.Detail(ctx, id)
	if err != nil {
		if errors.Is(err, catalog.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New(friendly(err)))
		}
		return nil, connect.NewError(connect.CodeUnavailable, errors.New(friendly(err)))
	}
	return connect.NewResponse(&bigflixv1.GetMovieResponse{
		Movie:     movieToProto(m),
		UpdatedAt: formatTime(at),
		Stale:     stale,
	}), nil
}

func (a *App) FindSources(ctx context.Context, req *connect.Request[bigflixv1.FindSourcesRequest]) (*connect.Response[bigflixv1.FindSourcesResponse], error) {
	id := int(req.Msg.TmdbId)
	if id <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid TMDB id"))
	}
	f, err := a.service.FindSources(ctx, id)
	if err != nil {
		if errors.Is(err, catalog.ErrConfiguration) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New(friendly(err)))
		}
		return nil, connect.NewError(connect.CodeUnavailable, errors.New(friendly(err)))
	}
	movies := make([]*bigflixv1.Movie, 0, len(f.Movies))
	for _, m := range f.Movies {
		movies = append(movies, movieToProto(m))
	}
	return connect.NewResponse(&bigflixv1.FindSourcesResponse{
		Movies: movies,
		Count:  int32(f.Count),
		Notice: f.Notice,
	}), nil
}

func (a *App) ProbeMedia(ctx context.Context, req *connect.Request[bigflixv1.ProbeMediaRequest]) (*connect.Response[bigflixv1.ProbeMediaResponse], error) {
	fileIdx := -1
	if req.Msg.FileIndex != nil && *req.Msg.FileIndex >= 0 {
		fileIdx = int(*req.Msg.FileIndex)
	}
	p, err := a.probeMediaForFile(ctx, strings.ToLower(strings.TrimSpace(req.Msg.Hash)), fileIdx)
	if err != nil {
		switch {
		case errors.Is(err, downloads.ErrNotFound):
			return nil, connect.NewError(connect.CodeNotFound, err)
		case errors.Is(err, errFFmpegMissing), errors.Is(err, errProbeWait):
			return nil, connect.NewError(connect.CodeUnavailable, err)
		default:
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}
	audioTracks := make([]*bigflixv1.EmbeddedAudioTrack, 0, len(p.AudioTracks))
	for _, tr := range p.AudioTracks {
		audioTracks = append(audioTracks, &bigflixv1.EmbeddedAudioTrack{
			StreamIndex: int32(tr.StreamIndex),
			Language:    tr.Language,
			Label:       tr.Label,
			Codec:       tr.Codec,
			Channels:    int32(tr.Channels),
			IsDefault:   tr.Default,
		})
	}
	return connect.NewResponse(&bigflixv1.ProbeMediaResponse{
		Duration:    p.Duration,
		Video:       p.Video,
		Codecs:      p.Codecs,
		Width:       int32(p.Width),
		Height:      int32(p.Height),
		Audio:       p.Audio,
		AudioTracks: audioTracks,
	}), nil
}

func (a *App) SaveNetworkSettings(ctx context.Context, req *connect.Request[bigflixv1.SaveNetworkSettingsRequest]) (*connect.Response[bigflixv1.SaveNetworkSettingsResponse], error) {
	if a.engine == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("download engine unavailable"))
	}
	if a.restart == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("network changes require the main application"))
	}
	if req.Msg.InterfaceSettings {
		bindings := make([]downloads.InterfaceBinding, 0, len(req.Msg.Bindings))
		for _, b := range req.Msg.Bindings {
			port := int(b.Port)
			if port != 0 && (port < 1024 || port > 65535) {
				return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("fixed ports must be 1024–65535"))
			}
			bindings = append(bindings, downloads.InterfaceBinding{Name: b.Name, Port: port})
		}
		if err := a.engine.SaveInterfaceSettings(ctx, bindings); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
	} else {
		port := 0
		switch req.Msg.Mode {
		case "dynamic":
		case "fixed":
			port = int(req.Msg.Port)
			if port < 1024 || port > 65535 {
				return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("fixed port must be 1024–65535"))
			}
		default:
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("choose fixed or dynamic mode"))
		}
		if err := a.engine.SaveNetworkSettings(ctx, port); err != nil {
			return nil, connect.NewError(connect.CodeInternal, errors.New("could not save network settings"))
		}
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		a.restart()
	}()
	return connect.NewResponse(&bigflixv1.SaveNetworkSettingsResponse{Restarting: true}), nil
}
