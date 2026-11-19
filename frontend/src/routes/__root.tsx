import {
  Link,
  Outlet,
  createRootRouteWithContext,
  useRouteContext,
  useRouterState,
} from "@tanstack/react-router";
import {
  BrandMarkIcon,
  CompassIcon,
  DownloadIcon,
  FlameIcon,
  LockIcon,
  SettingsIcon,
  SparklesIcon,
} from "../components/Icons";
import { NotFoundView } from "../components/NotFoundView";
import type { RouterContext } from "../router";

function RootLayout() {
  const { lock } = useRouteContext({ from: "__root__" });
  const { pathname, search } = useRouterState({
    select: (s) => ({
      pathname: s.location.pathname,
      search: s.location.searchStr,
    }),
  });
  const params = new URLSearchParams(search);
  const feed = pathname === "/" ? params.get("feed") || "library" : "";

  const isDiscover = pathname === "/" && feed === "library";
  const isTrending = pathname === "/" && (feed === "day" || feed === "week");
  const isNew = pathname === "/" && feed === "new";
  const isDownloads = pathname === "/downloads" || pathname.startsWith("/watch/");
  const isSettings = pathname === "/settings";

  return (
    <>
      <header className="kino-topbar">
        <div className="kino-topbar-inner">
          <Link className="kino-brand" to="/" aria-label="Bigflix home">
            <span className="kino-brand-mark">
              <BrandMarkIcon size={15} />
            </span>
            <span>Bigflix</span>
          </Link>

          <nav className="kino-nav" aria-label="Main navigation">
            <Link to="/" aria-current={isDiscover ? "page" : undefined}>
              Discover
            </Link>
            <Link
              to="/"
              search={{ feed: "day" }}
              aria-current={isTrending ? "page" : undefined}
            >
              Trending
            </Link>
            <Link
              to="/"
              search={{ feed: "new" }}
              aria-current={isNew ? "page" : undefined}
            >
              New
            </Link>
            <Link to="/downloads" aria-current={isDownloads ? "page" : undefined}>
              Downloads
            </Link>
          </nav>

          <div className="kino-actions">
            <Link
              className="ds-btn ds-btn-ghost ds-btn-icon"
              to="/settings"
              aria-label="Settings"
              aria-current={isSettings ? "page" : undefined}
              title="Settings"
            >
              <SettingsIcon size={18} />
            </Link>
            <button
              type="button"
              className="ds-btn ds-btn-ghost ds-btn-icon"
              onClick={lock}
              aria-label="Lock session"
              title="Lock session"
            >
              <LockIcon size={18} />
            </button>
          </div>
        </div>
      </header>

      <main className="kino-main">
        <Outlet />
      </main>

      <nav className="kino-dock" aria-label="Mobile navigation">
        <Link to="/" aria-current={isDiscover ? "page" : undefined}>
          <CompassIcon size={20} />
          <span>Discover</span>
        </Link>
        <Link
          to="/"
          search={{ feed: "day" }}
          aria-current={isTrending ? "page" : undefined}
        >
          <FlameIcon size={20} />
          <span>Trending</span>
        </Link>
        <Link
          to="/"
          search={{ feed: "new" }}
          aria-current={isNew ? "page" : undefined}
        >
          <SparklesIcon size={20} />
          <span>New</span>
        </Link>
        <Link to="/downloads" aria-current={isDownloads ? "page" : undefined}>
          <DownloadIcon size={20} />
          <span>Downloads</span>
        </Link>
      </nav>
    </>
  );
}

export const Route = createRootRouteWithContext<RouterContext>()({
  component: RootLayout,
  notFoundComponent: NotFoundView,
});
