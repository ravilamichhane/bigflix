package web

import (
	"bigflix/internal/downloads"
	bigflixv1 "bigflix/internal/gen/bigflix/v1"
	"bigflix/internal/subtitles"
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"connectrpc.com/connect"
)

func (a *App) subtitleService(hash string) (*subtitles.Service, error) {
	if a.engine == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("download engine unavailable"))
	}
	if _, err := a.engine.Get(hash); err != nil {
		return nil, downloadErr(err)
	}
	return a.engine.SubtitleService(), nil
}

func (a *App) ListSubtitles(ctx context.Context, req *connect.Request[bigflixv1.ListSubtitlesRequest]) (*connect.Response[bigflixv1.ListSubtitlesResponse], error) {
	hash := strings.ToLower(strings.TrimSpace(req.Msg.Hash))
	s, err := a.subtitleService(hash)
	if err != nil {
		return nil, err
	}
	list, err := s.DB.Subtitles(ctx, hash)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("cannot read subtitles"))
	}
	tracks := make([]*bigflixv1.SubtitleTrack, 0, len(list))
	for _, t := range list {
		tracks = append(tracks, &bigflixv1.SubtitleTrack{
			Id:       t.ID,
			Label:    t.Label,
			Language: t.Language,
		})
	}
	bundledFiles := a.engine.SubtitleFiles(hash)
	bundled := make([]*bigflixv1.BundledSubtitleFile, 0, len(bundledFiles))
	for _, f := range bundledFiles {
		bundled = append(bundled, &bigflixv1.BundledSubtitleFile{
			Index: int32(f.Index),
			Name:  f.Name,
		})
	}
	return connect.NewResponse(&bigflixv1.ListSubtitlesResponse{
		Tracks:     tracks,
		Bundled:    bundled,
		Configured: true,
	}), nil
}

func (a *App) GetSubtitle(ctx context.Context, req *connect.Request[bigflixv1.GetSubtitleRequest]) (*connect.Response[bigflixv1.GetSubtitleResponse], error) {
	hash := strings.ToLower(strings.TrimSpace(req.Msg.Hash))
	s, err := a.subtitleService(hash)
	if err != nil {
		return nil, err
	}
	b, err := s.DB.Subtitle(ctx, hash, req.Msg.Id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("subtitle not found"))
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&bigflixv1.GetSubtitleResponse{Vtt: string(b)}), nil
}

func (a *App) SearchSubtitles(ctx context.Context, req *connect.Request[bigflixv1.SearchSubtitlesRequest]) (*connect.Response[bigflixv1.SearchSubtitlesResponse], error) {
	hash := strings.ToLower(strings.TrimSpace(req.Msg.Hash))
	s, err := a.subtitleService(hash)
	if err != nil {
		return nil, err
	}
	movie, _ := a.engine.Get(hash)
	imdb := ""
	if movie.MovieID > 0 {
		if detail, _, _, err := a.service.Detail(ctx, movie.MovieID); err == nil {
			imdb = detail.IMDb
		}
	}
	results, err := s.Search(ctx, imdb, movie.Title, req.Msg.Language)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	// Search caches are shared; exclude downloads only for this movie.
	available := make([]*bigflixv1.SubtitleSearchResult, 0, len(results))
	for _, result := range results {
		key := subtitles.DownloadKey(movie.Hash, result.ID, result.Language)
		if _, _, err := s.DB.Get(ctx, key); err != nil {
			available = append(available, &bigflixv1.SubtitleSearchResult{
				Id:              int32(result.ID),
				Language:        result.Language,
				Label:           result.Label,
				Rating:          int32(result.Rating),
				HearingImpaired: result.HearingImpaired,
				Page:            result.Page,
			})
		}
	}
	return connect.NewResponse(&bigflixv1.SearchSubtitlesResponse{Results: available}), nil
}

func (a *App) AddSubtitle(ctx context.Context, req *connect.Request[bigflixv1.AddSubtitleRequest]) (*connect.Response[bigflixv1.AddSubtitleResponse], error) {
	hash := strings.ToLower(strings.TrimSpace(req.Msg.Hash))
	s, err := a.subtitleService(hash)
	if err != nil {
		return nil, err
	}
	language := req.Msg.Language
	if !subtitles.ValidLanguage(language) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid language"))
	}
	switch req.Msg.Action {
	case "download":
		if req.Msg.Id <= 0 {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid subtitle ID"))
		}
		sub, err := s.Download(ctx, hash, int(req.Msg.Id), req.Msg.Label, language)
		if err != nil {
			return nil, connect.NewError(connect.CodeUnavailable, err)
		}
		return connect.NewResponse(&bigflixv1.AddSubtitleResponse{
			Track: &bigflixv1.SubtitleTrack{Id: sub.ID, Label: sub.Label, Language: sub.Language},
		}), nil
	case "bundled":
		rctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		label, b, err := a.engine.ReadSubtitle(rctx, hash, int(req.Msg.Index))
		if err != nil {
			if errors.Is(err, downloads.ErrNotReady) {
				return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("subtitle pieces unavailable; retry when peers connect"))
			}
			return nil, connect.NewError(connect.CodeUnavailable, errors.New("subtitle pieces unavailable; retry when peers connect"))
		}
		sub, err := subtitles.Save(ctx, s.DB, hash, label, language, b)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		return connect.NewResponse(&bigflixv1.AddSubtitleResponse{
			Track: &bigflixv1.SubtitleTrack{Id: sub.ID, Label: sub.Label, Language: sub.Language},
		}), nil
	case "upload":
		if len(req.Msg.Data) == 0 {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("choose an SRT or VTT file"))
		}
		if len(req.Msg.Data) > subtitles.MaxSize {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("subtitle must be UTF-8 SRT/VTT, at most 2 MB"))
		}
		label := strings.TrimSpace(req.Msg.Filename)
		if label == "" {
			label = "Uploaded subtitle"
		}
		sub, err := subtitles.Save(ctx, s.DB, hash, label, language, req.Msg.Data)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		return connect.NewResponse(&bigflixv1.AddSubtitleResponse{
			Track: &bigflixv1.SubtitleTrack{Id: sub.ID, Label: sub.Label, Language: sub.Language},
		}), nil
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("unknown subtitle action"))
	}
}
