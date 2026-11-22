package web

import (
	"bigflix/internal/database"
	bigflixv1 "bigflix/internal/gen/bigflix/v1"
	"context"
	"errors"
	"net/url"
	"strings"

	"connectrpc.com/connect"
)

func (a *App) GetSettings(ctx context.Context, _ *connect.Request[bigflixv1.GetSettingsRequest]) (*connect.Response[bigflixv1.GetSettingsResponse], error) {
	resp := &bigflixv1.GetSettingsResponse{
		HasTmdb:   a.service != nil && a.service.HasTMDB(),
		HasEngine: a.engine != nil,
	}

	if a.aggregator != nil {
		for _, h := range a.aggregator.HealthSnapshot(ctx) {
			resp.Indexers = append(resp.Indexers, &bigflixv1.IndexerSetting{
				Id:           h.ID,
				Name:         h.Name,
				Enabled:      h.Enabled,
				BaseUrl:      h.BaseURL,
				Health:       h.Health,
				LastError:    h.LastError,
				AvgLatencyMs: int32(h.AvgLatencyMs),
			})
		}
	}

	if a.store != nil {
		srcCfg := a.store.LoadSourcesConfig(ctx)
		resp.Torznab = &bigflixv1.TorznabSetting{
			Enabled:   srcCfg.Torznab.Enabled,
			Url:       srcCfg.Torznab.URL,
			HasApiKey: strings.TrimSpace(srcCfg.Torznab.APIKey) != "",
		}
		prefs := a.store.LoadPlaybackPrefs(ctx)
		resp.Playback = &bigflixv1.PlaybackPreferences{
			TargetQuality:     prefs.TargetQuality,
			PreferDirectPlay:  prefs.PreferDirectPlay,
			PreferredLanguage: prefs.PreferredLanguage,
			AutoEvict:         prefs.AutoEvict,
		}
		aiCfg := a.store.LoadAIConfig(ctx)
		resp.Ai = &bigflixv1.AISetting{
			Enabled:    aiCfg.Enabled,
			ApiUrl:     aiCfg.APIURL,
			Model:      aiCfg.Model,
			HasApiKey:  strings.TrimSpace(aiCfg.APIKey) != "",
			TimeoutSec: int32(aiCfg.TimeoutSec),
		}
		subCfg := a.store.LoadSubtitleProviderConfig(ctx)
		resp.Subtitles = &bigflixv1.SubtitleProviderSetting{
			HasSubdlKey:         strings.TrimSpace(subCfg.SubDLAPIKey) != "",
			HasOpensubtitlesKey: strings.TrimSpace(subCfg.OpenSubtitlesAPIKey) != "",
		}
	}

	if a.engine == nil {
		return connect.NewResponse(resp), nil
	}
	used, budget := a.engine.StorageUsage()
	resp.StorageUsedBytes = used
	resp.StorageBudgetBytes = budget

	network, err := a.engine.NetworkSettings()
	resp.AllInterfaces = network.All
	resp.Port = int32(network.Port)
	resp.ActualPort = int32(network.ActualPort)
	resp.Errors = append(resp.Errors, network.Errors...)
	if err != nil {
		resp.Errors = append(resp.Errors, "Unable to read network interfaces")
	}
	for _, item := range network.Interfaces {
		resp.Interfaces = append(resp.Interfaces, &bigflixv1.NetworkInterface{
			Name:      item.Name,
			Up:        item.Up,
			Addresses: item.Addresses,
			Selected:  item.Selected,
			Port:      int32(item.Port),
			Listeners: item.Listeners,
		})
	}
	return connect.NewResponse(resp), nil
}

func validateHTTPURL(raw string, requireHTTPS bool) (string, error) {
	clean := strings.TrimSpace(raw)
	if clean == "" {
		return "", nil
	}
	u, err := url.Parse(clean)
	if err != nil || u.Host == "" || u.User != nil {
		return "", errors.New("invalid URL")
	}
	if requireHTTPS && u.Scheme != "https" {
		return "", errors.New("public indexer URL must use https://")
	}
	if !requireHTTPS && u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("URL must use http:// or https://")
	}
	return clean, nil
}

