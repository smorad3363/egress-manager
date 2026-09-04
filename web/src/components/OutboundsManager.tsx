import { useCallback, useEffect, useMemo, useState } from "react";
import { api } from "../lib/api";
import { Icon } from "./Icon";
import { Badge } from "./ui/Badge";
import { Button } from "./ui/Button";
import { Card } from "./ui/Card";
import { Dialog } from "./ui/Dialog";
import { TextField } from "./ui/Field";
import { Skeleton } from "./ui/Skeleton";
import { StatePanel } from "./ui/StatePanel";

type ProbeStatus = "unknown" | "passed" | "failed" | "unsupported" | "untestable";
type HealthStatus = "unknown" | "healthy" | "degraded" | "unhealthy" | "disabled";
type Health = { status: HealthStatus; checked_at?: string; configuration_valid: ProbeStatus; transport_reachable: ProbeStatus; internet_reachable: ProbeStatus; external_ip?: string; tcp: ProbeStatus; udp: ProbeStatus; latency?: number; detail?: string };
type Outbound = { id: string; name: string; adapter: "sing-box"; type: string; server: { host: string; port: number }; capabilities: { tcp: boolean; udp: boolean }; health: Health; enabled: boolean; secret_metadata?: string[] };
type StoredOutbound = { outbound: Outbound; revision: number; created_at: string; updated_at: string };
type Plan = { engine: string; state_hash: string; candidate_hash: string; enabled_outbounds: number; actions: Array<{ kind: string; resource: string; summary: string }> };
type TestResponse = { results: Array<{ outbound: Outbound; health: Health }> };
type LoadState = { status: "loading" } | { status: "error"; message: string } | { status: "ready"; items: StoredOutbound[] };

