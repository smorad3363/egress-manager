import { useState } from "react";
import type { FormEvent } from "react";

export function Login() {
  const [username, setUsername] = useState("operator");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError(null);
    setSubmitting(true);
    try {
      const response = await fetch("/api/v1/auth/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "same-origin",
        body: JSON.stringify({ username, password }),
      });
      const data = await response.json().catch(() => null) as { csrf_token?: string; message?: string } | null;
      if (!response.ok || !data?.csrf_token) {
        if (response.status === 429) throw new Error("Too many attempts. Try again in one minute.");
        throw new Error(data?.message || "Sign in failed.");
      }
      window.sessionStorage.setItem("egress.csrf", data.csrf_token);
      window.location.assign("/");
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Sign in failed.");
      setSubmitting(false);
    }
  }

  return (
    <main className="login-shell">
      <section className="login-card" aria-labelledby="login-title">
        <div className="login-brand">
          <span className="login-brand__mark" aria-hidden="true">&gt;_</span>
          <div><strong>Egress</strong><span>Manager</span></div>
        </div>
        <div className="login-copy">
          <p className="eyebrow">SECURE CONSOLE</p>
          <h1 id="login-title">Sign in to this gateway</h1>
          <p>Use the administrator account provisioned on this server.</p>
        </div>
        <form className="login-form" onSubmit={submit}>
          <label>
            <span>Username</span>
            <input autoComplete="username" value={username} onChange={(event) => setUsername(event.target.value)} required />
          </label>
          <label>
            <span>Password</span>
            <input autoFocus type="password" autoComplete="current-password" value={password} onChange={(event) => setPassword(event.target.value)} required />
          </label>
          {error ? <p className="login-error" role="alert">{error}</p> : null}
          <button className="login-submit" type="submit" disabled={submitting}>{submitting ? "Signing in…" : "Sign in"}</button>
        </form>
        <p className="login-note">Session cookies are restricted to this console. Use an SSH tunnel when the panel listens on localhost.</p>
      </section>
    </main>
  );
}
