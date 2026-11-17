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
      className="movie-card"
      href={href}
      onClick={(e) => {
        e.preventDefault();
        navigate(href);
      }}
    >
      <div className="poster">
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
          <span>BIGFLIX</span>
          <strong>{movie.title}</strong>
        </div>
        <span className="rating">★ {movie.rating.toFixed(1)}</span>
        <span className="card-arrow" aria-hidden="true">
          ↗
        </span>
      </div>
      <h2>{movie.title}</h2>
      <div className="card-meta">
        <span>{movie.year || "Year unknown"}</span>
        <span>{movie.tmdbId ? "Find sources" : movie.language || "Movie"}</span>
      </div>
    </a>
  );
}
