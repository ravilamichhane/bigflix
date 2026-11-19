import { useEffect, useState } from "react";
import { Code, ConnectError } from "@connectrpc/connect";
import { client, errMessage, formatBytes, mediaUrl } from "../api";
import type { DownloadStatus } from "../gen/bigflix/v1/bigflix_pb";
import type { NavigateFn } from "../types";
import { ArrowLeftIcon, DownloadIcon, TrashIcon } from "./Icons";
import { VideoPlayer } from "./VideoPlayer";

interface WatchPageProps {
  hash: string;
  navigate: NavigateFn;
}

export function WatchPage({ hash, navigate }: WatchPageProps) {
  const [latest, setLatest] = useState<DownloadStatus | null>(null);
  const [streamError, setStreamError] = useState("");
  const [actionError, setActionError] = useState("");
  const [actionBusy, setActionBusy] = useState(false);
  const [selectedFile, setSelectedFile] = useState("");
  const [codecNote, setCodecNote] = useState("");

  useEffect(() => {
    const ac = new AbortController();
    let timer: number | undefined;

    async function subscribe() {
      while (!ac.signal.aborted) {
        try {
          for await (const res of client.watchDownload(
            { hash },
            { signal: ac.signal },
          )) {
            if (ac.signal.aborted) return;
            if (res.download) {
              setLatest(res.download);
              setStreamError("");
            }
          }
        } catch (err) {
          if (ac.signal.aborted) return;
          if (err instanceof ConnectError && err.code === Code.NotFound) {
            setStreamError("This download no longer exists.");
            return;
          }
        }
        await new Promise<void>((resolve) => {
          timer = window.setTimeout(resolve, 2000);
        });
      }
    }

    void subscribe();
    return () => {
      ac.abort();
      if (timer !== undefined) window.clearTimeout(timer);
    };
  }, [hash]);

  useEffect(() => {
    if (latest?.state === "select-file" && latest.files.length > 0 && !selectedFile) {
      setSelectedFile(String(latest.files[0].index));
    }
  }, [latest, selectedFile]);

  async function runAction(action: string, fileIndex = 0) {
    setActionBusy(true);
    setActionError("");
    try {
      const res = await client.updateDownload({ hash, action, fileIndex });
      if (res.download) setLatest(res.download);
    } catch (err) {
      setActionError(errMessage(err));
    } finally {
      setActionBusy(false);
    }
  }

  async function handleDelete() {
    const label = latest?.title || hash;
    if (
      !window.confirm(
        `Delete "${label}" and remove all downloaded files from disk?`,
      )
    ) {
      return;
    }
    setActionBusy(true);
    setActionError("");
    try {
      await client.deleteDownload({ hash });
      navigate("/downloads");
    } catch (err) {
      setActionError(errMessage(err));
      setActionBusy(false);
    }
  }

  const warningText =
    actionError ||
    latest?.error ||
    (latest && !latest.paused && latest.peers === 0 && latest.state !== "complete"
      ? "Waiting for reachable peers. Discovery retries automatically."
      : "");

  return (
    <div className="watch-page">
      <div className="watch-header">
        <a
          className="back"
          href="/downloads"
          onClick={(e) => {
            e.preventDefault();
            navigate("/downloads");
          }}
        >
          <ArrowLeftIcon size={16} /> Downloads
        </a>
        <h1>{latest?.title || "Loading…"}</h1>
      </div>

      {streamError && !latest ? (
        <div className="empty">
          <h2>Download unavailable</h2>
          <p>{streamError}</p>
          <a
            className="button"
            href="/downloads"
            onClick={(e) => {
              e.preventDefault();
              navigate("/downloads");
            }}
          >
            Back to downloads
          </a>
        </div>
      ) : (
        <>
          <div className="player-shell">
            <VideoPlayer hash={hash} latest={latest} onCodecNoteChange={setCodecNote} />
          </div>

          {latest?.state === "select-file" && (
            <section id="file-picker" className="panel-card">
              <h2>Select video file</h2>
              <div className="file-picker-row">
                <select
                  id="file-select"
                  aria-label="Video file"
                  value={selectedFile}
                  onChange={(e) => setSelectedFile(e.target.value)}
                >
                  {latest.files.map((f) => (
                    <option key={f.index} value={f.index}>
                      {f.name} · {formatBytes(f.size)}
                    </option>
                  ))}
                </select>
                <button
                  type="button"
                  className="button"
                  disabled={actionBusy}
                  onClick={() => void runAction("select", Number(selectedFile))}
                >
                  Use video
                </button>
              </div>
            </section>
          )}

          <section className="panel-card download-panel">
            <div className="dl-head">
              <div className="dl-title-group">
                <span className="dl-state">{latest?.state || "loading"}</span>
                {latest && (
                  <span className="dl-meta">
                    {formatBytes(latest.completed)} / {formatBytes(latest.size)}
                    {latest.speed > 0n ? ` · ${formatBytes(latest.speed)}/s` : ""}
                    {` · ${latest.peers} peers`}
                    {latest.discovering ? " · searching…" : ""}
                  </span>
                )}
              </div>
              <strong className="dl-pct">
                {latest ? `${(latest.progress * 100).toFixed(1)}%` : "0%"}
              </strong>
            </div>

            <progress
              id="download-progress"
              max={1}
              value={latest?.progress || 0}
              aria-label="Download completion"
            />

            {warningText && (
              <p className="dl-warn" role="alert">
                {warningText}
              </p>
            )}

            {codecNote && <p className="muted dl-note">{codecNote}</p>}

            <div className="download-controls">
              <button
                type="button"
                className="button secondary"
                disabled={actionBusy || !latest}
                onClick={() => void runAction(latest?.paused ? "resume" : "pause")}
              >
                {latest?.paused ? "Resume" : "Pause"}
              </button>
              <button
                type="button"
                className="button secondary"
                disabled={actionBusy || !latest}
                onClick={() => void runAction(latest?.keep ? "unkeep" : "keep")}
              >
                {latest?.keep ? "Kept" : "Keep"}
              </button>
              <button
                type="button"
                className="button secondary"
                disabled={actionBusy || !latest}
                onClick={() => void runAction("reconnect")}
              >
                Find peers
              </button>
              <a
                className="button secondary"
                href={mediaUrl(`/media/${hash}?download=1`)}
              >
                <DownloadIcon size={15} /> Save
              </a>
              <button
                type="button"
                className="button danger"
                disabled={actionBusy || !latest}
                onClick={() => void handleDelete()}
              >
                <TrashIcon size={15} /> Delete
              </button>
            </div>
          </section>
        </>
      )}
    </div>
  );
}
