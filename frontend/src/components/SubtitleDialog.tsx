import { useCallback, useEffect, useRef, useState } from "react";
import { client, errMessage, mediaUrl } from "../api";
import type {
  BundledSubtitleFile,
  EmbeddedSubtitleTrack,
  SubtitleSearchResult,
  SubtitleTrack,
} from "../gen/bigflix/v1/bigflix_pb";
import {
  DEFAULT_SUBTITLE_STYLE,
  SUB_COLORS,
  cueLineStyle,
  type SubBg,
  type SubColor,
  type SubFont,
  type SubPos,
  type SubSize,
  type SubtitleStyle,
} from "../subtitleStyle";
import { CloseIcon } from "./Icons";
import { Badge, Button } from "./ui";

const LANGUAGES = [
  { value: "en", label: "English" },
  { value: "hi", label: "Hindi" },
  { value: "ne", label: "Nepali" },
  { value: "es", label: "Spanish" },
  { value: "fr", label: "French" },
  { value: "de", label: "German" },
  { value: "pt-br", label: "Portuguese (Brazil)" },
  { value: "ar", label: "Arabic" },
  { value: "ja", label: "Japanese" },
  { value: "ko", label: "Korean" },
  { value: "zh-cn", label: "Chinese (simplified)" },
];

interface SubtitleDialogProps {
  hash: string;
  fileIndex?: number;
  isComplete?: boolean;
  video: HTMLVideoElement | null;
  openSignal: number;
  nativeFullscreen: boolean;
  subtitleStyle: SubtitleStyle;
  onStyleChange: (next: SubtitleStyle) => void;
  onCuesChange: (cues: string[]) => void;
  onCloseFocus?: () => void;
  onActiveChange?: (active: boolean) => void;
}

interface ParsedCue {
  start: number;
  end: number;
  text: string;
}

const VTT_TIMING_RE =
  /^(\d{1,3}:\d{2}:\d{2}[.,]\d{3}|\d{2}:\d{2}[.,]\d{3})\s+-->\s+(\d{1,3}:\d{2}:\d{2}[.,]\d{3}|\d{2}:\d{2}[.,]\d{3})/;

function parseVTTTimestamp(raw: string): number {
  const parts = raw.replace(",", ".").split(":");
  if (parts.length === 2) {
    return Number(parts[0]) * 60 + Number(parts[1]);
  }
  if (parts.length === 3) {
    return Number(parts[0]) * 3600 + Number(parts[1]) * 60 + Number(parts[2]);
  }
  return NaN;
}

function cleanCueText(raw: string): string {
  return raw
    .replace(/<[^>]+>/g, "")
    .replace(/\{\\[^}]+\}/g, "")
    .replace(/&lt;/g, "<")
    .replace(/&gt;/g, ">")
    .replace(/&amp;/g, "&")
    .replace(/&nbsp;/g, " ")
    .trim();
}

function drainVTTCues(buffer: string): { cues: ParsedCue[]; rest: string } {
  const normalized = buffer.replace(/\r\n/g, "\n").replace(/\r/g, "\n");
  const blocks = normalized.split("\n\n");
  const rest = blocks.pop() ?? "";
  const cues: ParsedCue[] = [];
  for (const block of blocks) {
    const lines = block
      .split("\n")
      .map((l) => l.trim())
      .filter(Boolean);
    if (lines.length === 0) continue;
    let timingIdx = -1;
    for (let i = 0; i < lines.length; i++) {
      if (VTT_TIMING_RE.test(lines[i])) {
        timingIdx = i;
        break;
      }
    }
    if (timingIdx < 0) continue;
    const match = VTT_TIMING_RE.exec(lines[timingIdx]);
    if (!match) continue;
    const start = parseVTTTimestamp(match[1]);
    const end = parseVTTTimestamp(match[2]);
    if (!Number.isFinite(start) || !Number.isFinite(end) || end <= start) continue;
    const textLines = lines
      .slice(timingIdx + 1)
      .map(cleanCueText)
      .filter(Boolean);
    if (textLines.length === 0) continue;
    cues.push({ start, end, text: textLines.join("\n") });
  }
  return { cues, rest };
}

