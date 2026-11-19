import type { ButtonHTMLAttributes, ReactNode } from "react";

interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: "primary" | "secondary" | "ghost" | "danger";
  size?: "sm" | "md" | "icon" | "icon-sm";
}

export function Button({
  variant = "primary",
  size = "md",
  className = "",
  type = "button",
  ...props
}: ButtonProps) {
  const sizeClass =
    size === "sm"
      ? "ds-btn-sm"
      : size === "icon"
        ? "ds-btn-icon"
        : size === "icon-sm"
          ? "ds-btn-icon-sm"
          : "";
  return (
    <button
      type={type}
      className={`ds-btn ds-btn-${variant} ${sizeClass} ${className}`.trim()}
      {...props}
    />
  );
}

interface BadgeProps {
  tone?: "neutral" | "accent" | "ok" | "warn" | "danger";
  className?: string;
  children: ReactNode;
}

export function Badge({ tone = "neutral", className = "", children }: BadgeProps) {
  return <span className={`ds-badge ds-badge-${tone} ${className}`.trim()}>{children}</span>;
}

interface ProgressBarProps {
  value: number;
  label?: string;
}

export function ProgressBar({ value, label }: ProgressBarProps) {
  const pct = Math.max(0, Math.min(100, (Number.isFinite(value) ? value : 0) * 100));
  return (
    <div
      className="ds-progress"
      role="progressbar"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={Math.round(pct)}
    >
      <div className="ds-progress-fill" style={{ width: `${pct}%` }} />
    </div>
  );
}

interface EmptyStateProps {
  title: string;
  description?: string;
  actions?: ReactNode;
}

export function EmptyState({ title, description, actions }: EmptyStateProps) {
  return (
    <div className="ds-empty">
      <h2>{title}</h2>
      {description && <p>{description}</p>}
      {actions && <div className="ds-empty-actions">{actions}</div>}
    </div>
  );
}
