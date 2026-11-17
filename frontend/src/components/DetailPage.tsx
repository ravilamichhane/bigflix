import { useEffect, useState } from "react";
import { client, errMessage, formatTime } from "../api";
import type { GetMovieResponse } from "../gen/bigflix/v1/bigflix_pb";
import type { NavigateFn } from "../types";

interface DetailPageProps {
  id: number;
  navigate: NavigateFn;
}

export function DetailPage({ id, navigate }: DetailPageProps) {
  const [data, setData] = useState<GetMovieResponse | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [startingHash, setStartingHash] = useState("");
  const [copyStatus, setCopyStatus] = useState("");
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
      setCopyStatus("Torrent info hash copied. No download has been started.");
    } catch {
      setCopyStatus(`Copy unavailable. Info hash: ${hash}`);
    }
  }

  const movie = data?.movie;

  return (
    <>
      <a
        href="/"
        className="back"
        onClick={(e) => {
          e.preventDefault();
          navigate("/");
        }}
      >
        ← Back to discovery
      </a>

      {loading ? (
        <div className="empty">
          <h2>Loading movie…</h2>
        </div>
      ) : error && !movie ? (
        <div className="empty">
          <span className="empty-symbol">◉</span>
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
                  <span>BIGFLIX</span>
                  <strong>{movie.title}</strong>
                </div>
              </div>
              <div className="detail-copy">
                <div className="eyebrow">YOUR NEXT MOVIE NIGHT</div>
                <h1>{movie.title}</h1>
                <div className="detail-meta">
                  <span className="score">★ {movie.rating.toFixed(1)}</span>
                  <span>{movie.year}</span>
                  {!!movie.runtime && <span>{movie.runtime} min</span>}
                  <span>{movie.language}</span>
                </div>
                <div className="genre-tags">
                  {movie.genres.map((g) => (
                    <span key={g}>{g}</span>
                  ))}
                </div>
                <p className="synopsis">{movie.summary}</p>
                {movie.imdbCode && (
                  <a
                    className="text-link"
                    href={`https://www.imdb.com/title/${movie.imdbCode}/`}
                    rel="noreferrer"
                  >
                    View on IMDb ↗
                  </a>
                )}
                <div className="milestone-note">
                  <span className="dot" />
                  <div>
                    <strong>Start watching before the download finishes.</strong>
                    <p>
                      Choose a source below. The player buffers the pieces it needs, while the
                      rest downloads locally.
                    </p>
                  </div>
                </div>
              </div>
            </section>

            {error && (
              <div className="error-banner" role="alert">
                {error}
              </div>
            )}

            <section className="sources">
              <div className="section-top">
                <div>
                  <div className="eyebrow">AVAILABLE TORRENT VARIANTS</div>
                  <h2>Choose your picture.</h2>
                </div>
                <span className="count">{movie.torrents.length} sources</span>
              </div>
              <p className="muted">
                Quality and peer counts are reported by YTS. Availability and browser
                compatibility have not been verified.
              </p>

              <div className="source-grid">
                {movie.torrents.length ? (
                  movie.torrents.map((t) => (
                    <article key={t.hash} className="source-card">
                      <div className="source-quality">
                        {t.quality}
                        <span>{t.type}</span>
                      </div>
                      <dl>
                        <div>
                          <dt>Video</dt>
                          <dd>{t.videoCodec || "Unknown"}</dd>
                        </div>
                        <div>
                          <dt>Size</dt>
                          <dd>{t.size}</dd>
                        </div>
                        <div>
                          <dt>Seeds / peers</dt>
                          <dd>
                            {t.seeds} / {t.peers}
                          </dd>
                        </div>
                      </dl>
                      <div className="source-start">
                        <button
                          type="button"
                          className="button"
                          disabled={startingHash === t.hash}
                          onClick={() => start(t.hash, false)}
                        >
                          ▶ Watch now
                        </button>
                        <button
                          type="button"
                          className="button secondary"
                          disabled={startingHash === t.hash}
                          onClick={() => start(t.hash, true)}
                        >
                          ↓ Keep
                        </button>
                      </div>
                      <button
                        type="button"
                        className="copy-button"
                        onClick={() => copyHash(t.hash)}
                      >
                        Copy info hash <span>↗</span>
                      </button>
                    </article>
                  ))
                ) : (
                  <div className="empty">
                    <h2>No torrent variants listed.</h2>
                    <p>This movie has metadata, but no valid source hashes were returned.</p>
                  </div>
                )}
              </div>
            </section>

            {data?.stale && (
              <p className="notice">
                Saved source metadata · last fetched {formatTime(data.updatedAt)}
              </p>
            )}
            <p id="copy-status" role="status" aria-live="polite">
              {copyStatus}
            </p>
          </>
        )
      )}
    </>
  );
}
