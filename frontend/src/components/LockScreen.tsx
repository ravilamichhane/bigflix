import { useState, type FormEvent } from "react";
import { client, errMessage, setToken } from "../api";

interface LockScreenProps {
  onUnlock: () => void;
}

export function LockScreen({ onUnlock }: LockScreenProps) {
  const [input, setInput] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    const candidate = input.trim();
    if (!candidate) {
      setError("Enter your access token.");
      return;
    }
    setBusy(true);
    setError("");
    setToken(candidate);
    try {
      await client.getStatus({});
      onUnlock();
    } catch (err) {
      setToken("");
      setError(errMessage(err) || "Invalid access token.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="lock-screen">
      <div className="lock-card">
        <div className="brand">
          <span className="brand-mark">b</span>
          <span>bigflix</span>
        </div>
        <h1>Unlock</h1>
        <p>Enter your access token to continue.</p>
        <form className="lock-form" onSubmit={handleSubmit}>
          <input
            type="password"
            aria-label="Access token"
            placeholder="Access token"
            value={input}
            onChange={(e) => setInput(e.target.value)}
            autoFocus
            autoComplete="current-password"
          />
          {error && (
            <div className="error-banner" role="alert">
              {error}
            </div>
          )}
          <button className="button" type="submit" disabled={busy}>
            {busy ? "Verifying…" : "Unlock"}
          </button>
        </form>
      </div>
    </div>
  );
}
