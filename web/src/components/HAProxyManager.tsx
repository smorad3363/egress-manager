import { useCallback, useEffect, useMemo, useState } from "react";
import { api } from "../lib/api";
import { Icon } from "./Icon";
import { Badge } from "./ui/Badge";
import { Button } from "./ui/Button";
import { Card } from "./ui/Card";
import { Dialog } from "./ui/Dialog";
import { SelectField, TextField } from "./ui/Field";
import { Skeleton } from "./ui/Skeleton";
import { StatePanel } from "./ui/StatePanel";

type Health = { status: "unknown" | "healthy" | "degraded" | "unhealthy" | "disabled" };
type Backend = { id: string; name: string; server: { host: string; port: number }; weight: number; backup: boolean; health_check: boolean; health: Health; enabled: boolean };
type Frontend = { id: string; name: string; bind: string; port: number; backend_ids: string[]; algorithm: "roundrobin" | "leastconn"; enabled: boolean };
type StoredBackend = { backend: Backend; revision: number };
type StoredFrontend = { frontend: Frontend; revision: number };
type RuntimeStat = { frontend_id: string; backend_id?: string; kind: "frontend" | "backend" | "server"; status: Health["status"]; current_sessions: number; total_sessions: number; bytes_in: number; bytes_out: number; check_failures: number; downtime_seconds: number };
type RuntimeSnapshot = { info: { name: string; version: string; pid: number; uptime_seconds: number; current_connections: number; total_connections: number }; stats: RuntimeStat[] };
type Plan = { engine: string; enabled_frontends: number; enabled_backends: number; actions: Array<{ kind: string; resource: string; summary: string }>; candidate: string };
type ReadyState = { frontends: StoredFrontend[]; backends: StoredBackend[]; runtime: RuntimeSnapshot | null };
type LoadState = { status: "loading" } | { status: "error"; message: string } | ({ status: "ready" } & ReadyState);

