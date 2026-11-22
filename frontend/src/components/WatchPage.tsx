import { useCallback, useEffect, useState } from "react";
import { Code, ConnectError } from "@connectrpc/connect";
import { client, errMessage, formatBytes, mediaUrl } from "../api";
import type { DownloadStatus, Episode } from "../gen/bigflix/v1/bigflix_pb";
import type { NavigateFn } from "../types";
import {
  ArrowLeftIcon,
  CheckIcon,
  DownloadIcon,
  LinkIcon,
  PlayIcon,
  RefreshIcon,
  SkipNextIcon,
  SkipPrevIcon,
  SlidersIcon,
  TrashIcon,
} from "./Icons";
import { SourcesDrawer } from "./SourcesDrawer";
import { VideoPlayer } from "./VideoPlayer";
import { Badge, Button, EmptyState, ProgressBar } from "./ui";

interface WatchPageProps {
  hash: string;
  fileIndex?: number;
  navigate: NavigateFn;
}

function stateTone(state?: string): "ok" | "accent" | "warn" | "neutral" {
  if (state === "complete") return "ok";
  if (state === "downloading" || state === "failover") return "accent";
  if (state === "paused" || state === "metadata") return "warn";
  return "neutral";
}

function epTag(s: number, e: number): string {
  return `S${String(s).padStart(2, "0")}E${String(e).padStart(2, "0")}`;
}

