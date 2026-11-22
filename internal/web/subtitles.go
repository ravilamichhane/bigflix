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

func (a *App) subtitleService(hash string) (*subtitles.Service, downloads.Status, error) {
	if a.engine == nil {
		return nil, downloads.Status{}, connect.NewError(connect.CodeUnavailable, errors.New("download engine unavailable"))
	}
	st, err := a.engine.Get(hash)
	if err != nil {
		return nil, downloads.Status{}, downloadErr(err)
	}
	return a.engine.SubtitleService(), st, nil
}

func effectiveFileIndex(opt *int32, st downloads.Status) int {
	if opt != nil && *opt >= 0 {
		return int(*opt)
	}
	return st.FileIndex
}

func (a *App) ListSubtitles(ctx context.Context, req *connect.Request[bigflixv1.ListSubtitlesRequest]) (*connect.Response[bigflixv1.ListSubtitlesResponse], error) {
	hash := strings.ToLower(strings.TrimSpace(req.Msg.Hash))
	s, st, err := a.subtitleService(hash)
	if err != nil {
		return nil, err
	}
	fileIdx := effectiveFileIndex(req.Msg.FileIndex, st)
	list, err := s.DB.SubtitlesForFile(ctx, hash, fileIdx)
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
	bundledFiles := a.engine.SubtitleFilesForFile(hash, fileIdx)
	bundled := make([]*bigflixv1.BundledSubtitleFile, 0, len(bundledFiles))
	for _, f := range bundledFiles {
		bundled = append(bundled, &bigflixv1.BundledSubtitleFile{
			Index: int32(f.Index),
			Name:  f.Name,
		})
	}
	embeddedTracks := a.cachedEmbeddedSubtitlesForFile(ctx, hash, fileIdx)
	embedded := make([]*bigflixv1.EmbeddedSubtitleTrack, 0, len(embeddedTracks))
	for _, e := range embeddedTracks {
		embedded = append(embedded, &bigflixv1.EmbeddedSubtitleTrack{
			StreamIndex:     int32(e.StreamIndex),
			Language:        e.Language,
			Label:           e.Label,
			Codec:           e.Codec,
			IsDefault:       e.Default,
			Forced:          e.Forced,
			HearingImpaired: e.HearingImpaired,
		})
	}
	return connect.NewResponse(&bigflixv1.ListSubtitlesResponse{
		Tracks:         tracks,
		Bundled:        bundled,
		Configured:     true,
		Embedded:       embedded,
		ActiveProvider: s.ActiveProviderName(ctx, st.MediaType),
	}), nil
}

func (a *App) GetSubtitle(ctx context.Context, req *connect.Request[bigflixv1.GetSubtitleRequest]) (*connect.Response[bigflixv1.GetSubtitleResponse], error) {
	hash := strings.ToLower(strings.TrimSpace(req.Msg.Hash))
	s, st, err := a.subtitleService(hash)
	if err != nil {
		return nil, err
	}
	fileIdx := effectiveFileIndex(req.Msg.FileIndex, st)
	b, err := s.DB.SubtitleForFile(ctx, hash, fileIdx, req.Msg.Id)
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
	s, st, err := a.subtitleService(hash)
	if err != nil {
		return nil, err
	}
	imdb := st.IMDbID
	if imdb == "" && st.MovieID > 0 && a.service != nil {
		if detail, _, _, err := a.service.Detail(ctx, st.MovieID); err == nil {
			imdb = detail.IMDb
		}
	}
	if imdb == "" && st.TMDBID > 0 && a.service != nil {
		if st.MediaType == "episode" {
			if ser, _, _, err := a.service.SeriesDetail(ctx, st.TMDBID); err == nil {
				imdb = ser.IMDb
			}
		} else {
			if mov, _, _, err := a.service.MovieDetail(ctx, st.TMDBID); err == nil {
				imdb = mov.IMDb
			}
		}
	}

	mediaType := st.MediaType
	if mediaType == "" {
		mediaType = "movie"
	}
	results, provider, err := s.SearchMedia(ctx, subtitles.SearchQuery{
		IMDbID:    imdb,
		TMDBID:    st.TMDBID,
		MediaType: mediaType,
		Title:     st.Title,
		Season:    st.Season,
		Episode:   st.Episode,
		Language:  req.Msg.Language,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	available := make([]*bigflixv1.SubtitleSearchResult, 0, len(results))
	for _, result := range results {
		if result.Provider == "yify" {
			key := subtitles.DownloadKey(st.Hash, result.ID, result.Language)
			if _, _, err := s.DB.Get(ctx, key); err == nil {
				continue
			}
		}
		available = append(available, &bigflixv1.SubtitleSearchResult{
			Id:              int32(result.ID),
			Language:        result.Language,
			Label:           result.Label,
			Rating:          int32(result.Rating),
			HearingImpaired: result.HearingImpaired,
			Page:            result.Page,
			Provider:        result.Provider,
			DownloadKey:     result.DownloadKey,
		})
	}
	return connect.NewResponse(&bigflixv1.SearchSubtitlesResponse{
		Results:  available,
		Provider: provider,
	}), nil
}

func (a *App) AddSubtitle(ctx context.Context, req *connect.Request[bigflixv1.AddSubtitleRequest]) (*connect.Response[bigflixv1.AddSubtitleResponse], error) {
	hash := strings.ToLower(strings.TrimSpace(req.Msg.Hash))
	s, st, err := a.subtitleService(hash)
	if err != nil {
		return nil, err
	}
	fileIdx := effectiveFileIndex(req.Msg.FileIndex, st)
	language := req.Msg.Language
	if !subtitles.ValidLanguage(language) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid language"))
	}
	switch req.Msg.Action {
	case "download":
		if req.Msg.DownloadKey == "" && req.Msg.Id <= 0 {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid subtitle ID"))
		}
		sub, err := s.DownloadMedia(ctx, hash, fileIdx, req.Msg.DownloadKey, int(req.Msg.Id), req.Msg.Label, language)
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
		sub, err := subtitles.SaveForFile(ctx, s.DB, hash, fileIdx, label, language, b)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		return connect.NewResponse(&bigflixv1.AddSubtitleResponse{
			Track: &bigflixv1.SubtitleTrack{Id: sub.ID, Label: sub.Label, Language: sub.Language},
		}), nil
	case "embedded":
		track, vtt, err := a.extractEmbeddedSubtitleForFile(ctx, hash, fileIdx, int(req.Msg.Index))
		if err != nil {
			if errors.Is(err, errFFmpegMissing) {
				return nil, connect.NewError(connect.CodeFailedPrecondition, err)
			}
			return nil, connect.NewError(connect.CodeUnavailable, err)
		}
		lang := track.Language
		if !subtitles.ValidLanguage(lang) || lang == "und" {
			lang = language
		}
		label := "Built-in · " + track.Label
		sub, err := subtitles.SaveForFile(ctx, s.DB, hash, fileIdx, label, lang, vtt)
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
		sub, err := subtitles.SaveForFile(ctx, s.DB, hash, fileIdx, label, language, req.Msg.Data)
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
