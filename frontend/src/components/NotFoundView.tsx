import { Link } from "@tanstack/react-router";

export function NotFoundView() {
  return (
    <div className="empty">
      <h2>Page not found</h2>
      <p>That page does not exist.</p>
      <Link className="button" to="/">
        Browse movies
      </Link>
    </div>
  );
}
