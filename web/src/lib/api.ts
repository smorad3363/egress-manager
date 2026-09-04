export async function api<T = unknown>(path: string, init?: RequestInit): Promise<T> {
  const headers = new Headers(init?.headers);
  if (init?.body) headers.set("Content-Type", "application/json");
  if (init?.method && !["GET", "HEAD"].includes(init.method)) {
    const token = window.sessionStorage.getItem("egress.csrf") || document.querySelector<HTMLMetaElement>('meta[name="csrf-token"]')?.content;
    if (token) headers.set("X-CSRF-Token", token);
  }
  const response = await fetch(path, { ...init, headers, credentials: "same-origin" });
  if (!response.ok) {
    const body = await response.json().catch(() => null) as { message?: string } | null;
    throw new Error(body?.message || (response.status === 401 ? "Sign in to continue." : "Request failed."));
  }
  if (response.status === 204) return undefined as T;
  return response.json() as Promise<T>;
}
