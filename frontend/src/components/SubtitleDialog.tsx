import { useCallback, useEffect, useRef, useState } from "react";
import { client, errMessage } from "../api";
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
  video: HTMLVideoElement | null;
  openSignal: number;
  nativeFullscreen: boolean;
  subtitleStyle: SubtitleStyle;
  onStyleChange: (next: SubtitleStyle) => void;
  onCuesChange: (cues: string[]) => void;
  onCloseFocus?: () => void;
  onActiveChange?: (active: boolean) => void;
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
  const [status, setStatus] = useState("");
  const [statusError, setStatusError] = useState(false);
  const [busy, setBusy] = useState(false);
  const [extractingStream, setExtractingStream] = useState<number | null>(null);

  const prefKey = `bigflix-subtitles-${hash}`;
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

  useEffect(() => {
    nativeFsRef.current = nativeFullscreen;
    if (trackElRef.current?.track) {
      trackElRef.current.track.mode = nativeFullscreen ? "showing" : "hidden";
    }
  }, [nativeFullscreen]);

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

  const clearTrack = useCallback(() => {
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
  }, [onCuesChange]);

  const selectTrack = useCallback(
    async (id: string, trackList: SubtitleTrack[], delayVal: number) => {
      setPreferred(id);
      clearTrack();
      if (!id || !video) {
        onActiveChange?.(false);
        return;
      }

      try {
        const res = await client.getSubtitle({ hash, id });
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
    [applyDelay, clearTrack, hash, language, onActiveChange, onCuesChange, video],
  );

  const refresh = useCallback(
    async (selectedId?: string, autoSelect = true) => {
      try {
        const data = await client.listSubtitles({ hash });
        setTracks(data.tracks);
        setEmbedded(data.embedded);
        setBundled(data.bundled);
        if (!userPickedTabRef.current) {
          const hasLocal =
            data.tracks.length > 0 || data.embedded.length > 0 || data.bundled.length > 0;
          setTab(hasLocal ? "tracks" : "online");
        }
        if (autoSelect) {
          const target = selectedId !== undefined ? selectedId : preferred;
          if (target && data.tracks.some((t) => t.id === target)) {
            await selectTrack(target, data.tracks, offset);
          }
        }
      } catch (err) {
        setStatusError(true);
        setStatus(errMessage(err));
      }
    },
    [hash, offset, preferred, selectTrack],
  );

  useEffect(() => {
    if (!video) return;
    userPickedTabRef.current = false;
    void refresh();
    return () => {
      clearTrack();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hash, video]);

  useEffect(() => {
    if (!video) return;
    const handleMeta = () => {
      if (trackElRef.current?.track) {
        trackElRef.current.track.mode = nativeFsRef.current ? "showing" : "hidden";
        applyDelay(trackElRef.current.track, offset);
      } else if (preferred && tracks.some((t) => t.id === preferred)) {
        void selectTrack(preferred, tracks, offset);
      }
    };
    video.addEventListener("loadedmetadata", handleMeta);
    return () => video.removeEventListener("loadedmetadata", handleMeta);
  }, [video, preferred, tracks, offset, applyDelay, selectTrack]);

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
    setStatus("Searching YIFY Subtitles…");
    setResults([]);
    try {
      const res = await client.searchSubtitles({ hash, language });
      setResults(res.results);
      setStatus(
        res.results.length
          ? "Pick a release matching your video for best timing."
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
        label: params.label || "",
        index: params.index || 0,
        data: params.data || new Uint8Array(),
        filename: params.filename || "",
      });
      if (res.track) {
        await refresh(res.track.id, true);
        if (params.action === "download" && params.id) {
          setResults((prev) => prev.filter((r) => r.id !== params.id));
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
                applyDelay(trackElRef.current?.track, next);
              }}
            />
          </label>
        </div>

        {embedded.length > 0 && (
          <div className="sub-section">
            <div className="sub-section-head">
              <h3>Built-in subtitles</h3>
              <Badge className="mono">{embedded.length}</Badge>
            </div>
            <div className="sub-results">
              {embedded.map((item) => {
                const isExtracting = extractingStream === item.streamIndex;
                return (
                  <div key={item.streamIndex} className="sub-row">
                    <div className="sub-row-info">
                      <span>{item.label}</span>
                      <div className="sub-badges">
                        <small>{item.codec}</small>
                        {item.isDefault && <Badge tone="accent">Default</Badge>}
                        {item.hearingImpaired && <Badge>SDH</Badge>}
                        {item.forced && <Badge>Forced</Badge>}
                      </div>
                    </div>
                    <Button
                      variant="secondary"
                      size="sm"
                      disabled={busy}
                      onClick={() =>
                        void handleAdd({
                          action: "embedded",
                          index: item.streamIndex,
                          label: item.label,
                          lang:
                            item.language && item.language !== "und"
                              ? item.language
                              : language,
                        })
                      }
                    >
                      {isExtracting ? "Extracting…" : "Use"}
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
            <div key={match.id} className="sub-row">
              <div className="sub-row-info">
                <span>{match.label}</span>
                <small>
                  Rating {match.rating}
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
