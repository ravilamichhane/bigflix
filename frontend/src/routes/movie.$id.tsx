import { createFileRoute } from "@tanstack/react-router";
import { DetailPage } from "../components/DetailPage";
import { NotFoundView } from "../components/NotFoundView";
import { navigateTo } from "../router";

function MovieRouteComponent() {
  const { id } = Route.useParams();
  const num = Number(id);
  if (!Number.isInteger(num) || num <= 0) {
    return <NotFoundView />;
  }
  return <DetailPage id={num} navigate={navigateTo} />;
}

export const Route = createFileRoute("/movie/$id")({
  component: MovieRouteComponent,
});
