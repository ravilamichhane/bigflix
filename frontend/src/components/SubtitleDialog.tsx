import { useCallback, useEffect, useRef, useState } from "react";
import { client, errMessage } from "../api";
import type {
  BundledSubtitleFile,
  SubtitleSearchResult,
  SubtitleTrack,
} from "../gen/bigflix/v1/bigflix_pb";
import type { VjsPlayer } from "../types";

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
  player: VjsPlayer | null;
  openSignal: number;
  onCloseFocus?: () => void;
}

export function SubtitleDialog({
  hash,
  player,
  openSignal,
  onCloseFocus,
}: SubtitleDialogProps) {
  const dialogRef = useRef<HTMLDialogElement | null>(null);
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const currentTrackRef = useRef<TextTrack | undefined>(undefined);
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

  const selectTrack = useCallback(
    async (id: string, trackList: SubtitleTrack[], delayVal: number) => {
      setPreferred(id);
      if (currentTrackRef.current && player) {
        try {
          player.removeRemoteTextTrack(currentTrackRef.current);
        } catch {
          // Ignore if player already disposed.
        }
        currentTrackRef.current = undefined;
      }
      if (blobUrlRef.current) {
        URL.revokeObjectURL(blobUrlRef.current);
        blobUrlRef.current = "";
      }
      if (!id || !player) return;

      try {
        const res = await client.getSubtitle({ hash, id });
        const meta = trackList.find((t) => t.id === id);
        const blob = new Blob([res.vtt], { type: "text/vtt" });
        const url = URL.createObjectURL(blob);
        blobUrlRef.current = url;
        const remote = player.addRemoteTextTrack(
          {
            kind: "subtitles",
            src: url,
            label: meta ? `${meta.language} · ${meta.label}` : "Subtitles",
            srclang: meta?.language || language,
            default: true,
          },
          false,
        );
        currentTrackRef.current = remote.track;
        remote.track.mode = "showing";
        remote.addEventListener("load", () => {
          applyDelay(remote.track, delayVal);
          remote.track.mode = "showing";
        });
      } catch (err) {
        setStatus(errMessage(err));
      }
    },
    [applyDelay, hash, language, player],
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
    void refresh();
    return () => {
      if (blobUrlRef.current) {
        URL.revokeObjectURL(blobUrlRef.current);
        blobUrlRef.current = "";
      }
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hash]);

  useEffect(() => {
    if (!player) return;
    const handleMeta = () => {
      if (preferred && tracks.some((t) => t.id === preferred)) {
        void selectTrack(preferred, tracks, offset);
      }
    };
    player.on("loadedmetadata", handleMeta);
  }, [player, preferred, tracks, offset, selectTrack]);

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
          ? "Choose the release matching your video for the best timing."
          : "No new subtitles found for this language. Downloaded tracks are available in Saved & timing.",
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
        setStatus(`Ready: ${res.track.label}. Adjust timing if needed.`);
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
      <div className="section-top">
        <div>
          <div className="eyebrow">MAKE IT YOURS</div>
          <h2 id="subtitle-title">Subtitles</h2>
        </div>
        <button
          type="button"
          className="button secondary"
          aria-label="Close subtitles"
          onClick={() => dialogRef.current?.close()}
        >
          Close ✕
        </button>
      </div>
      <p className="muted">
        Find the right language and release, or bring your own subtitle file.
      </p>

      <div className="subtitle-tabs" role="group" aria-label="Subtitle sources">
        <button
          type="button"
          className="button secondary"
          aria-pressed={tab === "online"}
          onClick={() => setTab("online")}
        >
          Find online
        </button>
        <button
          type="button"
          className="button secondary"
          aria-pressed={tab === "saved"}
          onClick={() => setTab("saved")}
        >
          Saved & timing
        </button>
        <button
          type="button"
          className="button secondary"
          aria-pressed={tab === "upload"}
          onClick={() => setTab("upload")}
        >
          Upload file
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
            className="button secondary"
            disabled={busy}
            onClick={() => void handleSearch()}
          >
            Find subtitles
          </button>
        </div>
        <div>
          {results.map((match) => (
            <div key={match.id} className="subtitle-result">
              <span>
                {match.label} · rating {match.rating}
                {match.hearingImpaired ? " · SDH" : ""}
              </span>
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
                Download & use
              </button>
              {/^\/subtitles\/[a-zA-Z0-9_-]+$/.test(match.page || "") && (
                <a
                  href={`https://yifysubtitles.ch${match.page}`}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="text-link"
                >
                  Open subtitle page ↗
                </a>
              )}
            </div>
          ))}
        </div>
        <p className="muted">
          Powered by YIFY Subtitles. Pick a release matching your video for the best timing.
        </p>
      </div>

      <div hidden={tab !== "saved"}>
        <div className="subtitle-controls">
          <label>
            Saved subtitles
            <select
              value={preferred}
              onChange={(e) => void selectTrack(e.target.value, tracks, offset)}
            >
              <option value="">Subtitles off</option>
              {tracks.map((sub) => (
                <option key={sub.id} value={sub.id}>
                  {sub.language} · {sub.label}
                </option>
              ))}
            </select>
          </label>
          <label>
            Timing offset (seconds)
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
                applyDelay(currentTrackRef.current, next);
              }}
            />
          </label>
        </div>
        <p className="muted">
          Positive offset shows subtitles later; negative shows them earlier. Caption appearance
          is available in the player’s CC menu.
        </p>
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
      </div>

      <div hidden={tab !== "upload"}>
        <div className="subtitle-controls">
          <label>
            Upload SRT / VTT
            <input ref={fileInputRef} type="file" accept=".srt,.vtt,text/vtt" />
          </label>
          <button
            type="button"
            className="button secondary"
            disabled={busy}
            onClick={() => void handleUpload()}
          >
            Upload & use
          </button>
        </div>
        <p className="muted">SRT or WebVTT, up to 2 MB. Saved locally for your next watch.</p>
      </div>

      <p id="subtitle-status" role="status" aria-live="polite">
        {status}
      </p>
    </dialog>
  );
}
