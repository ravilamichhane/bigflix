import { useEffect, useState } from "react";
import { client, errMessage, formatTime } from "../api";
import type { GetMovieResponse } from "../gen/bigflix/v1/bigflix_pb";
import type { NavigateFn } from "../types";
import { ArrowLeftIcon, CheckIcon, CopyIcon, DownloadIcon, PlayIcon } from "./Icons";
import { Badge, Button, EmptyState } from "./ui";

interface DetailPageProps {
  id: number;
  navigate: NavigateFn;
}

export function DetailPage({ id, navigate }: DetailPageProps) {
  const [data, setData] = useState<GetMovieResponse | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [startingHash, setStartingHash] = useState("");
  const [copiedHash, setCopiedHash] = useState("");
  const [posterBroken, setPosterBroken] = useState(false);

  useEffect(() => {
    let active = true;
    setLoading(true);
    setError("");
    setPosterBroken(false);
    client
      .getMovie({ id })
      .then((res) => {
        if (active) setData(res);
      })
      .catch((err) => {
        if (active) setError(errMessage(err));
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [id]);

  async function start(hash: string, keep: boolean) {
    if (!data?.movie) return;
    setStartingHash(hash);
    setError("");
    try {
      const res = await client.startDownload({
        movieId: data.movie.id,
        hash,
        keep,
      });
      if (res.download?.hash) {
        navigate(`/watch/${res.download.hash}`);
      }
    } catch (err) {
      setError(errMessage(err));
      setStartingHash("");
    }
  }

  async function copyHash(hash: string) {
    try {
      await navigator.clipboard.writeText(hash);
      setCopiedHash(hash);
      window.setTimeout(() => setCopiedHash(""), 2000);
    } catch {
      // Ignore clipboard failure
    }
  }

  const movie = data?.movie;

  return (
    <div>
      <a
        href="/"
        className="ds-back"
        onClick={(e) => {
          e.preventDefault();
          navigate("/");
        }}
      >
        <ArrowLeftIcon size={16} /> Back
      </a>

      {loading ? (
        <EmptyState title="Loading movie…" />
      ) : error && !movie ? (
        <EmptyState
          title="Movie unavailable"
          description={error}
          actions={<Button onClick={() => navigate("/")}>Browse movies</Button>}
        />
      ) : (
        movie && (
          <>
            <section className="detail-hero">
              {movie.poster && !posterBroken && (
                <div
                  className="detail-backdrop"
                  style={{ backgroundImage: `url(${movie.poster})` }}
                  aria-hidden="true"
                />
              )}
              <div className="detail-hero-grid">
                <div className="detail-poster poster-frame">
                  {movie.poster && !posterBroken && (
                    <img
                      src={movie.poster}
                      alt={`${movie.title} poster`}
                      onError={() => setPosterBroken(true)}
                    />
                  )}
                  <div className="poster-fallback">
                    <strong>{movie.title}</strong>
                  </div>
                </div>

                <div className="detail-copy">
                  <h1>{movie.title}</h1>

                  <div className="detail-facts">
                    {movie.rating > 0 && (
                      <Badge tone="accent" className="mono">
                        ★ {movie.rating.toFixed(1)}
                      </Badge>
                    )}
                    {!!movie.year && <Badge className="mono">{movie.year}</Badge>}
                    {!!movie.runtime && <Badge className="mono">{movie.runtime}m</Badge>}
                    {movie.language && <Badge>{movie.language}</Badge>}
                    {movie.imdbCode && (
                      <a
                        className="ds-link"
                        href={`https://www.imdb.com/title/${movie.imdbCode}/`}
                        target="_blank"
                        rel="noreferrer"
                      >
                        IMDb ↗
                      </a>
                    )}
                  </div>

                  {movie.genres.length > 0 && (
                    <div className="detail-genres">
                      {movie.genres.map((g) => (
                        <Badge key={g}>{g}</Badge>
                      ))}
                    </div>
                  )}

                  {movie.summary && <p className="detail-synopsis">{movie.summary}</p>}
                </div>
              </div>
            </section>

            {error && (
              <div className="ds-alert ds-alert-danger" role="alert">
                {error}
              </div>
            )}

            <section className="sources-section">
              <div className="section-bar">
                <h2>Available sources</h2>
                <Badge className="mono">{movie.torrents.length}</Badge>
              </div>

              <div className="source-grid">
                {movie.torrents.length ? (
                  movie.torrents.map((t) => {
                    const health =
                      t.seeds >= 30 ? "health-good" : t.seeds >= 8 ? "health-mid" : "health-low";
                    return (
                      <article key={t.hash} className="source-card">
                        <div className="source-head">
                          <div className="source-title">
                            <strong>{t.quality}</strong>
                            <Badge>{t.type}</Badge>
                          </div>
                          <Button
                            variant="ghost"
                            size="icon-sm"
                            title="Copy info hash"
                            aria-label="Copy info hash"
                            onClick={() => void copyHash(t.hash)}
                          >
                            {copiedHash === t.hash ? (
                              <CheckIcon size={14} />
                            ) : (
                              <CopyIcon size={14} />
                            )}
                          </Button>
                        </div>

                        <div className="source-specs">
                          <span>{t.size}</span>
                          {t.videoCodec && <span>{t.videoCodec}</span>}
                          <span>
                            <span className={`health-dot ${health}`} />
                            {t.seeds} seeds · {t.peers} peers
                          </span>
                        </div>

                        <div className="source-actions">
                          <Button
                            disabled={startingHash === t.hash}
                            onClick={() => void start(t.hash, false)}
                          >
                            <PlayIcon size={14} />
                            {startingHash === t.hash ? "Starting…" : "Watch"}
                          </Button>
                          <Button
                            variant="secondary"
                            disabled={startingHash === t.hash}
                            onClick={() => void start(t.hash, true)}
                          >
                            <DownloadIcon size={14} /> Keep
                          </Button>
                        </div>
                      </article>
                    );
                  })
                ) : (
                  <EmptyState
                    title="No sources listed"
                    description="No valid torrent variants were returned for this title."
                  />
                )}
              </div>
            </section>

            {data?.stale && (
              <div className="ds-alert ds-alert-warn">
                Cached metadata · updated {formatTime(data.updatedAt)}
              </div>
            )}
          </>
        )
      )}
    </div>
  );
}
