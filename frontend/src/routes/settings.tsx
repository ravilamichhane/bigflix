import { createFileRoute, useRouteContext } from "@tanstack/react-router";
import { SettingsPage } from "../components/SettingsPage";
import { navigateTo } from "../router";

function SettingsRouteComponent() {
  const { lock } = useRouteContext({ from: "/settings" });
  return <SettingsPage navigate={navigateTo} onLock={lock} />;
}

export const Route = createFileRoute("/settings")({
  component: SettingsRouteComponent,
});
