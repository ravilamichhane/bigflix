import { useState } from "react";
import type { Movie } from "../gen/bigflix/v1/bigflix_pb";
import type { NavigateFn } from "../types";

interface MovieCardProps {
  movie: Movie;
  navigate: NavigateFn;
}

export function MovieCard({ movie, navigate }: MovieCardProps) {
  const [broken, setBroken] = useState(false);
  const href = movie.id > 0 ? `/movie/${movie.id}` : `/sources/${movie.tmdbId}`;

  return (
    <a
      className="poster-card"
      href={href}
      onClick={(e) => {
        e.preventDefault();
        navigate(href);
      }}
    >
      <div className="poster-frame">
        {movie.poster && !broken && (
          <img
            src={movie.poster}
            alt={`${movie.title} poster`}
            loading="lazy"
            referrerPolicy="no-referrer"
            onError={() => setBroken(true)}
          />
        )}
        <div className="poster-fallback" aria-hidden="true">
          <strong>{movie.title}</strong>
        </div>
        {movie.rating > 0 && <span className="poster-rating">★ {movie.rating.toFixed(1)}</span>}
      </div>
      <div className="poster-info">
        <h2>{movie.title}</h2>
        <div className="poster-meta">
          <span className="mono">{movie.year || "—"}</span>
          {movie.tmdbId ? (
            <span>Sources</span>
          ) : movie.language ? (
            <span>{movie.language}</span>
          ) : null}
        </div>
      </div>
    </a>
  );
}
