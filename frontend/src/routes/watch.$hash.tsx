import { createFileRoute, useRouterState } from "@tanstack/react-router";
import { NotFoundView } from "../components/NotFoundView";
import { WatchPage } from "../components/WatchPage";
import { navigateTo } from "../router";

function WatchRouteComponent() {
  const { hash } = Route.useParams();
  const search = useRouterState({ select: (s) => s.location.searchStr });
  if (!/^[a-f0-9]{40}$/i.test(hash)) {
    return <NotFoundView />;
  }
  const rawFile = new URLSearchParams(search).get("file");
  const fileIndex = rawFile !== null && /^\d+$/.test(rawFile) ? Number(rawFile) : undefined;
  return <WatchPage hash={hash.toLowerCase()} fileIndex={fileIndex} navigate={navigateTo} />;
}

export const Route = createFileRoute("/watch/$hash")({
  component: WatchRouteComponent,
});
