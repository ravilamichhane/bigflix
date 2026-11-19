import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { Code, ConnectError } from "@connectrpc/connect";
import { client, errMessage, formatBytes, mediaUrl } from "../api";
import type {
  DownloadStatus,
  ProbeMediaResponse,
} from "../gen/bigflix/v1/bigflix_pb";
import type { NavigateFn, VjsButton, VjsPlayer } from "../types";
import { SubtitleDialog } from "./SubtitleDialog";

interface WatchPageProps {
  hash: string;
  navigate: NavigateFn;
}

export function WatchPage({ hash, navigate }: WatchPageProps) {
  const playerWrapRef = useRef<HTMLDivElement | null>(null);
  const subtitleBtnRef = useRef<VjsButton | null>(null);

  const [latest, setLatest] = useState<DownloadStatus | null>(null);
  const [streamError, setStreamError] = useState("");
  const [actionError, setActionError] = useState("");
  const [actionBusy, setActionBusy] = useState(false);
  const [selectedFile, setSelectedFile] = useState("");

  const [playerInst, setPlayerInst] = useState<VjsPlayer | null>(null);
  const [overlayHost, setOverlayHost] = useState<HTMLDivElement | null>(null);
  const [started, setStarted] = useState(false);
  const [ready, setReady] = useState(false);
  const [placeholderText, setPlaceholderText] = useState("Getting your movie ready…");
  const [playerStateText, setPlayerStateText] = useState("Preparing torrent metadata…");
  const [codecNoteText, setCodecNoteText] = useState("");
  const [subOpenSignal, setSubOpenSignal] = useState(0);

  const latestRef = useRef<DownloadStatus | null>(null);
  const readyRef = useRef(false);
  const startedRef = useRef(false);
  const savedPositionRef = useRef(0);
  const restoredRef = useRef(false);
  const probePromiseRef = useRef<Promise<ProbeMediaResponse> | null>(null);

  useEffect(() => {
    latestRef.current = latest;
  }, [latest]);

  // Subscribe to real-time ConnectRPC WatchDownload stream.
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
          setPlayerStateText("Status connection lost; reconnecting…");
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

  // Keep file picker default in sync when multiple files are present.
  useEffect(() => {
    if (latest?.state === "select-file" && latest.files.length > 0 && !selectedFile) {
      setSelectedFile(String(latest.files[0].index));
    }
  }, [latest, selectedFile]);

  // Update readiness and pre-playback status labels when download updates arrive.
  useEffect(() => {
    if (!latest) return;
    if (!startedRef.current) {
      savedPositionRef.current = latest.position || 0;
    }
    const isReady =
      latest.playable && (latest.state === "downloading" || latest.state === "complete");
    readyRef.current = isReady;
    setReady(isReady);

    if (!startedRef.current) {
      setCodecNoteText(
        latest.size > 0n && !latest.playable
          ? "This container needs FFmpeg to play in the browser. Install ffmpeg and restart Bigflix, or download and use a desktop player."
          : !latest.direct
            ? "This file is converted on the fly for your browser. Seeking restarts the stream at the new position."
            : "MP4/WebM containers may still contain unsupported codecs. Try an x264/H.264 variant if playback fails.",
      );
      setPlaceholderText(
        latest.state === "metadata"
          ? "Finding peers and loading torrent metadata…"
          : latest.state === "paused"
            ? "Download paused"
            : latest.state === "select-file"
              ? "Select a video below"
              : latest.error || "Waiting for a playable video…",
      );
      setPlayerStateText(
        latest.state === "metadata"
          ? "The torrent needs reachable peers before playback can start."
          : "You can start before the download reaches 100%.",
      );
    }

    if (isReady && !latest.direct && !probePromiseRef.current) {
      probePromiseRef.current = client.probeMedia({ hash }).catch((e) => {
        probePromiseRef.current = null;
        throw e;
      });
      probePromiseRef.current.catch(() => {});
    }
  }, [latest, hash]);

  // Enable/disable Video.js play controls when ready changes.
  useEffect(() => {
    if (!playerInst) return;
    for (const control of [playerInst.bigPlayButton, playerInst.controlBar.playToggle]) {
      if (ready) control.enable();
      else control.disable();
    }
  }, [playerInst, ready]);

  // Initialize Video.js + MSE streaming once on mount for this hash.
  useEffect(() => {
    const wrap = playerWrapRef.current;
    if (!wrap) return;
    wrap.replaceChildren();
    const videoEl = document.createElement("video");
    videoEl.className = "video-js vjs-big-play-centered";
    videoEl.id = "player";
    videoEl.controls = true;
    videoEl.playsInline = true;
    videoEl.preload = "none";
    videoEl.setAttribute("aria-label", "Movie player");
    wrap.append(videoEl);

    probePromiseRef.current = null;
    startedRef.current = false;
    readyRef.current = false;
    restoredRef.current = false;

    const player = videojs(videoEl, {
      controls: true,
      bigPlayButton: true,
      fluid: true,
      preload: "none",
      playbackRates: [0.5, 0.75, 1, 1.25, 1.5, 2],
      html5: { nativeTextTracks: false },
      controlBar: { skipButtons: { forward: 10, backward: 10 } },
      userActions: { hotkeys: false },
    });

    const host = document.createElement("div");
    player.el().append(host);
    setOverlayHost(host);
    setPlayerInst(player);

    const video = (player.el().querySelector("video") || videoEl) as HTMLVideoElement;
    const nativePlay = player.play.bind(player);
    player.bigPlayButton.disable();
    player.controlBar.playToggle.disable();

    const retry = player.controlBar.addChild("Button", {});
    retry.controlText("Retry playback");
    retry.el().classList.add("player-text-button");
    const retryIcon = retry.el().querySelector(".vjs-icon-placeholder");
    if (retryIcon) retryIcon.textContent = "↻";

    const vlc = player.controlBar.addChild("Button", {});
    vlc.controlText("Copy VLC stream link");
    vlc.el().classList.add("player-text-button");
    const vlcIcon = vlc.el().querySelector(".vjs-icon-placeholder");
    if (vlcIcon) vlcIcon.textContent = "VLC";

    const subtitleBtn = player.controlBar.addChild("Button", {});
    subtitleBtn.controlText("Find and manage subtitles");
    subtitleBtn.el().classList.add("player-text-button");
    const subIcon = subtitleBtn.el().querySelector(".vjs-icon-placeholder");
    if (subIcon) subIcon.textContent = "CC+";
    subtitleBtn.on("click", () => setSubOpenSignal((n) => n + 1));
    subtitleBtnRef.current = subtitleBtn;

    const MS = window.ManagedMediaSource || window.MediaSource;
    let remux: "copy" | "compat" | null = null;
    let info: ProbeMediaResponse | null = null;
    let mediaSource: MediaSource | null = null;
    let buffer: SourceBuffer | null = null;
    let session = 0;
    let abort: AbortController | null = null;
    let streaming = false;
    let streamFrom = 0;
    let disposed = false;

    const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));
    const mime = (mode: "copy" | "compat") =>
      `video/mp4; codecs="${mode === "copy" ? info?.codecs || "" : "avc1.640033"}${
        info?.audio ? ", mp4a.40.2" : ""
      }"`;

    const idle = () =>
      new Promise<void>((resolve) => {
        if (!buffer || !buffer.updating) {
          resolve();
          return;
        }
        buffer.addEventListener("updateend", () => resolve(), { once: true });
      });

    function rangeAt(t: number): [number, number] | undefined {
      const ranges = buffer?.buffered;
      for (let i = 0; ranges && i < ranges.length; i++) {
        if (ranges.start(i) <= t + 0.3 && ranges.end(i) >= t) {
          return [ranges.start(i), ranges.end(i)];
        }
      }
      return undefined;
    }

    function probe(): Promise<ProbeMediaResponse> {
      if (!probePromiseRef.current) {
        probePromiseRef.current = client.probeMedia({ hash }).catch((e) => {
          probePromiseRef.current = null;
          throw e;
        });
      }
      return probePromiseRef.current;
    }

    function bufferedEnd(): number {
      const current = rangeAt(video.currentTime)?.[1] ?? rangeAt(streamFrom)?.[1];
      if (current !== undefined) return current;
      if (buffer?.buffered.length) {
        return buffer.buffered.end(buffer.buffered.length - 1);
      }
      return Math.max(video.currentTime, streamFrom);
    }

    async function evict(keepBehind = 20) {
      if (!buffer || !mediaSource) return;
      const keep = video.currentTime - keepBehind;
      if (buffer.buffered.length && buffer.buffered.start(0) < keep) {
        await idle();
        buffer.remove(0, keep);
        await idle();
        streamFrom = Math.max(streamFrom, keep);
      }
    }

    async function append(chunk: Uint8Array, id: number): Promise<boolean> {
      for (let attempt = 0; ; attempt++) {
        await idle();
        if (id !== session || disposed || !buffer) return false;
        try {
          buffer.appendBuffer(new Uint8Array(chunk));
          await idle();
          return true;
        } catch (error) {
          if (!(error instanceof Error) || error.name !== "QuotaExceededError") {
            throw error;
          }
          await evict(attempt === 0 ? 10 : 2);
          await sleep(500);
        }
      }
    }

    async function stream(from: number, resume = false) {
      const id = ++session;
      abort?.abort();
      abort = new AbortController();
      streaming = true;
      streamFrom = from;
      try {
        await idle();
        if (id !== session || disposed || !mediaSource || !buffer) return;
        if (mediaSource.readyState === "open") buffer.abort();
        const response = await fetch(
          mediaUrl(`/media/${hash}/remux?mode=${remux}&t=${from.toFixed(3)}`),
          { signal: abort.signal },
        );
        if (!response.ok || !response.body) {
          throw new Error((await response.text()) || "Remux request failed");
        }
        await idle();
        if (id !== session || disposed || !mediaSource || !buffer) return;
        if (!resume && buffer.buffered.length) {
          buffer.remove(0, mediaSource.duration || Infinity);
          await idle();
        }
        streamFrom = Number(response.headers.get("X-Stream-Start")) || 0;
        buffer.timestampOffset = streamFrom;
        const reader = response.body.getReader();
        let appended = 0;
        for (;;) {
          while (id === session && !disposed) {
            const range = rangeAt(video.currentTime);
            if (!range || range[1] - video.currentTime < 30) break;
            await evict();
            await sleep(500);
          }
          if (id !== session || disposed) {
            reader.cancel().catch(() => {});
            return;
          }
          const { value, done } = await reader.read();
          if (id !== session || disposed) {
            reader.cancel().catch(() => {});
            return;
          }
          if (done) break;
          if (value) {
            if (!(await append(value, id))) return;
            appended += value.byteLength;
            if (
              remux === "copy" &&
              appended >= 8 << 20 &&
              (!buffer.buffered.length || video.readyState < 2)
            ) {
              reader.cancel().catch(() => {});
              useCompat();
              return;
            }
          }
        }
        await idle();
        if (id !== session || disposed || !info || !mediaSource) return;
        const end = bufferedEnd();
        if (end < info.duration - 3) {
          await sleep(1000);
          if (id === session && !disposed) void stream(end, true);
          return;
        }
        if (mediaSource.readyState === "open") mediaSource.endOfStream();
      } catch (error) {
        if (
          id !== session ||
          disposed ||
          (error instanceof Error && error.name === "AbortError")
        ) {
          return;
        }
        if (remux === "copy" && (!buffer || !buffer.buffered.length)) {
          useCompat();
          return;
        }
        setPlayerStateText("Stream interrupted · retrying…");
        await sleep(3000);
        if (id === session && !disposed) {
          void stream(bufferedEnd(), true);
        }
      } finally {
        if (id === session) streaming = false;
      }
    }

    function openRemux(from: number) {
      if (!MS || !info) return;
      session++;
      abort?.abort();
      mediaSource = new MS();
      (video as HTMLVideoElement & { disableRemotePlayback?: boolean }).disableRemotePlayback =
        true;
      const url = URL.createObjectURL(mediaSource);
      const currentMS = mediaSource;
      currentMS.addEventListener(
        "sourceopen",
        () => {
          if (disposed || !remux || !info) return;
          buffer = currentMS.addSourceBuffer(mime(remux));
          currentMS.duration = info.duration;
          void stream(from);
        },
        { once: true },
      );
      player.src({ src: url, type: "video/mp4" });
    }

    function useCompat() {
      if (!info) return;
      remux = "compat";
      setCodecNoteText(
        `${info.video.toUpperCase()} isn't supported by this browser · converting to H.264 at the original ${info.width}×${info.height}`,
      );
      restoredRef.current = false;
      savedPositionRef.current = video.currentTime || savedPositionRef.current;
      openRemux(savedPositionRef.current);
      nativePlay()?.catch(() => {});
    }

    async function start() {
      const current = latestRef.current;
      if (!readyRef.current || !current) return;
      startedRef.current = true;
      setStarted(true);
      restoredRef.current = false;

      if (current.direct) {
        remux = null;
        const name =
          current.files.find((f) => f.index === current.fileIndex)?.name || "";
        player.src({
          src: mediaUrl(`/media/${hash}`),
          type: /\.webm$/i.test(name) ? "video/webm" : "video/mp4",
        });
      } else {
        setPlayerStateText("Reading the video header…");
        try {
          info = await probe();
        } catch (error) {
          startedRef.current = false;
          setStarted(false);
          setPlaceholderText("Not enough data yet — try again in a moment.");
          setPlayerStateText(errMessage(error).slice(0, 200));
          return;
        }
        if (disposed) return;
        const label = `${info.video.toUpperCase()} ${info.width}×${info.height}`;
        if (!MS) {
          remux = "compat";
          restoredRef.current = true;
          player.src({
            src: mediaUrl(`/media/${hash}/remux?mode=compat`),
            type: "video/mp4",
          });
          setCodecNoteText(
            `${label} · converted on the fly (seeking unavailable in this browser)`,
          );
        } else {
          if (remux !== "compat") {
            remux = MS.isTypeSupported(mime("copy")) ? "copy" : "compat";
          }
          setCodecNoteText(
            remux === "copy"
              ? `${label} · original quality, no re-encoding`
              : `${info.video.toUpperCase()} isn't supported by this browser · converting to H.264 at the original ${info.width}×${info.height}`,
          );
          const from =
            savedPositionRef.current > 0 && savedPositionRef.current < info.duration - 5
              ? savedPositionRef.current
              : 0;
          openRemux(from);
        }
      }
      setPlayerStateText("Buffering verified video pieces…");
      return nativePlay()?.catch(() => {
        setPlayerStateText("Use the video Play control when the buffer is ready.");
      });
    }

    player.play = () => {
      if (!readyRef.current) return Promise.resolve();
      if (!startedRef.current || player.error()) return start();
      return nativePlay();
    };

    retry.on("click", () => {
      savedPositionRef.current = player.currentTime() || savedPositionRef.current;
      void start();
    });

    vlc.on("click", () => {
      const url = `${window.location.origin}${mediaUrl(`/media/${hash}`)}`;
      navigator.clipboard
        .writeText(url)
        .then(() => {
          if (vlcIcon) vlcIcon.textContent = "✓";
          setPlayerStateText("VLC stream link copied: " + url);
          window.setTimeout(() => {
            if (!disposed && vlcIcon) vlcIcon.textContent = "VLC";
          }, 2000);
        })
        .catch(() => {
          setPlayerStateText("Copy unavailable. Stream URL: " + url);
        });
    });

    let seekTimer: number | undefined;
    const onSeeking = () => {
      setPlayerStateText("Seeking… prioritizing the new playback position.");
      if (!remux || !buffer || !MS) return;
      const t = video.currentTime;
      if (rangeAt(t)) return;
      const head = rangeAt(video.currentTime) || rangeAt(streamFrom) || [streamFrom, bufferedEnd()];
      if (streaming && t >= head[0] - 0.5 && t <= head[1] + 5) return;
      if (seekTimer !== undefined) window.clearTimeout(seekTimer);
      seekTimer = window.setTimeout(() => {
        if (!disposed) void stream(video.currentTime);
      }, 120);
    };

    const onLoadedMeta = () => {
      if (
        !restoredRef.current &&
        savedPositionRef.current > 0 &&
        savedPositionRef.current < video.duration - 5
      ) {
        video.currentTime = savedPositionRef.current;
      }
      restoredRef.current = true;
    };

    const onWaiting = () =>
      setPlayerStateText("Buffering… downloading the pieces needed for this position.");
    const onPlaying = () =>
      setPlayerStateText("Playing · download continues in the background");
    const onPause = () => {
      if (!video.ended) setPlayerStateText("Playback paused · download continues");
    };
    const onEnded = () => setPlayerStateText("Finished watching");
    const onError = () => {
      if (remux === "copy" && MS && [3, 4].includes(video.error?.code || 0)) {
        player.error(null);
        useCompat();
        return;
      }
      startedRef.current = false;
      setStarted(false);
      setPlaceholderText("Playback interrupted");
      setPlayerStateText(
        video.error?.code === 4
          ? "This browser cannot play this source, or its data is unavailable. Try an x264 MP4 variant or retry when peers connect."
          : "Playback interrupted. Check peers and download status, then retry playback.",
      );
    };

    let lastSave = 0;
    function saveProgress() {
      const position = player.currentTime();
      if (!startedRef.current || !Number.isFinite(position)) return;
      void client
        .updateDownload({ hash, action: "progress", position })
        .catch(() => {});
    }
    const onTimeUpdate = () => {
      if (Date.now() - lastSave > 10000) {
        lastSave = Date.now();
        saveProgress();
      }
    };

    function keyboardPlayback(event: KeyboardEvent) {
      const target = event.target as HTMLElement | null;
      if (
        event.defaultPrevented ||
        event.altKey ||
        event.ctrlKey ||
        event.metaKey ||
        document.querySelector("dialog[open]") ||
        target?.closest(
          "input, textarea, select, [contenteditable], [role=slider], [role=menuitem]",
        )
      ) {
        return;
      }
      const key = event.key.toLowerCase();
      if (key === " " && target?.closest("button, a")) return;
      if (
        ![
          " ",
          "k",
          "arrowleft",
          "arrowright",
          "j",
          "l",
          "arrowup",
          "arrowdown",
          "m",
          "f",
          "c",
        ].includes(key)
      ) {
        return;
      }
      event.preventDefault();
      if (event.repeat && [" ", "k", "m", "f", "c"].includes(key)) return;
      if (key === " " || key === "k") {
        if (player.paused()) player.play()?.catch(() => {});
        else player.pause();
      } else if (["arrowleft", "arrowright", "j", "l"].includes(key)) {
        const duration = player.duration();
        if (startedRef.current && Number.isFinite(duration)) {
          const step =
            key === "j" ? -10 : key === "l" ? 10 : key === "arrowleft" ? -5 : 5;
          player.currentTime(Math.max(0, Math.min(duration, player.currentTime() + step)));
        }
      } else if (key === "arrowup" || key === "arrowdown") {
        player.volume(
          Math.max(0, Math.min(1, player.volume() + (key === "arrowup" ? 0.05 : -0.05))),
        );
      } else if (key === "m") {
        player.muted(!player.muted());
      } else if (key === "f") {
        const action = player.isFullscreen()
          ? player.exitFullscreen()
          : player.requestFullscreen();
        action?.catch(() => {});
      } else if (key === "c") {
        setSubOpenSignal((n) => n + 1);
      }
    }

    video.addEventListener("seeking", onSeeking);
    video.addEventListener("loadedmetadata", onLoadedMeta);
    video.addEventListener("waiting", onWaiting);
    video.addEventListener("playing", onPlaying);
    video.addEventListener("pause", onPause);
    video.addEventListener("ended", onEnded);
    video.addEventListener("error", onError);
    video.addEventListener("timeupdate", onTimeUpdate);
    document.addEventListener("keydown", keyboardPlayback);
    player.el().title =
      "Space/K: play or pause · ←/→: seek 5s · J/L: seek 10s · ↑/↓: volume · M: mute · F: fullscreen · C: subtitles";

    return () => {
      disposed = true;
      session++;
      if (seekTimer !== undefined) window.clearTimeout(seekTimer);
      abort?.abort();
      saveProgress();
      video.removeEventListener("seeking", onSeeking);
      video.removeEventListener("loadedmetadata", onLoadedMeta);
      video.removeEventListener("waiting", onWaiting);
      video.removeEventListener("playing", onPlaying);
      video.removeEventListener("pause", onPause);
      video.removeEventListener("ended", onEnded);
      video.removeEventListener("error", onError);
      video.removeEventListener("timeupdate", onTimeUpdate);
      document.removeEventListener("keydown", keyboardPlayback);
      setOverlayHost(null);
      setPlayerInst(null);
      try {
        player.dispose();
      } catch {
        // Ignore cleanup errors.
      }
      wrap.replaceChildren();
    };
  }, [hash]);

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

  return (
    <>
      <a
        className="back"
        href="/downloads"
        onClick={(e) => {
          e.preventDefault();
          navigate("/downloads");
        }}
      >
        ← All downloads
      </a>

      {streamError && !latest ? (
        <div className="empty">
          <span className="empty-symbol">◉</span>
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
        <section className="watch-page">
          <div className="eyebrow">WATCH WHILE IT DOWNLOADS</div>
          <h1>{latest?.title || "Loading movie…"}</h1>
          <p className="muted">
            Playback fetches the pieces you need next. Seeking may buffer while a new section
            arrives.
          </p>

          <div className="player-wrap">
            <div ref={playerWrapRef} />
            {overlayHost &&
              createPortal(
                <>
                  <div id="player-placeholder" hidden={started || ready}>
                    <div>
                      <p id="player-ready">{placeholderText}</p>
                    </div>
                  </div>
                  <span id="player-state" role="status" aria-live="polite">
                    {playerStateText}
                  </span>
                  <SubtitleDialog
                    hash={hash}
                    player={playerInst}
                    openSignal={subOpenSignal}
                    onCloseFocus={() => subtitleBtnRef.current?.el().focus()}
                  />
                </>,
                overlayHost,
              )}
          </div>

          <div className="download-panel">
            <div className="section-top">
              <h2>Download progress</h2>
              <strong id="download-percent">
                {latest ? (latest.progress * 100).toFixed(1) + "%" : "0%"}
              </strong>
            </div>
            <progress
              id="download-progress"
              max={1}
              value={latest?.progress || 0}
              aria-label="Download completion"
            />
            <div className="download-stats">
              <span id="download-state">{latest?.state || "loading"}</span>
              <span id="download-bytes">
                {latest
                  ? `${formatBytes(latest.completed)} / ${formatBytes(latest.size)}`
                  : "Waiting for metadata"}
              </span>
              <span id="download-speed">
                {latest ? `${formatBytes(latest.speed)}/s verified` : ""}
              </span>
              <span id="download-peers">
                {latest
                  ? `${latest.peers} connected peers${
                      latest.discovering ? " · searching again…" : ""
                    }`
                  : ""}
              </span>
            </div>
            <p id="download-error" role="alert">
              {actionError ||
                latest?.error ||
                (latest && !latest.paused && latest.peers === 0 && latest.state !== "complete"
                  ? "Waiting for reachable peers. Discovery retries automatically; you can also use Find peers again. Cached video is retained."
                  : "")}
            </p>
            <p id="codec-note" className="muted">
              {codecNoteText}
            </p>
            <div className="download-controls">
              <button
                type="button"
                className="button secondary"
                disabled={actionBusy || !latest}
                onClick={() =>
                  void runAction(latest?.paused ? "resume" : "pause")
                }
              >
                {latest?.paused ? "Resume download" : "Pause download"}
              </button>
              <button
                type="button"
                className="button secondary"
                disabled={actionBusy || !latest}
                onClick={() =>
                  void runAction(latest?.keep ? "unkeep" : "keep")
                }
              >
                {latest?.keep ? "Kept · make temporary" : "Keep downloaded"}
              </button>
              <button
                type="button"
                className="button secondary"
                disabled={actionBusy || !latest}
                onClick={() => void runAction("reconnect")}
              >
                Find peers again
              </button>
              <button
                type="button"
                className="button danger"
                disabled={actionBusy || !latest}
                onClick={() => void handleDelete()}
              >
                Delete download
              </button>
              <a
                className="text-link"
                href={mediaUrl(`/media/${hash}?download=1`)}
              >
                Save video file to computer ↗
              </a>
            </div>
            <p className="muted">
              {latest?.keep
                ? "Kept on disk until you remove it."
                : "Stored in the local media cache. Use Delete download to free disk space."}{" "}
              Video bytes are shared between playback and the local download.
            </p>
          </div>

          {latest?.state === "select-file" && (
            <section id="file-picker">
              <h2>Select a video</h2>
              <p className="muted">This torrent has more than one possible main video.</p>
              <div>
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
                  Use this video
                </button>
              </div>
            </section>
          )}
        </section>
      )}
    </>
  );
}
