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

const RATES = [0.75, 1, 1.25, 1.5, 2];

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

  const startPlaybackRef = useRef<(fromOverride?: number) => Promise<void>>(async () => {});

  useEffect(() => {
    const rawVideo = videoRef.current;
    if (!rawVideo) return;
    const video: WebkitVideoElement = rawVideo;
    setVideoEl(video);

    const eng = engineRef.current;
    eng.disposed = false;
    eng.remux = null;
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
    restoredRef.current = false;
    targetTimeRef.current = 0;
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

    async function evict(keepBehind = 25) {
      if (!eng.buffer || !eng.mediaSource || eng.mediaSource.readyState !== "open") return;
      const keep = video.currentTime - keepBehind;
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

    async function appendBytes(data: Uint8Array<ArrayBuffer>, id: number): Promise<boolean> {
      for (let attempt = 0; attempt < 4; attempt++) {
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
          await evict(attempt === 0 ? 10 : 2);
          await sleep(300);
        }
      }
      return false;
    }

    function revokeCurrentUrl() {
      if (eng.objectUrl) {
        URL.revokeObjectURL(eng.objectUrl);
        eng.objectUrl = "";
      }
    }

    function useCompat() {
      if (!eng.info || eng.disposed) return;
      eng.remux = "compat";
      onCodecNoteChange?.(
        `Converting ${eng.info.video.toUpperCase()} to H.264 (${eng.info.width}×${eng.info.height}).`,
      );
      restoredRef.current = false;
      const pos = video.currentTime || savedPositionRef.current;
      savedPositionRef.current = pos;
      openRemux(pos);
      if (wantPlayRef.current) void attemptPlay();
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
        const response = await fetch(
          mediaUrl(`/media/${hash}/remux?mode=${eng.remux}&t=${from.toFixed(3)}`),
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
          while (id === eng.session && !eng.disposed) {
            const range = rangeAt(video.currentTime);
            if (!range || range[1] - video.currentTime < 45) break;
            if (!(await flushPending())) {
              reader.cancel().catch(() => {});
              return;
            }
            await evict();
            await sleep(500);
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
              useCompat();
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
          useCompat();
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
            if (eng.remux === "copy") useCompat();
          }
        },
        { once: true },
      );

      video.src = url;
      video.load();
    }

    async function startPlayback(fromOverride?: number) {
      const current = latestRef.current;
      if (!readyRef.current || !current || eng.disposed) return;

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

      if (current.direct) {
        eng.remux = null;
        eng.session++;
        eng.abort?.abort();
        revokeCurrentUrl();
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
        restoredRef.current = true;
        onCodecNoteChange?.(`${label} · converted on the fly`);
        video.src = mediaUrl(`/media/${hash}/remux?mode=compat`);
        video.load();
        await attemptPlay();
        return;
      }

      if (eng.remux !== "compat") {
        eng.remux = MS.isTypeSupported(mime("copy")) ? "copy" : "compat";
      }
      onCodecNoteChange?.(
        eng.remux === "copy"
          ? `${label} · original quality`
          : `Converting ${eng.info.video.toUpperCase()} to H.264 (${eng.info.width}×${eng.info.height}).`,
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

    const watchdog = window.setInterval(() => {
      if (eng.disposed || !startedRef.current || !wantPlayRef.current || video.ended) return;
      if (scrubbingRef.current) return;

      if (eng.buffer && eng.buffer.buffered.length > 0 && video.readyState < 3) {
        const t = video.currentTime;
        const inRange = rangeAt(t);
        if (!inRange) {
          for (let i = 0; i < eng.buffer.buffered.length; i++) {
            const s = eng.buffer.buffered.start(i);
            if (s > t && s - t <= 1.5) {
              internalSeekRef.current = true;
              video.currentTime = s + 0.02;
              break;
            }
          }
        }
      }

      if (video.paused && video.readyState >= 3 && !video.error) {
        void attemptPlay();
      }
    }, 400);

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
        useCompat();
        return;
      }
      if (!eng.remux && startedRef.current && eng.directRetries < 5) {
        eng.directRetries++;
        savedPositionRef.current = video.currentTime || savedPositionRef.current;
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
          ? "Source unavailable or unsupported codec. Try retrying once more peers connect."
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
