package web

import (
	"bigflix/internal/catalog"
	"bigflix/internal/downloads"
	bigflixv1 "bigflix/internal/gen/bigflix/v1"
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
)

func (a *App) downloadRoutes(mux *http.ServeMux) {
	mux.Handle("GET /media/{hash}", a.requireMediaAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.engine == nil {
			http.Error(w, "Download engine unavailable", http.StatusServiceUnavailable)
			return
		}
		a.engine.Stream(w, r, r.PathValue("hash"))
	})))
}

func downloadErr(err error) error {
	switch {
	case errors.Is(err, downloads.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, downloads.ErrPaused), errors.Is(err, downloads.ErrNotReady):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	default:
		return connect.NewError(connect.CodeInvalidArgument, err)
	}
}

func (a *App) ListDownloads(ctx context.Context, _ *connect.Request[bigflixv1.ListDownloadsRequest]) (*connect.Response[bigflixv1.ListDownloadsResponse], error) {
	if a.engine == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("download engine unavailable"))
	}
	list := a.engine.List()
	out := make([]*bigflixv1.DownloadStatus, 0, len(list))
	for _, item := range list {
		out = append(out, statusToProto(item))
	}
	used, budget := a.engine.StorageUsage()
	autoEvict := true
	if a.store != nil {
		autoEvict = a.store.LoadPlaybackPrefs(ctx).AutoEvict
	}
	return connect.NewResponse(&bigflixv1.ListDownloadsResponse{
		Downloads:          out,
		StorageUsedBytes:   used,
		StorageBudgetBytes: budget,
		AutoEvict:          autoEvict,
	}), nil
}

func (a *App) GetDownload(ctx context.Context, req *connect.Request[bigflixv1.GetDownloadRequest]) (*connect.Response[bigflixv1.GetDownloadResponse], error) {
	if a.engine == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("download engine unavailable"))
	}
	s, err := a.engine.Get(strings.ToLower(req.Msg.Hash))
	if err != nil {
		return nil, downloadErr(err)
	}
	return connect.NewResponse(&bigflixv1.GetDownloadResponse{Download: statusToProto(s)}), nil
}

func (a *App) WatchDownload(ctx context.Context, req *connect.Request[bigflixv1.WatchDownloadRequest], stream *connect.ServerStream[bigflixv1.WatchDownloadResponse]) error {
	if a.engine == nil {
		return connect.NewError(connect.CodeUnavailable, errors.New("download engine unavailable"))
	}
	hash := strings.ToLower(req.Msg.Hash)
	if _, err := a.engine.Get(hash); err != nil {
		return downloadErr(err)
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		status, err := a.engine.Get(hash)
		if err != nil {
			return downloadErr(err)
		}
		if err := stream.Send(&bigflixv1.WatchDownloadResponse{Download: statusToProto(status)}); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}

func (a *App) StartDownload(ctx context.Context, req *connect.Request[bigflixv1.StartDownloadRequest]) (*connect.Response[bigflixv1.StartDownloadResponse], error) {
	if a.engine == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("download engine unavailable"))
	}
	id := int(req.Msg.MovieId)
	if id <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid movie"))
	}
	m, _, _, err := a.service.Detail(ctx, id)
	if err != nil {
		if errors.Is(err, catalog.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("movie provider unavailable"))
	}
	hash := strings.ToLower(strings.TrimSpace(req.Msg.Hash))
	found := false
	for _, t := range m.Torrents {
		if strings.EqualFold(t.Hash, hash) {
			found = true
			break
		}
	}
	if !found {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("source is not listed for this movie"))
	}
	s, err := a.engine.Add(hash, m.Title, m.Poster, m.ID, req.Msg.Keep)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&bigflixv1.StartDownloadResponse{Download: statusToProto(s)}), nil
}

func (a *App) UpdateDownload(ctx context.Context, req *connect.Request[bigflixv1.UpdateDownloadRequest]) (*connect.Response[bigflixv1.UpdateDownloadResponse], error) {
	if a.engine == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("download engine unavailable"))
	}
	hash := strings.ToLower(strings.TrimSpace(req.Msg.Hash))
	if req.Msg.Action == "delete" {
		if err := a.engine.Delete(hash); err != nil {
			return nil, downloadErr(err)
		}
		clearProbeCache(hash)
		return connect.NewResponse(&bigflixv1.UpdateDownloadResponse{}), nil
	}
	if err := a.engine.UpdateWithDuration(hash, req.Msg.Action, int(req.Msg.FileIndex), req.Msg.Position, req.Msg.Duration); err != nil {
		return nil, downloadErr(err)
	}
	s, err := a.engine.Get(hash)
	if err != nil {
		return nil, downloadErr(err)
	}
	return connect.NewResponse(&bigflixv1.UpdateDownloadResponse{Download: statusToProto(s)}), nil
}

func (a *App) DeleteDownload(ctx context.Context, req *connect.Request[bigflixv1.DeleteDownloadRequest]) (*connect.Response[bigflixv1.DeleteDownloadResponse], error) {
	if a.engine == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("download engine unavailable"))
	}
	hash := strings.ToLower(strings.TrimSpace(req.Msg.Hash))
	if err := a.engine.Delete(hash); err != nil {
		return nil, downloadErr(err)
	}
	clearProbeCache(hash)
	return connect.NewResponse(&bigflixv1.DeleteDownloadResponse{}), nil
}
