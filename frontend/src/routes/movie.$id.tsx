import { createFileRoute, useRouterState } from "@tanstack/react-router";
import { DetailPage } from "../components/DetailPage";
import { NotFoundView } from "../components/NotFoundView";
import { navigateTo } from "../router";

function MovieRouteComponent() {
  const { id } = Route.useParams();
  const search = useRouterState({ select: (s) => s.location.searchStr });
  const num = Number(id);
  if (!Number.isInteger(num) || num <= 0) {
    return <NotFoundView />;
  }
  const yts = new URLSearchParams(search).get("yts") === "1";
  return <DetailPage id={num} ytsFallback={yts} navigate={navigateTo} />;
}

export const Route = createFileRoute("/movie/$id")({
  component: MovieRouteComponent,
});