function readActiveCueTexts(track: TextTrack | undefined): string[] {
  if (!track?.activeCues || track.activeCues.length === 0) return [];
  const out: string[] = [];
  for (let i = 0; i < track.activeCues.length; i++) {
    const cue = track.activeCues[i] as VTTCue;
    if (typeof cue.text === "string" && cue.text.trim()) {
      out.push(cue.text);
    }
  }
  return out;
}

export function SubtitleDialog({
  hash,
  fileIndex,
  isComplete = false,
  video,
  openSignal,
  nativeFullscreen,
  subtitleStyle,
  onStyleChange,
  onCuesChange,
  onCloseFocus,
  onActiveChange,
}: SubtitleDialogProps) {
  const dialogRef = useRef<HTMLDialogElement | null>(null);
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const trackElRef = useRef<HTMLTrackElement | null>(null);
  const liveTrackRef = useRef<TextTrack | null>(null);
  const liveAbortRef = useRef<AbortController | null>(null);
  const liveSeenRef = useRef<Set<string>>(new Set());
  const liveWindowRef = useRef<{ start: number; maxEnd: number }>({ start: Infinity, maxEnd: -1 });
  const cueListenerCleanupRef = useRef<(() => void) | null>(null);
  const blobUrlRef = useRef<string>("");
  const originalTimesRef = useRef(new WeakMap<TextTrackCue, [number, number]>());
  const nativeFsRef = useRef(nativeFullscreen);
  const userPickedTabRef = useRef(false);

  const [tab, setTab] = useState<"tracks" | "online" | "look" | "upload">("tracks");
  const [language, setLanguage] = useState("en");
  const [tracks, setTracks] = useState<SubtitleTrack[]>([]);
  const [embedded, setEmbedded] = useState<EmbeddedSubtitleTrack[]>([]);
  const [bundled, setBundled] = useState<BundledSubtitleFile[]>([]);
  const [results, setResults] = useState<SubtitleSearchResult[]>([]);
  const [activeProvider, setActiveProvider] = useState("Online Subtitles");
  const [status, setStatus] = useState("");
  const [statusError, setStatusError] = useState(false);
  const [busy, setBusy] = useState(false);
  const [extractingStream, setExtractingStream] = useState<number | null>(null);
  const [liveEmbedded, setLiveEmbedded] = useState<EmbeddedSubtitleTrack | null>(null);

  const prefKey = `bigflix-subtitles-${hash}-${fileIndex ?? "default"}`;
  const [preferred, setPreferred] = useState<string>(() => {
    try {
      const saved = JSON.parse(localStorage.getItem(prefKey) || "{}");
      return saved.id || "";
    } catch {
      return "";
    }
  });
  const [offset, setOffset] = useState<number>(() => {
    try {
      const saved = JSON.parse(localStorage.getItem(prefKey) || "{}");
      return Number(saved.offset) || 0;
    } catch {
      return 0;
    }
  });
  const offsetRef = useRef(offset);
  useEffect(() => {
    offsetRef.current = offset;
  }, [offset]);

  const activeTextTrack = useCallback((): TextTrack | undefined => {
    return trackElRef.current?.track || liveTrackRef.current || undefined;
  }, []);

  useEffect(() => {
    nativeFsRef.current = nativeFullscreen;
    const t = activeTextTrack();
    if (t) {
      t.mode = nativeFullscreen ? "showing" : "hidden";
    }
  }, [nativeFullscreen, activeTextTrack]);

  useEffect(() => {
    try {
      localStorage.setItem(prefKey, JSON.stringify({ id: preferred, offset }));
    } catch {
      // Ignore storage errors.
    }
  }, [prefKey, preferred, offset]);

  const applyDelay = useCallback(
    (track: TextTrack | undefined, delayVal: number) => {
      if (!track?.cues) return;
      for (const cue of Array.from(track.cues)) {
        if (!originalTimesRef.current.has(cue)) {
          originalTimesRef.current.set(cue, [cue.startTime, cue.endTime]);
        }
        const pair = originalTimesRef.current.get(cue)!;
        cue.endTime = Math.max(0.001, pair[1] + delayVal);
        cue.startTime = Math.max(0, Math.min(pair[0] + delayVal, cue.endTime - 0.001));
      }
      onCuesChange(readActiveCueTexts(track));
    },
    [onCuesChange],
  );

  const stopLiveStream = useCallback(() => {
    liveAbortRef.current?.abort();
    liveAbortRef.current = null;
    if (liveTrackRef.current) {
      liveTrackRef.current.mode = "disabled";
      if (liveTrackRef.current.cues) {
        for (const cue of Array.from(liveTrackRef.current.cues)) {
          try {
            liveTrackRef.current.removeCue(cue);
          } catch {
            // Ignore removal errors
          }
        }
      }
    }
    liveSeenRef.current.clear();
    liveWindowRef.current = { start: Infinity, maxEnd: -1 };
  }, []);

  const clearTrack = useCallback(() => {
    stopLiveStream();
    cueListenerCleanupRef.current?.();
    cueListenerCleanupRef.current = null;
    if (trackElRef.current) {
      if (trackElRef.current.track) {
        trackElRef.current.track.mode = "disabled";
      }
      trackElRef.current.remove();
      trackElRef.current = null;
    }
    if (blobUrlRef.current) {
      URL.revokeObjectURL(blobUrlRef.current);
      blobUrlRef.current = "";
    }
    onCuesChange([]);
  }, [onCuesChange, stopLiveStream]);

  const pumpLiveStream = useCallback(
    async (item: EmbeddedSubtitleTrack, track: TextTrack, fromSec: number) => {
      liveAbortRef.current?.abort();
      const ac = new AbortController();
      liveAbortRef.current = ac;
      const startSec = Math.max(0, fromSec);
      if (startSec < liveWindowRef.current.start) {
        liveWindowRef.current.start = startSec;
      }

      const fileParam = fileIndex !== undefined ? `&file=${fileIndex}` : "";
      const url = mediaUrl(
        `/media/${hash}/subtitle/${item.streamIndex}?t=${startSec.toFixed(1)}${fileParam}`,
      );
      try {
        const res = await fetch(url, { signal: ac.signal });
        if (!res.ok || !res.body) {
          throw new Error((await res.text()) || "Live subtitle stream unavailable");
        }
        const reader = res.body.getReader();
        const decoder = new TextDecoder("utf-8");
        let buf = "";

        const appendCues = (parsed: ParsedCue[]) => {
          let hitCurrent = false;
          const nowT = video?.currentTime ?? -1;
          const delay = offsetRef.current;
          for (const c of parsed) {
            const key = `${c.start.toFixed(3)}|${c.end.toFixed(3)}|${c.text}`;
            if (liveSeenRef.current.has(key)) continue;
            liveSeenRef.current.add(key);
            if (c.end > liveWindowRef.current.maxEnd) {
              liveWindowRef.current.maxEnd = c.end;
            }
            const adjEnd = Math.max(0.001, c.end + delay);
            const adjStart = Math.max(0, Math.min(c.start + delay, adjEnd - 0.001));
            try {
              const cue = new VTTCue(adjStart, adjEnd, c.text);
              originalTimesRef.current.set(cue, [c.start, c.end]);
              track.addCue(cue);
              if (nowT >= adjStart - 0.1 && nowT <= adjEnd + 0.1) {
                hitCurrent = true;
              }
            } catch {
              // Ignore invalid cue timings
            }
          }
          if (hitCurrent) {
            onCuesChange(readActiveCueTexts(track));
          }
        };

        for (;;) {
          const { value, done } = await reader.read();
          if (ac.signal.aborted) {
            reader.cancel().catch(() => {});
            return;
          }
          if (done) {
            buf += decoder.decode();
            const { cues } = drainVTTCues(buf + "\n\n");
            appendCues(cues);
            break;
          }
          if (value && value.byteLength > 0) {
            buf += decoder.decode(value, { stream: true });
            const { cues, rest } = drainVTTCues(buf);
            buf = rest;
            appendCues(cues);
          }
        }
      } catch (err) {
        if (ac.signal.aborted) return;
        setStatusError(true);
        setStatus(errMessage(err));
      }
    },
    [fileIndex, hash, onCuesChange, video],
  );

  const startLiveEmbedded = useCallback(
    (item: EmbeddedSubtitleTrack) => {
      if (!video) return;
      clearTrack();
      setPreferred(`live:${item.streamIndex}`);
      setLiveEmbedded(item);
      setStatusError(false);
      setStatus(`Live built-in: ${item.label} (streaming with video)`);

      let track = liveTrackRef.current;
      if (!track) {
        const lang = item.language && item.language !== "und" ? item.language : language;
        track = video.addTextTrack("subtitles", `Built-in · ${item.label}`, lang);
        liveTrackRef.current = track;
      }
      track.mode = nativeFsRef.current ? "showing" : "hidden";
      const syncCues = () => onCuesChange(readActiveCueTexts(track));
      track.addEventListener("cuechange", syncCues);

      const onSeeked = () => {
        const t = video.currentTime;
        const win = liveWindowRef.current;
        if (t < win.start - 2 || t > win.maxEnd - 5) {
          void pumpLiveStream(item, track, t);
        }
      };
      video.addEventListener("seeked", onSeeked);
      cueListenerCleanupRef.current = () => {
        track.removeEventListener("cuechange", syncCues);
        video.removeEventListener("seeked", onSeeked);
      };

      onActiveChange?.(true);
      void pumpLiveStream(item, track, video.currentTime || 0);
    },
    [clearTrack, language, onActiveChange, onCuesChange, pumpLiveStream, video],
  );

  const selectTrack = useCallback(
    async (id: string, trackList: SubtitleTrack[], delayVal: number) => {
      setPreferred(id);
      setLiveEmbedded(null);
      clearTrack();
      if (!id || !video) {
        onActiveChange?.(false);
        return;
      }
      if (id.startsWith("live:")) {
        const idx = Number(id.slice(5));
        const match = embedded.find((e) => e.streamIndex === idx);
        if (match) {
          startLiveEmbedded(match);
        }
        return;
      }

      try {
        const res = await client.getSubtitle({ hash, id, fileIndex });
        const meta = trackList.find((t) => t.id === id);
        const blob = new Blob([res.vtt], { type: "text/vtt" });
        const url = URL.createObjectURL(blob);
        blobUrlRef.current = url;

        const el = document.createElement("track");
        el.kind = "subtitles";
        el.src = url;
        el.label = meta ? `${meta.language} · ${meta.label}` : "Subtitles";
        el.srclang = meta?.language || language;
        el.default = true;

        const syncCues = () => onCuesChange(readActiveCueTexts(el.track));
        el.addEventListener("load", () => {
          applyDelay(el.track, delayVal);
          el.track.mode = nativeFsRef.current ? "showing" : "hidden";
          syncCues();
        });
        video.appendChild(el);
        trackElRef.current = el;
        el.track.mode = nativeFsRef.current ? "showing" : "hidden";
        el.track.addEventListener("cuechange", syncCues);
        cueListenerCleanupRef.current = () => {
          el.track.removeEventListener("cuechange", syncCues);
        };
        onActiveChange?.(true);
      } catch (err) {
        onActiveChange?.(false);
        setStatusError(true);
        setStatus(errMessage(err));
      }
    },
    [
      applyDelay,
      clearTrack,
      embedded,
      fileIndex,
      hash,
      language,
      onActiveChange,
      onCuesChange,
      startLiveEmbedded,
      video,
    ],
  );

  const refresh = useCallback(
    async (selectedId?: string, autoSelect = true) => {
      try {
        const data = await client.listSubtitles({ hash, fileIndex });
        setTracks(data.tracks);
        setEmbedded(data.embedded);
        setBundled(data.bundled);
        if (data.activeProvider) {
          setActiveProvider(data.activeProvider);
        }
        if (!userPickedTabRef.current) {
          const hasLocal =
            data.tracks.length > 0 || data.embedded.length > 0 || data.bundled.length > 0;
          setTab(hasLocal ? "tracks" : "online");
        }
        if (autoSelect) {
          const target = selectedId !== undefined ? selectedId : preferred;
          if (target && target.startsWith("live:")) {
            const idx = Number(target.slice(5));
            const match = data.embedded.find((e) => e.streamIndex === idx);
            if (match) {
              startLiveEmbedded(match);
            }
          } else if (target && data.tracks.some((t) => t.id === target)) {
            await selectTrack(target, data.tracks, offset);
          }
        }
      } catch (err) {
        setStatusError(true);
        setStatus(errMessage(err));
      }
    },
    [fileIndex, hash, offset, preferred, selectTrack, startLiveEmbedded],
  );

  useEffect(() => {
    if (!video) return;
    liveTrackRef.current = null;
    userPickedTabRef.current = false;
    void refresh();
    return () => {
      clearTrack();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hash, fileIndex, video]);

  useEffect(() => {
    if (!video) return;
    const handleMeta = () => {
      const curTrack = activeTextTrack();
      if (curTrack) {
        curTrack.mode = nativeFsRef.current ? "showing" : "hidden";
        applyDelay(curTrack, offset);
      } else if (preferred && tracks.some((t) => t.id === preferred)) {
        void selectTrack(preferred, tracks, offset);
      }
    };
    video.addEventListener("loadedmetadata", handleMeta);
    return () => video.removeEventListener("loadedmetadata", handleMeta);
  }, [video, preferred, tracks, offset, applyDelay, selectTrack, activeTextTrack]);

  // Refresh subtitle list (including newly probed built-in tracks) whenever opened
  useEffect(() => {
    if (openSignal > 0 && dialogRef.current && !dialogRef.current.open) {
      dialogRef.current.showModal();
      void refresh(undefined, false);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [openSignal]);

  function chooseTab(next: "tracks" | "online" | "look" | "upload") {
    userPickedTabRef.current = true;
    setTab(next);
  }

  async function handleSearch() {
    setBusy(true);
    setStatusError(false);
    setStatus(`Searching ${activeProvider}…`);
    setResults([]);
    try {
      const res = await client.searchSubtitles({ hash, language, fileIndex });
      setResults(res.results);
      setStatus(
        res.results.length
          ? `Found ${res.results.length} subtitles${res.provider ? ` (${res.provider})` : ""}. Pick a release matching your video.`
          : "No new subtitles found for this language.",
      );
    } catch (err) {
      setStatusError(true);
      setStatus(errMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function handleAdd(params: {
    action: string;
    id?: number;
    downloadKey?: string;
    label?: string;
    index?: number;
    data?: Uint8Array;
    filename?: string;
    lang?: string;
  }) {
    setBusy(true);
    setStatusError(false);
    if (params.action === "embedded" && params.index !== undefined) {
      setExtractingStream(params.index);
      setStatus("Extracting built-in subtitle…");
    } else {
      setStatus("Loading subtitle…");
    }
    try {
      const res = await client.addSubtitle({
        hash,
        action: params.action,
        language: params.lang || language,
        id: params.id || 0,
        downloadKey: params.downloadKey || "",
        label: params.label || "",
        index: params.index || 0,
        data: params.data || new Uint8Array(),
        filename: params.filename || "",
        fileIndex,
      });
      if (res.track) {
        await refresh(res.track.id, true);
        if (params.action === "download") {
          setResults((prev) =>
            prev.filter(
              (r) =>
                (params.downloadKey ? r.downloadKey !== params.downloadKey : true) &&
                (params.id ? r.id !== params.id : true),
            ),
          );
        }
        setStatus(`Active: ${res.track.label}`);
      }
    } catch (err) {
      setStatusError(true);
      setStatus(errMessage(err));
    } finally {
      setBusy(false);
      setExtractingStream(null);
    }
  }

  async function handleUpload() {
    const file = fileInputRef.current?.files?.[0];
    if (!file) {
      setStatusError(true);
      setStatus("Choose an SRT or VTT file first.");
      return;
    }
    if (file.size > 2 * 1024 * 1024) {
      setStatusError(true);
      setStatus("Subtitle must be at most 2 MB.");
      return;
    }
    const buf = new Uint8Array(await file.arrayBuffer());
    await handleAdd({ action: "upload", data: buf, filename: file.name });
  }

  const updateStyle = (patch: Partial<SubtitleStyle>) => {
    onStyleChange({ ...subtitleStyle, ...patch });
  };

  const localCount = embedded.length + bundled.length;

  return (
    <dialog
      ref={dialogRef}
      id="subtitle-dialog"
      className="ds-dialog"
      aria-labelledby="subtitle-title"
      onClose={() => onCloseFocus?.()}
      onKeyDown={(e) => e.stopPropagation()}
      onClick={(e) => {
        if (e.target === dialogRef.current && dialogRef.current) {
          const b = dialogRef.current.getBoundingClientRect();
          if (
            e.clientX < b.left ||
            e.clientX > b.right ||
            e.clientY < b.top ||
            e.clientY > b.bottom
          ) {
            dialogRef.current.close();
          }
        }
      }}
    >
      <div className="ds-dialog-head">
        <h2 id="subtitle-title">Subtitles</h2>
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label="Close subtitles"
          onClick={() => dialogRef.current?.close()}
        >
          <CloseIcon size={17} />
        </Button>
      </div>

      <div
        className="ds-seg sub-tabs"
        role="group"
        aria-label="Subtitle sections"
      >
        <button
          type="button"
          className="ds-seg-item"
          aria-pressed={tab === "tracks"}
          onClick={() => chooseTab("tracks")}
        >
          Tracks{localCount > 0 ? ` (${localCount})` : ""}
        </button>
        <button
          type="button"
          className="ds-seg-item"
          aria-pressed={tab === "online"}
          onClick={() => chooseTab("online")}
        >
          Find
        </button>
        <button
          type="button"
          className="ds-seg-item"
          aria-pressed={tab === "look"}
          onClick={() => chooseTab("look")}
        >
          Look
        </button>
        <button
          type="button"
          className="ds-seg-item"
          aria-pressed={tab === "upload"}
          onClick={() => chooseTab("upload")}
        >
          Upload
        </button>
      </div>

      {/* Tracks Tab: Active Track + Timing + Built-in & Bundled Subtitles */}
      <div hidden={tab !== "tracks"}>
        <div className="sub-controls">
          <label>
            Active track
            <select
              className="ds-select"
              value={preferred}
              onChange={(e) => void selectTrack(e.target.value, tracks, offset)}
            >
              <option value="">Off</option>
              {liveEmbedded && (
                <option value={`live:${liveEmbedded.streamIndex}`}>
                  Built-in (Live) · {liveEmbedded.label}
                </option>
              )}
              {tracks.map((sub) => (
                <option key={sub.id} value={sub.id}>
                  {sub.language} · {sub.label}
                </option>
              ))}
            </select>
          </label>
          <label className="sub-offset">
            Offset (sec)
            <input
              id="subtitle-delay"
              className="ds-input mono"
              type="number"
              min={-120}
              max={120}
              step={0.5}
              value={offset}
              onChange={(e) => {
                const next = Math.max(-120, Math.min(120, Number(e.target.value) || 0));
                setOffset(next);
                applyDelay(activeTextTrack(), next);
              }}
            />
          </label>
        </div>

        {embedded.length > 0 && (
          <div className="sub-section">
            <div className="sub-section-head">
              <h3>Built-in subtitles</h3>
              <Badge className="mono">{embedded.length}</Badge>
              {!isComplete && <Badge tone="accent">Live streaming</Badge>}
            </div>
            <div className="sub-results">
              {embedded.map((item) => {
                const isExtracting = extractingStream === item.streamIndex;
                const isLiveActive = liveEmbedded?.streamIndex === item.streamIndex;
                return (
                  <div key={item.streamIndex} className="sub-row">
                    <div className="sub-row-info">
                      <span>{item.label}</span>
                      <div className="sub-badges">
                        <small>{item.codec}</small>
                        {item.isDefault && <Badge tone="accent">Default</Badge>}
                        {item.hearingImpaired && <Badge>SDH</Badge>}
                        {item.forced && <Badge>Forced</Badge>}
                        {isLiveActive && <Badge tone="ok">Live</Badge>}
                      </div>
                    </div>
                    <Button
                      variant={isLiveActive ? "primary" : "secondary"}
                      size="sm"
                      disabled={busy || isLiveActive}
                      onClick={() => {
                        if (isComplete) {
                          void handleAdd({
                            action: "embedded",
                            index: item.streamIndex,
                            label: item.label,
                            lang:
                              item.language && item.language !== "und"
                                ? item.language
                                : language,
                          });
                        } else {
                          startLiveEmbedded(item);
                        }
                      }}
                    >
                      {isExtracting ? "Extracting…" : isLiveActive ? "Active" : "Use"}
                    </Button>
                  </div>
                );
              })}
            </div>
          </div>
        )}

        {bundled.length > 0 && (
          <div className="sub-section">
            <div className="sub-section-head">
              <h3>Torrent subtitle files</h3>
              <Badge className="mono">{bundled.length}</Badge>
            </div>
            <div className="sub-bundled">
              {bundled.map((file) => (
                <Button
                  key={file.index}
                  variant="secondary"
                  size="sm"
                  disabled={busy}
                  onClick={() => void handleAdd({ action: "bundled", index: file.index })}
                >
                  Use {file.name}
                </Button>
              ))}
            </div>
          </div>
        )}

        {embedded.length === 0 && bundled.length === 0 && tracks.length === 0 && (
          <p className="muted" style={{ marginTop: 8 }}>
            No built-in or saved subtitles found yet. Use the Find or Upload tab to add one.
          </p>
        )}
      </div>

      {/* Find Online Tab */}
      <div hidden={tab !== "online"}>
        <div className="sub-controls">
          <label>
            Language
            <select
              className="ds-select"
              value={language}
              onChange={(e) => setLanguage(e.target.value)}
            >
              {LANGUAGES.map((l) => (
                <option key={l.value} value={l.value}>
                  {l.label}
                </option>
              ))}
            </select>
          </label>
          <Button disabled={busy} onClick={() => void handleSearch()}>
            {busy ? "Searching…" : "Search"}
          </Button>
        </div>

        <div className="sub-results">
          {results.map((match) => (
            <div key={match.downloadKey || String(match.id)} className="sub-row">
              <div className="sub-row-info">
                <span>{match.label}</span>
                <small>
                  {match.provider ? `${match.provider.toUpperCase()} · ` : ""}
                  {match.rating > 0 ? `Rating ${match.rating}` : "Subtitle"}
                  {match.hearingImpaired ? " · SDH" : ""}
                </small>
              </div>
              <Button
                variant="secondary"
                size="sm"
                disabled={busy}
                onClick={() =>
                  void handleAdd({
                    action: "download",
                    id: match.id,
                    downloadKey: match.downloadKey,
                    label: match.label,
                  })
                }
              >
                Use
              </Button>
            </div>
          ))}
        </div>
      </div>

      {/* Look (Appearance) Tab */}
      <div hidden={tab !== "look"} className="sub-look">
        <div className="sub-preview-stage" aria-label="Subtitle style preview">
          <div className="sub-preview-lines">
            <span style={cueLineStyle(subtitleStyle, true)}>
              This is how your subtitles look.
            </span>
          </div>
        </div>

        <div className="sub-look-grid">
          <div className="sub-look-row">
            <span className="sub-look-label">Size</span>
            <div className="ds-seg">
              {(
                [
                  { id: "sm", label: "S" },
                  { id: "md", label: "M" },
                  { id: "lg", label: "L" },
                  { id: "xl", label: "XL" },
                ] as { id: SubSize; label: string }[]
              ).map((opt) => (
                <button
                  key={opt.id}
                  type="button"
                  className="ds-seg-item"
                  aria-pressed={subtitleStyle.size === opt.id}
                  onClick={() => updateStyle({ size: opt.id })}
                >
                  {opt.label}
                </button>
              ))}
            </div>
          </div>

          <div className="sub-look-row">
            <span className="sub-look-label">Color</span>
            <div className="sub-swatches">
              {(Object.keys(SUB_COLORS) as SubColor[]).map((key) => (
                <button
                  key={key}
                  type="button"
                  className={`sub-swatch ${subtitleStyle.color === key ? "active" : ""}`}
                  style={{ background: SUB_COLORS[key].hex }}
                  title={SUB_COLORS[key].label}
                  aria-label={SUB_COLORS[key].label}
                  aria-pressed={subtitleStyle.color === key}
                  onClick={() => updateStyle({ color: key })}
                />
              ))}
            </div>
          </div>

          <div className="sub-look-row">
            <span className="sub-look-label">Backdrop</span>
            <div className="ds-seg">
              {(
                [
                  { id: "outline", label: "Shadow" },
                  { id: "translucent", label: "Soft" },
                  { id: "solid", label: "Solid" },
                ] as { id: SubBg; label: string }[]
              ).map((opt) => (
                <button
                  key={opt.id}
                  type="button"
                  className="ds-seg-item"
                  aria-pressed={subtitleStyle.bg === opt.id}
                  onClick={() => updateStyle({ bg: opt.id })}
                >
                  {opt.label}
                </button>
              ))}
            </div>
          </div>

          <div className="sub-look-row">
            <span className="sub-look-label">Font</span>
            <div className="ds-seg">
              {(
                [
                  { id: "sans", label: "Sans" },
                  { id: "serif", label: "Serif" },
                  { id: "mono", label: "Mono" },
                ] as { id: SubFont; label: string }[]
              ).map((opt) => (
                <button
                  key={opt.id}
                  type="button"
                  className="ds-seg-item"
                  aria-pressed={subtitleStyle.font === opt.id}
                  onClick={() => updateStyle({ font: opt.id })}
                >
                  {opt.label}
                </button>
              ))}
            </div>
          </div>

          <div className="sub-look-row">
            <span className="sub-look-label">Position</span>
            <div className="ds-seg">
              {(
                [
                  { id: "low", label: "Low" },
                  { id: "mid", label: "Mid" },
                  { id: "high", label: "High" },
                ] as { id: SubPos; label: string }[]
              ).map((opt) => (
                <button
                  key={opt.id}
                  type="button"
                  className="ds-seg-item"
                  aria-pressed={subtitleStyle.position === opt.id}
                  onClick={() => updateStyle({ position: opt.id })}
                >
                  {opt.label}
                </button>
              ))}
            </div>
          </div>
        </div>

        <div className="sub-look-foot">
          <Button
            variant="ghost"
            size="sm"
            onClick={() => onStyleChange(DEFAULT_SUBTITLE_STYLE)}
          >
            Reset defaults
          </Button>
        </div>
      </div>

      {/* Upload Tab */}
      <div hidden={tab !== "upload"}>
        <div className="sub-controls">
          <label>
            SRT or WebVTT (max 2 MB)
            <input
              ref={fileInputRef}
              className="ds-input"
              style={{ paddingTop: 7 }}
              type="file"
              accept=".srt,.vtt,text/vtt"
            />
          </label>
          <Button disabled={busy} onClick={() => void handleUpload()}>
            Upload
          </Button>
        </div>
      </div>

      {status && (
        <p
          className={`sub-status ${statusError ? "sub-status-warn" : ""}`}
          role="status"
          aria-live="polite"
        >
          {status}
        </p>
      )}
    </dialog>
  );
}
