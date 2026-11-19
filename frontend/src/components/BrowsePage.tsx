import { useEffect, useState, type FormEvent } from "react";
import { client, errMessage, formatTime } from "../api";
import type { ListMoviesResponse } from "../gen/bigflix/v1/bigflix_pb";
import type { NavigateFn } from "../types";
import { CloseIcon, SearchIcon } from "./Icons";
import { MovieCard } from "./MovieCard";
import { Badge, Button, EmptyState } from "./ui";

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
    <div>
      <section className="browse-head">
        <div className="browse-controls">
          <form className="search-box" onSubmit={handleSearch}>
            <SearchIcon size={16} className="search-box-icon" />
            <input
              aria-label="Search movies"
              placeholder="Search titles or IMDb ID…"
              value={searchInput}
              onChange={(e) => setSearchInput(e.target.value)}
              maxLength={200}
            />
            {searchInput && (
              <button
                type="button"
                className="search-box-clear"
                aria-label="Clear search"
                onClick={() => {
                  setSearchInput("");
                  if (query) navigate(buildUrl({ feed: "", q: "", page: "" }));
                }}
              >
                <CloseIcon size={13} />
              </button>
            )}
          </form>

          {feed === "library" && (
            <div className="filter-group">
              <select
                className="ds-select"
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
                className="ds-select"
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
                <option value="year">Release year</option>
              </select>
            </div>
          )}
        </div>

        <div className="browse-subbar">
          <div className="feed-scroll">
            <div className="ds-seg">
              {[
                { id: "library", label: "Library" },
                { id: "day", label: "Trending" },
                { id: "week", label: "This week" },
                { id: "new", label: "New" },
              ].map((t) => {
                const href = t.id === "library" ? "/" : `/?feed=${t.id}`;
                return (
                  <a
                    key={t.id}
                    href={href}
                    className={`ds-seg-item ${feed === t.id ? "active" : ""}`}
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
          </div>

          {!!data?.count && <Badge className="mono">{data.count.toLocaleString()}</Badge>}
        </div>
      </section>

      {loading && !data ? (
        <div className="poster-grid" aria-label="Loading movies">
          {Array.from({ length: 12 }).map((_, i) => (
            <div key={i} className="poster-card">
              <div className="skeleton-frame" />
              <div className="poster-info">
                <div className="skeleton-bar" />
                <div className="skeleton-bar sm" />
              </div>
            </div>
          ))}
        </div>
      ) : displayError ? (
        <EmptyState
          title={!hasTmdb && feed !== "library" ? "TMDB not configured" : "Couldn’t load catalog"}
          description={displayError}
          actions={
            <>
              <Button onClick={() => navigate("/settings")}>Settings</Button>
              <Button variant="secondary" onClick={() => navigate("/")}>
                Back to Library
              </Button>
            </>
          }
        />
      ) : (
        <>
          {data?.stale && (
            <div className="ds-alert ds-alert-warn">
              Showing cached results · last updated {formatTime(data.updatedAt)}
            </div>
          )}
          {data?.notice && <div className="ds-alert">{data.notice}</div>}

          <div className="poster-grid">
            {data?.movies.length ? (
              data.movies.map((m) => (
                <MovieCard key={`${m.id}-${m.tmdbId}`} movie={m} navigate={navigate} />
              ))
            ) : (
              <EmptyState
                title="No movies found"
                description="Try a different title, IMDb ID, or genre filter."
                actions={<Button onClick={() => navigate("/")}>Reset filters</Button>}
              />
            )}
          </div>

          {(data?.hasPrev || data?.hasNext) && (
            <div className="pagination">
              {data?.hasPrev && (
                <Button
                  variant="secondary"
                  size="sm"
                  onClick={() => navigate(buildUrl({ page: page - 1 }))}
                >
                  ← Previous
                </Button>
              )}
              {!!data?.page && <span className="pagination-label">Page {data.page}</span>}
              {data?.hasNext && (
                <Button
                  variant="secondary"
                  size="sm"
                  onClick={() => navigate(buildUrl({ page: page + 1 }))}
                >
                  Next →
                </Button>
              )}
            </div>
          )}

          {feed !== "library" && (
            <p className="attribution">
              Metadata provided by{" "}
              <a href="https://www.themoviedb.org/" target="_blank" rel="noreferrer">
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
