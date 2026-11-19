import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type PointerEvent as ReactPointerEvent,
} from "react";
import { client, errMessage, mediaUrl } from "../api";
import type {
  DownloadStatus,
  ProbeMediaResponse,
} from "../gen/bigflix/v1/bigflix_pb";
import type { WebkitVideoElement } from "../types";
import {
  CheckIcon,
  ExitFullscreenIcon,
  FullscreenIcon,
  LinkIcon,
  PauseIcon,
  PlayIcon,
  RefreshIcon,
  SkipBackIcon,
  SkipForwardIcon,
  SubtitlesIcon,
  VolumeIcon,
  VolumeMuteIcon,
} from "./Icons";
import { SubtitleDialog } from "./SubtitleDialog";
import { Button } from "./ui";

interface VideoPlayerProps {
  hash: string;
  latest: DownloadStatus | null;
  onCodecNoteChange?: (note: string) => void;
}

type QualityMode = "auto" | "1080" | "720";

const RATES = [0.75, 1, 1.25, 1.5, 2];
const QUALITIES: QualityMode[] = ["auto", "1080", "720"];

function qualityLabel(q: QualityMode): string {
  if (q === "1080") return "1080p";
  if (q === "720") return "720p";
  return "Auto";
}

function formatClock(sec: number): string {
  if (!Number.isFinite(sec) || sec < 0) return "0:00";
  const total = Math.floor(sec);
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  if (h > 0) {
    return `${h}:${String(m).padStart(2, "0")}:${String(s).padStart(2, "0")}`;
  }
  return `${m}:${String(s).padStart(2, "0")}`;
}

function concatChunks(chunks: Uint8Array[], totalBytes: number): Uint8Array<ArrayBuffer> {
  const out = new Uint8Array(new ArrayBuffer(totalBytes));
  let offset = 0;
  for (const c of chunks) {
    out.set(c, offset);
    offset += c.byteLength;
  }
  return out;
}

