import { createRouter } from "@tanstack/react-router";
import { routeTree } from "./routeTree.gen";
import { NotFoundView } from "./components/NotFoundView";

export interface RouterContext {
  lock: () => void;
}

export const router = createRouter({
  routeTree,
  scrollRestoration: true,
  defaultNotFoundComponent: NotFoundView,
  context: {
    lock: () => {},
  },
});

export function navigateTo(to: string) {
  void router.navigate({ href: to });
}

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