export function HAProxyManager({ createRequest }: { createRequest: number }) {
  const [state, setState] = useState<LoadState>({ status: "loading" });
  const [frontendOpen, setFrontendOpen] = useState(false);
  const [backendOpen, setBackendOpen] = useState(false);
  const [plan, setPlan] = useState<Plan | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [backendForm, setBackendForm] = useState({ name: "", host: "", port: "", weight: "100", backup: false, healthCheck: true });
  const [frontendForm, setFrontendForm] = useState({ name: "", bind: "", port: "", algorithm: "roundrobin" as Frontend["algorithm"], backendIDs: [] as string[] });

  const load = useCallback(async () => {
    setState({ status: "loading" });
    try {
      const [frontends, backends, runtime] = await Promise.all([
        api<{ items: StoredFrontend[] }>("/api/v1/haproxy/frontends?limit=100"),
        api<{ items: StoredBackend[] }>("/api/v1/haproxy/backends?limit=100"),
        api<RuntimeSnapshot>("/api/v1/haproxy/stats").catch(() => null),
      ]);
      setState({ status: "ready", frontends: frontends.items, backends: backends.items, runtime });
    } catch (caught) { setState({ status: "error", message: message(caught) }); }
  }, []);
  useEffect(() => { void load(); }, [load]);
  useEffect(() => { if (createRequest > 0) setFrontendOpen(true); }, [createRequest]);

  const statsByBackend = useMemo(() => new Map(state.status === "ready" && state.runtime ? state.runtime.stats.filter((item) => item.kind === "server" && item.backend_id).map((item) => [item.backend_id as string, item]) : []), [state]);
  const mutate = async (path: string, method: "POST" | "PUT" | "DELETE", body: unknown): Promise<boolean> => {
    setBusy(true); setError(null);
    try { await api(path, { method, body: JSON.stringify(body) }); await load(); return true; }
    catch (caught) { setError(message(caught)); return false; }
    finally { setBusy(false); }
  };
  const createBackend = async () => {
    const suffix = Date.now().toString(36); const base = slug(backendForm.name);
    if (await mutate("/api/v1/haproxy/backends", "POST", { id: `${base.slice(0, 63-suffix.length)}_${suffix}`, name: backendForm.name.trim(), server: { host: backendForm.host.trim(), port: Number(backendForm.port) }, weight: Number(backendForm.weight), backup: backendForm.backup, health_check: backendForm.healthCheck, health: { status: "unknown" }, enabled: true })) {
      setBackendForm({ name: "", host: "", port: "", weight: "100", backup: false, healthCheck: true });
      setBackendOpen(false);
    }
  };
  const createFrontend = async () => {
    const suffix = Date.now().toString(36); const base = slug(frontendForm.name);
    if (await mutate("/api/v1/haproxy/frontends", "POST", { id: `${base.slice(0, 63-suffix.length)}_${suffix}`, name: frontendForm.name.trim(), bind: frontendForm.bind.trim(), port: Number(frontendForm.port), backend_ids: frontendForm.backendIDs, algorithm: frontendForm.algorithm, enabled: true })) {
      setFrontendForm({ name: "", bind: "", port: "", algorithm: "roundrobin", backendIDs: [] });
      setFrontendOpen(false);
    }
  };
  const preview = async () => {
    if (state.status !== "ready") return;
    setBusy(true); setError(null);
    try { setPlan(await api<Plan>("/api/v1/haproxy/plan", { method: "POST", body: JSON.stringify({ frontends: state.frontends.map((item) => item.frontend), backends: state.backends.map((item) => item.backend) }) })); }
    catch (caught) { setError(message(caught)); }
    finally { setBusy(false); }
  };
  const apply = async () => {
    setBusy(true); setError(null);
    try { await api("/api/v1/haproxy/apply", { method: "POST", body: "{}" }); setPlan(null); await load(); }
    catch (caught) { setError(message(caught)); }
    finally { setBusy(false); }
  };

  if (state.status === "loading") return <section><Heading /><div className="forward-loading"><Skeleton /><Skeleton /></div></section>;
  if (state.status === "error") return <section><Heading /><StatePanel tone="error" title="HAProxy unavailable" description={state.message} action="Try again" onAction={() => void load()} /></section>;
  const healthy = new Set(state.runtime?.stats.filter((item) => item.kind === "server" && item.status === "healthy" && item.backend_id).map((item) => item.backend_id)).size;
  return <section>
    <div className="inventory-heading forward-heading"><Heading /><div className="forward-actions"><Button variant="secondary" onClick={() => setBackendOpen(true)}>New backend</Button><Button variant="primary" disabled={busy} onClick={() => void preview()}>Review & apply</Button></div></div>
    {error ? <div className="inventory-warning" role="alert"><Icon name="shield" /><span>{error}</span></div> : null}
    <div className="forward-summary"><Card><span>Frontends</span><strong>{state.frontends.length}</strong><small>TCP listeners</small></Card><Card><span>Backends</span><strong>{state.backends.length}</strong><small>Pool servers</small></Card><Card><span>Healthy</span><strong>{healthy} / {state.backends.filter((item) => item.backend.enabled).length}</strong><small>Runtime health</small></Card><Card><span>Connections</span><strong>{formatCount(state.runtime?.info.current_connections || 0)}</strong><small>{formatCount(state.runtime?.info.total_connections || 0)} total</small></Card></div>
    <Card className="forward-card haproxy-frontends"><header className="card-header"><div><p className="eyebrow">TRAFFIC ENTRY</p><h3>TCP frontends</h3></div><Badge tone={state.runtime ? "success" : "warning"}>{state.runtime ? `PID ${state.runtime.info.pid}` : "Not running"}</Badge></header>{state.frontends.length === 0 ? <StatePanel title="No frontends" description="Create a TCP listener after adding at least one backend." action="Create frontend" onAction={() => setFrontendOpen(true)} /> : <div className="table-scroll"><table><thead><tr><th>Name</th><th>Bind</th><th>Balance</th><th>Pool</th><th>Status</th><th><span className="sr-only">Actions</span></th></tr></thead><tbody>{state.frontends.map((item) => <tr key={item.frontend.id}><td><strong className="forward-name">{item.frontend.name}</strong></td><td><code>{item.frontend.bind}:{item.frontend.port}</code></td><td>{item.frontend.algorithm}</td><td>{item.frontend.backend_ids.length} servers</td><td><Badge tone={item.frontend.enabled ? "success" : "neutral"}>{item.frontend.enabled ? "Enabled" : "Disabled"}</Badge></td><td><div className="row-actions"><Button size="sm" variant="ghost" onClick={() => void mutate("/api/v1/haproxy/frontends", "PUT", { frontend: { ...item.frontend, enabled: !item.frontend.enabled }, expected_revision: item.revision })}>{item.frontend.enabled ? "Disable" : "Enable"}</Button><Button size="sm" variant="danger" onClick={() => void mutate("/api/v1/haproxy/frontends", "DELETE", { id: item.frontend.id, expected_revision: item.revision })}>Delete</Button></div></td></tr>)}</tbody></table></div>}</Card>
    <div className="backend-grid">{state.backends.map((item) => { const stat = statsByBackend.get(item.backend.id); return <Card key={item.backend.id} className="backend-card"><div><span className={`service-dot ${stat?.status === "healthy" ? "" : "service-dot--off"}`} /><div><strong>{item.backend.name}</strong><code>{item.backend.server.host}:{item.backend.server.port}</code></div><Badge tone={stat?.status === "healthy" ? "success" : stat?.status === "unhealthy" ? "danger" : "neutral"}>{stat?.status || (item.backend.enabled ? "Unknown" : "Disabled")}</Badge></div><dl><div><dt>Weight</dt><dd>{item.backend.weight}</dd></div><div><dt>Role</dt><dd>{item.backend.backup ? "Backup" : "Active"}</dd></div><div><dt>Sessions</dt><dd>{formatCount(stat?.total_sessions || 0)}</dd></div><div><dt>Traffic</dt><dd>{formatBytes((stat?.bytes_in || 0)+(stat?.bytes_out || 0))}</dd></div></dl><div className="row-actions"><Button size="sm" variant="ghost" onClick={() => void mutate("/api/v1/haproxy/backends", "PUT", { backend: { ...item.backend, enabled: !item.backend.enabled, health: { status: "unknown" } }, expected_revision: item.revision })}>{item.backend.enabled ? "Disable" : "Enable"}</Button><Button size="sm" variant="danger" onClick={() => void mutate("/api/v1/haproxy/backends", "DELETE", { id: item.backend.id, expected_revision: item.revision })}>Delete</Button></div></Card>; })}</div>

    <Dialog open={backendOpen} onOpenChange={setBackendOpen} title="Create backend" description="Add a TCP server to one or more frontend pools."><form className="dialog__form" onSubmit={(event) => { event.preventDefault(); void createBackend(); }}><TextField autoFocus required label="Name" placeholder="API primary" value={backendForm.name} onChange={(event) => setBackendForm({ ...backendForm, name: event.target.value })} /><div className="form-grid"><TextField required label="Host" placeholder="10.20.0.10" value={backendForm.host} onChange={(event) => setBackendForm({ ...backendForm, host: event.target.value })} /><TextField required label="Port" inputMode="numeric" placeholder="8080" value={backendForm.port} onChange={(event) => setBackendForm({ ...backendForm, port: event.target.value })} /></div><TextField required label="Weight" inputMode="numeric" value={backendForm.weight} onChange={(event) => setBackendForm({ ...backendForm, weight: event.target.value })} /><fieldset className="protocol-field"><legend>Behavior</legend><label><input type="checkbox" checked={backendForm.healthCheck} onChange={(event) => setBackendForm({ ...backendForm, healthCheck: event.target.checked })} />Health check</label><label><input type="checkbox" checked={backendForm.backup} onChange={(event) => setBackendForm({ ...backendForm, backup: event.target.checked })} />Backup server</label></fieldset><div className="dialog__actions"><Button type="button" variant="ghost" onClick={() => setBackendOpen(false)}>Cancel</Button><Button type="submit" variant="primary" disabled={busy}>Save backend</Button></div></form></Dialog>
    <Dialog open={frontendOpen} onOpenChange={setFrontendOpen} title="Create frontend" description="Bind a TCP listener to a managed backend pool."><form className="dialog__form" onSubmit={(event) => { event.preventDefault(); void createFrontend(); }}><TextField autoFocus required label="Name" placeholder="Public API" value={frontendForm.name} onChange={(event) => setFrontendForm({ ...frontendForm, name: event.target.value })} /><div className="form-grid"><TextField required label="Bind address" placeholder="192.0.2.10" value={frontendForm.bind} onChange={(event) => setFrontendForm({ ...frontendForm, bind: event.target.value })} /><TextField required label="Port" inputMode="numeric" placeholder="443" value={frontendForm.port} onChange={(event) => setFrontendForm({ ...frontendForm, port: event.target.value })} /></div><SelectField label="Balance algorithm" value={frontendForm.algorithm} onChange={(event) => setFrontendForm({ ...frontendForm, algorithm: event.target.value as Frontend["algorithm"] })}><option value="roundrobin">Round robin</option><option value="leastconn">Least connections</option></SelectField><fieldset className="backend-picker"><legend>Backend pool</legend>{state.backends.length ? state.backends.map((item) => <label key={item.backend.id}><input type="checkbox" checked={frontendForm.backendIDs.includes(item.backend.id)} onChange={(event) => setFrontendForm({ ...frontendForm, backendIDs: event.target.checked ? [...frontendForm.backendIDs, item.backend.id] : frontendForm.backendIDs.filter((id) => id !== item.backend.id) })} /><span>{item.backend.name}<small>{item.backend.server.host}:{item.backend.server.port}</small></span></label>) : <p>Add a backend first.</p>}</fieldset><div className="dialog__actions"><Button type="button" variant="ghost" onClick={() => setFrontendOpen(false)}>Cancel</Button><Button type="submit" variant="primary" disabled={busy || frontendForm.backendIDs.length === 0}>Save frontend</Button></div></form></Dialog>
    <Dialog open={plan !== null} onOpenChange={(open) => { if (!open) setPlan(null); }} title="Review HAProxy plan" description="Candidate is validated before atomic install and graceful reload.">{plan ? <div className="plan-review"><div className="plan-meta"><Badge tone="success">{plan.engine}</Badge><span>{plan.enabled_frontends} frontends</span><span>{plan.enabled_backends} enabled backends</span></div><ol>{plan.actions.map((action, index) => <li key={`${action.kind}-${index}`}><strong>{action.kind}</strong><span>{action.summary}</span><code>{action.resource}</code></li>)}</ol><details><summary>Advanced read-only configuration</summary><pre>{plan.candidate}</pre></details><div className="dialog__actions"><Button variant="ghost" onClick={() => setPlan(null)}>Cancel</Button><Button variant="primary" disabled={busy} onClick={() => void apply()}>Apply & reload</Button></div></div> : null}</Dialog>
  </section>;
}

function Heading() { return <div><p className="eyebrow">TCP LOAD BALANCER</p><h2>HAProxy</h2><p>Health-aware pools with graceful, transactional reloads.</p></div>; }
function slug(value: string) { return value.toLowerCase().replace(/[^a-z0-9]+/g, "_").replace(/^_+|_+$/g, "") || "haproxy"; }
function message(error: unknown) { return error instanceof Error ? error.message : "Request failed."; }
function formatCount(value: number) { return new Intl.NumberFormat("en", { notation: value >= 10_000 ? "compact" : "standard" }).format(value); }
function formatBytes(value: number) { if (value < 1024) return `${value} B`; if (value < 1024*1024) return `${(value/1024).toFixed(1)} KB`; return `${(value/1024/1024).toFixed(1)} MB`; }
