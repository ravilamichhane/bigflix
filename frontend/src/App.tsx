import { useCallback, useEffect, useState } from "react";
import { RouterProvider } from "@tanstack/react-router";
import { client, getToken, onUnauthorized, setToken } from "./api";
import { LockScreen } from "./components/LockScreen";
import { router } from "./router";

export function App() {
  const [authState, setAuthState] = useState<"checking" | "locked" | "unlocked">(
    () => (getToken() ? "checking" : "locked"),
  );

  const lock = useCallback(() => {
    setToken("");
    setAuthState("locked");
  }, []);

  useEffect(() => {
    onUnauthorized(() => setAuthState("locked"));
    return () => onUnauthorized(null);
  }, []);

  useEffect(() => {
    if (authState !== "checking") return;
    let active = true;
    client
      .getStatus({})
      .then(() => {
        if (active) setAuthState("unlocked");
      })
      .catch(() => {
        if (active) setAuthState("locked");
      });
    return () => {
      active = false;
    };
  }, [authState]);

  if (authState === "checking") {
    return (
      <div className="lock-screen">
        <div className="lock-card">
          <h1>Unlocking Bigflix…</h1>
          <p>Verifying session token.</p>
        </div>
      </div>
    );
  }

  if (authState === "locked") {
    return <LockScreen onUnlock={() => setAuthState("unlocked")} />;
  }

  return <RouterProvider router={router} context={{ lock }} />;
}
