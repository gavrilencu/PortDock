import { t, tMaybe } from "./i18n";

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

type Method = "GET" | "POST" | "PUT" | "PATCH" | "DELETE";

async function request<T>(method: Method, path: string, body?: unknown): Promise<T> {
  const form = body instanceof FormData;
  const res = await fetch(`/api${path}`, {
    method,
    credentials: "same-origin",
    headers: body !== undefined && !form ? { "Content-Type": "application/json" } : undefined,
    body: body === undefined ? undefined : form ? body : JSON.stringify(body),
  });
  if (res.status === 401 && !path.startsWith("/auth/")) {
    if (!location.pathname.startsWith("/login")) {
      location.href = `/login?next=${encodeURIComponent(location.pathname)}`;
    }
    throw new ApiError(401, t("authentication required"));
  }
  const text = await res.text();
  let data = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    // A proxy error page (502 during a restart, 413…) is not JSON.
    if (res.ok) throw new ApiError(res.status, t("unexpected response from the server"));
  }
  if (!res.ok) {
    // Server messages are English; known ones are shown in the interface language.
    throw new ApiError(res.status, tMaybe((data && data.error) || res.statusText));
  }
  return data as T;
}

export const api = {
  get: <T>(path: string) => request<T>("GET", path),
  post: <T>(path: string, body: unknown = {}) => request<T>("POST", path, body),
  put: <T>(path: string, body: unknown) => request<T>("PUT", path, body),
  patch: <T>(path: string, body: unknown) => request<T>("PATCH", path, body),
  del: <T = { ok: boolean }>(path: string) => request<T>("DELETE", path),
  /** Sends a multipart form (files). */
  upload: <T>(path: string, form: FormData) => request<T>("POST", path, form),
};

/** Waits until PortDock answers again after a restart (restore, update). */
export async function waitForRestart(timeoutMs = 180_000): Promise<boolean> {
  const start = Date.now();
  let wentDown = false;
  await new Promise((r) => setTimeout(r, 1500));
  while (Date.now() - start < timeoutMs) {
    try {
      const res = await fetch("/api/auth/status", { cache: "no-store" });
      if (res.ok && (wentDown || Date.now() - start > 8000)) return true;
      if (!res.ok) wentDown = true;
    } catch {
      wentDown = true;
    }
    await new Promise((r) => setTimeout(r, 1500));
  }
  return false;
}

export function wsURL(path: string): string {
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  return `${proto}//${location.host}/api${path}`;
}
