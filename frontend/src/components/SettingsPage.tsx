import { useEffect, useState, type FormEvent } from "react";
import { client, errMessage, setToken } from "../api";
import type { GetSettingsResponse } from "../gen/bigflix/v1/bigflix_pb";
import type { NavigateFn } from "../types";

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
      setTokenMsg("Access token updated and saved.");
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
      <section className="settings-page">
        <h1>Applying network settings…</h1>
        <p>The client is restarting. This page will refresh settings shortly.</p>
        <button
          type="button"
          className="button"
          onClick={() => {
            setRestarting(false);
            void loadSettings();
          }}
        >
          Reload settings
        </button>
      </section>
    );
  }

  return (
    <section className="settings-page">
      <div className="eyebrow">ADMIN SETTINGS</div>
      <h1>Your local setup.</h1>
      <p className="subtitle">
        Manage access lock, catalog configuration, and torrent network bindings.
      </p>

      {error && (
        <p className="notice" role="alert">
          {error}
        </p>
      )}

      <div className="settings-grid">
        <article className="settings-card">
          <span className="pill">PROTECTED</span>
          <h2>Access lock</h2>
          <p>
            Every ConnectRPC API and media stream requires your token. Change the token or lock
            the current browser session immediately.
          </p>
          <form onSubmit={handleSaveToken}>
            <div className="token-row">
              <input
                type="password"
                aria-label="New access token"
                placeholder="New token (min 4 chars)"
                value={newToken}
                onChange={(e) => setNewToken(e.target.value)}
              />
              <button className="button" type="submit" disabled={savingToken}>
                {savingToken ? "Saving…" : "Update token"}
              </button>
            </div>
            {tokenErr && (
              <p className="error-banner" role="alert">
                {tokenErr}
              </p>
            )}
            {tokenMsg && <p className="notice">{tokenMsg}</p>}
          </form>
          <p>
            <button type="button" className="button secondary" onClick={onLock}>
              Lock session now
            </button>
          </p>
        </article>

        <article className="settings-card">
          <span className="pill">AVAILABLE</span>
          <h2>YTS catalog</h2>
          <p>
            Movie search, details, posters, and source variants. Results are saved locally in
            SQLite and refreshed after 15 minutes.
          </p>
          <a
            className="text-link"
            href="/"
            onClick={(e) => {
              e.preventDefault();
              navigate("/");
            }}
          >
            Browse catalog →
          </a>
        </article>

        <article className="settings-card">
          <span className={`pill ${!data?.hasTmdb ? "pending" : ""}`}>
            {data?.hasTmdb ? "CONFIGURED" : "SETUP NEEDED"}
          </span>
          <h2>Worldwide discovery</h2>
          <p>
            Trending and new releases use TMDB. Set your API Read Access Token in the server
            environment, then restart.
          </p>
          <code>TMDB_READ_TOKEN=… AUTH_TOKEN=… go run ./cmd/bigflix</code>
          <a
            className="text-link"
            href="https://www.themoviedb.org/settings/api"
            rel="noreferrer"
          >
            Get a TMDB credential ↗
          </a>
        </article>
      </div>

      {loading && !data ? (
        <p className="notice">Loading network settings…</p>
      ) : (
        data?.hasEngine && (
          <form onSubmit={handleSaveNetwork}>
            {data.allInterfaces && (
              <p className="notice">
                The current client still uses all interfaces on port {data.actualPort}. Active
                interfaces with usable addresses are preselected below; save to apply explicit
                interface selection.
              </p>
            )}
            {data.errors.map((errText, idx) => (
              <p key={idx} className="notice" role="alert">
                {errText}
              </p>
            ))}

            <h2>Network interfaces</h2>
            <p>
              Each selected interface uses its own TCP/UDP port. Outgoing peer connections are
              bound to the selected interfaces. Dynamic mode chooses an available port when the
              client starts.
            </p>

            <div className="settings-grid">
              {data.interfaces.map((iface) => {
                const state = bindings[iface.name] || {
                  selected: iface.selected,
                  mode: iface.port !== 0 ? "fixed" : "dynamic",
                  port: iface.port || 51413,
                };
                return (
                  <article key={iface.name} className="settings-card">
                    <h3>
                      <label>
                        <input
                          type="checkbox"
                          checked={state.selected}
                          onChange={(e) =>
                            setBindings((prev) => ({
                              ...prev,
                              [iface.name]: { ...state, selected: e.target.checked },
                            }))
                          }
                        />{" "}
                        Use {iface.name}
                      </label>{" "}
                      · {iface.up ? "Up" : "Down"}
                    </h3>
                    {iface.addresses.length ? (
                      iface.addresses.map((addr) => (
                        <p key={addr}>
                          <code>{addr}</code>
                        </p>
                      ))
                    ) : (
                      <p className="muted">No IP address</p>
                    )}
                    <p>
                      <label htmlFor={`mode-${iface.name}`}>{iface.name} port mode</label>
                      <br />
                      <select
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
                        <option value="fixed">Fixed port</option>
                        <option value="dynamic">Dynamic port</option>
                      </select>
                    </p>
                    <p>
                      <label htmlFor={`port-${iface.name}`}>{iface.name} fixed port</label>
                      <br />
                      <input
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
                    </p>
                    {iface.listeners.length ? (
                      iface.listeners.map((l) => (
                        <p key={l} className="muted">
                          Listening: <code>{l}</code>
                        </p>
                      ))
                    ) : state.selected ? (
                      <p className="muted">No active listener</p>
                    ) : null}
                  </article>
                );
              })}
            </div>

            <p>
              <button className="button" type="submit" disabled={savingNet}>
                {savingNet ? "Saving…" : "Save and restart client"}
              </button>
            </p>
            <p className="muted">
              Select at least one interface. Saving briefly interrupts playback; cached downloads
              and watch positions are retained.
            </p>
          </form>
        )
      )}
    </section>
  );
}
