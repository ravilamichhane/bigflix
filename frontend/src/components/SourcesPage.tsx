import { useEffect, useState } from "react";
import { client, errMessage } from "../api";
import type { FindSourcesResponse } from "../gen/bigflix/v1/bigflix_pb";
import type { NavigateFn } from "../types";
import { ArrowLeftIcon } from "./Icons";
import { MovieCard } from "./MovieCard";
import { Button, EmptyState } from "./ui";

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

      <div className="page-title-row">
        <h1>Matching sources</h1>
      </div>

      {loading ? (
        <EmptyState title="Searching sources…" />
      ) : error ? (
        <EmptyState
          title="Unable to match sources"
          description={error}
          actions={<Button onClick={() => navigate("/")}>Browse movies</Button>}
        />
      ) : (
        <>
          {data?.notice && <div className="ds-alert">{data.notice}</div>}
          <div className="poster-grid">
            {data?.movies.length ? (
              data.movies.map((m) => (
                <MovieCard key={m.id} movie={m} navigate={navigate} />
              ))
            ) : (
              <EmptyState
                title="No matching sources"
                description="No YTS listing matched this title’s IMDb ID."
                actions={<Button onClick={() => navigate("/")}>Browse movies</Button>}
              />
            )}
          </div>
        </>
      )}
    </div>
  );
}
