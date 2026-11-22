import { useEffect, useState, type FormEvent } from "react";
import { client, errMessage, formatTime } from "../api";
import type { BrowseCatalogResponse, WatchProgressItem } from "../gen/bigflix/v1/bigflix_pb";
import type { NavigateFn } from "../types";
import { CloseIcon, PlayIcon, SearchIcon } from "./Icons";
import { CatalogCard } from "./MovieCard";
import { Badge, Button, EmptyState, ProgressBar } from "./ui";

const MOVIE_GENRES = [
  "Action",
  "Adventure",
  "Animation",
  "Anime",
  "Comedy",
  "Crime",
  "Documentary",
  "Drama",
  "Family",
  "Fantasy",
  "History",
  "Horror",
  "Mystery",
  "Romance",
  "Sci-Fi",
  "Thriller",
  "War",
];

const TV_GENRES = [
  "Action",
  "Animation",
  "Anime",
  "Comedy",
  "Crime",
  "Documentary",
  "Drama",
  "Family",
  "Mystery",
  "Sci-Fi",
  "War",
  "Western",
];

export type BrowseMode = "discover" | "movie" | "tv";

interface BrowsePageProps {
  mode?: BrowseMode;
  search: string;
  navigate: NavigateFn;
}

function formatEpisodeTag(s: number, e: number): string {
  return `S${String(s).padStart(2, "0")}E${String(e).padStart(2, "0")}`;
}

