import { useEffect, useState } from "react";
import { client, errMessage, formatBytes } from "../api";
import type { DownloadStatus } from "../gen/bigflix/v1/bigflix_pb";
import type { NavigateFn } from "../types";
import { PlayIcon, TrashIcon } from "./Icons";
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
    <div>
      <div className="page-title-row">
        <h1>Downloads</h1>
        {downloads.length > 0 && <Badge className="mono">{downloads.length}</Badge>}
      </div>

      {error && (
        <div className="ds-alert ds-alert-danger" role="alert">
          {error}
        </div>
      )}

      {loading ? (
        <EmptyState title="Loading downloads…" />
      ) : downloads.length ? (
        <div className="download-list">
          {downloads.map((d) => (
            <div key={d.hash} className="download-item">
              <a
                className="download-item-link"
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

                <div className="download-item-body">
                  <div className="download-item-title">
                    <h2>{d.title}</h2>
                    <Badge tone={stateTone(d.state)}>{d.state}</Badge>
                    {d.keep && <Badge>Kept</Badge>}
                  </div>

                  <div className="download-item-meta">
                    {(d.progress * 100).toFixed(0)}% · {formatBytes(d.completed)} /{" "}
                    {formatBytes(d.size)}
                    {d.speed > 0n ? ` · ${formatBytes(d.speed)}/s` : ""}
                  </div>

                  <ProgressBar value={d.progress} label={`${d.title} progress`} />
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
                aria-label={`Delete ${d.title}`}
                title="Delete download"
                disabled={deletingHash === d.hash}
                onClick={() => void handleDelete(d)}
              >
                <TrashIcon size={17} />
              </Button>
            </div>
          ))}
        </div>
      ) : (
        <EmptyState
          title="No downloads yet"
          description="Pick any movie from Discover to start streaming or saving locally."
          actions={<Button onClick={() => navigate("/")}>Browse movies</Button>}
        />
      )}
    </div>
  );
}
