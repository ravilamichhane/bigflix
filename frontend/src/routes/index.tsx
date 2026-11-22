import { createFileRoute, useRouterState } from "@tanstack/react-router";
import { BrowsePage } from "../components/BrowsePage";
import { navigateTo } from "../router";

export interface BrowseSearch {
  feed?: string;
  q?: string;
  genre?: string;
  sort?: string;
  page?: number;
}

export function validateBrowseSearch(search: Record<string, unknown>): BrowseSearch {
  return {
    feed: typeof search.feed === "string" ? search.feed : undefined,
    q: typeof search.q === "string" ? search.q : undefined,
    genre: typeof search.genre === "string" ? search.genre : undefined,
    sort: typeof search.sort === "string" ? search.sort : undefined,
    page:
      search.page !== undefined && Number(search.page) > 0
        ? Number(search.page)
        : undefined,
  };
}

function DiscoverRouteComponent() {
  const search = useRouterState({ select: (s) => s.location.searchStr });
  return <BrowsePage mode="discover" search={search} navigate={navigateTo} />;
}

export const Route = createFileRoute("/")({
  validateSearch: validateBrowseSearch,
  component: DiscoverRouteComponent,
});
