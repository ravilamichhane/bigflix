import { useEffect, useState, type FormEvent } from "react";
import { client, errMessage, formatBytes, setToken } from "../api";
import type { GetSettingsResponse, IndexerSetting } from "../gen/bigflix/v1/bigflix_pb";
import type { NavigateFn } from "../types";
import { Badge, Button, EmptyState } from "./ui";

interface SettingsPageProps {
  navigate: NavigateFn;
  onLock: () => void;
}

interface BindingForm {
  selected: boolean;
  mode: "fixed" | "dynamic";
  port: number;
}

interface IndexerForm {
  id: string;
  name: string;
  enabled: boolean;
  baseUrl: string;
  health: string;
  lastError: string;
  avgLatencyMs: number;
}

export function SettingsPage({ navigate, onLock }: SettingsPageProps) {
  const [data, setData] = useState<GetSettingsResponse | null>(null);
  const [bindings, setBindings] = useState<Record<string, BindingForm>>({});
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [restarting, setRestarting] = useState(false);
  const [savingNet, setSavingNet] = useState(false);

  // Access lock token
  const [newToken, setNewToken] = useState("");
  const [tokenMsg, setTokenMsg] = useState("");
  const [tokenErr, setTokenErr] = useState("");
  const [savingToken, setSavingToken] = useState(false);

  // TMDB
  const [tmdbToken, setTmdbToken] = useState("");
  const [savingTmdb, setSavingTmdb] = useState(false);
  const [tmdbMsg, setTmdbMsg] = useState("");

  // Playback prefs
  const [targetQuality, setTargetQuality] = useState("1080p");
  const [preferDirectPlay, setPreferDirectPlay] = useState(true);
  const [preferredLanguage, setPreferredLanguage] = useState("original");
  const [autoEvict, setAutoEvict] = useState(true);
  const [savingPrefs, setSavingPrefs] = useState(false);
  const [prefsMsg, setPrefsMsg] = useState("");

  // Built-in Indexers + Torznab
  const [indexers, setIndexers] = useState<IndexerForm[]>([]);
  const [torznabEnabled, setTorznabEnabled] = useState(false);
  const [torznabUrl, setTorznabUrl] = useState("");
  const [torznabKey, setTorznabKey] = useState("");
  const [savingIndexers, setSavingIndexers] = useState(false);
  const [indexersMsg, setIndexersMsg] = useState("");

  // Local CPU AI Classifier
  const [aiEnabled, setAiEnabled] = useState(false);
  const [aiUrl, setAiUrl] = useState("http://llama-server:8080/v1");
  const [aiModel, setAiModel] = useState("Qwen3.5-0.8B");
  const [aiKey, setAiKey] = useState("");
  const [aiTimeout, setAiTimeout] = useState(30);
  const [savingAi, setSavingAi] = useState(false);
  const [testingAi, setTestingAi] = useState(false);
  const [aiMsg, setAiMsg] = useState("");
  const [aiErr, setAiErr] = useState("");

  // Subtitles API keys
  const [subdlKey, setSubdlKey] = useState("");
  const [osKey, setOsKey] = useState("");
  const [savingSubs, setSavingSubs] = useState(false);
  const [subsMsg, setSubsMsg] = useState("");

  async function loadSettings() {
    setLoading(true);
    setError("");
    try {
      const res = await client.getSettings({});
      setData(res);

      const map: Record<string, BindingForm> = {};
      for (const item of res.interfaces) {
        map[item.name] = {
          selected: item.selected,
          mode: item.port !== 0 ? "fixed" : "dynamic",
          port: item.port || 51413,
        };
      }
      setBindings(map);

      setIndexers(
        res.indexers.map((ix: IndexerSetting) => ({
          id: ix.id,
          name: ix.name,
          enabled: ix.enabled,
          baseUrl: ix.baseUrl,
          health: ix.health,
          lastError: ix.lastError,
          avgLatencyMs: ix.avgLatencyMs,
        })),
      );
      if (res.torznab) {
        setTorznabEnabled(res.torznab.enabled);
        setTorznabUrl(res.torznab.url);
      }
      if (res.playback) {
        setTargetQuality(res.playback.targetQuality || "1080p");
        setPreferDirectPlay(res.playback.preferDirectPlay);
        setPreferredLanguage(res.playback.preferredLanguage || "original");
        setAutoEvict(res.playback.autoEvict);
      }
      if (res.ai) {
        setAiEnabled(res.ai.enabled);
        setAiUrl(res.ai.apiUrl || "http://llama-server:8080/v1");
        setAiModel(res.ai.model || "Qwen3.5-0.8B");
        setAiTimeout(res.ai.timeoutSec || 30);
      }
    } catch (err) {
      setError(errMessage(err));
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    void loadSettings();
  }, []);

  async function handleSaveToken(e: FormEvent) {
    e.preventDefault();
    const trimmed = newToken.trim();
    if (trimmed.length < 4) {
      setTokenErr("Token must be at least 4 characters.");
      setTokenMsg("");
      return;
    }
    setSavingToken(true);
    setTokenErr("");
    setTokenMsg("");
    try {
      await client.updateAuthToken({ newToken: trimmed });
      setToken(trimmed);
      setNewToken("");
      setTokenMsg("Access token updated.");
    } catch (err) {
      setTokenErr(errMessage(err));
    } finally {
      setSavingToken(false);
    }
  }

  async function handleSaveTmdb(e: FormEvent) {
    e.preventDefault();
    setSavingTmdb(true);
    setTmdbMsg("");
    setError("");
    try {
      await client.saveAppSettings({ tmdbReadToken: tmdbToken.trim() });
      setTmdbToken("");
      setTmdbMsg("TMDB token saved.");
      await loadSettings();
    } catch (err) {
      setError(errMessage(err));
    } finally {
      setSavingTmdb(false);
    }
  }

  async function handleSavePrefs(e: FormEvent) {
    e.preventDefault();
    setSavingPrefs(true);
    setPrefsMsg("");
    setError("");
    try {
      await client.saveAppSettings({
        playback: {
          targetQuality,
          preferDirectPlay,
          preferredLanguage,
          autoEvict,
        },
      });
      setPrefsMsg("Playback preferences saved.");
    } catch (err) {
      setError(errMessage(err));
    } finally {
      setSavingPrefs(false);
    }
  }

  async function handleSaveIndexers(e: FormEvent) {
    e.preventDefault();
    setSavingIndexers(true);
    setIndexersMsg("");
    setError("");
    try {
      await client.saveAppSettings({
        indexers: indexers.map((ix) => ({
          id: ix.id,
          name: ix.name,
          enabled: ix.enabled,
          baseUrl: ix.baseUrl,
        })),
        torznab: {
          enabled: torznabEnabled,
          url: torznabUrl,
          apiKey: torznabKey,
        },
      });
      setTorznabKey("");
      setIndexersMsg("Indexer configuration saved.");
      await loadSettings();
    } catch (err) {
      setError(errMessage(err));
    } finally {
      setSavingIndexers(false);
    }
  }

  async function handleSaveAi(e: FormEvent) {
    e.preventDefault();
    setSavingAi(true);
    setAiMsg("");
    setAiErr("");
    try {
      await client.saveAppSettings({
        ai: {
          enabled: aiEnabled,
          apiUrl: aiUrl,
          model: aiModel,
          apiKey: aiKey,
          timeoutSec: aiTimeout,
        },
      });
      setAiKey("");
      setAiMsg("AI classifier settings saved.");
      await loadSettings();
    } catch (err) {
      setAiErr(errMessage(err));
    } finally {
      setSavingAi(false);
    }
  }

  async function handleTestAi() {
    setTestingAi(true);
    setAiMsg("");
    setAiErr("");
    try {
      const res = await client.testAIConnection({
        apiUrl: aiUrl,
        model: aiModel,
        apiKey: aiKey,
      });
      if (res.ok) {
        setAiMsg(res.message || `Connected (${res.latencyMs}ms)`);
      } else {
        setAiErr(res.message || "AI connection failed");
      }
    } catch (err) {
      setAiErr(errMessage(err));
    } finally {
      setTestingAi(false);
    }
  }

  async function handleSaveSubs(e: FormEvent) {
    e.preventDefault();
    setSavingSubs(true);
    setSubsMsg("");
    setError("");
    try {
      await client.saveAppSettings({
        subtitles: {
          subdlApiKey: subdlKey,
          opensubtitlesApiKey: osKey,
        },
      });
      setSubdlKey("");
      setOsKey("");
      setSubsMsg("Subtitle provider keys saved.");
      await loadSettings();
    } catch (err) {
      setError(errMessage(err));
    } finally {
      setSavingSubs(false);
    }
  }

  async function handleSaveNetwork(e: FormEvent) {
    e.preventDefault();
    if (!data) return;
    setSavingNet(true);
    setError("");
    const selectedBindings = data.interfaces
      .filter((iface) => bindings[iface.name]?.selected)
      .map((iface) => {
        const b = bindings[iface.name];
        return {
          name: iface.name,
          port: b.mode === "fixed" ? Number(b.port) : 0,
        };
      });

    try {
      await client.saveNetworkSettings({
        interfaceSettings: true,
        bindings: selectedBindings,
      });
      setRestarting(true);
      window.setTimeout(() => {
        setRestarting(false);
        void loadSettings();
      }, 4000);
    } catch (err) {
      setError(errMessage(err));
    } finally {
      setSavingNet(false);
    }
  }

  if (restarting) {
    return (
      <EmptyState
        title="Applying network settings…"
        description="The client is restarting and will refresh shortly."
        actions={
          <Button
            onClick={() => {
              setRestarting(false);
              void loadSettings();
            }}
          >
            Reload
          </Button>
        }
      />
    );
  }

  return (
    <section className="settings-wrap">
      <div className="page-title-row" style={{ marginBottom: 0 }}>
        <h1>Settings</h1>
      </div>

      {error && (
        <div className="ds-alert ds-alert-warn" role="alert">
          {error}
        </div>
      )}

      <div className="settings-grid">
        {/* TMDB Catalog */}
        <article className="ds-card settings-card">
          <div className="settings-card-head">
            <h2>TMDB Catalog</h2>
            <Badge tone={data?.hasTmdb ? "ok" : "warn"}>
              {data?.hasTmdb ? "Connected" : "Required"}
            </Badge>
          </div>
          <p className="muted">
            Powers unified Movies & TV Series discovery, seasons, episodes, and IMDb matching.
          </p>
          <form onSubmit={handleSaveTmdb} className="settings-form">
            <div className="token-row">
              <input
                type="password"
                className="ds-input"
                aria-label="TMDB Read Access Token"
                placeholder={data?.hasTmdb ? "Replace TMDB Read Token…" : "Paste TMDB Read Token…"}
                value={tmdbToken}
                onChange={(e) => setTmdbToken(e.target.value)}
              />
              <Button type="submit" disabled={savingTmdb}>
                {savingTmdb ? "Saving…" : "Save"}
              </Button>
            </div>
            {tmdbMsg && <div className="ds-alert ds-alert-ok">{tmdbMsg}</div>}
          </form>
          <div className="settings-foot">
            <a
              className="ds-link"
              href="https://www.themoviedb.org/settings/api"
              target="_blank"
              rel="noreferrer"
            >
              Get TMDB API Read Token ↗
            </a>
            <a
              className="ds-link"
              href="/"
              onClick={(e) => {
                e.preventDefault();
                navigate("/");
              }}
            >
              Discover →
            </a>
          </div>
        </article>

        {/* Playback & Storage Preferences */}
        <article className="ds-card settings-card">
          <div className="settings-card-head">
            <h2>Playback & Cache</h2>
            {data && data.storageBudgetBytes > 0n && (
              <Badge className="mono">
                {formatBytes(data.storageUsedBytes)} / {formatBytes(data.storageBudgetBytes)}
              </Badge>
            )}
          </div>
          <form onSubmit={handleSavePrefs} className="settings-form">
            <div className="settings-row-2">
              <label>
                Target quality
                <select
                  className="ds-select"
                  value={targetQuality}
                  onChange={(e) => setTargetQuality(e.target.value)}
                >
                  <option value="1080p">1080p Full HD (Recommended)</option>
                  <option value="720p">720p HD (Fastest start)</option>
                  <option value="2160p">2160p 4K</option>
                </select>
              </label>
              <label>
                Audio language
                <select
                  className="ds-select"
                  value={preferredLanguage}
                  onChange={(e) => setPreferredLanguage(e.target.value)}
                >
                  <option value="original">Original audio</option>
                  <option value="en">English</option>
                  <option value="hi">Hindi</option>
                  <option value="ta">Tamil</option>
                  <option value="te">Telugu</option>
                  <option value="kn">Kannada</option>
                  <option value="ml">Malayalam</option>
                  <option value="ne">Nepali</option>
                  <option value="ja">Japanese</option>
                  <option value="ko">Korean</option>
                  <option value="es">Spanish</option>
                  <option value="fr">French</option>
                  <option value="de">German</option>
                </select>
              </label>
            </div>
            <label className="iface-check">
              <input
                type="checkbox"
                checked={preferDirectPlay}
                onChange={(e) => setPreferDirectPlay(e.target.checked)}
              />
              <span>Prefer browser-friendly H.264/AAC (low VPS CPU)</span>
            </label>
            <label className="iface-check">
              <input
                type="checkbox"
                checked={autoEvict}
                onChange={(e) => setAutoEvict(e.target.checked)}
              />
              <span>Auto-evict oldest un-kept downloads when budget fills</span>
            </label>
            <div>
              <Button type="submit" size="sm" disabled={savingPrefs}>
                {savingPrefs ? "Saving…" : "Save preferences"}
              </Button>
            </div>
            {prefsMsg && <div className="ds-alert ds-alert-ok">{prefsMsg}</div>}
          </form>
        </article>

        {/* Local CPU AI Classifier */}
        <article className="ds-card settings-card">
          <div className="settings-card-head">
            <h2>Local CPU AI Classifier</h2>
            <Badge tone={aiEnabled ? "ok" : "neutral"}>
              {aiEnabled ? "Enabled" : "Deterministic only"}
            </Badge>
          </div>
          <p className="muted">
            Connects to local <code>llama-server</code> (e.g. Qwen3.5-0.8B) or Ollama to rank top
            candidates and map ambiguous season-pack files. Falls back automatically if offline.
          </p>
          <form onSubmit={handleSaveAi} className="settings-form">
            <label className="iface-check">
              <input
                type="checkbox"
                checked={aiEnabled}
                onChange={(e) => setAiEnabled(e.target.checked)}
              />
              <span>Enable AI source ranking & file mapping</span>
            </label>
            <label>
              OpenAI-compatible API URL
              <input
                className="ds-input mono"
                value={aiUrl}
                placeholder="http://llama-server:8080/v1"
                onChange={(e) => setAiUrl(e.target.value)}
              />
            </label>
            <div className="settings-row-2">
              <label>
                Model name
                <input
                  className="ds-input mono"
                  value={aiModel}
                  placeholder="Qwen3.5-0.8B"
                  onChange={(e) => setAiModel(e.target.value)}
                />
              </label>
              <label>
                Timeout (sec)
                <input
                  className="ds-input mono"
                  type="number"
                  min={2}
                  max={60}
                  value={aiTimeout}
                  onChange={(e) => setAiTimeout(Number(e.target.value) || 12)}
                />
              </label>
            </div>
            <label>
              API Key (optional)
              <input
                type="password"
                className="ds-input"
                placeholder={data?.ai?.hasApiKey ? "Saved (leave blank to keep)" : "Not required for llama-server"}
                value={aiKey}
                onChange={(e) => setAiKey(e.target.value)}
              />
            </label>
            <div className="settings-btn-row">
              <Button type="submit" size="sm" disabled={savingAi}>
                {savingAi ? "Saving…" : "Save AI settings"}
              </Button>
              <Button
                type="button"
                variant="secondary"
                size="sm"
                disabled={testingAi}
                onClick={() => void handleTestAi()}
              >
                {testingAi ? "Testing…" : "Test connection"}
              </Button>
            </div>
            {aiErr && <div className="ds-alert ds-alert-danger">{aiErr}</div>}
            {aiMsg && <div className="ds-alert ds-alert-ok">{aiMsg}</div>}
          </form>
        </article>

        {/* Online Subtitles */}
        <article className="ds-card settings-card">
          <div className="settings-card-head">
            <h2>Online TV & Movie Subtitles</h2>
            <Badge
              tone={
                data?.subtitles?.hasSubdlKey || data?.subtitles?.hasOpensubtitlesKey
                  ? "ok"
                  : "neutral"
              }
            >
              {data?.subtitles?.hasSubdlKey
                ? "SubDL"
                : data?.subtitles?.hasOpensubtitlesKey
                  ? "OpenSubtitles"
                  : "Built-in + YIFY"}
            </Badge>
          </div>
          <p className="muted">
            Built-in MKV subtitles, torrent SRT/VTT, and uploads always work. Add a free SubDL or
            OpenSubtitles API key to enable online TV episode search.
          </p>
          <form onSubmit={handleSaveSubs} className="settings-form">
            <label>
              SubDL API Key
              <input
                type="password"
                className="ds-input"
                placeholder={
                  data?.subtitles?.hasSubdlKey
                    ? "Saved (paste to replace)"
                    : "Optional SubDL API key"
                }
                value={subdlKey}
                onChange={(e) => setSubdlKey(e.target.value)}
              />
            </label>
            <label>
              OpenSubtitles API Key
              <input
                type="password"
                className="ds-input"
                placeholder={
                  data?.subtitles?.hasOpensubtitlesKey
                    ? "Saved (paste to replace)"
                    : "Optional OpenSubtitles v1 API key"
                }
                value={osKey}
                onChange={(e) => setOsKey(e.target.value)}
              />
            </label>
            <div>
              <Button type="submit" size="sm" disabled={savingSubs}>
                {savingSubs ? "Saving…" : "Save subtitle keys"}
              </Button>
            </div>
            {subsMsg && <div className="ds-alert ds-alert-ok">{subsMsg}</div>}
          </form>
        </article>

        {/* Access Lock */}
        <article className="ds-card settings-card">
          <div className="settings-card-head">
            <h2>Access lock</h2>
            <Badge tone="ok">Protected</Badge>
          </div>
          <p className="muted">Update your access token or lock this browser session.</p>
          <form onSubmit={handleSaveToken} className="settings-form">
            <div className="token-row">
              <input
                type="password"
                className="ds-input"
                aria-label="New access token"
                placeholder="New token (min 4 chars)"
                value={newToken}
                onChange={(e) => setNewToken(e.target.value)}
              />
              <Button type="submit" disabled={savingToken}>
                {savingToken ? "Saving…" : "Update"}
              </Button>
            </div>
            {tokenErr && (
              <div className="ds-alert ds-alert-danger" role="alert">
                {tokenErr}
              </div>
            )}
            {tokenMsg && <div className="ds-alert ds-alert-ok">{tokenMsg}</div>}
          </form>
          <div className="settings-foot">
            <Button variant="secondary" size="sm" onClick={onLock}>
              Lock session
            </Button>
          </div>
        </article>
      </div>

      {/* Built-in Multi-Indexer Aggregator ("Mini-Prowlarr") */}
      {indexers.length > 0 && (
        <form onSubmit={handleSaveIndexers} className="ds-card settings-indexers-card">
          <div className="section-bar">
            <div>
              <h2>Built-in Torrent Indexers</h2>
              <p className="muted" style={{ margin: "4px 0 0" }}>
                Queried in parallel and deduplicated automatically. Edit mirror URLs if a domain
                rotates.
              </p>
            </div>
          </div>

          <div className="indexer-list">
            {indexers.map((ix, i) => (
              <div key={ix.id} className="indexer-row">
                <div className="indexer-row-head">
                  <label className="iface-check">
                    <input
                      type="checkbox"
                      checked={ix.enabled}
                      onChange={(e) =>
                        setIndexers((prev) =>
                          prev.map((item, idx) =>
                            idx === i ? { ...item, enabled: e.target.checked } : item,
                          ),
                        )
                      }
                    />
                    <span>{ix.name}</span>
                  </label>
                  <div className="indexer-badges">
                    {ix.avgLatencyMs > 0 && <span className="muted mono">{ix.avgLatencyMs}ms</span>}
                    <Badge
                      tone={
                        ix.health === "ok"
                          ? "ok"
                          : ix.health === "degraded"
                            ? "warn"
                            : "neutral"
                      }
                    >
                      {ix.health}
                    </Badge>
                  </div>
                </div>
                <input
                  className="ds-input mono"
                  aria-label={`${ix.name} URL`}
                  value={ix.baseUrl}
                  onChange={(e) =>
                    setIndexers((prev) =>
                      prev.map((item, idx) =>
                        idx === i ? { ...item, baseUrl: e.target.value } : item,
                      ),
                    )
                  }
                />
                {ix.lastError && (
                  <small className="muted" style={{ color: "var(--status-warn)" }}>
                    Last error: {ix.lastError}
                  </small>
                )}
              </div>
            ))}
          </div>

          <div className="torznab-box">
            <label className="iface-check">
              <input
                type="checkbox"
                checked={torznabEnabled}
                onChange={(e) => setTorznabEnabled(e.target.checked)}
              />
              <span>Optional custom Torznab endpoint (Prowlarr / Jackett / Private tracker)</span>
            </label>
            <div className="settings-row-2">
              <input
                className="ds-input mono"
                placeholder="http://prowlarr:9696/1/api"
                value={torznabUrl}
                onChange={(e) => setTorznabUrl(e.target.value)}
              />
              <input
                type="password"
                className="ds-input"
                placeholder={
                  data?.torznab?.hasApiKey ? "API key saved (paste to replace)" : "Torznab API key"
                }
                value={torznabKey}
                onChange={(e) => setTorznabKey(e.target.value)}
              />
            </div>
          </div>

          <div className="settings-btn-row">
            <Button type="submit" disabled={savingIndexers}>
              {savingIndexers ? "Saving…" : "Save indexers"}
            </Button>
          </div>
          {indexersMsg && <div className="ds-alert ds-alert-ok">{indexersMsg}</div>}
        </form>
      )}

      {loading && !data ? (
        <p className="muted">Loading network interfaces…</p>
      ) : (
        data?.hasEngine && (
          <form onSubmit={handleSaveNetwork} style={{ display: "grid", gap: 16 }}>
            <div className="section-bar">
              <h2>Network interfaces</h2>
            </div>

            {data.allInterfaces && (
              <div className="ds-alert ds-alert-warn" style={{ margin: 0 }}>
                Using all interfaces on port {data.actualPort}. Save below to bind specific
                interfaces (for example a VPN interface like <code>wg0</code>).
              </div>
            )}
            {data.errors.map((errText, idx) => (
              <div key={idx} className="ds-alert ds-alert-warn" role="alert" style={{ margin: 0 }}>
                {errText}
              </div>
            ))}

            <div className="settings-grid">
              {data.interfaces.map((iface) => {
                const state = bindings[iface.name] || {
                  selected: iface.selected,
                  mode: iface.port !== 0 ? "fixed" : "dynamic",
                  port: iface.port || 51413,
                };
                return (
                  <article key={iface.name} className="ds-card settings-card">
                    <div className="settings-card-head">
                      <label className="iface-check">
                        <input
                          type="checkbox"
                          checked={state.selected}
                          onChange={(e) =>
                            setBindings((prev) => ({
                              ...prev,
                              [iface.name]: { ...state, selected: e.target.checked },
                            }))
                          }
                        />
                        <span>{iface.name}</span>
                      </label>
                      <Badge tone={iface.up ? "ok" : "warn"}>{iface.up ? "Up" : "Down"}</Badge>
                    </div>

                    {iface.addresses.length ? (
                      <div className="iface-addrs">
                        {iface.addresses.map((addr) => (
                          <code key={addr}>{addr}</code>
                        ))}
                      </div>
                    ) : (
                      <p className="muted">No IP address</p>
                    )}

                    <div className="iface-fields">
                      <label>
                        Port mode
                        <select
                          className="ds-select"
                          id={`mode-${iface.name}`}
                          value={state.mode}
                          onChange={(e) =>
                            setBindings((prev) => ({
                              ...prev,
                              [iface.name]: {
                                ...state,
                                mode: e.target.value as "fixed" | "dynamic",
                              },
                            }))
                          }
                        >
                          <option value="fixed">Fixed</option>
                          <option value="dynamic">Dynamic</option>
                        </select>
                      </label>
                      {state.mode === "fixed" && (
                        <label>
                          Port
                          <input
                            className="ds-input mono"
                            id={`port-${iface.name}`}
                            type="number"
                            min={1024}
                            max={65535}
                            value={state.port}
                            onChange={(e) =>
                              setBindings((prev) => ({
                                ...prev,
                                [iface.name]: {
                                  ...state,
                                  port: Number(e.target.value) || 51413,
                                },
                              }))
                            }
                          />
                        </label>
                      )}
                    </div>

                    {iface.listeners.length > 0 && (
                      <p className="muted mono" style={{ fontSize: 11.5 }}>
                        Listening: {iface.listeners.join(", ")}
                      </p>
                    )}
                  </article>
                );
              })}
            </div>

            <div>
              <Button type="submit" disabled={savingNet}>
                {savingNet ? "Saving…" : "Save & restart client"}
              </Button>
            </div>
          </form>
        )
      )}
    </section>
  );
}
