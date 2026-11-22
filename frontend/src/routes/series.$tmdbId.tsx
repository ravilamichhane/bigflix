import { createFileRoute } from "@tanstack/react-router";
import { NotFoundView } from "../components/NotFoundView";
import { SeriesDetailPage } from "../components/SeriesDetailPage";
import { navigateTo } from "../router";

function SeriesDetailRouteComponent() {
  const { tmdbId } = Route.useParams();
  const num = Number(tmdbId);
  if (!Number.isInteger(num) || num <= 0) {
    return <NotFoundView />;
  }
  return <SeriesDetailPage tmdbId={num} navigate={navigateTo} />;
}

export const Route = createFileRoute("/series/$tmdbId")({
  component: SeriesDetailRouteComponent,
});
