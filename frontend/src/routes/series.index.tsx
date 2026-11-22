import { createFileRoute, useRouterState } from "@tanstack/react-router";
import { BrowsePage } from "../components/BrowsePage";
import { navigateTo } from "../router";
import { validateBrowseSearch } from "./index";

function SeriesBrowseRouteComponent() {
  const search = useRouterState({ select: (s) => s.location.searchStr });
  return <BrowsePage mode="tv" search={search} navigate={navigateTo} />;
}

export const Route = createFileRoute("/series/")({
  validateSearch: validateBrowseSearch,
  component: SeriesBrowseRouteComponent,
});
