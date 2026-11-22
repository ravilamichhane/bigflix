import { useEffect, useState } from "react";
import { client, errMessage, formatTime } from "../api";
import type { GetMovieDetailResponse, GetMovieResponse } from "../gen/bigflix/v1/bigflix_pb";
import type { NavigateFn } from "../types";
import { ArrowLeftIcon, DownloadIcon, PlayIcon, SlidersIcon } from "./Icons";
import { SourcesDrawer } from "./SourcesDrawer";
import { Badge, Button, EmptyState, ProgressBar } from "./ui";

interface DetailPageProps {
  id: number;
  ytsFallback?: boolean;
  navigate: NavigateFn;
}

function formatResume(sec: number): string {
  const m = Math.floor(sec / 60);
  const h = Math.floor(m / 60);
  if (h > 0) return `${h}h ${m % 60}m`;
  return `${m}m`;
}

export function DetailPage({ id, ytsFallback = false, navigate }: DetailPageProps) {
  const [tmdbData, setTmdbData] = useState<GetMovieDetailResponse | null>(null);
  const [ytsData, setYtsData] = useState<GetMovieResponse | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [starting, setStarting] = useState<"play" | "keep" | "">("");
  const [showSources, setShowSources] = useState(false);
  const [posterBroken, setPosterBroken] = useState(false);

  useEffect(() => {
    let active = true;
    setLoading(true);
    setError("");
    setPosterBroken(false);
    setShowSources(false);

    if (ytsFallback) {
      client
        .getMovie({ id })
        .then((res) => {
          if (active) setYtsData(res);
        })
        .catch((err) => {
          if (active) setError(errMessage(err));
        })
        .finally(() => {
          if (active) setLoading(false);
        });
    } else {
      client
        .getMovieDetail({ tmdbId: id })
        .then((res) => {
          if (active) setTmdbData(res);
        })
        .catch((err) => {
          if (active) setError(errMessage(err));
        })
        .finally(() => {
          if (active) setLoading(false);
        });
    }
    return () => {
      active = false;
    };
  }, [id, ytsFallback]);

  async function startAutoPlay(keep: boolean, overrideHash = "") {
    setStarting(keep ? "keep" : "play");
    setError("");
    try {
      if (ytsFallback && ytsData?.movie) {
        const best = ytsData.movie.torrents.find((t) => t.quality === "1080p") || ytsData.movie.torrents[0];
        if (!best) throw new Error("No torrent source listed for this movie.");
        const res = await client.startDownload({
          movieId: ytsData.movie.id,
          hash: overrideHash || best.hash,
          keep,
        });
        if (res.download?.hash) {
          navigate(`/watch/${res.download.hash}`);
        }
        return;
      }
      const res = await client.startPlayback({
        mediaType: "movie",
        tmdbId: id,
        keep,
        overrideHash,
      });
      if (res.download?.hash) {
        navigate(`/watch/${res.download.hash}`);
      }
    } catch (err) {
      setError(errMessage(err));
      setStarting("");
    }
  }

  const movie = tmdbData?.movie;
  const legacy = ytsData?.movie;
  const title = movie?.title || legacy?.title || "";
  const poster = movie?.poster || legacy?.poster || "";
  const backdrop = movie?.backdrop || poster;
  const rating = movie?.rating || legacy?.rating || 0;
  const year = movie?.year || legacy?.year || 0;
  const runtime = movie?.runtime || legacy?.runtime || 0;
  const language = movie?.language || legacy?.language || "";
  const imdb = movie?.imdbCode || legacy?.imdbCode || "";
  const genres = movie?.genres || legacy?.genres || [];
  const overview = movie?.overview || legacy?.summary || "";
  const progress = tmdbData?.progress;
  const hasResume = !!progress && !progress.completed && progress.position > 15;
  const progressPct =
    progress && progress.duration > 0
      ? Math.min(1, Math.max(0, progress.position / progress.duration))
      : 0;

  return (
    <div>
      <a
        href="/movies"
        className="ds-back"
        onClick={(e) => {
          e.preventDefault();
          navigate("/movies");
        }}
      >
        <ArrowLeftIcon size={16} /> Movies
      </a>

      {loading ? (
        <EmptyState title="Loading movie…" />
      ) : error && !title ? (
        <EmptyState
          title="Movie unavailable"
          description={error}
          actions={<Button onClick={() => navigate("/movies")}>Browse movies</Button>}
        />
      ) : (
        title && (
          <>
            <section className="detail-hero">
              {backdrop && !posterBroken && (
                <div
                  className="detail-backdrop"
                  style={{ backgroundImage: `url(${backdrop})` }}
                  aria-hidden="true"
                />
              )}
              <div className="detail-hero-grid">
                <div className="detail-poster poster-frame">
                  {poster && !posterBroken && (
                    <img
                      src={poster}
                      alt={`${title} poster`}
                      onError={() => setPosterBroken(true)}
                    />
                  )}
                  <div className="poster-fallback">
                    <strong>{title}</strong>
                  </div>
                </div>

                <div className="detail-copy">
                  <h1>{title}</h1>

                  <div className="detail-facts">
                    {rating > 0 && (
                      <Badge tone="accent" className="mono">
                        ★ {rating.toFixed(1)}
                      </Badge>
                    )}
                    {!!year && <Badge className="mono">{year}</Badge>}
                    {!!runtime && <Badge className="mono">{runtime}m</Badge>}
                    {language && <Badge>{language.toUpperCase()}</Badge>}
                    {tmdbData?.activeDownloadHash && <Badge tone="ok">In library</Badge>}
                    {imdb && (
                      <a
                        className="ds-link"
                        href={`https://www.imdb.com/title/${imdb}/`}
                        target="_blank"
                        rel="noreferrer"
                      >
                        IMDb ↗
                      </a>
                    )}
                  </div>

                  {genres.length > 0 && (
                    <div className="detail-genres">
                      {genres.map((g) => (
                        <Badge key={g}>{g}</Badge>
                      ))}
                    </div>
                  )}

                  {overview && <p className="detail-synopsis">{overview}</p>}

                  {hasResume && progressPct > 0 && (
                    <div className="detail-progress">
                      <ProgressBar value={progressPct} label="Resume progress" />
                      <span className="muted mono">
                        Paused at {formatResume(progress.position)}
                      </span>
                    </div>
                  )}

                  <div className="detail-cta-row">
                    <Button
                      disabled={starting !== ""}
                      onClick={() => void startAutoPlay(false)}
                    >
                      <PlayIcon size={16} />
                      {starting === "play"
                        ? "Finding best source…"
                        : hasResume
                          ? "Resume"
                          : "Play"}
                    </Button>
                    <Button
                      variant="secondary"
                      disabled={starting !== ""}
                      onClick={() => void startAutoPlay(true)}
                    >
                      <DownloadIcon size={16} />
                      {starting === "keep" ? "Starting…" : "Keep offline"}
                    </Button>
                    {!ytsFallback && (
                      <Button
                        variant="ghost"
                        onClick={() => setShowSources((v) => !v)}
                      >
                        <SlidersIcon size={15} />
                        {showSources ? "Hide sources" : "Sources"}
                      </Button>
                    )}
                  </div>
                </div>
              </div>
            </section>

            {error && (
              <div className="ds-alert ds-alert-danger" role="alert">
                {error}
              </div>
            )}

            {showSources && !ytsFallback && (
              <section className="sources-section">
                <SourcesDrawer
                  inline
                  mediaType="movie"
                  tmdbId={id}
                  activeHash={tmdbData?.activeDownloadHash || ""}
                  title={`Sources for ${title}`}
                  onSelect={(hash, keep) => startAutoPlay(keep, hash)}
                  onClose={() => setShowSources(false)}
                />
              </section>
            )}

            {(tmdbData?.stale || ytsData?.stale) && (
              <div className="ds-alert ds-alert-warn">
                Cached metadata · updated {formatTime(tmdbData?.updatedAt || ytsData?.updatedAt || "")}
              </div>
            )}
          </>
        )
      )}
    </div>
  );
}
