import { useEffect, useState } from "react";
import { Code, ConnectError } from "@connectrpc/connect";
import { client, errMessage, formatBytes, mediaUrl } from "../api";
import type { DownloadStatus } from "../gen/bigflix/v1/bigflix_pb";
import type { NavigateFn } from "../types";
import {
  ArrowLeftIcon,
  CheckIcon,
  DownloadIcon,
  LinkIcon,
  RefreshIcon,
  TrashIcon,
} from "./Icons";
import { VideoPlayer } from "./VideoPlayer";
import { Badge, Button, EmptyState, ProgressBar } from "./ui";

interface WatchPageProps {
  hash: string;
  navigate: NavigateFn;
}

function stateTone(state?: string): "ok" | "accent" | "warn" | "neutral" {
  if (state === "complete") return "ok";
  if (state === "downloading") return "accent";
  if (state === "paused" || state === "metadata") return "warn";
  return "neutral";
}

export function WatchPage({ hash, navigate }: WatchPageProps) {
  const [latest, setLatest] = useState<DownloadStatus | null>(null);
  const [streamError, setStreamError] = useState("");
  const [actionError, setActionError] = useState("");
  const [actionBusy, setActionBusy] = useState(false);
  const [selectedFile, setSelectedFile] = useState("");
  const [codecNote, setCodecNote] = useState("");
  const [copiedVlc, setCopiedVlc] = useState(false);
  const [retrySignal, setRetrySignal] = useState(0);

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

  function handleCopyVlc() {
    const url = `${window.location.origin}${mediaUrl(`/media/${hash}`)}`;
    navigator.clipboard
      .writeText(url)
      .then(() => {
        setCopiedVlc(true);
        window.setTimeout(() => setCopiedVlc(false), 2000);
      })
      .catch(() => {
        setActionError(`Stream URL: ${url}`);
      });
  }

  const warningText =
    actionError ||
    latest?.error ||
    (latest && !latest.paused && latest.peers === 0 && latest.state !== "complete"
      ? "Waiting for reachable peers. Discovery retries automatically."
      : "");

  return (
    <div className="watch-shell">
      <div className="watch-top">
        <a
          className="ds-back"
          href="/downloads"
          onClick={(e) => {
            e.preventDefault();
            navigate("/downloads");
          }}
        >
          <ArrowLeftIcon size={16} /> Downloads
        </a>
        <div className="watch-top-row">
          <h1>{latest?.title || "Loading…"}</h1>
          {latest?.state && <Badge tone={stateTone(latest.state)}>{latest.state}</Badge>}
        </div>
      </div>

      {streamError && !latest ? (
        <EmptyState
          title="Download unavailable"
          description={streamError}
          actions={<Button onClick={() => navigate("/downloads")}>Back to downloads</Button>}
        />
      ) : (
        <>
          <div className="player-stage">
            <VideoPlayer
              hash={hash}
              latest={latest}
              retrySignal={retrySignal}
              onCodecNoteChange={setCodecNote}
            />
          </div>

          {latest?.state === "select-file" && (
            <section className="ds-card">
              <h2>Select video file</h2>
              <div className="file-picker-row">
                <select
                  id="file-select"
                  className="ds-select"
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
                <Button
                  disabled={actionBusy}
                  onClick={() => void runAction("select", Number(selectedFile))}
                >
                  Use video
                </Button>
              </div>
            </section>
          )}

          <section className="ds-card dl-card">
            <div className="dl-card-head">
              <div className="dl-stats">
                {latest ? (
                  <>
                    <span>
                      {formatBytes(latest.completed)} / {formatBytes(latest.size)}
                    </span>
                    {latest.speed > 0n && <span>{formatBytes(latest.speed)}/s</span>}
                    <span>
                      {latest.peers} peers{latest.discovering ? " · searching…" : ""}
                    </span>
                  </>
                ) : (
                  <span>Waiting for status…</span>
                )}
              </div>
              <span className="dl-pct">
                {latest ? `${(latest.progress * 100).toFixed(1)}%` : "0%"}
              </span>
            </div>

            <ProgressBar value={latest?.progress || 0} label="Download completion" />

            {warningText && (
              <div className="ds-alert ds-alert-warn" role="alert" style={{ margin: 0 }}>
                {warningText}
              </div>
            )}

            {codecNote && (
              <p className="muted" style={{ margin: 0 }}>
                {codecNote}
              </p>
            )}

            <div className="dl-actions">
              <Button
                variant="secondary"
                size="sm"
                disabled={!latest}
                onClick={handleCopyVlc}
              >
                {copiedVlc ? <CheckIcon size={14} /> : <LinkIcon size={14} />}
                {copiedVlc ? "VLC link copied" : "Copy VLC link"}
              </Button>
              <Button
                variant="secondary"
                size="sm"
                disabled={!latest}
                onClick={() => setRetrySignal((n) => n + 1)}
              >
                <RefreshIcon size={14} /> Retry player
              </Button>
              <Button
                variant="secondary"
                size="sm"
                disabled={actionBusy || !latest}
                onClick={() => void runAction(latest?.paused ? "resume" : "pause")}
              >
                {latest?.paused ? "Resume" : "Pause"}
              </Button>
              <Button
                variant="secondary"
                size="sm"
                disabled={actionBusy || !latest}
                onClick={() => void runAction(latest?.keep ? "unkeep" : "keep")}
              >
                {latest?.keep ? "Kept" : "Keep"}
              </Button>
              <Button
                variant="secondary"
                size="sm"
                disabled={actionBusy || !latest}
                onClick={() => void runAction("reconnect")}
              >
                Find peers
              </Button>
              <a
                className="ds-btn ds-btn-secondary ds-btn-sm"
                href={mediaUrl(`/media/${hash}?download=1`)}
              >
                <DownloadIcon size={14} /> Save file
              </a>
              <Button
                variant="danger"
                size="sm"
                disabled={actionBusy || !latest}
                onClick={() => void handleDelete()}
              >
                <TrashIcon size={14} /> Delete
              </Button>
            </div>
          </section>
        </>
      )}
    </div>
  );
}