export function WatchPage({ hash, fileIndex, navigate }: WatchPageProps) {
  const [latest, setLatest] = useState<DownloadStatus | null>(null);
  const [streamError, setStreamError] = useState("");
  const [actionError, setActionError] = useState("");
  const [actionBusy, setActionBusy] = useState(false);
  const [selectedFile, setSelectedFile] = useState("");
  const [codecNote, setCodecNote] = useState("");
  const [copiedVlc, setCopiedVlc] = useState(false);
  const [retrySignal, setRetrySignal] = useState(0);
  const [showSources, setShowSources] = useState(false);
  const [nearEnd, setNearEnd] = useState(false);
  const [seasonEpisodes, setSeasonEpisodes] = useState<Episode[]>([]);
  const [switchingEp, setSwitchingEp] = useState(false);

  const handleNearEnd = useCallback((v: boolean) => {
    setNearEnd(v);
  }, []);

  useEffect(() => {
    setShowSources(false);
    setNearEnd(false);
    setSwitchingEp(false);
    setActionError("");
  }, [hash, fileIndex]);

  useEffect(() => {
    const ac = new AbortController();
    let timer: number | undefined;

    async function subscribe() {
      while (!ac.signal.aborted) {
        try {
          for await (const res of client.watchDownload(
            { hash, fileIndex },
            { signal: ac.signal },
          )) {
            if (ac.signal.aborted) return;
            if (res.download) {
              setLatest(res.download);
              setStreamError("");
              // Follow silent auto-failover transparently to backup source!
              if (res.download.replacedByHash && res.download.replacedByHash !== hash) {
                navigate(`/watch/${res.download.replacedByHash}`);
                return;
              }
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
  }, [hash, fileIndex, navigate]);

  // Load current season's episode list so Prev / Next Episode controls know exact neighbors
  const isEpisode = latest?.mediaType === "episode" && (latest?.tmdbId || 0) > 0;
  const seriesId = isEpisode ? latest!.tmdbId : 0;
  const currentSeason = isEpisode ? latest!.seasonNumber : 0;
  const currentEpisode = isEpisode ? latest!.episodeNumber : 0;

  useEffect(() => {
    if (!seriesId || currentSeason < 0) {
      setSeasonEpisodes([]);
      return;
    }
    let active = true;
    client
      .getSeasonDetail({ tmdbId: seriesId, seasonNumber: currentSeason })
      .then((res) => {
        if (active) setSeasonEpisodes(res.episodes);
      })
      .catch(() => {});
    return () => {
      active = false;
    };
  }, [seriesId, currentSeason]);

  const prevEpisode = seasonEpisodes.find((e) => e.episodeNumber === currentEpisode - 1);
  const nextEpisode = seasonEpisodes.find((e) => e.episodeNumber === currentEpisode + 1);

  useEffect(() => {
    if (latest?.state === "select-file" && latest.files.length > 0 && !selectedFile) {
      setSelectedFile(String(latest.files[0].index));
    }
  }, [latest, selectedFile]);

  async function runAction(action: string, idx = 0) {
    setActionBusy(true);
    setActionError("");
    try {
      const res = await client.updateDownload({ hash, action, fileIndex: idx });
      if (res.download) setLatest(res.download);
    } catch (err) {
      setActionError(errMessage(err));
    } finally {
      setActionBusy(false);
    }
  }

  async function jumpEpisode(ep: Episode) {
    if (!seriesId) return;
    setSwitchingEp(true);
    setActionError("");
    try {
      const res = await client.startPlayback({
        mediaType: "episode",
        tmdbId: seriesId,
        seasonNumber: ep.seasonNumber,
        episodeNumber: ep.episodeNumber,
        keep: latest?.keep || false,
      });
      if (res.download?.hash) {
        const fQs = res.fileIndex >= 0 ? `?file=${res.fileIndex}` : "";
        navigate(`/watch/${res.download.hash}${fQs}`);
      }
    } catch (err) {
      setActionError(errMessage(err));
      setSwitchingEp(false);
    }
  }

  async function handleSwitchSource(nextHash: string, keep: boolean) {
    setActionBusy(true);
    setActionError("");
    try {
      const res = await client.switchSource({ currentHash: hash, nextHash, keep });
      setShowSources(false);
      if (res.download?.hash) {
        navigate(`/watch/${res.download.hash}`);
      }
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
    const path = fileIndex !== undefined ? `/media/${hash}?file=${fileIndex}` : `/media/${hash}`;
    const url = `${window.location.origin}${mediaUrl(path)}`;
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

  const backHref = isEpisode
    ? `/series/${seriesId}`
    : (latest?.tmdbId || 0) > 0
      ? `/movie/${latest!.tmdbId}`
      : "/downloads";
  const backLabel = isEpisode
    ? latest?.title || "Back to series"
    : (latest?.tmdbId || 0) > 0
      ? "Back to movie"
      : "Downloads";

  const warningText =
    actionError ||
    latest?.error ||
    (latest &&
    !latest.paused &&
    latest.peers === 0 &&
    latest.state !== "complete" &&
    latest.state !== "failover"
      ? latest.failoverTotal > latest.failoverAttempt
        ? `Searching peers… will auto-switch to backup source (${latest.failoverAttempt}/${latest.failoverTotal}) if stalled.`
        : "Waiting for reachable peers. Discovery retries automatically."
      : "");

  const saveFilePath =
    fileIndex !== undefined
      ? `/media/${hash}?download=1&file=${fileIndex}`
      : `/media/${hash}?download=1`;

  return (
    <div className="watch-shell">
      <div className="watch-top">
        <div className="watch-top-nav">
          <a
            className="ds-back"
            href={backHref}
            onClick={(e) => {
              e.preventDefault();
              navigate(backHref);
            }}
          >
            <ArrowLeftIcon size={16} /> {backLabel}
          </a>

          {isEpisode && (prevEpisode || nextEpisode) && (
            <div className="watch-ep-nav">
              {prevEpisode && (
                <Button
                  variant="secondary"
                  size="sm"
                  disabled={switchingEp}
                  onClick={() => void jumpEpisode(prevEpisode)}
                  title={prevEpisode.name}
                >
                  <SkipPrevIcon size={13} />
                  {epTag(prevEpisode.seasonNumber, prevEpisode.episodeNumber)}
                </Button>
              )}
              {nextEpisode && (
                <Button
                  size="sm"
                  disabled={switchingEp}
                  onClick={() => void jumpEpisode(nextEpisode)}
                  title={nextEpisode.name}
                >
                  {switchingEp
                    ? "Loading next…"
                    : `Next · ${epTag(nextEpisode.seasonNumber, nextEpisode.episodeNumber)}`}
                  <SkipNextIcon size={13} />
                </Button>
              )}
            </div>
          )}
        </div>

        <div className="watch-top-row">
          <div>
            <h1>
              {latest?.title || "Loading…"}
              {isEpisode && (
                <span className="watch-ep-pill mono">
                  {" "}
                  · {epTag(currentSeason, currentEpisode)}
                </span>
              )}
            </h1>
            {isEpisode && latest?.episodeTitle && (
              <p className="watch-ep-subtitle">{latest.episodeTitle}</p>
            )}
          </div>
          <div className="watch-badges">
            {latest?.activeSourceLabel && <Badge>{latest.activeSourceLabel}</Badge>}
            {latest?.state && <Badge tone={stateTone(latest.state)}>{latest.state}</Badge>}
          </div>
        </div>
      </div>

      {latest?.failoverNote && (
        <div className="ds-alert">{latest.failoverNote}</div>
      )}

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
              fileIndex={fileIndex}
              latest={latest}
              retrySignal={retrySignal}
              onCodecNoteChange={setCodecNote}
              onNearEnd={handleNearEnd}
            />
          </div>

          {nearEnd && nextEpisode && (
            <div className="ds-card up-next-banner">
              <div className="up-next-copy">
                <span className="muted mono">UP NEXT</span>
                <strong>
                  {epTag(nextEpisode.seasonNumber, nextEpisode.episodeNumber)} ·{" "}
                  {nextEpisode.name || `Episode ${nextEpisode.episodeNumber}`}
                </strong>
              </div>
              <Button
                disabled={switchingEp}
                onClick={() => void jumpEpisode(nextEpisode)}
              >
                <PlayIcon size={14} />
                {switchingEp ? "Starting…" : "Play next episode"}
              </Button>
            </div>
          )}

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
              {(latest?.tmdbId || 0) > 0 && (
                <Button
                  variant="secondary"
                  size="sm"
                  disabled={!latest || actionBusy}
                  onClick={() => setShowSources(true)}
                >
                  <SlidersIcon size={14} /> Switch source
                </Button>
              )}
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
                href={mediaUrl(saveFilePath)}
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

          {showSources && latest && latest.tmdbId > 0 && (
            <SourcesDrawer
              mediaType={latest.mediaType === "episode" ? "episode" : "movie"}
              tmdbId={latest.tmdbId}
              seasonNumber={latest.seasonNumber}
              episodeNumber={latest.episodeNumber}
              activeHash={hash}
              title={`Switch source · ${latest.title}`}
              onSelect={handleSwitchSource}
              onClose={() => setShowSources(false)}
            />
          )}
        </>
      )}
    </div>
  );
}