export function OutboundsManager({ createRequest }: { createRequest: number }) {
  const [state, setState] = useState<LoadState>({ status: "loading" });
  const [importOpen, setImportOpen] = useState(false);
  const [cloneSource, setCloneSource] = useState<StoredOutbound | null>(null);
  const [cloneForm, setCloneForm] = useState({ id: "", name: "" });
  const [input, setInput] = useState("");
  const [testResult, setTestResult] = useState<Health | null>(null);
  const [plan, setPlan] = useState<Plan | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setState({ status: "loading" });
    try { const response = await api<{ items: StoredOutbound[] }>("/api/v1/outbounds?limit=100"); setState({ status: "ready", items: response.items }); }
    catch (caught) { setState({ status: "error", message: message(caught) }); }
  }, []);
  useEffect(() => { void load(); }, [load]);
  useEffect(() => { if (createRequest > 0) setImportOpen(true); }, [createRequest]);

  const counts = useMemo(() => {
    const items = state.status === "ready" ? state.items : [];
    return { enabled: items.filter((item) => item.outbound.enabled).length, healthy: items.filter((item) => item.outbound.health.status === "healthy").length, degraded: items.filter((item) => ["degraded", "unhealthy"].includes(item.outbound.health.status)).length };
  }, [state]);
  const run = async (work: () => Promise<void>) => { setBusy(true); setError(null); try { await work(); } catch (caught) { setError(message(caught)); } finally { setBusy(false); } };
  const testInput = () => run(async () => { const result = await api<TestResponse>("/api/v1/outbounds/test", { method: "POST", body: JSON.stringify({ input }) }); setTestResult(result.results[0]?.health || null); });
  const saveInput = () => run(async () => { await api("/api/v1/outbounds/import", { method: "POST", body: JSON.stringify({ input }) }); setInput(""); setTestResult(null); setImportOpen(false); await load(); });
  const testStored = (item: StoredOutbound) => run(async () => { await api("/api/v1/outbounds/test", { method: "POST", body: JSON.stringify({ id: item.outbound.id, expected_revision: item.revision }) }); await load(); });
  const toggle = (item: StoredOutbound) => run(async () => { await api("/api/v1/outbounds", { method: "PUT", body: JSON.stringify({ outbound: { ...item.outbound, enabled: !item.outbound.enabled }, expected_revision: item.revision }) }); await load(); });
  const remove = (item: StoredOutbound) => { if (!window.confirm(`Delete ${item.outbound.name}?`)) return; void run(async () => { await api("/api/v1/outbounds", { method: "DELETE", body: JSON.stringify({ id: item.outbound.id, expected_revision: item.revision }) }); await load(); }); };
  const clone = () => { if (!cloneSource) return; void run(async () => { await api("/api/v1/outbounds/clone", { method: "POST", body: JSON.stringify({ source_id: cloneSource.outbound.id, expected_revision: cloneSource.revision, id: cloneForm.id.trim(), name: cloneForm.name.trim() }) }); setCloneSource(null); setCloneForm({ id: "", name: "" }); await load(); }); };
  const preview = () => run(async () => setPlan(await api<Plan>("/api/v1/outbounds/plan", { method: "POST", body: "{}" })));
  const apply = () => run(async () => { if (!plan) return; await api("/api/v1/outbounds/apply", { method: "POST", body: JSON.stringify({ expected_state_hash: plan.state_hash, expected_candidate_hash: plan.candidate_hash }) }); setPlan(null); await load(); });

  if (state.status === "loading") return <section><Heading /><div className="forward-loading"><Skeleton /><Skeleton /></div></section>;
  if (state.status === "error") return <section><Heading /><StatePanel tone="error" title="Outbounds unavailable" description={state.message} action="Try again" onAction={() => void load()} /></section>;
  return <section>
    <div className="inventory-heading forward-heading"><Heading /><div className="forward-actions"><Button variant="secondary" onClick={() => setImportOpen(true)}>Import outbound</Button><Button variant="primary" disabled={busy} onClick={() => void preview()}>Review & apply</Button></div></div>
    {error ? <div className="inventory-warning" role="alert"><Icon name="shield" /><span>{error}</span></div> : null}
    <div className="forward-summary"><Card><span>Total</span><strong>{state.items.length}</strong><small>Stored adapters</small></Card><Card><span>Enabled</span><strong>{counts.enabled}</strong><small>Desired state</small></Card><Card><span>Healthy</span><strong>{counts.healthy}</strong><small>Last tested</small></Card><Card><span>Attention</span><strong>{counts.degraded}</strong><small>Degraded or unhealthy</small></Card></div>
    {state.items.length === 0 ? <Card className="forward-card"><StatePanel title="No outbounds" description="Import a URI or sing-box JSON configuration. Credentials remain encrypted and never return through the API." action="Import outbound" onAction={() => setImportOpen(true)} /></Card> : <div className="outbound-grid">{state.items.map((item) => <OutboundCard key={item.outbound.id} item={item} busy={busy} onTest={() => void testStored(item)} onToggle={() => void toggle(item)} onClone={() => { setCloneSource(item); setCloneForm({ id: `${item.outbound.id}_copy`, name: `${item.outbound.name} copy` }); }} onDelete={() => remove(item)} />)}</div>}
    <Dialog open={importOpen} onOpenChange={setImportOpen} title="Import outbound" description="Paste one supported URI or a bounded sing-box JSON document."><form className="dialog__form" onSubmit={(event) => { event.preventDefault(); void saveInput(); }}><label className="field"><span className="field__label">URI or sing-box JSON</span><textarea className="input outbound-input" aria-label="URI or sing-box JSON" required value={input} onChange={(event) => { setInput(event.target.value); setTestResult(null); }} placeholder="vless://… or { &quot;outbounds&quot;: […] }" /><span className="field__hint">Supported: VLESS, Trojan, Shadowsocks, VMess, Hysteria2, TUIC, SOCKS5, WireGuard.</span></label>{testResult ? <HealthStrip health={testResult} /> : null}<div className="dialog__actions"><Button variant="ghost" type="button" onClick={() => setImportOpen(false)}>Cancel</Button><Button variant="secondary" type="button" disabled={busy || !input.trim()} onClick={() => void testInput()}>Test connection</Button><Button variant="primary" type="submit" disabled={busy || !input.trim()}>Save outbound</Button></div></form></Dialog>
    <Dialog open={cloneSource !== null} onOpenChange={(open) => { if (!open) setCloneSource(null); }} title="Clone outbound" description="Credentials are re-encrypted for the new ID. Clone starts disabled."><form className="dialog__form" onSubmit={(event) => { event.preventDefault(); clone(); }}><TextField label="Outbound ID" required value={cloneForm.id} onChange={(event) => setCloneForm((current) => ({ ...current, id: event.target.value }))} /><TextField label="Display name" required value={cloneForm.name} onChange={(event) => setCloneForm((current) => ({ ...current, name: event.target.value }))} /><div className="dialog__actions"><Button variant="ghost" type="button" onClick={() => setCloneSource(null)}>Cancel</Button><Button variant="primary" type="submit" disabled={busy}>Create clone</Button></div></form></Dialog>
    <Dialog open={plan !== null} onOpenChange={(open) => { if (!open) setPlan(null); }} title="Review sing-box plan" description="Only public actions and hashes are shown. Credentials and candidate configuration remain inside egressd.">{plan ? <div className="plan-review"><div className="plan-meta"><Badge tone="success">{plan.engine}</Badge><span>{plan.enabled_outbounds} enabled</span><code>{shortHash(plan.candidate_hash)}</code></div><ol>{plan.actions.map((action, index) => <li key={`${action.kind}-${action.resource}-${index}`}><strong>{action.kind}</strong><span>{action.summary}</span><code>{action.resource}</code></li>)}</ol><div className="dialog__actions"><Button variant="ghost" onClick={() => setPlan(null)}>Cancel</Button><Button variant="primary" disabled={busy} onClick={() => void apply()}>Apply atomically</Button></div></div> : null}</Dialog>
  </section>;
}

