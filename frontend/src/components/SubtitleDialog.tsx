import { useCallback, useEffect, useRef, useState } from "react";
import { client, errMessage } from "../api";
import type {
  BundledSubtitleFile,
  SubtitleSearchResult,
  SubtitleTrack,
} from "../gen/bigflix/v1/bigflix_pb";
import { CloseIcon } from "./Icons";

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
  onCloseFocus?: () => void;
  onActiveChange?: (active: boolean) => void;
}

export function SubtitleDialog({
  hash,
  video,
  openSignal,
  onCloseFocus,
  onActiveChange,
}: SubtitleDialogProps) {
  const dialogRef = useRef<HTMLDialogElement | null>(null);
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const trackElRef = useRef<HTMLTrackElement | null>(null);
  const blobUrlRef = useRef<string>("");
  const originalTimesRef = useRef(new WeakMap<TextTrackCue, [number, number]>());

  const [tab, setTab] = useState<"online" | "saved" | "upload">("online");
  const [language, setLanguage] = useState("en");
  const [tracks, setTracks] = useState<SubtitleTrack[]>([]);
  const [bundled, setBundled] = useState<BundledSubtitleFile[]>([]);
  const [results, setResults] = useState<SubtitleSearchResult[]>([]);
  const [status, setStatus] = useState("");
  const [busy, setBusy] = useState(false);

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
    try {
      localStorage.setItem(prefKey, JSON.stringify({ id: preferred, offset }));
    } catch {
      // Ignore storage errors.
    }
  }, [prefKey, preferred, offset]);

  const applyDelay = useCallback((track: TextTrack | undefined, delayVal: number) => {
    if (!track?.cues) return;
    for (const cue of Array.from(track.cues)) {
      if (!originalTimesRef.current.has(cue)) {
        originalTimesRef.current.set(cue, [cue.startTime, cue.endTime]);
      }
      const pair = originalTimesRef.current.get(cue)!;
      cue.endTime = Math.max(0.001, pair[1] + delayVal);
      cue.startTime = Math.max(0, Math.min(pair[0] + delayVal, cue.endTime - 0.001));
    }
  }, []);

  const clearTrack = useCallback(() => {
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
  }, []);

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
        el.addEventListener("load", () => {
          applyDelay(el.track, delayVal);
          el.track.mode = "showing";
        });
        video.appendChild(el);
        trackElRef.current = el;
        el.track.mode = "showing";
        onActiveChange?.(true);
      } catch (err) {
        onActiveChange?.(false);
        setStatus(errMessage(err));
      }
    },
    [applyDelay, clearTrack, hash, language, onActiveChange, video],
  );

  const refresh = useCallback(
    async (selectedId?: string) => {
      try {
        const data = await client.listSubtitles({ hash });
        setTracks(data.tracks);
        setBundled(data.bundled);
        const target = selectedId !== undefined ? selectedId : preferred;
        if (target && data.tracks.some((t) => t.id === target)) {
          await selectTrack(target, data.tracks, offset);
        }
      } catch (err) {
        setStatus(errMessage(err));
      }
    },
    [hash, offset, preferred, selectTrack],
  );

  useEffect(() => {
    if (!video) return;
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
        trackElRef.current.track.mode = "showing";
        applyDelay(trackElRef.current.track, offset);
      } else if (preferred && tracks.some((t) => t.id === preferred)) {
        void selectTrack(preferred, tracks, offset);
      }
    };
    video.addEventListener("loadedmetadata", handleMeta);
    return () => video.removeEventListener("loadedmetadata", handleMeta);
  }, [video, preferred, tracks, offset, applyDelay, selectTrack]);

  useEffect(() => {
    if (openSignal > 0 && dialogRef.current && !dialogRef.current.open) {
      dialogRef.current.showModal();
    }
  }, [openSignal]);

  async function handleSearch() {
    setBusy(true);
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
  }) {
    setBusy(true);
    setStatus("Loading subtitle…");
    try {
      const res = await client.addSubtitle({
        hash,
        action: params.action,
        language,
        id: params.id || 0,
        label: params.label || "",
        index: params.index || 0,
        data: params.data || new Uint8Array(),
        filename: params.filename || "",
      });
      if (res.track) {
        await refresh(res.track.id);
        if (params.action === "download" && params.id) {
          setResults((prev) => prev.filter((r) => r.id !== params.id));
        }
        setStatus(`Active: ${res.track.label}`);
      }
    } catch (err) {
      setStatus(errMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function handleUpload() {
    const file = fileInputRef.current?.files?.[0];
    if (!file) {
      setStatus("Choose an SRT or VTT file first.");
      return;
    }
    if (file.size > 2 * 1024 * 1024) {
      setStatus("Subtitle must be at most 2 MB.");
      return;
    }
    const buf = new Uint8Array(await file.arrayBuffer());
    await handleAdd({ action: "upload", data: buf, filename: file.name });
  }

  return (
    <dialog
      ref={dialogRef}
      id="subtitle-dialog"
      className="subtitle-panel"
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
      <div className="dialog-head">
        <h2 id="subtitle-title">Subtitles</h2>
        <button
          type="button"
          className="icon-btn"
          aria-label="Close subtitles"
          onClick={() => dialogRef.current?.close()}
        >
          <CloseIcon size={18} />
        </button>
      </div>

      <div className="seg-tabs" role="group" aria-label="Subtitle sources">
        <button
          type="button"
          aria-pressed={tab === "online"}
          onClick={() => setTab("online")}
        >
          Find online
        </button>
        <button
          type="button"
          aria-pressed={tab === "saved"}
          onClick={() => setTab("saved")}
        >
          Saved &amp; timing
        </button>
        <button
          type="button"
          aria-pressed={tab === "upload"}
          onClick={() => setTab("upload")}
        >
          Upload
        </button>
      </div>

      <div hidden={tab !== "online"}>
        <div className="subtitle-controls">
          <label>
            Language
            <select value={language} onChange={(e) => setLanguage(e.target.value)}>
              {LANGUAGES.map((l) => (
                <option key={l.value} value={l.value}>
                  {l.label}
                </option>
              ))}
            </select>
          </label>
          <button
            type="button"
            className="button"
            disabled={busy}
            onClick={() => void handleSearch()}
          >
            {busy ? "Searching…" : "Search"}
          </button>
        </div>
        <div className="subtitle-list">
          {results.map((match) => (
            <div key={match.id} className="subtitle-result">
              <div className="subtitle-result-info">
                <span>{match.label}</span>
                <small>
                  Rating {match.rating}
                  {match.hearingImpaired ? " · SDH" : ""}
                </small>
              </div>
              <div className="subtitle-result-actions">
                <button
                  type="button"
                  className="button secondary"
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
                </button>
              </div>
            </div>
          ))}
        </div>
      </div>

      <div hidden={tab !== "saved"}>
        <div className="subtitle-controls">
          <label>
            Active track
            <select
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
          <label className="offset-field">
            Timing offset (s)
            <input
              id="subtitle-delay"
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
        <p className="muted">Positive shows subtitles later; negative earlier.</p>
        {bundled.length > 0 && (
          <div id="subtitle-bundled">
            {bundled.map((file) => (
              <button
                key={file.index}
                type="button"
                className="button secondary"
                disabled={busy}
                onClick={() => void handleAdd({ action: "bundled", index: file.index })}
              >
                Use {file.name}
              </button>
            ))}
          </div>
        )}
      </div>

      <div hidden={tab !== "upload"}>
        <div className="subtitle-controls">
          <label>
            SRT or WebVTT file (max 2 MB)
            <input ref={fileInputRef} type="file" accept=".srt,.vtt,text/vtt" />
          </label>
          <button
            type="button"
            className="button"
            disabled={busy}
            onClick={() => void handleUpload()}
          >
            Upload
          </button>
        </div>
      </div>

      {status && (
        <p id="subtitle-status" role="status" aria-live="polite">
          {status}
        </p>
      )}
    </dialog>
  );
}
