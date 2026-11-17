import {
  Link,
  Outlet,
  createRootRouteWithContext,
  useRouteContext,
  useRouterState,
} from "@tanstack/react-router";
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

  return (
    <>
      <header>
        <Link className="brand" to="/" aria-label="Bigflix home">
          <span className="brand-mark">b</span>bigflix
          <span className="local">LOCAL</span>
        </Link>
        <nav aria-label="Main navigation">
          <Link
            to="/"
            aria-current={pathname === "/" && feed === "library" ? "page" : undefined}
          >
            Discover
          </Link>
          <Link
            to="/"
            search={{ feed: "day" }}
            aria-current={pathname === "/" && feed === "day" ? "page" : undefined}
          >
            Trending
          </Link>
          <Link
            to="/"
            search={{ feed: "new" }}
            aria-current={pathname === "/" && feed === "new" ? "page" : undefined}
          >
            New releases
          </Link>
          <Link
            to="/downloads"
            aria-current={
              pathname === "/downloads" || pathname.startsWith("/watch/")
                ? "page"
                : undefined
            }
          >
            Downloads
          </Link>
        </nav>
        <div className="header-actions">
          <Link className="setup" to="/settings" aria-label="Local setup">
            <span className="dot" /> Local workspace <span className="avatar">B</span>
          </Link>
          <button type="button" className="lock-button" onClick={lock}>
            Lock
          </button>
        </div>
      </header>
      <main>
        <Outlet />
      </main>
      <footer>
        <span className="footer-brand">bigflix</span>
        <span>Your movies. Your space.</span>
        <span className="footer-status">Local downloads · Stream while downloading</span>
      </footer>
    </>
  );
}

export const Route = createRootRouteWithContext<RouterContext>()({
  component: RootLayout,
  notFoundComponent: NotFoundView,
});