function OutboundCard({ item, busy, onTest, onToggle, onClone, onDelete }: { item: StoredOutbound; busy: boolean; onTest: () => void; onToggle: () => void; onClone: () => void; onDelete: () => void }) { const outbound = item.outbound; return <Card className="outbound-card"><header><div><p className="eyebrow">{outbound.type}</p><h3>{outbound.name}</h3></div><Badge tone={healthTone(outbound.health.status)}>{outbound.health.status}</Badge></header><code className="outbound-endpoint">{outbound.server.host}:{outbound.server.port}</code><dl><div><dt>Capabilities</dt><dd>{[outbound.capabilities.tcp && "TCP", outbound.capabilities.udp && "UDP"].filter(Boolean).join(" + ")}</dd></div><div><dt>External IP</dt><dd>{outbound.health.external_ip || "Not tested"}</dd></div><div><dt>Latency</dt><dd>{formatLatency(outbound.health.latency)}</dd></div><div><dt>Secrets</dt><dd>{outbound.secret_metadata?.length || 0} protected fields</dd></div></dl><HealthStrip health={outbound.health} /><div className="row-actions"><Button size="sm" variant="ghost" disabled={busy} onClick={onTest}>Test</Button><Button size="sm" variant="ghost" disabled={busy} onClick={onClone}>Clone</Button><Button size="sm" variant="secondary" disabled={busy} onClick={onToggle}>{outbound.enabled ? "Disable" : "Enable"}</Button><Button size="sm" variant="danger" disabled={busy} onClick={onDelete}>Delete</Button></div></Card>; }
function HealthStrip({ health }: { health: Health }) { return <div className="outbound-health" aria-label={`Health ${health.status}`}><span>Config <strong>{health.configuration_valid}</strong></span><span>Transport <strong>{health.transport_reachable}</strong></span><span>Internet <strong>{health.internet_reachable}</strong></span><span>TCP <strong>{health.tcp}</strong></span><span>UDP <strong>{health.udp}</strong></span></div>; }
function Heading() { return <div><p className="eyebrow">SING-BOX ADAPTER</p><h2>Outbound connections</h2><p>Encrypted credentials, layered health, and transactional runtime apply.</p></div>; }
function healthTone(status: HealthStatus): "success" | "warning" | "danger" | "neutral" { return status === "healthy" ? "success" : status === "degraded" ? "warning" : status === "unhealthy" ? "danger" : "neutral"; }
function formatLatency(value?: number) { return value ? `${Math.max(1, Math.round(value / 1_000_000))} ms` : "Not tested"; }
function shortHash(value: string) { return value ? `${value.slice(0, 12)}…` : "pending"; }
function message(value: unknown) { return value instanceof Error ? value.message : "Request failed."; }
