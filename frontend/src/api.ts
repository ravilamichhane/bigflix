import { Code, ConnectError, createClient, type Interceptor } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { BigflixService } from "./gen/bigflix/v1/bigflix_pb";

const TOKEN_KEY = "bigflix_token";

let unauthorizedListener: (() => void) | null = null;

export function onUnauthorized(listener: (() => void) | null) {
  unauthorizedListener = listener;
}

export function getToken(): string {
  try {
    return localStorage.getItem(TOKEN_KEY) || "";
  } catch {
    return "";
  }
}

export function setToken(token: string) {
  const clean = token.trim();
  try {
    if (clean) {
      localStorage.setItem(TOKEN_KEY, clean);
      document.cookie = `bigflix_token=${encodeURIComponent(clean)}; path=/; SameSite=Strict`;
    } else {
      localStorage.removeItem(TOKEN_KEY);
      document.cookie = "bigflix_token=; path=/; Max-Age=0; SameSite=Strict";
    }
  } catch {
    // Ignore storage errors in restricted contexts.
  }
}

export function mediaUrl(path: string): string {
  const token = getToken();
  if (!token) return path;
  const sep = path.includes("?") ? "&" : "?";
  return `${path}${sep}token=${encodeURIComponent(token)}`;
}

const authInterceptor: Interceptor = (next) => async (req) => {
  const token = getToken();
  if (token) {
    req.header.set("Authorization", `Bearer ${token}`);
  }
  try {
    return await next(req);
  } catch (err) {
    if (err instanceof ConnectError && err.code === Code.Unauthenticated) {
      setToken("");
      unauthorizedListener?.();
    }
    throw err;
  }
};

const transport = createConnectTransport({
  baseUrl: window.location.origin,
  interceptors: [authInterceptor],
});

export const client = createClient(BigflixService, transport);

export function formatBytes(n: bigint | number): string {
  const num = Number(n);
  if (!Number.isFinite(num) || num <= 0) return "0.0 MB";
  return num >= 1e9 ? (num / 1e9).toFixed(2) + " GB" : (num / 1e6).toFixed(1) + " MB";
}

export function formatTime(iso: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return d.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  });
}

export function errMessage(err: unknown): string {
  if (err instanceof ConnectError) {
    return err.rawMessage || err.message;
  }
  if (err instanceof Error) return err.message;
  return String(err);
}
