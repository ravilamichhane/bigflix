import { useEffect, useState } from "react";
import { client, errMessage, formatTime } from "../api";
import type { GetMovieResponse } from "../gen/bigflix/v1/bigflix_pb";
import type { NavigateFn } from "../types";
import { ArrowLeftIcon, CheckIcon, CopyIcon, DownloadIcon, PlayIcon } from "./Icons";

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
    <div className="detail-page">
      <a
        href="/"
        className="back"
        onClick={(e) => {
          e.preventDefault();
          navigate("/");
        }}
      >
        <ArrowLeftIcon size={16} /> Back
      </a>

      {loading ? (
        <div className="empty">
          <p>Loading…</p>
        </div>
      ) : error && !movie ? (
        <div className="empty">
          <h2>Movie unavailable</h2>
          <p>{error}</p>
          <a
            className="button"
            href="/"
            onClick={(e) => {
              e.preventDefault();
              navigate("/");
            }}
          >
            Browse movies
          </a>
        </div>
      ) : (
        movie && (
          <>
            <section className="movie-detail">
              <div className="detail-poster poster">
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
                <div className="detail-meta">
                  {movie.rating > 0 && <span className="score">★ {movie.rating.toFixed(1)}</span>}
                  {!!movie.year && <span>{movie.year}</span>}
                  {!!movie.runtime && <span>{movie.runtime}m</span>}
                  {movie.language && <span>{movie.language}</span>}
                  {movie.imdbCode && (
                    <a
                      className="meta-link"
                      href={`https://www.imdb.com/title/${movie.imdbCode}/`}
                      target="_blank"
                      rel="noreferrer"
                    >
                      IMDb ↗
                    </a>
                  )}
                </div>

                {movie.genres.length > 0 && (
                  <div className="genre-tags">
                    {movie.genres.map((g) => (
                      <span key={g}>{g}</span>
                    ))}
                  </div>
                )}

                {movie.summary && <p className="synopsis">{movie.summary}</p>}
              </div>
            </section>

            {error && (
              <div className="error-banner" role="alert">
                {error}
              </div>
            )}

            <section className="sources">
              <div className="section-head">
                <h2>Sources</h2>
                <span className="count">{movie.torrents.length}</span>
              </div>

              <div className="source-grid">
                {movie.torrents.length ? (
                  movie.torrents.map((t) => (
                    <article key={t.hash} className="source-card">
                      <div className="source-top">
                        <div className="source-quality">
                          <strong>{t.quality}</strong>
                          <span className="tag">{t.type}</span>
                        </div>
                        <button
                          type="button"
                          className="icon-btn-sm"
                          title="Copy info hash"
                          aria-label="Copy info hash"
                          onClick={() => void copyHash(t.hash)}
                        >
                          {copiedHash === t.hash ? <CheckIcon size={14} /> : <CopyIcon size={14} />}
                        </button>
                      </div>

                      <div className="source-meta">
                        <span>{t.size}</span>
                        {t.videoCodec && <span>{t.videoCodec}</span>}
                        <span>
                          {t.seeds} seeds · {t.peers} peers
                        </span>
                      </div>

                      <div className="source-actions">
                        <button
                          type="button"
                          className="button"
                          disabled={startingHash === t.hash}
                          onClick={() => void start(t.hash, false)}
                        >
                          <PlayIcon size={14} />
                          {startingHash === t.hash ? "Starting…" : "Watch"}
                        </button>
                        <button
                          type="button"
                          className="button secondary"
                          disabled={startingHash === t.hash}
                          onClick={() => void start(t.hash, true)}
                        >
                          <DownloadIcon size={14} /> Keep
                        </button>
                      </div>
                    </article>
                  ))
                ) : (
                  <div className="empty">
                    <h2>No sources listed</h2>
                    <p>No valid torrent variants were returned for this title.</p>
                  </div>
                )}
              </div>
            </section>

            {data?.stale && (
              <p className="notice">Cached metadata · updated {formatTime(data.updatedAt)}</p>
            )}
          </>
        )
      )}
    </div>
  );
}
