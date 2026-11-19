import { useState, type FormEvent } from "react";
import { client, errMessage, setToken } from "../api";
import { BrandMarkIcon } from "./Icons";
import { Button } from "./ui";

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
        <div className="kino-brand">
          <span className="kino-brand-mark">
            <BrandMarkIcon size={15} />
          </span>
          <span>Bigflix</span>
        </div>
        <h1>Unlock library</h1>
        <p>Enter your server access token to browse and stream.</p>
        <form className="lock-form" onSubmit={handleSubmit}>
          <input
            type="password"
            className="ds-input"
            aria-label="Access token"
            placeholder="Access token"
            value={input}
            onChange={(e) => setInput(e.target.value)}
            autoFocus
            autoComplete="current-password"
          />
          {error && (
            <div className="ds-alert ds-alert-danger" role="alert" style={{ margin: 0 }}>
              {error}
            </div>
          )}
          <Button type="submit" disabled={busy}>
            {busy ? "Verifying…" : "Unlock"}
          </Button>
        </form>
      </div>
    </div>
  );
}
