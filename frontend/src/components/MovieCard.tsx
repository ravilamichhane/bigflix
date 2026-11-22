import { useState } from "react";
import type { CatalogItem, Movie } from "../gen/bigflix/v1/bigflix_pb";
import type { NavigateFn } from "../types";

interface CatalogCardProps {
  item: CatalogItem;
  showTypeBadge?: boolean;
  navigate: NavigateFn;
}

export function CatalogCard({ item, showTypeBadge = false, navigate }: CatalogCardProps) {
  const [broken, setBroken] = useState(false);
  const href =
    item.mediaType === "tv"
      ? `/series/${item.tmdbId}`
      : item.ytsId > 0 && item.tmdbId === 0
        ? `/movie/${item.ytsId}?yts=1`
        : `/movie/${item.tmdbId}`;

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
        {item.poster && !broken && (
          <img
            src={item.poster}
            alt={`${item.title} poster`}
            loading="lazy"
            referrerPolicy="no-referrer"
            onError={() => setBroken(true)}
          />
        )}
        <div className="poster-fallback" aria-hidden="true">
          <strong>{item.title}</strong>
        </div>
        {showTypeBadge && (
          <span className={`poster-type ${item.mediaType === "tv" ? "is-tv" : ""}`}>
            {item.mediaType === "tv" ? "TV" : "Movie"}
          </span>
        )}
        {item.rating > 0 && <span className="poster-rating">★ {item.rating.toFixed(1)}</span>}
      </div>
      <div className="poster-info">
        <h2>{item.title}</h2>
        <div className="poster-meta">
          <span className="mono">{item.year || "—"}</span>
          {item.language && <span>{item.language.toUpperCase()}</span>}
        </div>
      </div>
    </a>
  );
}

interface MovieCardProps {
  movie: Movie;
  navigate: NavigateFn;
}

export function MovieCard({ movie, navigate }: MovieCardProps) {
  const [broken, setBroken] = useState(false);
  const href = movie.tmdbId > 0 ? `/movie/${movie.tmdbId}` : `/movie/${movie.id}?yts=1`;

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
          {movie.language && <span>{movie.language}</span>}
        </div>
      </div>
    </a>
  );
}
