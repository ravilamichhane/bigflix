import { useCallback, useEffect, useState } from "react";
import { client, getToken, onUnauthorized, setToken } from "./api";
import { BrowsePage } from "./components/BrowsePage";
import { DetailPage } from "./components/DetailPage";
import { DownloadsPage } from "./components/DownloadsPage";
import { LockScreen } from "./components/LockScreen";
import { SettingsPage } from "./components/SettingsPage";
import { SourcesPage } from "./components/SourcesPage";
import { WatchPage } from "./components/WatchPage";

interface RouteState {
  pathname: string;
  search: string;
}

export function App() {
  const [route, setRoute] = useState<RouteState>({
    pathname: window.location.pathname,
    search: window.location.search,
  });
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

  useEffect(() => {
    const onPopState = () => {
      setRoute({
        pathname: window.location.pathname,
        search: window.location.search,
      });
    };
    window.addEventListener("popstate", onPopState);
    return () => window.removeEventListener("popstate", onPopState);
  }, []);

  const navigate = useCallback((to: string) => {
    const u = new URL(to, window.location.origin);
    window.history.pushState(null, "", u.pathname + u.search);
    setRoute({ pathname: u.pathname, search: u.search });
    window.scrollTo({ top: 0, behavior: "auto" });
  }, []);

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

  const params = new URLSearchParams(route.search);
  const feed = route.pathname === "/" ? params.get("feed") || "library" : "";

  function renderMain() {
    if (route.pathname === "/") {
      return <BrowsePage search={route.search} navigate={navigate} />;
    }
    const movieMatch = /^\/movie\/(\d+)$/.exec(route.pathname);
    if (movieMatch) {
      return <DetailPage id={Number(movieMatch[1])} navigate={navigate} />;
    }
    const sourcesMatch = /^\/sources\/(\d+)$/.exec(route.pathname);
    if (sourcesMatch) {
      return <SourcesPage tmdbId={Number(sourcesMatch[1])} navigate={navigate} />;
    }
    if (route.pathname === "/downloads") {
      return <DownloadsPage navigate={navigate} />;
    }
    const watchMatch = /^\/watch\/([a-f0-9]{40})$/i.exec(route.pathname);
    if (watchMatch) {
      return <WatchPage hash={watchMatch[1].toLowerCase()} navigate={navigate} />;
    }
    if (route.pathname === "/settings") {
      return <SettingsPage navigate={navigate} onLock={lock} />;
    }
    return (
      <section className="intro">
        <div className="eyebrow">NOT FOUND</div>
        <h1>Page not found</h1>
        <p>That page does not exist.</p>
        <a
          className="button"
          href="/"
          onClick={(e) => {
            e.preventDefault();
            navigate("/");
          }}
        >
          Browse movies →
        </a>
      </section>
    );
  }

  return (
    <>
      <header>
        <a
          className="brand"
          href="/"
          aria-label="Bigflix home"
          onClick={(e) => {
            e.preventDefault();
            navigate("/");
          }}
        >
          <span className="brand-mark">b</span>bigflix
          <span className="local">LOCAL</span>
        </a>
        <nav aria-label="Main navigation">
          <a
            href="/"
            aria-current={feed === "library" ? "page" : undefined}
            onClick={(e) => {
              e.preventDefault();
              navigate("/");
            }}
          >
            Discover
          </a>
          <a
            href="/?feed=day"
            aria-current={feed === "day" ? "page" : undefined}
            onClick={(e) => {
              e.preventDefault();
              navigate("/?feed=day");
            }}
          >
            Trending
          </a>
          <a
            href="/?feed=new"
            aria-current={feed === "new" ? "page" : undefined}
            onClick={(e) => {
              e.preventDefault();
              navigate("/?feed=new");
            }}
          >
            New releases
          </a>
          <a
            href="/downloads"
            aria-current={
              route.pathname === "/downloads" || route.pathname.startsWith("/watch/")
                ? "page"
                : undefined
            }
            onClick={(e) => {
              e.preventDefault();
              navigate("/downloads");
            }}
          >
            Downloads
          </a>
        </nav>
        <div className="header-actions">
          <a
            className="setup"
            href="/settings"
            aria-label="Local setup"
            onClick={(e) => {
              e.preventDefault();
              navigate("/settings");
            }}
          >
            <span className="dot" /> Local workspace <span className="avatar">B</span>
          </a>
          <button type="button" className="lock-button" onClick={lock}>
            Lock
          </button>
        </div>
      </header>
      <main>{renderMain()}</main>
      <footer>
        <span className="footer-brand">bigflix</span>
        <span>Your movies. Your space.</span>
        <span className="footer-status">Local downloads · Stream while downloading</span>
      </footer>
    </>
  );
}
