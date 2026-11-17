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

function BrowseRouteComponent() {
  const search = useRouterState({ select: (s) => s.location.searchStr });
  return <BrowsePage search={search} navigate={navigateTo} />;
}

export const Route = createFileRoute("/")({
  validateSearch: (search: Record<string, unknown>): BrowseSearch => ({
    feed: typeof search.feed === "string" ? search.feed : undefined,
    q: typeof search.q === "string" ? search.q : undefined,
    genre: typeof search.genre === "string" ? search.genre : undefined,
    sort: typeof search.sort === "string" ? search.sort : undefined,
    page:
      search.page !== undefined && Number(search.page) > 0
        ? Number(search.page)
        : undefined,
  }),
  component: BrowseRouteComponent,
});
