import { useEffect, useState, type FormEvent } from "react";
import { client, errMessage, formatTime } from "../api";
import type { ListMoviesResponse } from "../gen/bigflix/v1/bigflix_pb";
import type { NavigateFn } from "../types";
import { CloseIcon, SearchIcon } from "./Icons";
import { MovieCard } from "./MovieCard";

const GENRES = [
  "Action",
  "Adventure",
  "Animation",
  "Comedy",
  "Crime",
  "Documentary",
  "Drama",
  "Fantasy",
  "Horror",
  "Mystery",
  "Romance",
  "Sci-Fi",
  "Thriller",
];

interface BrowsePageProps {
  search: string;
  navigate: NavigateFn;
}

export function BrowsePage({ search, navigate }: BrowsePageProps) {
  const params = new URLSearchParams(search);
  const feed = params.get("feed") || "library";
  const query = params.get("q") || "";
  const genre = params.get("genre") || "";
  const sort = params.get("sort") || "date_added";
  const page = Math.max(1, Number(params.get("page")) || 1);

  const [searchInput, setSearchInput] = useState(query);
  const [data, setData] = useState<ListMoviesResponse | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    setSearchInput(query);
  }, [query]);

  useEffect(() => {
    let active = true;
    setLoading(true);
    setError("");
    client
      .listMovies({ feed, query, genre, sort, page })
      .then((res) => {
        if (!active) return;
        setData(res);
      })
      .catch((err) => {
        if (!active) return;
        setError(errMessage(err));
        setData(null);
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [feed, query, genre, sort, page]);

  function buildUrl(updates: Record<string, string | number | undefined>) {
    const next = new URLSearchParams();
    const merged: Record<string, string> = {
      feed: feed === "library" ? "" : feed,
      q: query,
      genre: feed === "library" ? genre : "",
      sort: feed === "library" && sort !== "date_added" ? sort : "",
      page: page > 1 ? String(page) : "",
    };
    for (const [k, v] of Object.entries(updates)) {
      merged[k] = v === undefined ? "" : String(v);
    }
    for (const [k, v] of Object.entries(merged)) {
      if (v) next.set(k, v);
    }
    const qs = next.toString();
    return qs ? `/?${qs}` : "/";
  }

  function handleSearch(e: FormEvent) {
    e.preventDefault();
    navigate(buildUrl({ feed: "", q: searchInput.trim(), page: "" }));
  }

  const displayError = error || data?.errorMessage || "";
  const hasTmdb = data?.hasTmdb ?? false;

  return (
    <div className="browse-page">
      <div className="browse-toolbar">
        <form className="search" onSubmit={handleSearch}>
          <SearchIcon size={16} className="search-icon" />
          <input
            aria-label="Search movies"
            placeholder="Search movies or IMDb ID…"
            value={searchInput}
            onChange={(e) => setSearchInput(e.target.value)}
            maxLength={200}
          />
          {searchInput && (
            <button
              type="button"
              className="search-clear"
              aria-label="Clear search"
              onClick={() => {
                setSearchInput("");
                if (query) navigate(buildUrl({ feed: "", q: "", page: "" }));
              }}
            >
              <CloseIcon size={14} />
            </button>
          )}
        </form>

        {feed === "library" && (
          <div className="filter-selects">
            <select
              aria-label="Genre"
              value={genre}
              onChange={(e) =>
                navigate(buildUrl({ feed: "", q: query, genre: e.target.value, page: "" }))
              }
            >
              <option value="">All genres</option>
              {GENRES.map((g) => (
                <option key={g} value={g}>
                  {g}
                </option>
              ))}
            </select>
            <select
              aria-label="Sort by"
              value={sort}
              onChange={(e) =>
                navigate(
                  buildUrl({
                    feed: "",
                    q: query,
                    sort: e.target.value === "date_added" ? "" : e.target.value,
                    page: "",
                  }),
                )
              }
            >
              <option value="date_added">Recent</option>
              <option value="rating">Top rated</option>
              <option value="year">Newest year</option>
            </select>
          </div>
        )}
      </div>

      <div className="feed-bar">
        <div className="tabs">
          {[
            { id: "library", label: "Library" },
            { id: "day", label: "Trending today" },
            { id: "week", label: "This week" },
            { id: "new", label: "New releases" },
          ].map((t) => {
            const href = t.id === "library" ? "/" : `/?feed=${t.id}`;
            return (
              <a
                key={t.id}
                href={href}
                className={feed === t.id ? "active" : ""}
                onClick={(e) => {
                  e.preventDefault();
                  navigate(href);
                }}
              >
                {t.label}
              </a>
            );
          })}
        </div>
        {!!data?.count && <span className="count">{data.count.toLocaleString()}</span>}
      </div>

      {loading && !data ? (
        <div className="skeleton-grid" aria-label="Loading movies">
          {Array.from({ length: 12 }).map((_, i) => (
            <div key={i} className="skeleton-card">
              <div className="skeleton-poster" />
              <div className="skeleton-line" />
              <div className="skeleton-line short" />
            </div>
          ))}
        </div>
      ) : displayError ? (
        <div className="empty">
          <h2>
            {!hasTmdb && feed !== "library" ? "TMDB not configured" : "Couldn’t load catalog"}
          </h2>
          <p>{displayError}</p>
          <div className="empty-actions">
            <a
              className="button"
              href="/settings"
              onClick={(e) => {
                e.preventDefault();
                navigate("/settings");
              }}
            >
              Settings
            </a>
            <a
              className="button secondary"
              href="/"
              onClick={(e) => {
                e.preventDefault();
                navigate("/");
              }}
            >
              Library
            </a>
          </div>
        </div>
      ) : (
        <>
          {data?.stale && (
            <div className="notice">
              Showing cached results · last updated {formatTime(data.updatedAt)}
            </div>
          )}
          {data?.notice && <p className="notice">{data.notice}</p>}

          <div className="movie-grid">
            {data?.movies.length ? (
              data.movies.map((m) => (
                <MovieCard key={`${m.id}-${m.tmdbId}`} movie={m} navigate={navigate} />
              ))
            ) : (
              <div className="empty">
                <h2>No movies found</h2>
                <p>Try a different title, IMDb ID, or genre.</p>
                <a
                  className="button"
                  href="/"
                  onClick={(e) => {
                    e.preventDefault();
                    navigate("/");
                  }}
                >
                  Clear filters
                </a>
              </div>
            )}
          </div>

          {(data?.hasPrev || data?.hasNext) && (
            <div className="pagination">
              {data?.hasPrev && (
                <a
                  className="button secondary"
                  href={buildUrl({ page: page - 1 })}
                  onClick={(e) => {
                    e.preventDefault();
                    navigate(buildUrl({ page: page - 1 }));
                  }}
                >
                  ← Previous
                </a>
              )}
              {!!data?.page && <span className="page-num">Page {data.page}</span>}
              {data?.hasNext && (
                <a
                  className="button secondary"
                  href={buildUrl({ page: page + 1 })}
                  onClick={(e) => {
                    e.preventDefault();
                    navigate(buildUrl({ page: page + 1 }));
                  }}
                >
                  Next →
                </a>
              )}
            </div>
          )}

          {feed !== "library" && (
            <p className="attribution">
              Metadata by{" "}
              <a href="https://www.themoviedb.org/" rel="noreferrer">
                TMDB
              </a>
              .
            </p>
          )}
        </>
      )}
    </div>
  );
}
