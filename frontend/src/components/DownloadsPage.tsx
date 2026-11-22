import { useEffect, useMemo, useState } from "react";
import { client, errMessage, formatBytes } from "../api";
import type { DownloadStatus } from "../gen/bigflix/v1/bigflix_pb";
import type { NavigateFn } from "../types";
import { ChevronDownIcon, ChevronRightIcon, PlayIcon, TrashIcon } from "./Icons";
import { Badge, Button, EmptyState, ProgressBar } from "./ui";

interface DownloadsPageProps {
  navigate: NavigateFn;
}

function stateTone(state: string): "ok" | "accent" | "warn" | "neutral" {
  if (state === "complete") return "ok";
  if (state === "downloading") return "accent";
  if (state === "paused" || state === "metadata") return "warn";
  return "neutral";
}

function epTag(s: number, e: number): string {
  return `S${String(s).padStart(2, "0")}E${String(e).padStart(2, "0")}`;
}

interface SeriesGroup {
  key: string;
  tmdbId: number;
  title: string;
  poster: string;
  items: DownloadStatus[];
}

export function DownloadsPage({ navigate }: DownloadsPageProps) {
  const [downloads, setDownloads] = useState<DownloadStatus[]>([]);
  const [storageUsed, setStorageUsed] = useState<bigint>(0n);
  const [storageBudget, setStorageBudget] = useState<bigint>(0n);
  const [autoEvict, setAutoEvict] = useState(true);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [deletingHash, setDeletingHash] = useState("");
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({});

  async function load(silent = false) {
    if (!silent) setLoading(true);
    try {
      const res = await client.listDownloads({});
      setDownloads(res.downloads);
      setStorageUsed(res.storageUsedBytes);
      setStorageBudget(res.storageBudgetBytes);
      setAutoEvict(res.autoEvict);
      setError("");
    } catch (err) {
      setError(errMessage(err));
    } finally {
      if (!silent) setLoading(false);
    }
  }

  useEffect(() => {
    void load(false);
    const id = window.setInterval(() => {
      void load(true);
    }, 2000);
    return () => window.clearInterval(id);
  }, []);

  const { movies, seriesGroups } = useMemo(() => {
    const mov: DownloadStatus[] = [];
    const bySeries = new Map<string, SeriesGroup>();
    for (const d of downloads) {
      if (d.mediaType === "episode") {
        const key = d.tmdbId > 0 ? `tv:${d.tmdbId}` : `title:${d.title}`;
        let grp = bySeries.get(key);
        if (!grp) {
          grp = { key, tmdbId: d.tmdbId, title: d.title, poster: d.poster, items: [] };
          bySeries.set(key, grp);
        }
        grp.items.push(d);
      } else {
        mov.push(d);
      }
    }
    for (const g of bySeries.values()) {
      g.items.sort((a, b) =>
        a.seasonNumber !== b.seasonNumber
          ? a.seasonNumber - b.seasonNumber
          : a.episodeNumber - b.episodeNumber,
      );
    }
    return { movies: mov, seriesGroups: Array.from(bySeries.values()) };
  }, [downloads]);

  async function handleDelete(d: DownloadStatus) {
    const label =
      d.mediaType === "episode"
        ? `${d.title} ${epTag(d.seasonNumber, d.episodeNumber)}`
        : d.title;
    if (
      !window.confirm(
        `Delete "${label}" and remove downloaded files (${formatBytes(d.completed)})?`,
      )
    ) {
      return;
    }
    setDeletingHash(d.hash);
    setError("");
    try {
      await client.deleteDownload({ hash: d.hash });
      setDownloads((prev) => prev.filter((item) => item.hash !== d.hash));
    } catch (err) {
      setError(errMessage(err));
    } finally {
      setDeletingHash("");
    }
  }

  const budgetPct =
    storageBudget > 0n ? Math.min(1, Number(storageUsed) / Number(storageBudget)) : 0;

  function renderDownloadRow(d: DownloadStatus, showEpisodePrefix = false) {
    const label = showEpisodePrefix
      ? `${epTag(d.seasonNumber, d.episodeNumber)}${d.episodeTitle ? ` · ${d.episodeTitle}` : ""}`
      : d.title;
    return (
      <div key={d.hash} className="download-item">
        <a
          className="download-item-link"
          href={`/watch/${d.hash}`}
          onClick={(e) => {
            e.preventDefault();
            navigate(`/watch/${d.hash}`);
          }}
        >
          {!showEpisodePrefix && (
            <div className="download-thumb">
              {d.poster ? (
                <img src={d.poster} alt="" loading="lazy" />
              ) : (
                <PlayIcon size={18} />
              )}
            </div>
          )}

          <div className="download-item-body">
            <div className="download-item-title">
              <h2>{label}</h2>
              <Badge tone={stateTone(d.state)}>{d.state}</Badge>
              {d.keep && <Badge>Kept</Badge>}
              {d.episodeFiles.length > 1 && (
                <Badge tone="accent">Pack · {d.episodeFiles.length} eps</Badge>
              )}
            </div>

            <div className="download-item-meta">
              {(d.progress * 100).toFixed(0)}% · {formatBytes(d.completed)} /{" "}
              {formatBytes(d.size)}
              {d.speed > 0n ? ` · ${formatBytes(d.speed)}/s` : ""}
              {d.activeSourceLabel ? ` · ${d.activeSourceLabel}` : ""}
            </div>

            <ProgressBar value={d.progress} label={`${label} progress`} />
            {d.error && (
              <span className="muted" style={{ color: "var(--status-warn)" }}>
                {d.error}
              </span>
            )}
          </div>
        </a>

        <Button
          variant="ghost"
          size="icon"
          aria-label={`Delete ${label}`}
          title="Delete download"
          disabled={deletingHash === d.hash}
          onClick={() => void handleDelete(d)}
        >
          <TrashIcon size={17} />
        </Button>
      </div>
    );
  }

  return (
    <div>
      <div className="page-title-row">
        <h1>Downloads</h1>
        {downloads.length > 0 && <Badge className="mono">{downloads.length}</Badge>}
      </div>

      {storageBudget > 0n && (
        <div className="ds-card storage-bar-card">
          <div className="storage-bar-head">
            <span className="mono">
              Cache: {formatBytes(storageUsed)} / {formatBytes(storageBudget)}
            </span>
            <Badge tone={autoEvict ? "ok" : "neutral"}>
              {autoEvict ? "Auto-evict cached ON" : "Manual cleanup"}
            </Badge>
          </div>
          <ProgressBar value={budgetPct} label="Storage budget usage" />
        </div>
      )}

      {error && (
        <div className="ds-alert ds-alert-danger" role="alert">
          {error}
        </div>
      )}

      {loading ? (
        <EmptyState title="Loading downloads…" />
      ) : downloads.length ? (
        <div className="download-sections">
          {seriesGroups.map((grp) => {
            const isCollapsed = !!collapsed[grp.key];
            return (
              <section key={grp.key} className="ds-card dl-series-group">
                <div className="dl-series-head">
                  <button
                    type="button"
                    className="dl-series-toggle"
                    onClick={() =>
                      setCollapsed((prev) => ({ ...prev, [grp.key]: !prev[grp.key] }))
                    }
                  >
                    {isCollapsed ? <ChevronRightIcon size={16} /> : <ChevronDownIcon size={16} />}
                    <div className="download-thumb dl-series-thumb">
                      {grp.poster ? <img src={grp.poster} alt="" /> : <PlayIcon size={16} />}
                    </div>
                    <div className="dl-series-title">
                      <strong>{grp.title}</strong>
                      <span className="muted mono">
                        {grp.items.length} {grp.items.length === 1 ? "item" : "items"}
                      </span>
                    </div>
                  </button>
                  {grp.tmdbId > 0 && (
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => navigate(`/series/${grp.tmdbId}`)}
                    >
                      Open show →
                    </Button>
                  )}
                </div>

                {!isCollapsed && (
                  <div className="download-list dl-series-items">
                    {grp.items.map((d) => renderDownloadRow(d, true))}
                  </div>
                )}
              </section>
            );
          })}

          {movies.length > 0 && (
            <div className="download-list">
              {movies.map((d) => renderDownloadRow(d, false))}
            </div>
          )}
        </div>
      ) : (
        <EmptyState
          title="No downloads yet"
          description="Pick any movie or TV episode from Discover to start streaming or saving locally."
          actions={<Button onClick={() => navigate("/")}>Explore catalog</Button>}
        />
      )}
    </div>
  );
}
