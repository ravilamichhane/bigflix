import { createFileRoute } from "@tanstack/react-router";
import { useEffect } from "react";
import { NotFoundView } from "../components/NotFoundView";
import { navigateTo } from "../router";

function SourcesRedirectComponent() {
  const { tmdbId } = Route.useParams();
  const num = Number(tmdbId);
  useEffect(() => {
    if (Number.isInteger(num) && num > 0) {
      navigateTo(`/movie/${num}`);
    }
  }, [num]);
  if (!Number.isInteger(num) || num <= 0) {
    return <NotFoundView />;
  }
  return null;
}

export const Route = createFileRoute("/sources/$tmdbId")({
  component: SourcesRedirectComponent,
});
