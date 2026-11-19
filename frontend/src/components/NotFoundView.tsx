import { Link } from "@tanstack/react-router";
import { EmptyState } from "./ui";

export function NotFoundView() {
  return (
    <EmptyState
      title="Page not found"
      description="The page you requested doesn’t exist."
      actions={
        <Link className="ds-btn ds-btn-primary" to="/">
          Browse movies
        </Link>
      }
    />
  );
}
