import { useEffect, useState } from "react";
import { client, errMessage, formatBytes } from "../api";
import type { DownloadStatus } from "../gen/bigflix/v1/bigflix_pb";
import type { NavigateFn } from "../types";

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
        `Delete "${d.title}" and remove its downloaded files from disk (${formatBytes(d.completed)})?`,
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
    <>
      <section className="intro">
        <div className="eyebrow">YOUR LOCAL LIBRARY</div>
        <h1>Watch now. Keep for later.</h1>
        <p>
          Your downloads continue while you browse. Open a movie to watch its progress and
          start playback, or delete downloads to free disk space.
        </p>
      </section>

      {error && (
        <div className="error-banner" role="alert">
          {error}
        </div>
      )}

      {loading ? (
        <div className="empty">
          <h2>Loading downloads…</h2>
        </div>
      ) : (
        <div className="download-list">
          {downloads.length ? (
            downloads.map((d) => (
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
                    {d.poster ? <img src={d.poster} alt="" loading="lazy" /> : "▶"}
                  </div>
                  <div className="download-row-info">
                    <h2>{d.title}</h2>
                    <p>
                      {d.state} · {(d.progress * 100).toFixed(1)}% ·{" "}
                      {formatBytes(d.completed)} / {formatBytes(d.size)}
                      {d.keep ? " · Kept" : ""}
                    </p>
                    <progress
                      max={1}
                      value={d.progress}
                      aria-label={`${d.title} download progress`}
                    />
                    {d.error && <p>{d.error}</p>}
                  </div>
                </a>
                <div className="download-row-actions">
                  <a
                    className="text-link"
                    href={`/watch/${d.hash}`}
                    onClick={(e) => {
                      e.preventDefault();
                      navigate(`/watch/${d.hash}`);
                    }}
                  >
                    Open player →
                  </a>
                  <button
                    type="button"
                    className="button danger"
                    disabled={deletingHash === d.hash}
                    onClick={() => handleDelete(d)}
                  >
                    {deletingHash === d.hash ? "Deleting…" : "Delete"}
                  </button>
                </div>
              </div>
            ))
          ) : (
            <div className="empty">
              <h2>A movie night starts here.</h2>
              <p>Choose a movie and select Watch now or Keep downloaded to start.</p>
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
      )}

      <p className="freshness">Live download list · refreshes automatically.</p>
    </>
  );
}