func (a *App) SaveAppSettings(ctx context.Context, req *connect.Request[bigflixv1.SaveAppSettingsRequest]) (*connect.Response[bigflixv1.SaveAppSettingsResponse], error) {
	if a.store == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("settings store unavailable"))
	}

	if req.Msg.TmdbReadToken != nil && a.service != nil {
		if err := a.service.SetTMDBToken(ctx, *req.Msg.TmdbReadToken); err != nil {
			return nil, connect.NewError(connect.CodeInternal, errors.New("could not save TMDB token"))
		}
	}

	invalidateSources := false
	if len(req.Msg.Indexers) > 0 || req.Msg.Torznab != nil {
		cur := a.store.LoadSourcesConfig(ctx)
		if len(req.Msg.Indexers) > 0 {
			byID := map[string]*bigflixv1.IndexerSetting{}
			for _, ix := range req.Msg.Indexers {
				byID[ix.Id] = ix
			}
			for i := range cur.Indexers {
				if in, ok := byID[cur.Indexers[i].ID]; ok {
					cleanURL, err := validateHTTPURL(in.BaseUrl, true)
					if err != nil {
						return nil, connect.NewError(connect.CodeInvalidArgument, errors.New(cur.Indexers[i].Name+": "+err.Error()))
					}
					cur.Indexers[i].Enabled = in.Enabled
					if cleanURL != "" {
						cur.Indexers[i].BaseURL = cleanURL
					}
				}
			}
		}
		if req.Msg.Torznab != nil {
			cleanURL, err := validateHTTPURL(req.Msg.Torznab.Url, false)
			if err != nil {
				return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("Torznab URL: "+err.Error()))
			}
			cur.Torznab.Enabled = req.Msg.Torznab.Enabled && cleanURL != ""
			cur.Torznab.URL = cleanURL
			if strings.TrimSpace(req.Msg.Torznab.ApiKey) != "" {
				cur.Torznab.APIKey = strings.TrimSpace(req.Msg.Torznab.ApiKey)
			}
		}
		if err := a.store.PutConfig(ctx, "sources", cur); err != nil {
			return nil, connect.NewError(connect.CodeInternal, errors.New("could not save indexer settings"))
		}
		if a.aggregator != nil {
			a.aggregator.ResetHealth()
		}
		invalidateSources = true
	}

	if req.Msg.Playback != nil {
		tq := req.Msg.Playback.TargetQuality
		if tq != "720p" && tq != "1080p" && tq != "2160p" {
			tq = "1080p"
		}
		lang := strings.TrimSpace(req.Msg.Playback.PreferredLanguage)
		if lang == "" {
			lang = "original"
		}
		if err := a.store.PutConfig(ctx, "playback", database.PlaybackPrefs{
			TargetQuality:     tq,
			PreferDirectPlay:  req.Msg.Playback.PreferDirectPlay,
			PreferredLanguage: lang,
			AutoEvict:         req.Msg.Playback.AutoEvict,
		}); err != nil {
			return nil, connect.NewError(connect.CodeInternal, errors.New("could not save playback preferences"))
		}
		invalidateSources = true
	}

	if req.Msg.Ai != nil {
		curAI := a.store.LoadAIConfig(ctx)
		cleanURL, err := validateHTTPURL(req.Msg.Ai.ApiUrl, false)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("AI API URL: "+err.Error()))
		}
		timeout := int(req.Msg.Ai.TimeoutSec)
		if timeout < 2 || timeout > 60 {
			timeout = 12
		}
		model := strings.TrimSpace(req.Msg.Ai.Model)
		if model == "" {
			model = "Qwen2.5-1.5B-Instruct"
		}
		nextAI := database.AIConfig{
			Enabled:    req.Msg.Ai.Enabled && cleanURL != "",
			APIURL:     cleanURL,
			Model:      model,
			APIKey:     curAI.APIKey,
			TimeoutSec: timeout,
		}
		if strings.TrimSpace(req.Msg.Ai.ApiKey) != "" {
			nextAI.APIKey = strings.TrimSpace(req.Msg.Ai.ApiKey)
		}
		if err := a.store.PutConfig(ctx, "ai", nextAI); err != nil {
			return nil, connect.NewError(connect.CodeInternal, errors.New("could not save AI settings"))
		}
		invalidateSources = true
	}

	if req.Msg.Subtitles != nil {
		curSub := a.store.LoadSubtitleProviderConfig(ctx)
		if strings.TrimSpace(req.Msg.Subtitles.SubdlApiKey) != "" {
			curSub.SubDLAPIKey = strings.TrimSpace(req.Msg.Subtitles.SubdlApiKey)
		}
		if strings.TrimSpace(req.Msg.Subtitles.OpensubtitlesApiKey) != "" {
			curSub.OpenSubtitlesAPIKey = strings.TrimSpace(req.Msg.Subtitles.OpensubtitlesApiKey)
		}
		if err := a.store.PutConfig(ctx, "subtitles", curSub); err != nil {
			return nil, connect.NewError(connect.CodeInternal, errors.New("could not save subtitle provider keys"))
		}
	}

	if invalidateSources {
		_ = a.store.DeleteSnapshotPrefix(ctx, "resolved:")
	}

	return connect.NewResponse(&bigflixv1.SaveAppSettingsResponse{
		HasTmdb: a.service != nil && a.service.HasTMDB(),
	}), nil
}

func (a *App) TestAIConnection(ctx context.Context, req *connect.Request[bigflixv1.TestAIConnectionRequest]) (*connect.Response[bigflixv1.TestAIConnectionResponse], error) {
	if a.classifier == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("AI classifier unavailable"))
	}
	apiURL := strings.TrimSpace(req.Msg.ApiUrl)
	model := strings.TrimSpace(req.Msg.Model)
	apiKey := strings.TrimSpace(req.Msg.ApiKey)
	if a.store != nil {
		saved := a.store.LoadAIConfig(ctx)
		if apiURL == "" {
			apiURL = saved.APIURL
		}
		if model == "" {
			model = saved.Model
		}
		if apiKey == "" {
			apiKey = saved.APIKey
		}
	}
	latency, msg, err := a.classifier.TestConnection(ctx, apiURL, model, apiKey)
	if err != nil {
		return connect.NewResponse(&bigflixv1.TestAIConnectionResponse{
			Ok:        false,
			Message:   err.Error(),
			LatencyMs: int32(latency),
		}), nil
	}
	return connect.NewResponse(&bigflixv1.TestAIConnectionResponse{
		Ok:        true,
		Message:   msg,
		LatencyMs: int32(latency),
	}), nil
}
