import { useEffect, useState } from "react";
import { client, errMessage } from "../api";
import type { FindSourcesResponse } from "../gen/bigflix/v1/bigflix_pb";
import type { NavigateFn } from "../types";
import { MovieCard } from "./MovieCard";

interface SourcesPageProps {
  tmdbId: number;
  navigate: NavigateFn;
}

export function SourcesPage({ tmdbId, navigate }: SourcesPageProps) {
  const [data, setData] = useState<FindSourcesResponse | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let active = true;
    setLoading(true);
    setError("");
    client
      .findSources({ tmdbId })
      .then((res) => {
        if (!active) return;
        if (res.movies.length === 1 && res.movies[0].id > 0) {
          navigate(`/movie/${res.movies[0].id}`);
          return;
        }
        setData(res);
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
  }, [tmdbId, navigate]);

  return (
    <>
      <section className="intro">
        <div className="eyebrow">EXACT IMDB MATCHES</div>
        <h1>Find your source.</h1>
        <p>Exact IMDb matches in the YTS catalog.</p>
      </section>

      <section className="catalog">
        {loading ? (
          <div className="empty">
            <h2>Searching sources…</h2>
          </div>
        ) : error ? (
          <div className="empty">
            <span className="empty-symbol">◉</span>
            <h2>Unable to match sources</h2>
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
          <>
            {data?.notice && <p className="notice">{data.notice}</p>}
            <div className="movie-grid">
              {data?.movies.length ? (
                data.movies.map((m) => (
                  <MovieCard key={m.id} movie={m} navigate={navigate} />
                ))
              ) : (
                <div className="empty">
                  <h2>No matching sources found.</h2>
                  <p>No YTS listing matched this title’s IMDb ID.</p>
                  <a
                    className="button"
                    href="/"
                    onClick={(e) => {
                      e.preventDefault();
                      navigate("/");
                    }}
                  >
                    Explore the catalog
                  </a>
                </div>
              )}
            </div>
          </>
        )}
      </section>
    </>
  );
}