export function BrowsePage({ mode = "discover", search, navigate }: BrowsePageProps) {
  const params = new URLSearchParams(search);
  const defaultFeed = mode === "discover" ? "trending" : "popular";
  const feed = params.get("feed") || defaultFeed;
  const query = params.get("q") || "";
  const genre = params.get("genre") || "";
  const sort = params.get("sort") || "";
  const page = Math.max(1, Number(params.get("page")) || 1);

  const [searchInput, setSearchInput] = useState(query);
  const [data, setData] = useState<BrowseCatalogResponse | null>(null);
  const [continueItems, setContinueItems] = useState<WatchProgressItem[]>([]);
  const [resumingKey, setResumingKey] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);

  const basePath = mode === "movie" ? "/movies" : mode === "tv" ? "/series" : "/";
  const mediaType = mode === "discover" ? "all" : mode;
  const genres = mode === "tv" ? TV_GENRES : MOVIE_GENRES;

  useEffect(() => {
    setSearchInput(query);
  }, [query]);

  useEffect(() => {
    if (mode !== "discover" || query) {
      setContinueItems([]);
      return;
    }
    let active = true;
    client
      .listContinueWatching({ limit: 12 })
      .then((res) => {
        if (active) setContinueItems(res.items);
      })
      .catch(() => {});
    return () => {
      active = false;
    };
  }, [mode, query]);

  useEffect(() => {
    let active = true;
    setLoading(true);
    setError("");
    client
      .browseCatalog({ mediaType, feed, query, genre, sort, page })
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
  }, [mediaType, feed, query, genre, sort, page]);

  function buildUrl(updates: Record<string, string | number | undefined>) {
    const next = new URLSearchParams();
    const merged: Record<string, string> = {
      feed: feed === defaultFeed ? "" : feed,
      q: query,
      genre,
      sort,
      page: page > 1 ? String(page) : "",
    };
    for (const [k, v] of Object.entries(updates)) {
      merged[k] = v === undefined ? "" : String(v);
    }
    for (const [k, v] of Object.entries(merged)) {
      if (v) next.set(k, v);
    }
    const qs = next.toString();
    return qs ? `${basePath}?${qs}` : basePath;
  }

  function handleSearch(e: FormEvent) {
    e.preventDefault();
    navigate(buildUrl({ feed: "", q: searchInput.trim(), genre: "", sort: "", page: "" }));
  }

  async function handleResumeItem(item: WatchProgressItem) {
    setResumingKey(item.mediaKey);
    try {
      const res = await client.startPlayback({
        mediaType: item.mediaType,
        tmdbId: item.tmdbId,
        seasonNumber: item.seasonNumber,
        episodeNumber: item.episodeNumber,
        keep: false,
      });
      if (res.download?.hash) {
        const fileQs = res.fileIndex >= 0 ? `?file=${res.fileIndex}` : "";
        navigate(`/watch/${res.download.hash}${fileQs}`);
        return;
      }
    } catch {
      // Fallback to detail page if auto-play fails
      navigate(
        item.mediaType === "episode" ? `/series/${item.tmdbId}` : `/movie/${item.tmdbId}`,
      );
    } finally {
      setResumingKey("");
    }
  }

  const displayError = error || data?.errorMessage || "";
  const hasTmdb = data?.hasTmdb ?? false;

  const feedTabs =
    mode === "discover"
      ? [
          { id: "trending", label: "Trending" },
          { id: "week", label: "This week" },
          { id: "new", label: "New & On air" },
        ]
      : mode === "tv"
        ? [
            { id: "popular", label: "Popular" },
            { id: "trending", label: "Trending" },
            { id: "new", label: "On the air" },
            { id: "top_rated", label: "Top rated" },
          ]
        : [
            { id: "popular", label: "Popular" },
            { id: "trending", label: "Trending" },
            { id: "new", label: "New releases" },
            { id: "top_rated", label: "Top rated" },
          ];

  const searchPlaceholder =
    mode === "movie"
      ? "Search any movie or IMDb ID…"
      : mode === "tv"
        ? "Search any TV series or IMDb ID…"
        : "Search movies, TV series, or IMDb ID…";

  return (
    <div>
      {mode === "discover" && !query && continueItems.length > 0 && (
        <section className="cw-section" aria-label="Continue watching">
          <div className="section-bar">
            <h2>Continue watching</h2>
            <Badge className="mono">{continueItems.length}</Badge>
          </div>
          <div className="cw-rail">
            {continueItems.map((item) => {
              const pct =
                item.duration > 0
                  ? Math.min(1, Math.max(0, item.position / item.duration))
                  : item.completed
                    ? 1
                    : 0;
              const isBusy = resumingKey === item.mediaKey;
              return (
                <article key={item.mediaKey} className="cw-card">
                  <button
                    type="button"
                    className="cw-thumb"
                    disabled={isBusy}
                    onClick={() => void handleResumeItem(item)}
                  >
                    {item.backdrop || item.poster ? (
                      <img
                        src={item.backdrop || item.poster}
                        alt={item.title}
                        loading="lazy"
                        referrerPolicy="no-referrer"
                      />
                    ) : null}
                    <span className="cw-play-pill">
                      <PlayIcon size={14} /> {isBusy ? "Starting…" : "Resume"}
                    </span>
                    {item.mediaType === "episode" && (
                      <span className="cw-ep-badge mono">
                        {formatEpisodeTag(item.seasonNumber, item.episodeNumber)}
                      </span>
                    )}
                    <div className="cw-progress-track">
                      <ProgressBar value={pct} label={`${item.title} watch progress`} />
                    </div>
                  </button>
                  <div className="cw-info">
                    <a
                      href={
                        item.mediaType === "episode"
                          ? `/series/${item.tmdbId}`
                          : `/movie/${item.tmdbId}`
                      }
                      onClick={(e) => {
                        e.preventDefault();
                        navigate(
                          item.mediaType === "episode"
                            ? `/series/${item.tmdbId}`
                            : `/movie/${item.tmdbId}`,
                        );
                      }}
                    >
                      <strong>{item.title}</strong>
                    </a>
                    {item.episodeTitle && <span className="muted">{item.episodeTitle}</span>}
                  </div>
                </article>
              );
            })}
          </div>
        </section>
      )}

      <section className="browse-head">
        <div className="browse-controls">
          <form className="search-box" onSubmit={handleSearch}>
            <SearchIcon size={16} className="search-box-icon" />
            <input
              aria-label={searchPlaceholder}
              placeholder={searchPlaceholder}
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
                  if (query) navigate(buildUrl({ q: "", page: "" }));
                }}
              >
                <CloseIcon size={13} />
              </button>
            )}
          </form>

          {mode !== "discover" && !query && feed !== "yts_fallback" && (
            <div className="filter-group">
              <select
                className="ds-select"
                aria-label="Genre"
                value={genre}
                onChange={(e) =>
                  navigate(buildUrl({ feed: "popular", genre: e.target.value, page: "" }))
                }
              >
                <option value="">All genres</option>
                {genres.map((g) => (
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
                  navigate(buildUrl({ feed: "popular", sort: e.target.value, page: "" }))
                }
              >
                <option value="">Popular</option>
                <option value="rating">Top rated</option>
                <option value="year">{mode === "tv" ? "Newest aired" : "Release date"}</option>
              </select>
            </div>
          )}
        </div>

        <div className="browse-subbar">
          <div className="feed-scroll">
            <div className="ds-seg">
              {feedTabs.map((t) => {
                const href = buildUrl({ feed: t.id === defaultFeed ? "" : t.id, q: "", genre: "", sort: "", page: "" });
                const isActive = !query && feed === t.id;
                return (
                  <a
                    key={t.id}
                    href={href}
                    className={`ds-seg-item ${isActive ? "active" : ""}`}
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
        <div className="poster-grid" aria-label="Loading catalog">
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
          title={!hasTmdb && feed !== "yts_fallback" ? "Connect TMDB to unlock Movies & TV" : "Couldn’t load catalog"}
          description={displayError}
          actions={
            <>
              <Button onClick={() => navigate("/settings")}>Open Settings</Button>
              {!hasTmdb && feed !== "yts_fallback" && (
                <Button
                  variant="secondary"
                  onClick={() => navigate("/movies?feed=yts_fallback")}
                >
                  Browse YTS movies fallback
                </Button>
              )}
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
            {data?.items.length ? (
              data.items.map((item) => (
                <CatalogCard
                  key={`${item.mediaType}-${item.tmdbId}-${item.ytsId}`}
                  item={item}
                  showTypeBadge={mode === "discover" || !!query}
                  navigate={navigate}
                />
              ))
            ) : (
              <EmptyState
                title="No titles found"
                description="Try a different title, IMDb ID, or genre filter."
                actions={<Button onClick={() => navigate(basePath)}>Reset filters</Button>}
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

          {feed !== "yts_fallback" && (
            <p className="attribution">
              Catalog metadata provided by{" "}
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
