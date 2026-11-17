import { Link } from "@tanstack/react-router";

export function NotFoundView() {
  return (
    <section className="intro">
      <div className="eyebrow">NOT FOUND</div>
      <h1>Page not found</h1>
      <p>That page does not exist.</p>
      <Link className="button" to="/">
        Browse movies →
      </Link>
    </section>
  );
}
