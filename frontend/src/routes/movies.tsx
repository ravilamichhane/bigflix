import { createFileRoute, useRouterState } from "@tanstack/react-router";
import { BrowsePage } from "../components/BrowsePage";
import { navigateTo } from "../router";
import { validateBrowseSearch } from "./index";

function MoviesRouteComponent() {
  const search = useRouterState({ select: (s) => s.location.searchStr });
  return <BrowsePage mode="movie" search={search} navigate={navigateTo} />;
}

export const Route = createFileRoute("/movies")({
  validateSearch: validateBrowseSearch,
  component: MoviesRouteComponent,
});
