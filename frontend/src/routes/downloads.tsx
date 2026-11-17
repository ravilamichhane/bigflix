import { createFileRoute } from "@tanstack/react-router";
import { DownloadsPage } from "../components/DownloadsPage";
import { navigateTo } from "../router";

function DownloadsRouteComponent() {
  return <DownloadsPage navigate={navigateTo} />;
}

export const Route = createFileRoute("/downloads")({
  component: DownloadsRouteComponent,
});
