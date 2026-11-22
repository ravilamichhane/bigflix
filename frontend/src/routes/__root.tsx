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
  FilmIcon,
  LockIcon,
  SettingsIcon,
  TvIcon,
} from "../components/Icons";
import { NotFoundView } from "../components/NotFoundView";
import type { RouterContext } from "../router";

function RootLayout() {
  const { lock } = useRouteContext({ from: "__root__" });
  const { pathname } = useRouterState({
    select: (s) => ({
      pathname: s.location.pathname,
    }),
  });

  const isDiscover = pathname === "/";
  const isMovies = pathname === "/movies" || pathname.startsWith("/movie/") || pathname.startsWith("/sources/");
  const isSeries = pathname === "/series" || pathname.startsWith("/series/");
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
            <Link to="/movies" aria-current={isMovies ? "page" : undefined}>
              Movies
            </Link>
            <Link to="/series" aria-current={isSeries ? "page" : undefined}>
              TV Series
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
        <Link to="/movies" aria-current={isMovies ? "page" : undefined}>
          <FilmIcon size={20} />
          <span>Movies</span>
        </Link>
        <Link to="/series" aria-current={isSeries ? "page" : undefined}>
          <TvIcon size={20} />
          <span>Series</span>
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
