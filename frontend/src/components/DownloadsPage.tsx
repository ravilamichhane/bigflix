import { useEffect, useState } from "react";
import { client, errMessage, formatBytes } from "../api";
import type { DownloadStatus } from "../gen/bigflix/v1/bigflix_pb";
import type { NavigateFn } from "../types";
import { PlayIcon, TrashIcon } from "./Icons";

interface DownloadsPageProps {
  navigate: NavigateFn;
}

export function DownloadsPage({ navigate }: DownloadsPageProps) {
  const [downloads, setDownloads] = useState<DownloadStatus[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [deletingHash, setDeletingHash] = useState("");

  async function load(silent = false) {
    if (!silent) setLoading(true);
    try {
      const res = await client.listDownloads({});
      setDownloads(res.downloads);
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

  async function handleDelete(d: DownloadStatus) {
    if (
      !window.confirm(
        `Delete "${d.title}" and remove downloaded files (${formatBytes(d.completed)})?`,
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

  return (
    <div className="downloads-page">
      <div className="page-head">
        <h1>Downloads</h1>
        {downloads.length > 0 && <span className="count">{downloads.length}</span>}
      </div>

      {error && (
        <div className="error-banner" role="alert">
          {error}
        </div>
      )}

      {loading ? (
        <div className="empty">
          <p>Loading…</p>
        </div>
      ) : downloads.length ? (
        <div className="download-list">
          {downloads.map((d) => (
            <div key={d.hash} className="download-row">
              <a
                className="download-row-main"
                href={`/watch/${d.hash}`}
                onClick={(e) => {
                  e.preventDefault();
                  navigate(`/watch/${d.hash}`);
                }}
              >
                <div className="download-thumb">
                  {d.poster ? (
                    <img src={d.poster} alt="" loading="lazy" />
                  ) : (
                    <PlayIcon size={18} />
                  )}
                </div>
                <div className="download-row-info">
                  <h2>{d.title}</h2>
                  <p className="dl-row-meta">
                    <span className="dl-state-inline">{d.state}</span>
                    {" · "}
                    {(d.progress * 100).toFixed(0)}% · {formatBytes(d.completed)} /{" "}
                    {formatBytes(d.size)}
                    {d.speed > 0n ? ` · ${formatBytes(d.speed)}/s` : ""}
                    {d.keep ? " · Kept" : ""}
                  </p>
                  <progress
                    max={1}
                    value={d.progress}
                    aria-label={`${d.title} download progress`}
                  />
                  {d.error && <p className="dl-warn">{d.error}</p>}
                </div>
              </a>
              <button
                type="button"
                className="icon-btn danger-icon"
                aria-label={`Delete ${d.title}`}
                title="Delete download"
                disabled={deletingHash === d.hash}
                onClick={() => void handleDelete(d)}
              >
                <TrashIcon size={17} />
              </button>
            </div>
          ))}
        </div>
      ) : (
        <div className="empty">
          <h2>No downloads yet</h2>
          <p>Pick a movie from Discover to start streaming or downloading.</p>
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
      )}
    </div>
  );
}
