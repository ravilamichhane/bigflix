import { createFileRoute } from "@tanstack/react-router";
import { NotFoundView } from "../components/NotFoundView";
import { WatchPage } from "../components/WatchPage";
import { navigateTo } from "../router";

function WatchRouteComponent() {
  const { hash } = Route.useParams();
  if (!/^[a-f0-9]{40}$/i.test(hash)) {
    return <NotFoundView />;
  }
  return <WatchPage hash={hash.toLowerCase()} navigate={navigateTo} />;
}

export const Route = createFileRoute("/watch/$hash")({
  component: WatchRouteComponent,
});
