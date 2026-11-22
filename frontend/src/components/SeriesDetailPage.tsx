import { useEffect, useState } from "react";
import { client, errMessage, formatTime } from "../api";
import type {
  Episode,
  GetSeasonDetailResponse,
  GetSeriesDetailResponse,
} from "../gen/bigflix/v1/bigflix_pb";
import type { NavigateFn } from "../types";
import {
  ArrowLeftIcon,
  CheckIcon,
  DownloadIcon,
  PlayIcon,
  SlidersIcon,
} from "./Icons";
import { SourcesDrawer } from "./SourcesDrawer";
import { Badge, Button, EmptyState, ProgressBar } from "./ui";

interface SeriesDetailPageProps {
  tmdbId: number;
  navigate: NavigateFn;
}

function epCode(s: number, e: number): string {
  return `S${String(s).padStart(2, "0")}E${String(e).padStart(2, "0")}`;
}

export function SeriesDetailPage({ tmdbId, navigate }: SeriesDetailPageProps) {
  const [seriesRes, setSeriesRes] = useState<GetSeriesDetailResponse | null>(null);
  const [selectedSeason, setSelectedSeason] = useState<number>(1);
  const [seasonRes, setSeasonRes] = useState<GetSeasonDetailResponse | null>(null);
  const [loadingSeries, setLoadingSeries] = useState(true);
  const [loadingSeason, setLoadingSeason] = useState(false);
  const [error, setError] = useState("");
  const [playError, setPlayError] = useState("");
  const [startingKey, setStartingKey] = useState("");
  const [posterBroken, setPosterBroken] = useState(false);
  const [sourcesForEp, setSourcesForEp] = useState<Episode | null>(null);

  useEffect(() => {
    let active = true;
    setLoadingSeries(true);
    setError("");
    setPosterBroken(false);
    setSourcesForEp(null);

    client
      .getSeriesDetail({ tmdbId })
      .then((res) => {
        if (!active) return;
        setSeriesRes(res);
        const initialSeason =
          res.nextUp?.seasonNumber ||
          res.series?.seasons?.[0]?.seasonNumber ||
          1;
        setSelectedSeason(initialSeason);
      })
      .catch((err) => {
        if (active) setError(errMessage(err));
      })
      .finally(() => {
        if (active) setLoadingSeries(false);
      });

    return () => {
      active = false;
    };
  }, [tmdbId]);

  useEffect(() => {
    if (!seriesRes?.series) return;
    let active = true;
    setLoadingSeason(true);
    client
      .getSeasonDetail({ tmdbId, seasonNumber: selectedSeason })
      .then((res) => {
        if (active) setSeasonRes(res);
      })
      .catch((err) => {
        if (active) setPlayError(errMessage(err));
      })
      .finally(() => {
        if (active) setLoadingSeason(false);
      });
    return () => {
      active = false;
    };
  }, [tmdbId, selectedSeason, seriesRes?.series]);

  async function playEpisode(season: number, episode: number, keep: boolean, overrideHash = "") {
    const key = `${season}:${episode}:${keep ? "keep" : "play"}`;
    setStartingKey(key);
    setPlayError("");
    try {
      const res = await client.startPlayback({
        mediaType: "episode",
        tmdbId,
        seasonNumber: season,
        episodeNumber: episode,
        keep,
        overrideHash,
      });
      if (res.download?.hash) {
        const fileQs = res.fileIndex >= 0 ? `?file=${res.fileIndex}` : "";
        navigate(`/watch/${res.download.hash}${fileQs}`);
        return;
      }
    } catch (err) {
      setPlayError(errMessage(err));
      setStartingKey("");
    }
  }

  const series = seriesRes?.series;
  const nextUp = seriesRes?.nextUp;

  return (
    <div>
      <a
        href="/series"
        className="ds-back"
        onClick={(e) => {
          e.preventDefault();
          navigate("/series");
        }}
      >
        <ArrowLeftIcon size={16} /> TV Series
      </a>

      {loadingSeries ? (
        <EmptyState title="Loading TV series…" />
      ) : error && !series ? (
        <EmptyState
          title="Series unavailable"
          description={error}
          actions={<Button onClick={() => navigate("/series")}>Browse TV series</Button>}
        />
      ) : (
        series && (
          <>
            <section className="detail-hero">
              {(series.backdrop || series.poster) && !posterBroken && (
                <div
                  className="detail-backdrop"
                  style={{ backgroundImage: `url(${series.backdrop || series.poster})` }}
                  aria-hidden="true"
                />
              )}
              <div className="detail-hero-grid">
                <div className="detail-poster poster-frame">
                  {series.poster && !posterBroken && (
                    <img
                      src={series.poster}
                      alt={`${series.title} poster`}
                      onError={() => setPosterBroken(true)}
                    />
                  )}
                  <div className="poster-fallback">
                    <strong>{series.title}</strong>
                  </div>
                </div>

                <div className="detail-copy">
                  <h1>{series.title}</h1>

                  <div className="detail-facts">
                    {series.rating > 0 && (
                      <Badge tone="accent" className="mono">
                        ★ {series.rating.toFixed(1)}
                      </Badge>
                    )}
                    {!!series.year && <Badge className="mono">{series.year}</Badge>}
                    {series.seasonCount > 0 && (
                      <Badge className="mono">
                        {series.seasonCount} {series.seasonCount === 1 ? "Season" : "Seasons"}
                      </Badge>
                    )}
                    {series.status && <Badge>{series.status}</Badge>}
                    {series.language && <Badge>{series.language.toUpperCase()}</Badge>}
                    {series.imdbCode && (
                      <a
                        className="ds-link"
                        href={`https://www.imdb.com/title/${series.imdbCode}/`}
                        target="_blank"
                        rel="noreferrer"
                      >
                        IMDb ↗
                      </a>
                    )}
                  </div>

                  {series.genres.length > 0 && (
                    <div className="detail-genres">
                      {series.genres.map((g) => (
                        <Badge key={g}>{g}</Badge>
                      ))}
                    </div>
                  )}

                  {series.overview && <p className="detail-synopsis">{series.overview}</p>}

                  {nextUp && (
                    <div className="detail-cta-row">
                      <Button
                        disabled={startingKey !== ""}
                        onClick={() =>
                          void playEpisode(nextUp.seasonNumber, nextUp.episodeNumber, false)
                        }
                      >
                        <PlayIcon size={16} />
                        {startingKey === `${nextUp.seasonNumber}:${nextUp.episodeNumber}:play`
                          ? "Finding best source…"
                          : nextUp.isResume
                            ? `Resume ${epCode(nextUp.seasonNumber, nextUp.episodeNumber)}`
                            : `Play ${epCode(nextUp.seasonNumber, nextUp.episodeNumber)}`}
                      </Button>
                    </div>
                  )}
                </div>
              </div>
            </section>

            {playError && (
              <div className="ds-alert ds-alert-danger" role="alert">
                {playError}
              </div>
            )}

            <section className="episodes-section">
              <div className="season-bar">
                <div className="feed-scroll">
                  <div className="ds-seg" role="tablist" aria-label="Seasons">
                    {series.seasons.map((sn) => (
                      <button
                        key={sn.seasonNumber}
                        type="button"
                        role="tab"
                        aria-selected={selectedSeason === sn.seasonNumber}
                        className={`ds-seg-item ${selectedSeason === sn.seasonNumber ? "active" : ""}`}
                        onClick={() => setSelectedSeason(sn.seasonNumber)}
                      >
                        {sn.seasonNumber === 0 ? "Specials" : `Season ${sn.seasonNumber}`}
                      </button>
                    ))}
                  </div>
                </div>
                {seasonRes?.episodes?.length ? (
                  <Badge className="mono">{seasonRes.episodes.length} episodes</Badge>
                ) : null}
              </div>

              {loadingSeason && !seasonRes ? (
                <EmptyState title="Loading episodes…" />
              ) : seasonRes?.episodes.length ? (
                <div className="episode-list">
                  {seasonRes.episodes.map((ep) => {
                    const playK = `${ep.seasonNumber}:${ep.episodeNumber}:play`;
                    const keepK = `${ep.seasonNumber}:${ep.episodeNumber}:keep`;
                    const pct =
                      ep.duration > 0
                        ? Math.min(1, Math.max(0, ep.position / ep.duration))
                        : ep.completed
                          ? 1
                          : 0;
                    const isBusy = startingKey === playK || startingKey === keepK;
                    return (
                      <article key={ep.episodeNumber} className="episode-card">
                        <button
                          type="button"
                          className="episode-still"
                          disabled={startingKey !== ""}
                          onClick={() => void playEpisode(ep.seasonNumber, ep.episodeNumber, false)}
                          aria-label={`Play ${epCode(ep.seasonNumber, ep.episodeNumber)} ${ep.name}`}
                        >
                          {ep.still ? (
                            <img src={ep.still} alt="" loading="lazy" referrerPolicy="no-referrer" />
                          ) : (
                            <span className="episode-still-fallback mono">
                              {epCode(ep.seasonNumber, ep.episodeNumber)}
                            </span>
                          )}
                          <span className="episode-still-play">
                            <PlayIcon size={18} />
                          </span>
                          {pct > 0 && (
                            <div className="episode-still-progress">
                              <ProgressBar value={pct} label="Episode progress" />
                            </div>
                          )}
                        </button>

                        <div className="episode-body">
                          <div className="episode-head">
                            <div className="episode-title-wrap">
                              <span className="mono episode-code">
                                {epCode(ep.seasonNumber, ep.episodeNumber)}
                              </span>
                              <h3>{ep.name || `Episode ${ep.episodeNumber}`}</h3>
                              {ep.completed && (
                                <Badge tone="ok">
                                  <CheckIcon size={11} /> Watched
                                </Badge>
                              )}
                              {ep.downloadHash && <Badge tone="accent">Cached</Badge>}
                            </div>
                            <div className="episode-meta mono">
                              {ep.runtime > 0 && <span>{ep.runtime}m</span>}
                              {ep.airDate && <span>{ep.airDate}</span>}
                            </div>
                          </div>

                          {ep.overview && <p className="episode-overview">{ep.overview}</p>}

                          <div className="episode-actions">
                            <Button
                              size="sm"
                              disabled={startingKey !== ""}
                              onClick={() =>
                                void playEpisode(ep.seasonNumber, ep.episodeNumber, false)
                              }
                            >
                              <PlayIcon size={13} />
                              {startingKey === playK
                                ? "Finding source…"
                                : pct > 0 && !ep.completed
                                  ? "Resume"
                                  : "Play"}
                            </Button>
                            <Button
                              variant="secondary"
                              size="sm"
                              disabled={startingKey !== ""}
                              onClick={() =>
                                void playEpisode(ep.seasonNumber, ep.episodeNumber, true)
                              }
                            >
                              <DownloadIcon size={13} />
                              {startingKey === keepK ? "Starting…" : "Keep"}
                            </Button>
                            <Button
                              variant="ghost"
                              size="sm"
                              disabled={isBusy}
                              onClick={() => setSourcesForEp(ep)}
                              title="Inspect or choose torrent source manually"
                            >
                              <SlidersIcon size={13} /> Sources
                            </Button>
                          </div>
                        </div>
                      </article>
                    );
                  })}
                </div>
              ) : (
                <EmptyState title="No episodes listed for this season" />
              )}
            </section>

            {sourcesForEp && (
              <SourcesDrawer
                mediaType="episode"
                tmdbId={tmdbId}
                seasonNumber={sourcesForEp.seasonNumber}
                episodeNumber={sourcesForEp.episodeNumber}
                activeHash={sourcesForEp.downloadHash}
                title={`${series.title} · ${epCode(sourcesForEp.seasonNumber, sourcesForEp.episodeNumber)} Sources`}
                onSelect={async (hash, keep) => {
                  await playEpisode(
                    sourcesForEp.seasonNumber,
                    sourcesForEp.episodeNumber,
                    keep,
                    hash,
                  );
                }}
                onClose={() => setSourcesForEp(null)}
              />
            )}

            {seriesRes?.stale && (
              <div className="ds-alert ds-alert-warn">
                Cached metadata · updated {formatTime(seriesRes.updatedAt)}
              </div>
            )}
          </>
        )
      )}
    </div>
  );
}
