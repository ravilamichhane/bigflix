import { createFileRoute } from "@tanstack/react-router";
import { NotFoundView } from "../components/NotFoundView";
import { SourcesPage } from "../components/SourcesPage";
import { navigateTo } from "../router";

function SourcesRouteComponent() {
  const { tmdbId } = Route.useParams();
  const num = Number(tmdbId);
  if (!Number.isInteger(num) || num <= 0) {
    return <NotFoundView />;
  }
  return <SourcesPage tmdbId={num} navigate={navigateTo} />;
}

export const Route = createFileRoute("/sources/$tmdbId")({
  component: SourcesRouteComponent,
});
