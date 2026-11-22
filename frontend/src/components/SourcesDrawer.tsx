import { useCallback, useEffect, useMemo, useState } from "react";
import { client, errMessage } from "../api";
import type { ResolveSourcesResponse, SourceCandidate } from "../gen/bigflix/v1/bigflix_pb";
import { CheckIcon, CloseIcon, CopyIcon, DownloadIcon, PlayIcon, RefreshIcon } from "./Icons";
import { Badge, Button, EmptyState } from "./ui";

function normalizeLangChip(tag: string): string {
  return tag.replace(/\s+Dub$/i, "").trim();
}

interface SourcesDrawerProps {
  mediaType: "movie" | "episode";
  tmdbId: number;
  seasonNumber?: number;
  episodeNumber?: number;
  activeHash?: string;
  title?: string;
  onSelect: (hash: string, keep: boolean) => Promise<void> | void;
  onClose?: () => void;
  inline?: boolean;
}

export function SourcesDrawer({
  mediaType,
  tmdbId,
  seasonNumber = 0,
  episodeNumber = 0,
  activeHash = "",
  title,
  onSelect,
  onClose,
  inline = false,
}: SourcesDrawerProps) {
  const [data, setData] = useState<ResolveSourcesResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [busyHash, setBusyHash] = useState("");
  const [copiedHash, setCopiedHash] = useState("");
  const [langFilter, setLangFilter] = useState("");

  const load = useCallback(
    async (forceRefresh = false) => {
      setLoading(true);
      setError("");
      setLangFilter("");
      try {
        const res = await client.resolveSources({
          mediaType,
          tmdbId,
          seasonNumber,
          episodeNumber,
          forceRefresh,
        });
        setData(res);
      } catch (err) {
        setError(errMessage(err));
      } finally {
        setLoading(false);
      }
    },
    [mediaType, tmdbId, seasonNumber, episodeNumber],
  );

  useEffect(() => {
    void load(false);
  }, [load]);

  async function handlePick(c: SourceCandidate, keep: boolean) {
    setBusyHash(c.hash);
    setError("");
    try {
      await onSelect(c.hash, keep);
    } catch (err) {
      setError(errMessage(err));
      setBusyHash("");
    }
  }

  async function copyHash(hash: string) {
    try {
      await navigator.clipboard.writeText(hash);
      setCopiedHash(hash);
      window.setTimeout(() => setCopiedHash(""), 2000);
    } catch {
      // Ignore clipboard failure
    }
  }

  const languageChips = useMemo(() => {
    const counts = new Map<string, number>();
    for (const c of data?.candidates ?? []) {
      const seen = new Set<string>();
      for (const raw of c.languages) {
        const chip = normalizeLangChip(raw);
        if (!chip || seen.has(chip)) continue;
        seen.add(chip);
        counts.set(chip, (counts.get(chip) ?? 0) + 1);
      }
    }
    return Array.from(counts.entries())
      .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
      .map(([label, count]) => ({ label, count }));
  }, [data]);

  const visibleCandidates = useMemo(() => {
    const list = data?.candidates ?? [];
    if (!langFilter) return list.slice(0, 20);
    const want = langFilter.toLowerCase();
    return list
      .filter((c) => c.languages.some((l) => normalizeLangChip(l).toLowerCase() === want))
      .slice(0, 20);
  }, [data, langFilter]);

  const content = (
    <div className="sources-drawer-body">
      <div className="sources-drawer-head">
        <div>
          <h3>{title || "Available sources"}</h3>
          <div className="sources-drawer-meta">
            {data?.rankedBy && (
              <Badge tone={data.rankedBy === "ai" ? "accent" : "neutral"}>
                {data.rankedBy === "ai" ? "AI ranked" : "Auto ranked"}
              </Badge>
            )}
            {data?.providersQueried?.length ? (
              <span className="muted">
                Indexers: {data.providersQueried.join(", ")}
              </span>
            ) : null}
          </div>
        </div>
        <div className="sources-drawer-controls">
          <Button
            variant="ghost"
            size="sm"
            disabled={loading}
            onClick={() => void load(true)}
            title="Re-query indexers"
          >
            <RefreshIcon size={14} /> Refresh
          </Button>
          {onClose && (
            <Button variant="ghost" size="icon-sm" aria-label="Close sources" onClick={onClose}>
              <CloseIcon size={16} />
            </Button>
          )}
        </div>
      </div>

      {data?.aiNote && <div className="ds-alert">{data.aiNote}</div>}
      {error && (
        <div className="ds-alert ds-alert-danger" role="alert">
          {error}
        </div>
      )}

      {!loading && languageChips.length > 0 && (
        <div className="sources-lang-bar" role="group" aria-label="Filter sources by audio language">
          <span className="sources-lang-label muted">Audio:</span>
          <Button
            size="sm"
            variant={langFilter === "" ? "primary" : "secondary"}
            onClick={() => setLangFilter("")}
          >
            All ({data?.candidates.length ?? 0})
          </Button>
          {languageChips.map((chip) => (
            <Button
              key={chip.label}
              size="sm"
              variant={langFilter === chip.label ? "primary" : "secondary"}
              onClick={() => setLangFilter(langFilter === chip.label ? "" : chip.label)}
            >
              {chip.label} ({chip.count})
            </Button>
          ))}
        </div>
      )}

      {loading ? (
        <EmptyState title="Searching indexers & ranking sources…" />
      ) : visibleCandidates.length ? (
        <div className="source-list">
          {visibleCandidates.map((c, idx) => {
            const health =
              c.seeds >= 30 ? "health-good" : c.seeds >= 8 ? "health-mid" : "health-low";
            const isActive = Boolean(
              activeHash && activeHash.toLowerCase() === c.hash.toLowerCase(),
            );
            return (
              <article key={c.hash} className={`source-row ${isActive ? "is-active" : ""}`}>
                <div className="source-row-main">
                  <div className="source-row-badges">
                    <span className="mono source-rank">#{idx + 1}</span>
                    <strong>{c.quality || "SD"}</strong>
                    {c.codec && <Badge>{c.codec.toUpperCase()}</Badge>}
                    {c.sourceType && <Badge>{c.sourceType}</Badge>}
                    {c.languages.map((lang) => (
                      <Badge key={lang} tone="accent">
                        {lang}
                      </Badge>
                    ))}
                    {c.isSeasonPack && <Badge tone="accent">Season Pack</Badge>}
                    {c.browserFriendly && <Badge tone="ok">Direct-play</Badge>}
                    {c.provider && <Badge>{c.provider}</Badge>}
                    {isActive && <Badge tone="ok">Current</Badge>}
                  </div>
                  <div className="source-row-title" title={c.title}>
                    {c.title}
                  </div>
                  <div className="source-specs">
                    {c.size && <span>{c.size}</span>}
                    <span>
                      <span className={`health-dot ${health}`} />
                      {c.seeds} seeds · {c.peers} peers
                    </span>
                    {c.reason && <span className="muted">{c.reason}</span>}
                  </div>
                </div>

                <div className="source-row-actions">
                  <Button
                    size="sm"
                    disabled={busyHash === c.hash || isActive}
                    onClick={() => void handlePick(c, false)}
                  >
                    <PlayIcon size={13} />
                    {busyHash === c.hash ? "Starting…" : isActive ? "Playing" : "Play"}
                  </Button>
                  {!isActive && (
                    <Button
                      variant="secondary"
                      size="sm"
                      disabled={busyHash === c.hash}
                      onClick={() => void handlePick(c, true)}
                      title="Keep offline"
                    >
                      <DownloadIcon size={13} />
                    </Button>
                  )}
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    title="Copy info hash"
                    aria-label="Copy info hash"
                    onClick={() => void copyHash(c.hash)}
                  >
                    {copiedHash === c.hash ? <CheckIcon size={13} /> : <CopyIcon size={13} />}
                  </Button>
                </div>
              </article>
            );
          })}
        </div>
      ) : (
        <EmptyState
          title="No playable sources found"
          description="Try Refresh or check your enabled built-in indexers in Settings."
        />
      )}
    </div>
  );

  if (inline) {
    return <div className="ds-card sources-drawer-inline">{content}</div>;
  }

  return (
    <div
      className="sources-modal-backdrop"
      role="dialog"
      aria-modal="true"
      aria-label={title || "Available sources"}
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose?.();
      }}
    >
      <div className="ds-card sources-modal-panel">{content}</div>
    </div>
  );
}
