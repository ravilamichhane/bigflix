import { useEffect, useState, type FormEvent } from "react";
import { client, errMessage, formatTime } from "../api";
import type { ListMoviesResponse } from "../gen/bigflix/v1/bigflix_pb";
import type { NavigateFn } from "../types";
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
  const [genreInput, setGenreInput] = useState(genre);
  const [sortInput, setSortInput] = useState(sort);
  const [data, setData] = useState<ListMoviesResponse | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    setSearchInput(query);
    setGenreInput(genre);
    setSortInput(sort);
  }, [query, genre, sort]);

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

  function handleFilters(e: FormEvent) {
    e.preventDefault();
    navigate(
      buildUrl({
        feed: "",
        q: query,
        genre: genreInput,
        sort: sortInput === "date_added" ? "" : sortInput,
        page: "",
      }),
    );
  }

  const displayError = error || data?.errorMessage || "";
  const hasTmdb = data?.hasTmdb ?? false;

  return (
    <>
      <section className="intro">
        <div className="eyebrow">A LITTLE ESCAPE, ON YOUR TERMS</div>
        <h1>{data?.heading || "Find your next great watch."}</h1>
        <p>{data?.subtitle || "Movies worth staying in for. Your library starts here."}</p>
        <form className="search" onSubmit={handleSearch}>
          <span aria-hidden="true">⌕</span>
          <input
            aria-label="Search movies"
            placeholder="Search movies, or enter an IMDb ID…"
            value={searchInput}
            onChange={(e) => setSearchInput(e.target.value)}
            maxLength={200}
          />
          <button type="submit">
            Search <span aria-hidden="true">↗</span>
          </button>
        </form>
      </section>

      <section className="catalog">
        <div className="section-top">
          <div className="tabs">
            {[
              { id: "library", label: "Recently added" },
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
          {!!data?.count && <span className="count">{data.count} titles</span>}
        </div>

        {feed === "library" && (
          <form className="filters" onSubmit={handleFilters}>
            <label>
              Genre
              <select value={genreInput} onChange={(e) => setGenreInput(e.target.value)}>
                <option value="">All genres</option>
                {GENRES.map((g) => (
                  <option key={g} value={g}>
                    {g}
                  </option>
                ))}
              </select>
            </label>
            <label>
              Sort by
              <select value={sortInput} onChange={(e) => setSortInput(e.target.value)}>
                <option value="date_added">Recently added</option>
                <option value="rating">Rating</option>
                <option value="year">Release year</option>
              </select>
            </label>
            <button className="filter-button" type="submit">
              Apply
            </button>
            <span className="source-label">
              SOURCE: YTS <span className="dot" />
            </span>
          </form>
        )}

        {loading && !data ? (
          <div className="empty">
            <h2>Loading catalog…</h2>
          </div>
        ) : displayError ? (
          <div className="empty">
            <span className="empty-symbol">◉</span>
            <h2>
              {!hasTmdb && feed !== "library"
                ? "A wider world awaits."
                : "Let’s try that again."}
            </h2>
            <p>{displayError}</p>
            <a
              className="button"
              href="/settings"
              onClick={(e) => {
                e.preventDefault();
                navigate("/settings");
              }}
            >
              View setup
            </a>
            <a
              className="text-link"
              href="/"
              onClick={(e) => {
                e.preventDefault();
                navigate("/");
              }}
            >
              Browse movies →
            </a>
          </div>
        ) : (
          <>
            {data?.stale && (
              <div className="notice">
                Showing saved results while the provider is unavailable. Last fetched{" "}
                {formatTime(data.updatedAt)}.
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
                  <h2>No movies here yet.</h2>
                  <p>Try a different title, IMDb ID, or filter.</p>
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
              {!!data?.page && <span>Page {data.page}</span>}
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

            {data?.updatedAt && (
              <p className="freshness">
                Catalog checked {formatTime(data.updatedAt)} · Listing does not guarantee a
                playable source.
              </p>
            )}
          </>
        )}
      </section>

      {feed !== "library" && (
        <div className="attribution">
          Metadata provided by{" "}
          <a href="https://www.themoviedb.org/" rel="noreferrer">
            TMDB
          </a>
          . This product uses the TMDB API but is not endorsed or certified by TMDB.
        </div>
      )}
    </>
  );
}