export function VideoPlayer({ hash, latest, onCodecNoteChange }: VideoPlayerProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const videoRef = useRef<WebkitVideoElement | null>(null);
  const scrubRef = useRef<HTMLDivElement | null>(null);
  const subBtnRef = useRef<HTMLButtonElement | null>(null);

  const [videoEl, setVideoEl] = useState<HTMLVideoElement | null>(null);
  const [ready, setReady] = useState(false);
  const [started, setStarted] = useState(false);
  const [playing, setPlaying] = useState(false);
  const [buffering, setBuffering] = useState(false);
  const [needsTap, setNeedsTap] = useState(false);
  const [fatalError, setFatalError] = useState("");
  const [statusText, setStatusText] = useState("Preparing…");
  const [currentTime, setCurrentTime] = useState(0);
  const [duration, setDuration] = useState(0);
  const [bufferedEndTime, setBufferedEndTime] = useState(0);
  const [volume, setVolume] = useState(1);
  const [muted, setMuted] = useState(false);
  const [rate, setRate] = useState(1);
  const [quality, setQuality] = useState<QualityMode>("auto");
  const [isFullscreen, setIsFullscreen] = useState(false);
  const [controlsVisible, setControlsVisible] = useState(true);
  const [subOpenSignal, setSubOpenSignal] = useState(0);
  const [subActive, setSubActive] = useState(false);
  const [copiedVlc, setCopiedVlc] = useState(false);
  const [seekFeedback, setSeekFeedback] = useState<{
    label: string;
    side: "left" | "right" | "center";
  } | null>(null);
  const [scrubTime, setScrubTime] = useState<number | null>(null);

  // Mutable engine refs
  const latestRef = useRef<DownloadStatus | null>(null);
  const readyRef = useRef(false);
  const startedRef = useRef(false);
  const wantPlayRef = useRef(false);
  const qualityRef = useRef<QualityMode>("auto");
  const savedPositionRef = useRef(0);
  const restoredRef = useRef(false);
  const targetTimeRef = useRef(0);
  const internalSeekRef = useRef(false);
  const scrubbingRef = useRef(false);
  const probePromiseRef = useRef<Promise<ProbeMediaResponse> | null>(null);
  const hideTimerRef = useRef<number | undefined>(undefined);
  const feedbackTimerRef = useRef<number | undefined>(undefined);
  const lastTapRef = useRef<{ time: number; side: "left" | "right" | "center" }>({
    time: 0,
    side: "center",
  });

  const engineRef = useRef({
    remux: null as "copy" | "compat" | null,
    compatMaxH: 0,
    info: null as ProbeMediaResponse | null,
    mediaSource: null as MediaSource | null,
    objectUrl: "",
    buffer: null as SourceBuffer | null,
    session: 0,
    abort: null as AbortController | null,
    streaming: false,
    streamFrom: 0,
    directRetries: 0,
    retryTimer: undefined as number | undefined,
    seekTimer: undefined as number | undefined,
    disposed: false,
  });

  useEffect(() => {
    latestRef.current = latest;
  }, [latest]);

  const showControls = useCallback((keepOpen = false) => {
    setControlsVisible(true);
    if (hideTimerRef.current !== undefined) {
      window.clearTimeout(hideTimerRef.current);
      hideTimerRef.current = undefined;
    }
    if (!keepOpen) {
      hideTimerRef.current = window.setTimeout(() => {
        if (wantPlayRef.current && !scrubbingRef.current) {
          setControlsVisible(false);
        }
      }, 4000);
    }
  }, []);

  const flashSeek = useCallback((label: string, side: "left" | "right" | "center") => {
    setSeekFeedback({ label, side });
    if (feedbackTimerRef.current !== undefined) {
      window.clearTimeout(feedbackTimerRef.current);
    }
    feedbackTimerRef.current = window.setTimeout(() => {
      setSeekFeedback(null);
    }, 650);
  }, []);

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
      onCodecNoteChange?.(
        latest.size > 0n && !latest.playable
          ? "FFmpeg is required to play this container in the browser."
          : !latest.direct
            ? "Remuxing on the fly for browser playback."
            : "Direct browser playback.",
      );
      if (latest.state === "metadata") {
        setStatusText("Finding peers & metadata…");
      } else if (latest.state === "paused") {
        setStatusText("Download paused");
      } else if (latest.state === "select-file") {
        setStatusText("Select a video below");
      } else if (latest.error) {
        setStatusText(latest.error);
      } else if (isReady) {
        setStatusText("Ready");
      } else {
        setStatusText("Waiting for video…");
      }
    }

    if (isReady && !latest.direct && !probePromiseRef.current) {
      probePromiseRef.current = client.probeMedia({ hash }).catch((e) => {
        probePromiseRef.current = null;
        throw e;
      });
      probePromiseRef.current.catch(() => {});
    }
  }, [latest, hash, onCodecNoteChange]);

  const attemptPlay = useCallback(async () => {
    const video = videoRef.current;
    if (!video || engineRef.current.disposed) return;
    try {
      await video.play();
      setNeedsTap(false);
    } catch (err) {
      if (err instanceof DOMException && err.name === "NotAllowedError") {
        setNeedsTap(true);
        setBuffering(false);
        setStatusText("Tap play to start");
        showControls(true);
      }
    }
  }, [showControls]);

  const saveProgress = useCallback(() => {
    const video = videoRef.current;
    if (!video || !startedRef.current) return;
    const pos = video.currentTime;
    if (!Number.isFinite(pos) || pos <= 0) return;
    void client.updateDownload({ hash, action: "progress", position: pos }).catch(() => {});
  }, [hash]);

  const probe = useCallback((): Promise<ProbeMediaResponse> => {
    if (!probePromiseRef.current) {
      probePromiseRef.current = client.probeMedia({ hash }).catch((e) => {
        probePromiseRef.current = null;
        throw e;
      });
    }
    return probePromiseRef.current;
  }, [hash]);

  const startPlaybackRef = useRef<
    (fromOverride?: number, qualityOverride?: QualityMode) => Promise<void>
  >(async () => {});

  useEffect(() => {
    const rawVideo = videoRef.current;
    if (!rawVideo) return;
    const video: WebkitVideoElement = rawVideo;
    setVideoEl(video);

    const eng = engineRef.current;
    eng.disposed = false;
    eng.remux = null;
    eng.compatMaxH = 0;
    eng.info = null;
    eng.mediaSource = null;
    eng.buffer = null;
    eng.session = 0;
    eng.abort = null;
    eng.streaming = false;
    eng.streamFrom = 0;
    eng.directRetries = 0;

    probePromiseRef.current = null;
    startedRef.current = false;
    wantPlayRef.current = false;
    qualityRef.current = "auto";
    restoredRef.current = false;
    targetTimeRef.current = 0;
    setQuality("auto");
    setStarted(false);
    setPlaying(false);
    setBuffering(false);
    setNeedsTap(false);
    setFatalError("");
    setCurrentTime(0);
    setDuration(0);
    setBufferedEndTime(0);

    const MS = window.ManagedMediaSource || window.MediaSource;
    const sleep = (ms: number) => new Promise((r) => window.setTimeout(r, ms));

    const mime = (mode: "copy" | "compat") =>
      `video/mp4; codecs="${mode === "copy" ? eng.info?.codecs || "" : "avc1.640033"}${
        eng.info?.audio ? ", mp4a.40.2" : ""
      }"`;

    // Adaptive MSE buffer limits: 4K / high-res streams use much smaller time windows
    // so they stay well under browser SourceBuffer memory quotas (~80-150 MB).
    function bufferLimits(): { aheadLimit: number; keepBehind: number } {
      const highRes =
        eng.remux === "copy" &&
        ((eng.info?.height ?? 0) > 1080 || (eng.info?.width ?? 0) > 1920);
      return highRes
        ? { aheadLimit: 18, keepBehind: 8 }
        : { aheadLimit: 30, keepBehind: 15 };
    }

    const idle = () =>
      new Promise<void>((resolve) => {
        if (!eng.buffer || !eng.buffer.updating) {
          resolve();
          return;
        }
        eng.buffer.addEventListener("updateend", () => resolve(), { once: true });
      });

    function rangeAt(t: number): [number, number] | undefined {
      const ranges = eng.buffer?.buffered;
      for (let i = 0; ranges && i < ranges.length; i++) {
        if (ranges.start(i) <= t + 0.35 && ranges.end(i) >= t) {
          return [ranges.start(i), ranges.end(i)];
        }
      }
      return undefined;
    }

    function bufferedEnd(): number {
      const current = rangeAt(video.currentTime)?.[1] ?? rangeAt(eng.streamFrom)?.[1];
      if (current !== undefined) return current;
      if (eng.buffer?.buffered.length) {
        return eng.buffer.buffered.end(eng.buffer.buffered.length - 1);
      }
      return Math.max(video.currentTime, eng.streamFrom);
    }

    function updateBufferedUI() {
      const ranges = video.buffered;
      if (!ranges.length) return;
      const t = video.currentTime;
      for (let i = 0; i < ranges.length; i++) {
        if (ranges.start(i) <= t + 0.5 && ranges.end(i) >= t) {
          setBufferedEndTime(ranges.end(i));
          return;
        }
      }
      setBufferedEndTime(ranges.end(ranges.length - 1));
    }

    async function evict(keepBehind?: number) {
      if (!eng.buffer || !eng.mediaSource || eng.mediaSource.readyState !== "open") return;
      const behind = keepBehind ?? bufferLimits().keepBehind;
      const keep = video.currentTime - behind;
      if (eng.buffer.buffered.length && eng.buffer.buffered.start(0) < keep) {
        await idle();
        if (eng.disposed || !eng.buffer || eng.mediaSource.readyState !== "open") return;
        try {
          eng.buffer.remove(0, keep);
          await idle();
          eng.streamFrom = Math.max(eng.streamFrom, keep);
        } catch {
          // Ignore eviction race on close.
        }
      }
    }

    // Patient appendBytes: proactively evicts old data, and on QuotaExceededError
    // NEVER kills the stream — instead frees space or waits for playback to consume
    // buffered data so high-res 4K streams never silently freeze.
    async function appendBytes(data: Uint8Array<ArrayBuffer>, id: number): Promise<boolean> {
      const { keepBehind } = bufferLimits();
      if (
        eng.buffer?.buffered.length &&
        video.currentTime - eng.buffer.buffered.start(0) > keepBehind + 4
      ) {
        await evict(keepBehind);
      }

      for (let attempt = 0; ; attempt++) {
        await idle();
        if (id !== eng.session || eng.disposed || !eng.buffer) return false;
        if (!eng.mediaSource || eng.mediaSource.readyState !== "open") return false;
        try {
          eng.buffer.appendBuffer(data);
          await idle();
          updateBufferedUI();
          return true;
        } catch (error) {
          if (!(error instanceof Error) || error.name !== "QuotaExceededError") {
            throw error;
          }
          // 1. Evict aggressively behind currentTime
          await evict(attempt === 0 ? 4 : 1);
          // 2. If buffer ahead is large and still full, wait while playback drains it
          if (attempt >= 2) {
            await sleep(500);
          } else {
            await sleep(150);
          }
        }
      }
    }

    function revokeCurrentUrl() {
      if (eng.objectUrl) {
        URL.revokeObjectURL(eng.objectUrl);
        eng.objectUrl = "";
      }
    }

    function useCompat(maxH = 1080, reason?: string) {
      if (!eng.info || eng.disposed) return;
      eng.remux = "compat";
      eng.compatMaxH = maxH;
      const capped = maxH > 0 && (eng.info.height > maxH || eng.info.width > (maxH * 16) / 9);
      const resText = capped ? `≤${maxH}p` : `${eng.info.width}×${eng.info.height}`;
      onCodecNoteChange?.(
        reason || `Smooth H.264 playback (${resText}) for stable browser decoding.`,
      );
      setStatusText(`Switching to smooth ${capped ? `${maxH}p` : "H.264"}…`);
      setBuffering(true);
      restoredRef.current = false;
      const pos = video.currentTime || savedPositionRef.current;
      savedPositionRef.current = pos;
      openRemux(pos);
      if (wantPlayRef.current) void attemptPlay();
    }

    async function switchDirectToCompat(maxH: number, reason: string) {
      if (eng.disposed || !MS) return;
      const pos = video.currentTime || savedPositionRef.current;
      savedPositionRef.current = pos;
      setBuffering(true);
      setStatusText(`Switching to smooth ${maxH}p…`);
      try {
        eng.info = await probe();
      } catch {
        // Fallback: reload direct stream if probe unavailable (e.g. no ffmpeg).
        restoredRef.current = false;
        video.src = mediaUrl(`/media/${hash}`);
        video.load();
        if (wantPlayRef.current) void attemptPlay();
        return;
      }
      if (eng.disposed || !eng.info) return;
      useCompat(maxH, reason);
    }

    async function stream(from: number, resume = false) {
      const id = ++eng.session;
      eng.abort?.abort();
      eng.abort = new AbortController();
      eng.streaming = true;
      eng.streamFrom = from;
      if (!resume) {
        targetTimeRef.current = from;
      }

      try {
        await idle();
        if (id !== eng.session || eng.disposed || !eng.mediaSource || !eng.buffer) return;
        if (eng.mediaSource.readyState === "open") {
          try {
            eng.buffer.abort();
          } catch {
            // Ignore abort if buffer already closed.
          }
        }
        const maxhParam =
          eng.remux === "compat" && eng.compatMaxH > 0 ? `&maxh=${eng.compatMaxH}` : "";
        const response = await fetch(
          mediaUrl(`/media/${hash}/remux?mode=${eng.remux}&t=${from.toFixed(3)}${maxhParam}`),
          { signal: eng.abort.signal },
        );
        if (!response.ok || !response.body) {
          throw new Error((await response.text()) || "Stream request failed");
        }
        await idle();
        if (id !== eng.session || eng.disposed || !eng.mediaSource || !eng.buffer) return;
        if (!resume && eng.buffer.buffered.length) {
          try {
            eng.buffer.remove(0, eng.mediaSource.duration || Infinity);
            await idle();
          } catch {
            // Ignore remove race.
          }
        }
        if (id !== eng.session || eng.disposed || !eng.mediaSource || !eng.buffer) return;

        eng.streamFrom = Number(response.headers.get("X-Stream-Start")) || 0;
        eng.buffer.timestampOffset = eng.streamFrom;

        const reader = response.body.getReader();
        let appended = 0;
        let aligned = resume;
        let pending: Uint8Array[] = [];
        let pendingBytes = 0;

        const flushPending = async (): Promise<boolean> => {
          if (pendingBytes === 0) return true;
          const merged =
            pending.length === 1
              ? new Uint8Array(pending[0])
              : concatChunks(pending, pendingBytes);
          pending = [];
          pendingBytes = 0;
          if (!(await appendBytes(merged, id))) return false;
          appended += merged.byteLength;

          if (!aligned && eng.buffer && eng.buffer.buffered.length > 0) {
            const bStart = eng.buffer.buffered.start(0);
            const bEnd = eng.buffer.buffered.end(0);
            const desired = Math.max(targetTimeRef.current, bStart + 0.02);
            if (
              video.currentTime < bStart ||
              (Math.abs(video.currentTime - desired) > 0.35 && desired <= bEnd)
            ) {
              internalSeekRef.current = true;
              video.currentTime = Math.min(desired, bEnd);
            }
            aligned = true;
          }

          if (wantPlayRef.current && video.paused && !video.ended && video.readyState >= 2) {
            void attemptPlay();
          }
          return true;
        };

        for (;;) {
          const { aheadLimit } = bufferLimits();
          while (id === eng.session && !eng.disposed) {
            const range = rangeAt(video.currentTime);
            if (!range || range[1] - video.currentTime < aheadLimit) break;
            if (!(await flushPending())) {
              reader.cancel().catch(() => {});
              return;
            }
            await evict();
            await sleep(450);
          }

          if (id !== eng.session || eng.disposed) {
            reader.cancel().catch(() => {});
            return;
          }

          const { value, done } = await reader.read();
          if (id !== eng.session || eng.disposed) {
            reader.cancel().catch(() => {});
            return;
          }
          if (done) {
            await flushPending();
            break;
          }

          if (value && value.byteLength > 0) {
            pending.push(value);
            pendingBytes += value.byteLength;

            const range = rangeAt(video.currentTime);
            const ahead = range ? range[1] - video.currentTime : 0;
            // Flush immediately on startup/seek (<3s ahead); coalesce to 512KB once playing.
            const threshold = ahead < 3 ? 0 : 512 * 1024;

            if (pendingBytes >= threshold) {
              if (!(await flushPending())) {
                reader.cancel().catch(() => {});
                return;
              }
            }

            if (
              eng.remux === "copy" &&
              appended >= 8 << 20 &&
              (!eng.buffer?.buffered.length || video.readyState < 2)
            ) {
              reader.cancel().catch(() => {});
              useCompat(1080);
              return;
            }
          }
        }

        await idle();
        if (id !== eng.session || eng.disposed || !eng.info || !eng.mediaSource) return;
        const end = bufferedEnd();
        if (end < eng.info.duration - 3) {
          await sleep(800);
          if (id === eng.session && !eng.disposed) void stream(end, true);
          return;
        }
        if (eng.mediaSource.readyState === "open") {
          try {
            eng.mediaSource.endOfStream();
          } catch {
            // Ignore if already ended.
          }
        }
      } catch (error) {
        if (
          id !== eng.session ||
          eng.disposed ||
          (error instanceof Error && error.name === "AbortError")
        ) {
          return;
        }
        if (eng.remux === "copy" && (!eng.buffer || !eng.buffer.buffered.length)) {
          useCompat(1080);
          return;
        }
        setStatusText("Reconnecting…");
        setBuffering(true);
        await sleep(2000);
        if (id === eng.session && !eng.disposed) {
          void stream(bufferedEnd(), true);
        }
      } finally {
        if (id === eng.session) eng.streaming = false;
      }
    }

    function openRemux(from: number) {
      if (!MS || !eng.info) return;
      eng.session++;
      eng.abort?.abort();
      revokeCurrentUrl();

      const ms = new MS();
      eng.mediaSource = ms;
      eng.buffer = null;
      video.disableRemotePlayback = true;
      const url = URL.createObjectURL(ms);
      eng.objectUrl = url;

      ms.addEventListener(
        "sourceopen",
        () => {
          if (eng.disposed || eng.mediaSource !== ms || !eng.remux || !eng.info) return;
          try {
            eng.buffer = ms.addSourceBuffer(mime(eng.remux));
            if (Number.isFinite(eng.info.duration) && eng.info.duration > 0) {
              ms.duration = eng.info.duration;
              setDuration(eng.info.duration);
            }
            void stream(from);
          } catch {
            if (eng.remux === "copy") useCompat(1080);
          }
        },
        { once: true },
      );

      video.src = url;
      video.load();
    }

    async function startPlayback(fromOverride?: number, qualityOverride?: QualityMode) {
      const current = latestRef.current;
      if (!readyRef.current || !current || eng.disposed) return;

      const activeQuality = qualityOverride ?? qualityRef.current;
      const forceCompatMaxH =
        activeQuality === "1080" ? 1080 : activeQuality === "720" ? 720 : 0;

      wantPlayRef.current = true;
      startedRef.current = true;
      setStarted(true);
      setFatalError("");
      setNeedsTap(false);
      setBuffering(true);
      restoredRef.current = false;
      eng.directRetries = 0;

      if (fromOverride !== undefined && Number.isFinite(fromOverride)) {
        savedPositionRef.current = fromOverride;
      }

      // Direct playback when container is browser-native AND user hasn't requested a downscaled smooth tier
      if (current.direct && forceCompatMaxH === 0) {
        eng.remux = null;
        eng.compatMaxH = 0;
        eng.session++;
        eng.abort?.abort();
        revokeCurrentUrl();
        onCodecNoteChange?.("Direct browser playback.");
        setStatusText("Buffering…");
        video.src = mediaUrl(`/media/${hash}`);
        video.load();
        await attemptPlay();
        return;
      }

      setStatusText("Reading header…");
      try {
        eng.info = await probe();
      } catch (error) {
        if (eng.disposed) return;
        // If direct playback is possible and probe failed (e.g. no ffmpeg), fall back to direct
        if (current.direct) {
          eng.remux = null;
          eng.compatMaxH = 0;
          video.src = mediaUrl(`/media/${hash}`);
          video.load();
          await attemptPlay();
          return;
        }
        startedRef.current = false;
        wantPlayRef.current = false;
        setStarted(false);
        setBuffering(false);
        setFatalError(
          errMessage(error).slice(0, 180) || "Not enough data yet — try again in a moment.",
        );
        return;
      }
      if (eng.disposed || !eng.info) return;

      const label = `${eng.info.video.toUpperCase()} ${eng.info.width}×${eng.info.height}`;
      if (!MS) {
        eng.remux = "compat";
        eng.compatMaxH = forceCompatMaxH || 1080;
        restoredRef.current = true;
        onCodecNoteChange?.(`${label} · converted on the fly`);
        video.src = mediaUrl(`/media/${hash}/remux?mode=compat&maxh=${eng.compatMaxH}`);
        video.load();
        await attemptPlay();
        return;
      }

      if (forceCompatMaxH > 0) {
        eng.remux = "compat";
        eng.compatMaxH = forceCompatMaxH;
      } else {
        eng.compatMaxH = 1080;
        eng.remux = MS.isTypeSupported(mime("copy")) ? "copy" : "compat";
      }

      onCodecNoteChange?.(
        eng.remux === "copy"
          ? `${label} · original quality`
          : `Smooth H.264 playback (≤${eng.compatMaxH || 1080}p) from ${label}.`,
      );
      const from =
        savedPositionRef.current > 0 && savedPositionRef.current < eng.info.duration - 5
          ? savedPositionRef.current
          : 0;
      setStatusText("Buffering…");
      openRemux(from);
      await attemptPlay();
    }

    startPlaybackRef.current = startPlayback;

    // Smart Freeze & Stall Watchdog:
    // 1. Jumps small MSE gaps (<= 2.5s)
    // 2. Detects silent high-res decoder freezes (currentTime stops advancing even when !paused)
    // 3. Detects severe 4K frame drops and auto-switches to smooth 1080p H.264
    // 4. Recovers wedged direct-stream HTTP byte-range stalls automatically
    let lastObservedTime = -1;
    let stalledSince = 0;
    let stallRecoveries = 0;
    let lastTotalFrames = 0;
    let lastDroppedFrames = 0;
    let dropCheckAt = Date.now();

    const watchdog = window.setInterval(() => {
      if (eng.disposed || !startedRef.current || !wantPlayRef.current || video.ended) {
        stalledSince = 0;
        return;
      }
      if (scrubbingRef.current) return;

      const now = Date.now();
      const t = video.currentTime;

      // Check if video is actively advancing
      if (!video.paused && lastObservedTime >= 0 && Math.abs(t - lastObservedTime) >= 0.05) {
        lastObservedTime = t;
        stalledSince = 0;
        if (stallRecoveries > 0 && video.readyState >= 3) {
          stallRecoveries = 0;
        }

        // Check severe frame-drop choking on high-res video (>1080p) every 4s
        if (now - dropCheckAt >= 4000 && typeof video.getVideoPlaybackQuality === "function") {
          const q = video.getVideoPlaybackQuality();
          const dTotal = q.totalVideoFrames - lastTotalFrames;
          const dDrop = q.droppedVideoFrames - lastDroppedFrames;
          lastTotalFrames = q.totalVideoFrames;
          lastDroppedFrames = q.droppedVideoFrames;
          dropCheckAt = now;

          const isHighResOriginal =
            (eng.remux === "copy" &&
              ((eng.info?.height ?? 0) > 1080 || (eng.info?.width ?? 0) > 1920)) ||
            (!eng.remux && (video.videoHeight > 1080 || video.videoWidth > 1920));

          if (isHighResOriginal && dTotal >= 45 && dDrop / dTotal > 0.45 && MS) {
            if (eng.remux === "copy") {
              useCompat(1080, "High resolution was dropping frames · switched to smooth 1080p.");
            } else {
              void switchDirectToCompat(
                1080,
                "High resolution was dropping frames · switched to smooth 1080p.",
              );
            }
          }
        }
        return;
      }

      lastObservedTime = t;
      if (!stalledSince) {
        stalledSince = now;
        return;
      }

      const stalledMs = now - stalledSince;

      // After 1.2s stalled: jump small MSE gaps or resume paused-but-ready video
      if (stalledMs >= 1200) {
        if (eng.buffer && eng.buffer.buffered.length > 0 && video.readyState < 3) {
          const inRange = rangeAt(t);
          if (!inRange) {
            for (let i = 0; i < eng.buffer.buffered.length; i++) {
              const s = eng.buffer.buffered.start(i);
              if (s > t && s - t <= 2.5) {
                internalSeekRef.current = true;
                video.currentTime = s + 0.03;
                stalledSince = now;
                return;
              }
            }
          }
        }

        if (video.paused && video.readyState >= 3 && !video.error) {
          void attemptPlay();
          return;
        }
      }

      // After 4.5s truly frozen: actively recover!
      if (stalledMs >= 4500) {
        stalledSince = now;
        stallRecoveries++;
        setBuffering(true);

        if (eng.remux === "copy") {
          const hasDataHere = Boolean(rangeAt(t));
          if (hasDataHere || stallRecoveries >= 2) {
            // Decoder is stuck despite having buffered frames, or repeated stalls: switch to smooth H.264!
            useCompat(
              1080,
              "High-resolution stream froze · switched to smooth 1080p H.264.",
            );
          } else {
            setStatusText("Recovering stream…");
            void stream(t);
          }
        } else if (eng.remux === "compat") {
          if (
            stallRecoveries >= 2 &&
            eng.compatMaxH !== 720 &&
            ((eng.info?.height ?? 0) > 1080 || (eng.info?.width ?? 0) > 1920)
          ) {
            useCompat(720, "Stepped down to 720p for smooth uninterrupted playback.");
          } else {
            setStatusText("Recovering stream…");
            void stream(t);
          }
        } else {
          // Direct MP4 mode freeze recovery
          const isHighResDirect = video.videoHeight > 1080 || video.videoWidth > 1920;
          if (stallRecoveries >= 2 && isHighResDirect && MS) {
            void switchDirectToCompat(
              1080,
              "High-resolution direct playback froze · switched to smooth 1080p.",
            );
          } else {
            setStatusText("Recovering playback…");
            savedPositionRef.current = t;
            restoredRef.current = false;
            video.src = mediaUrl(`/media/${hash}`);
            video.load();
            void attemptPlay();
          }
        }
      }
    }, 500);

    const onLoadedMeta = () => {
      if (Number.isFinite(video.duration) && video.duration > 0) {
        setDuration(video.duration);
      }
      if (
        !restoredRef.current &&
        !eng.remux &&
        savedPositionRef.current > 0 &&
        savedPositionRef.current < video.duration - 5
      ) {
        internalSeekRef.current = true;
        video.currentTime = savedPositionRef.current;
      }
      restoredRef.current = true;
    };

    const onCanPlay = () => {
      setBuffering(false);
      updateBufferedUI();
      if (wantPlayRef.current && video.paused && !video.ended) {
        void attemptPlay();
      }
    };

    const onPlaying = () => {
      setPlaying(true);
      setBuffering(false);
      setNeedsTap(false);
      setFatalError("");
      eng.directRetries = 0;
      setStatusText("Playing");
      showControls(false);
    };

    const onPause = () => {
      setPlaying(false);
      if (!video.ended) {
        setStatusText("Paused");
        showControls(true);
      }
      saveProgress();
    };

    const onWaiting = () => {
      if (startedRef.current) {
        setBuffering(true);
        setStatusText("Buffering…");
      }
    };

    const onEnded = () => {
      wantPlayRef.current = false;
      setPlaying(false);
      setBuffering(false);
      setStatusText("Finished");
      showControls(true);
      saveProgress();
    };

    const onSeeking = () => {
      if (internalSeekRef.current) {
        internalSeekRef.current = false;
        return;
      }
      stalledSince = 0;
      setBuffering(true);
      setStatusText("Seeking…");
      if (!eng.remux || !eng.buffer || !MS) return;
      const t = video.currentTime;
      targetTimeRef.current = t;
      if (rangeAt(t)) {
        setBuffering(false);
        return;
      }
      const head = rangeAt(t) || rangeAt(eng.streamFrom) || [eng.streamFrom, bufferedEnd()];
      if (eng.streaming && t >= head[0] - 0.5 && t <= head[1] + 5) return;
      if (eng.seekTimer !== undefined) window.clearTimeout(eng.seekTimer);
      eng.seekTimer = window.setTimeout(() => {
        if (!eng.disposed) void stream(video.currentTime);
      }, 100);
    };

    const onSeeked = () => {
      updateBufferedUI();
      if (video.readyState >= 3) setBuffering(false);
      if (wantPlayRef.current && video.paused && !video.ended) {
        void attemptPlay();
      }
    };

    let lastSave = 0;
    const onTimeUpdate = () => {
      if (!scrubbingRef.current) setCurrentTime(video.currentTime);
      updateBufferedUI();
      if (Date.now() - lastSave > 10000) {
        lastSave = Date.now();
        saveProgress();
      }
    };

    const onProgress = () => updateBufferedUI();

    const onVolumeChange = () => {
      setVolume(video.volume);
      setMuted(video.muted);
    };

    const onError = () => {
      if (eng.disposed) return;
      if (eng.remux === "copy" && MS && [3, 4].includes(video.error?.code || 0)) {
        useCompat(1080);
        return;
      }
      if (!eng.remux && startedRef.current && eng.directRetries < 5) {
        eng.directRetries++;
        savedPositionRef.current = video.currentTime || savedPositionRef.current;
        // On decode error (code 3) or unsupported source (code 4) in direct MP4,
        // try smooth H.264 remux right away on second retry!
        if (eng.directRetries >= 2 && [3, 4].includes(video.error?.code || 0) && MS) {
          void switchDirectToCompat(
            1080,
            "Codec incompatible with direct play · switched to smooth 1080p.",
          );
          return;
        }
        setBuffering(true);
        setStatusText(`Waiting for pieces · retry ${eng.directRetries}/5`);
        if (eng.retryTimer !== undefined) window.clearTimeout(eng.retryTimer);
        eng.retryTimer = window.setTimeout(() => {
          if (eng.disposed || !wantPlayRef.current) return;
          restoredRef.current = false;
          video.src = mediaUrl(`/media/${hash}`);
          video.load();
          void attemptPlay();
        }, eng.directRetries * 1500);
        return;
      }

      startedRef.current = false;
      wantPlayRef.current = false;
      setStarted(false);
      setPlaying(false);
      setBuffering(false);
      setFatalError(
        video.error?.code === 4
          ? "Source unavailable or unsupported codec. Try switching to 1080p or retrying."
          : "Playback interrupted. Check peers and retry.",
      );
      showControls(true);
    };

    const onFsChange = () => {
      setIsFullscreen(Boolean(document.fullscreenElement || video.webkitDisplayingFullscreen));
    };

    video.addEventListener("loadedmetadata", onLoadedMeta);
    video.addEventListener("loadeddata", onCanPlay);
    video.addEventListener("canplay", onCanPlay);
    video.addEventListener("canplaythrough", onCanPlay);
    video.addEventListener("playing", onPlaying);
    video.addEventListener("pause", onPause);
    video.addEventListener("waiting", onWaiting);
    video.addEventListener("ended", onEnded);
    video.addEventListener("seeking", onSeeking);
    video.addEventListener("seeked", onSeeked);
    video.addEventListener("timeupdate", onTimeUpdate);
    video.addEventListener("progress", onProgress);
    video.addEventListener("volumechange", onVolumeChange);
    video.addEventListener("error", onError);
    document.addEventListener("fullscreenchange", onFsChange);
    video.addEventListener("webkitbeginfullscreen", onFsChange as EventListener);
    video.addEventListener("webkitendfullscreen", onFsChange as EventListener);

    return () => {
      eng.disposed = true;
      eng.session++;
      window.clearInterval(watchdog);
      if (eng.seekTimer !== undefined) window.clearTimeout(eng.seekTimer);
      if (eng.retryTimer !== undefined) window.clearTimeout(eng.retryTimer);
      if (hideTimerRef.current !== undefined) window.clearTimeout(hideTimerRef.current);
      if (feedbackTimerRef.current !== undefined) window.clearTimeout(feedbackTimerRef.current);
      eng.abort?.abort();
      saveProgress();
      revokeCurrentUrl();
      video.removeEventListener("loadedmetadata", onLoadedMeta);
      video.removeEventListener("loadeddata", onCanPlay);
      video.removeEventListener("canplay", onCanPlay);
      video.removeEventListener("canplaythrough", onCanPlay);
      video.removeEventListener("playing", onPlaying);
      video.removeEventListener("pause", onPause);
      video.removeEventListener("waiting", onWaiting);
      video.removeEventListener("ended", onEnded);
      video.removeEventListener("seeking", onSeeking);
      video.removeEventListener("seeked", onSeeked);
      video.removeEventListener("timeupdate", onTimeUpdate);
      video.removeEventListener("progress", onProgress);
      video.removeEventListener("volumechange", onVolumeChange);
      video.removeEventListener("error", onError);
      document.removeEventListener("fullscreenchange", onFsChange);
      video.removeEventListener("webkitbeginfullscreen", onFsChange as EventListener);
      video.removeEventListener("webkitendfullscreen", onFsChange as EventListener);
      video.pause();
      video.removeAttribute("src");
      video.load();
    };
  }, [attemptPlay, hash, onCodecNoteChange, probe, saveProgress, showControls]);

  const togglePlay = useCallback(() => {
    const video = videoRef.current;
    if (!video || !readyRef.current) return;
    if (!startedRef.current || fatalError) {
      void startPlaybackRef.current();
      return;
    }
    if (video.paused || video.ended) {
      wantPlayRef.current = true;
      setNeedsTap(false);
      void attemptPlay();
      showControls(false);
    } else {
      wantPlayRef.current = false;
      video.pause();
      showControls(true);
    }
  }, [attemptPlay, fatalError, showControls]);

  const seekBy = useCallback(
    (delta: number, side: "left" | "right" | "center" = "center") => {
      const video = videoRef.current;
      if (!video || !startedRef.current) return;
      const dur = duration || video.duration;
      if (!Number.isFinite(dur) || dur <= 0) return;
      const next = Math.max(0, Math.min(dur, video.currentTime + delta));
      video.currentTime = next;
      setCurrentTime(next);
      flashSeek(delta > 0 ? `+${delta}s` : `${delta}s`, side);
      showControls(false);
    },
    [duration, flashSeek, showControls],
  );

  const toggleMute = useCallback(() => {
    const video = videoRef.current;
    if (!video) return;
    video.muted = !video.muted;
    showControls(false);
  }, [showControls]);

  const toggleFullscreen = useCallback(() => {
    const container = containerRef.current;
    const video = videoRef.current;
    if (!container || !video) return;

    if (document.fullscreenElement) {
      void document.exitFullscreen().catch(() => {});
    } else if (container.requestFullscreen) {
      void container.requestFullscreen().catch(() => {
        if (video.webkitEnterFullscreen) video.webkitEnterFullscreen();
      });
    } else if (video.webkitEnterFullscreen) {
      video.webkitEnterFullscreen();
    }
    showControls(false);
  }, [showControls]);

  const cycleRate = useCallback(() => {
    const video = videoRef.current;
    if (!video) return;
    const idx = RATES.indexOf(rate);
    const next = RATES[(idx + 1) % RATES.length];
    video.playbackRate = next;
    setRate(next);
    showControls(false);
  }, [rate, showControls]);

  const cycleQuality = useCallback(() => {
    const idx = QUALITIES.indexOf(qualityRef.current);
    const next = QUALITIES[(idx + 1) % QUALITIES.length];
    qualityRef.current = next;
    setQuality(next);
    showControls(false);
    if (startedRef.current) {
      const pos = videoRef.current?.currentTime || savedPositionRef.current;
      void startPlaybackRef.current(pos, next);
    }
  }, [showControls]);

  const handleRetry = useCallback(() => {
    const video = videoRef.current;
    const pos = video?.currentTime || savedPositionRef.current;
    void startPlaybackRef.current(pos);
  }, []);

  const handleCopyVlc = useCallback(() => {
    const url = `${window.location.origin}${mediaUrl(`/media/${hash}`)}`;
    navigator.clipboard
      .writeText(url)
      .then(() => {
        setCopiedVlc(true);
        window.setTimeout(() => setCopiedVlc(false), 2000);
      })
      .catch(() => {});
    showControls(false);
  }, [hash, showControls]);

  // Keyboard shortcuts
  useEffect(() => {
    function onKeyDown(event: KeyboardEvent) {
      const target = event.target as HTMLElement | null;
      if (
        event.defaultPrevented ||
        event.altKey ||
        event.ctrlKey ||
        event.metaKey ||
        document.querySelector("dialog[open]") ||
        target?.closest("input, textarea, select, [contenteditable], [role=slider]")
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

      const video = videoRef.current;
      if (key === " " || key === "k") {
        togglePlay();
      } else if (key === "arrowleft") {
        seekBy(-5, "left");
      } else if (key === "arrowright") {
        seekBy(5, "right");
      } else if (key === "j") {
        seekBy(-10, "left");
      } else if (key === "l") {
        seekBy(10, "right");
      } else if (key === "arrowup" && video) {
        video.volume = Math.min(1, video.volume + 0.05);
        video.muted = false;
      } else if (key === "arrowdown" && video) {
        video.volume = Math.max(0, video.volume - 0.05);
      } else if (key === "m") {
        toggleMute();
      } else if (key === "f") {
        toggleFullscreen();
      } else if (key === "c") {
        setSubOpenSignal((n) => n + 1);
      }
    }

    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [seekBy, toggleFullscreen, toggleMute, togglePlay]);

  const calcScrubTime = (clientX: number): number => {
    const el = scrubRef.current;
    if (!el || !duration) return 0;
    const rect = el.getBoundingClientRect();
    const pct = Math.max(0, Math.min(1, (clientX - rect.left) / rect.width));
    return pct * duration;
  };

  const onScrubDown = (e: ReactPointerEvent<HTMLDivElement>) => {
    if (!started || !duration) return;
    e.preventDefault();
    e.stopPropagation();
    e.currentTarget.setPointerCapture(e.pointerId);
    scrubbingRef.current = true;
    setScrubTime(calcScrubTime(e.clientX));
    showControls(true);
  };

  const onScrubMove = (e: ReactPointerEvent<HTMLDivElement>) => {
    if (!scrubbingRef.current) return;
    setScrubTime(calcScrubTime(e.clientX));
  };

  const onScrubUp = (e: ReactPointerEvent<HTMLDivElement>) => {
    if (!scrubbingRef.current) return;
    scrubbingRef.current = false;
    const t = calcScrubTime(e.clientX);
    setScrubTime(null);
    const video = videoRef.current;
    if (video && Number.isFinite(t)) {
      video.currentTime = t;
      setCurrentTime(t);
    }
    showControls(false);
  };

  const onSurfaceUp = (e: ReactPointerEvent<HTMLDivElement>) => {
    if (!ready) return;
    if (!started) {
      togglePlay();
      return;
    }

    const rect = e.currentTarget.getBoundingClientRect();
    const xRatio = (e.clientX - rect.left) / rect.width;
    const side: "left" | "right" | "center" =
      xRatio < 0.32 ? "left" : xRatio > 0.68 ? "right" : "center";

    if (e.pointerType !== "mouse") {
      const now = Date.now();
      if (
        side !== "center" &&
        lastTapRef.current.side === side &&
        now - lastTapRef.current.time < 320
      ) {
        seekBy(side === "left" ? -10 : 10, side);
        lastTapRef.current = { time: 0, side: "center" };
        return;
      }
      lastTapRef.current = { time: now, side };
      if (controlsVisible && playing) {
        setControlsVisible(false);
      } else {
        showControls(false);
      }
    } else {
      togglePlay();
    }
  };

  const uiOpen = controlsVisible || !playing || !started || buffering || Boolean(fatalError);
  const displayTime = scrubTime ?? currentTime;
  const playedPct = duration > 0 ? Math.min(100, (displayTime / duration) * 100) : 0;
  const bufferedPct = duration > 0 ? Math.min(100, (bufferedEndTime / duration) * 100) : 0;

  return (
    <div
      ref={containerRef}
      className={`kp ${uiOpen ? "kp-ui-open" : ""} ${isFullscreen ? "kp-fs" : ""}`}
      onPointerMove={(e) => {
        if (e.pointerType === "mouse") showControls(false);
      }}
      onPointerLeave={(e) => {
        if (e.pointerType === "mouse" && playing && !scrubbingRef.current) {
          setControlsVisible(false);
        }
      }}
    >
      <video
        ref={videoRef}
        className="kp-video"
        playsInline
        preload="none"
        aria-label="Movie player"
      />

      <div className="kp-surface" onPointerUp={onSurfaceUp} />
      <div className="kp-scrim" />

      {/* Top Overlay Bar */}
      <div className="kp-top" onClick={(e) => e.stopPropagation()}>
        <span className="kp-status-pill" role="status" aria-live="polite">
          <span className={`kp-status-dot ${playing && !buffering ? "live" : ""}`} />
          {statusText}
        </span>

        <div className="kp-top-actions">
          <button
            type="button"
            className="kp-pill-btn mono"
            aria-label={`Stream quality ${qualityLabel(quality)}`}
            title="Switch stream quality (Auto / 1080p / 720p)"
            onClick={cycleQuality}
          >
            {qualityLabel(quality)}
          </button>
          <button
            type="button"
            className="kp-pill-btn"
            aria-label="Retry stream"
            title="Retry stream"
            onClick={handleRetry}
          >
            <RefreshIcon size={13} />
          </button>
          <button
            type="button"
            className="kp-pill-btn"
            aria-label="Copy VLC stream link"
            title="Copy VLC stream link"
            onClick={handleCopyVlc}
          >
            {copiedVlc ? <CheckIcon size={13} /> : <LinkIcon size={13} />}
            <span>{copiedVlc ? "Copied" : "VLC"}</span>
          </button>
        </div>
      </div>

      {/* Center Transport Controls */}
      <div className="kp-center">
        {!ready && !started ? (
          <div className="kp-loading-pill">
            <span className="kp-spinner" />
            <span>{statusText}</span>
          </div>
        ) : fatalError ? (
          <div className="kp-error" onClick={(e) => e.stopPropagation()}>
            <p>{fatalError}</p>
            <Button size="sm" onClick={handleRetry}>
              <RefreshIcon size={14} /> Retry
            </Button>
          </div>
        ) : !started || needsTap ? (
          <div className="kp-transport always" onClick={(e) => e.stopPropagation()}>
            <button
              type="button"
              className="kp-play-hero"
              aria-label="Play movie"
              onClick={togglePlay}
            >
              <PlayIcon size={28} style={{ marginLeft: 3 }} />
            </button>
          </div>
        ) : (
          <div
            className={`kp-transport ${buffering ? "always" : ""}`}
            onClick={(e) => e.stopPropagation()}
          >
            <button
              type="button"
              className="kp-skip-hero"
              aria-label="Back 10 seconds"
              onClick={() => seekBy(-10, "left")}
            >
              <SkipBackIcon size={20} />
            </button>

            {buffering ? (
              <div className="kp-buffering-ring" aria-label="Buffering">
                <span className="kp-spinner kp-spinner-lg" />
              </div>
            ) : (
              <button
                type="button"
                className="kp-play-hero"
                aria-label={playing ? "Pause" : "Play"}
                onClick={togglePlay}
              >
                {playing ? (
                  <PauseIcon size={26} />
                ) : (
                  <PlayIcon size={28} style={{ marginLeft: 3 }} />
                )}
              </button>
            )}

            <button
              type="button"
              className="kp-skip-hero"
              aria-label="Forward 10 seconds"
              onClick={() => seekBy(10, "right")}
            >
              <SkipForwardIcon size={20} />
            </button>
          </div>
        )}
      </div>

      {seekFeedback && (
        <div className={`kp-seek-badge ${seekFeedback.side}`}>{seekFeedback.label}</div>
      )}

      {/* Bottom Scrubber & Action Row */}
      <div className="kp-bottom" onClick={(e) => e.stopPropagation()}>
        <div
          ref={scrubRef}
          className={`kp-scrub ${scrubTime !== null ? "active" : ""}`}
          role="slider"
          aria-label="Seek timeline"
          aria-valuemin={0}
          aria-valuemax={Math.floor(duration)}
          aria-valuenow={Math.floor(displayTime)}
          tabIndex={0}
          onPointerDown={onScrubDown}
          onPointerMove={onScrubMove}
          onPointerUp={onScrubUp}
          onPointerCancel={onScrubUp}
        >
          <div className="kp-scrub-rail">
            <div className="kp-scrub-buf" style={{ width: `${bufferedPct}%` }} />
            <div className="kp-scrub-fill" style={{ width: `${playedPct}%` }} />
            <div className="kp-scrub-knob" style={{ left: `${playedPct}%` }} />
          </div>
          {scrubTime !== null && (
            <div className="kp-scrub-tip" style={{ left: `${playedPct}%` }}>
              {formatClock(scrubTime)}
            </div>
          )}
        </div>

        <div className="kp-row">
          <div className="kp-row-left">
            <button
              type="button"
              className="kp-btn kp-desktop-only"
              disabled={!ready}
              aria-label={playing ? "Pause" : "Play"}
              onClick={togglePlay}
            >
              {playing ? <PauseIcon size={18} /> : <PlayIcon size={18} />}
            </button>

            <div className="kp-vol">
              <button
                type="button"
                className="kp-btn"
                aria-label={muted || volume === 0 ? "Unmute" : "Mute"}
                onClick={toggleMute}
              >
                {muted || volume === 0 ? (
                  <VolumeMuteIcon size={17} />
                ) : (
                  <VolumeIcon size={17} />
                )}
              </button>
              <input
                type="range"
                className="kp-vol-range"
                aria-label="Volume"
                min={0}
                max={1}
                step={0.05}
                value={muted ? 0 : volume}
                onChange={(e) => {
                  const v = Number(e.target.value);
                  if (videoRef.current) {
                    videoRef.current.volume = v;
                    videoRef.current.muted = v === 0;
                  }
                }}
              />
            </div>

            <span className="kp-time">
              {formatClock(displayTime)}
              <span className="kp-time-sep">/</span>
              {formatClock(duration)}
            </span>
          </div>

          <div className="kp-row-right">
            <button
              type="button"
              className="kp-btn kp-rate"
              aria-label={`Speed ${rate}x`}
              title="Playback speed"
              onClick={cycleRate}
            >
              {rate}×
            </button>
            <button
              ref={subBtnRef}
              type="button"
              className={`kp-btn ${subActive ? "active" : ""}`}
              aria-label="Subtitles"
              title="Subtitles (C)"
              onClick={() => setSubOpenSignal((n) => n + 1)}
            >
              <SubtitlesIcon size={18} />
            </button>
            <button
              type="button"
              className="kp-btn"
              aria-label={isFullscreen ? "Exit fullscreen" : "Fullscreen"}
              title="Fullscreen (F)"
              onClick={toggleFullscreen}
            >
              {isFullscreen ? (
                <ExitFullscreenIcon size={17} />
              ) : (
                <FullscreenIcon size={17} />
              )}
            </button>
          </div>
        </div>
      </div>

      <SubtitleDialog
        hash={hash}
        video={videoEl}
        openSignal={subOpenSignal}
        onCloseFocus={() => subBtnRef.current?.focus()}
        onActiveChange={setSubActive}
      />
    </div>
  );
}
