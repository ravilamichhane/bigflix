import { useEffect, useState, type FormEvent } from "react";
import { client, errMessage, setToken } from "../api";
import type { GetSettingsResponse } from "../gen/bigflix/v1/bigflix_pb";
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

export function SettingsPage({ navigate, onLock }: SettingsPageProps) {
  const [data, setData] = useState<GetSettingsResponse | null>(null);
  const [bindings, setBindings] = useState<Record<string, BindingForm>>({});
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [restarting, setRestarting] = useState(false);
  const [savingNet, setSavingNet] = useState(false);

  const [newToken, setNewToken] = useState("");
  const [tokenMsg, setTokenMsg] = useState("");
  const [tokenErr, setTokenErr] = useState("");
  const [savingToken, setSavingToken] = useState(false);

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
        <article className="ds-card settings-card">
          <div className="settings-card-head">
            <h2>Access lock</h2>
            <Badge tone="ok">Protected</Badge>
          </div>
          <p className="muted">Update your access token or lock this browser session.</p>
          <form onSubmit={handleSaveToken}>
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

        <article className="ds-card settings-card">
          <div className="settings-card-head">
            <h2>Catalog</h2>
            <Badge tone="ok">Active</Badge>
          </div>
          <p className="muted">YTS search and metadata cached locally in SQLite.</p>
          <div className="settings-foot">
            <a
              className="ds-link"
              href="/"
              onClick={(e) => {
                e.preventDefault();
                navigate("/");
              }}
            >
              Browse catalog →
            </a>
          </div>
        </article>

        <article className="ds-card settings-card">
          <div className="settings-card-head">
            <h2>TMDB Discovery</h2>
            <Badge tone={data?.hasTmdb ? "ok" : "warn"}>
              {data?.hasTmdb ? "Configured" : "Optional"}
            </Badge>
          </div>
          <p className="muted">
            Trending and New use TMDB. Set <code>TMDB_READ_TOKEN</code> in the server environment.
          </p>
          <div className="settings-foot">
            <a
              className="ds-link"
              href="https://www.themoviedb.org/settings/api"
              target="_blank"
              rel="noreferrer"
            >
              TMDB API settings ↗
            </a>
          </div>
        </article>
      </div>

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
                interfaces.
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
