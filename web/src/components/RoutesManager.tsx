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

type RouteSource = { kind: "interface"; interface: string } | { kind: "subnet"; subnet: string };
type EgressRoute = {
  id: string; name: string; source: RouteSource; outbound_id: string; fallback_outbound_id?: string;
  failure_policy: "block" | "failover" | "direct"; dns_policy: "follow_outbound" | "system" | "block";
  dns_servers?: string[]; ipv4_policy: "follow_outbound" | "block" | "direct";
  ipv6_policy: "follow_outbound" | "block" | "direct"; kill_switch: boolean; mtu?: number; tcp_mss?: number; enabled: boolean;
};
type StoredRoute = { route: EgressRoute; revision: number; created_at?: string; updated_at?: string };
type Outbound = { id: string; name: string; type: string; enabled: boolean; health: { status: string } };
type StoredOutbound = { outbound: Outbound; revision: number };
type Action = { kind: string; resource: string; summary: string };
type RouteReview = {
  engine: string; sing_box_state_hash: string; routing_state_hash: string; interface_state_hash: string; sing_box_candidate_hash: string;
  routing_candidate_hash: string; native_candidate_hash: string; combined_candidate_hash: string;
  enabled_outbounds: number; enabled_routes: number; sing_box_actions: Action[]; routing_actions: Action[]; native_actions: Action[];
};
type FormState = {
  id: string; name: string; sourceKind: "interface" | "subnet"; sourceValue: string; outboundID: string;
  fallbackOutboundID: string; failurePolicy: EgressRoute["failure_policy"]; dnsPolicy: EgressRoute["dns_policy"];
  dnsServers: string; ipv4Policy: EgressRoute["ipv4_policy"]; ipv6Policy: EgressRoute["ipv6_policy"];
  killSwitch: boolean; mtu: string; tcpMSS: string; enabled: boolean;
};

const emptyForm: FormState = {
  id: "", name: "", sourceKind: "interface", sourceValue: "", outboundID: "", fallbackOutboundID: "",
  failurePolicy: "block", dnsPolicy: "follow_outbound", dnsServers: "1.1.1.1", ipv4Policy: "follow_outbound",
  ipv6Policy: "block", killSwitch: true, mtu: "1400", tcpMSS: "1360", enabled: true,
};

export function RoutesManager({ createRequest }: { createRequest: number }) {
  const [routes, setRoutes] = useState<StoredRoute[] | null>(null);
  const [outbounds, setOutbounds] = useState<StoredOutbound[]>([]);
  const [form, setForm] = useState<FormState>(emptyForm);
  const [editing, setEditing] = useState<StoredRoute | null>(null);
  const [editorOpen, setEditorOpen] = useState(false);
  const [review, setReview] = useState<RouteReview | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setError(null);
    try {
      const [routeResponse, outboundResponse] = await Promise.all([
        api<{ items: StoredRoute[] }>("/api/v1/routes?limit=100"),
        api<{ items: StoredOutbound[] }>("/api/v1/outbounds?limit=128"),
      ]);
      setRoutes(routeResponse.items);
      setOutbounds(outboundResponse.items);
    } catch (caught) { setError(message(caught)); setRoutes([]); }
  }, []);
  useEffect(() => { void load(); }, [load]);
  useEffect(() => { if (createRequest > 0) openCreate(); }, [createRequest]);

  const run = async (work: () => Promise<void>) => {
    setBusy(true); setError(null);
    try { await work(); } catch (caught) { setError(message(caught)); } finally { setBusy(false); }
  };
  const openCreate = () => { setEditing(null); setForm(emptyForm); setEditorOpen(true); };
  const openEdit = (item: StoredRoute) => {
    const route = item.route;
    setEditing(item);
    setForm({
      id: route.id, name: route.name, sourceKind: route.source.kind,
      sourceValue: route.source.kind === "interface" ? route.source.interface : route.source.subnet,
      outboundID: route.outbound_id, fallbackOutboundID: route.fallback_outbound_id || "", failurePolicy: route.failure_policy,
      dnsPolicy: route.dns_policy, dnsServers: (route.dns_servers || []).join(", "), ipv4Policy: route.ipv4_policy,
      ipv6Policy: route.ipv6_policy, killSwitch: route.kill_switch, mtu: route.mtu ? String(route.mtu) : "",
      tcpMSS: route.tcp_mss ? String(route.tcp_mss) : "", enabled: route.enabled,
    });
    setEditorOpen(true);
  };
  const save = () => void run(async () => {
    const route = formToRoute(form);
    if (editing) await api("/api/v1/routes", { method: "PUT", body: JSON.stringify({ route, expected_revision: editing.revision }) });
    else await api("/api/v1/routes", { method: "POST", body: JSON.stringify(route) });
    setEditorOpen(false); await load();
  });
  const toggle = (item: StoredRoute) => void run(async () => {
    await api("/api/v1/routes", { method: "PUT", body: JSON.stringify({ route: { ...item.route, enabled: !item.route.enabled }, expected_revision: item.revision }) });
    await load();
  });
  const remove = (item: StoredRoute) => {
    if (!window.confirm(`Delete ${item.route.name}?`)) return;
    void run(async () => { await api("/api/v1/routes", { method: "DELETE", body: JSON.stringify({ id: item.route.id, expected_revision: item.revision }) }); await load(); });
  };
  const preview = () => void run(async () => setReview(await api<RouteReview>("/api/v1/routes/plan", { method: "POST", body: "{}" })));
  const apply = () => void run(async () => {
    if (!review) return;
    await api("/api/v1/routes/apply", { method: "POST", body: JSON.stringify({
      expected_sing_box_state_hash: review.sing_box_state_hash, expected_routing_state_hash: review.routing_state_hash,
      expected_interface_state_hash: review.interface_state_hash,
      expected_sing_box_candidate_hash: review.sing_box_candidate_hash, expected_routing_candidate_hash: review.routing_candidate_hash,
      expected_native_candidate_hash: review.native_candidate_hash, expected_combined_candidate_hash: review.combined_candidate_hash,
    }) });
    setReview(null); await load();
  });

  const enabled = useMemo(() => (routes || []).filter((item) => item.route.enabled).length, [routes]);
  const killSwitches = useMemo(() => (routes || []).filter((item) => item.route.enabled && item.route.kill_switch).length, [routes]);
  if (routes === null) return <section><Heading /><div className="forward-loading"><Skeleton /><Skeleton /></div></section>;
  return <section>
    <div className="inventory-heading forward-heading"><Heading /><div className="forward-actions"><Button variant="secondary" onClick={openCreate}>New route</Button><Button variant="primary" disabled={busy} onClick={preview}>Review & apply</Button></div></div>
    {error ? <div className="inventory-warning" role="alert"><Icon name="shield" /><span>{error}</span></div> : null}
    <div className="forward-summary"><Card><span>Total</span><strong>{routes.length}</strong><small>Saved policies</small></Card><Card><span>Enabled</span><strong>{enabled}</strong><small>Desired routes</small></Card><Card><span>Kill switches</span><strong>{killSwitches}</strong><small>Leak protected</small></Card><Card><span>Outbounds</span><strong>{outbounds.filter((item) => item.outbound.enabled).length}</strong><small>Available targets</small></Card></div>
    {routes.length === 0 ? <Card className="forward-card"><StatePanel title="No egress routes" description="Bind an interface or subnet to an enabled outbound. Every policy is reviewed before privileged apply." action="Create route" onAction={openCreate} /></Card> : <div className="outbound-grid">{routes.map((item) => <RouteCard key={item.route.id} item={item} outbounds={outbounds} busy={busy} onEdit={() => openEdit(item)} onToggle={() => toggle(item)} onDelete={() => remove(item)} />)}</div>}
    <Dialog open={editorOpen} onOpenChange={setEditorOpen} title={editing ? "Edit egress route" : "Create egress route"} description="All failure, DNS, and IP-family behavior is explicit.">
      <form className="dialog__form" onSubmit={(event) => { event.preventDefault(); save(); }}>
        <div className="form-grid"><TextField autoFocus label="Route ID" required disabled={editing !== null} value={form.id} onChange={(event) => setForm({ ...form, id: event.target.value })} placeholder="vpn_clients" /><TextField label="Display name" required value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} placeholder="VPN clients" /></div>
        <div className="form-grid"><SelectField label="Source type" value={form.sourceKind} onChange={(event) => setForm({ ...form, sourceKind: event.target.value as FormState["sourceKind"] })}><option value="interface">Interface</option><option value="subnet">Subnet</option></SelectField><TextField label={form.sourceKind === "interface" ? "Interface" : "Canonical CIDR"} required value={form.sourceValue} onChange={(event) => setForm({ ...form, sourceValue: event.target.value })} placeholder={form.sourceKind === "interface" ? "tun0" : "10.8.0.0/24"} /></div>
        <div className="form-grid"><SelectField label="Primary outbound" required value={form.outboundID} onChange={(event) => setForm({ ...form, outboundID: event.target.value })}><option value="" disabled>Select outbound</option>{outbounds.map(({ outbound }) => <option key={outbound.id} value={outbound.id} disabled={!outbound.enabled}>{outbound.name} · {outbound.type}</option>)}</SelectField><SelectField label="Failure behavior" value={form.failurePolicy} onChange={(event) => { const failurePolicy = event.target.value as FormState["failurePolicy"]; setForm({ ...form, failurePolicy, killSwitch: failurePolicy !== "direct" }); }}><option value="block">Block</option><option value="failover">Fail over</option><option value="direct">Direct (explicit)</option></SelectField></div>
        {form.failurePolicy === "failover" ? <SelectField label="Fallback outbound" required value={form.fallbackOutboundID} onChange={(event) => setForm({ ...form, fallbackOutboundID: event.target.value })}><option value="" disabled>Select fallback</option>{outbounds.filter(({ outbound }) => outbound.id !== form.outboundID).map(({ outbound }) => <option key={outbound.id} value={outbound.id} disabled={!outbound.enabled}>{outbound.name} · {outbound.type}</option>)}</SelectField> : null}
        <div className="form-grid"><SelectField label="DNS policy" value={form.dnsPolicy} onChange={(event) => setForm({ ...form, dnsPolicy: event.target.value as FormState["dnsPolicy"] })}><option value="follow_outbound">Follow outbound</option><option value="system">System / direct</option><option value="block">Block</option></SelectField>{form.dnsPolicy === "follow_outbound" ? <TextField label="DNS servers" required hint="1–4 comma-separated IP addresses" value={form.dnsServers} onChange={(event) => setForm({ ...form, dnsServers: event.target.value })} /> : <div />}</div>
        <div className="form-grid"><SelectField label="IPv4" value={form.ipv4Policy} onChange={(event) => setForm({ ...form, ipv4Policy: event.target.value as FormState["ipv4Policy"] })}><PolicyOptions /></SelectField><SelectField label="IPv6" value={form.ipv6Policy} onChange={(event) => setForm({ ...form, ipv6Policy: event.target.value as FormState["ipv6Policy"] })}><PolicyOptions /></SelectField></div>
        <div className="form-grid"><TextField label="MTU" type="number" min="576" max="9000" value={form.mtu} onChange={(event) => setForm({ ...form, mtu: event.target.value })} /><TextField label="TCP MSS" type="number" min="536" max="8960" value={form.tcpMSS} onChange={(event) => setForm({ ...form, tcpMSS: event.target.value })} /></div>
        <div className="route-toggles"><label><input type="checkbox" checked={form.killSwitch} disabled={form.failurePolicy === "direct"} onChange={(event) => setForm({ ...form, killSwitch: event.target.checked })} /> Kill switch</label><label><input type="checkbox" checked={form.enabled} onChange={(event) => setForm({ ...form, enabled: event.target.checked })} /> Enabled</label></div>
        <div className="dialog__actions"><Button type="button" variant="ghost" onClick={() => setEditorOpen(false)}>Cancel</Button><Button type="submit" variant="primary" disabled={busy}>Save route</Button></div>
      </form>
    </Dialog>
    <Dialog open={review !== null} onOpenChange={(open) => { if (!open) setReview(null); }} title="Review egress route plan" description="Only public actions and authenticated hashes are shown. Native candidates remain inside egressd.">
      {review ? <div className="plan-review"><div className="plan-meta"><Badge tone="warning">Atomic mutation</Badge><span>{review.enabled_routes} routes · {review.enabled_outbounds} outbounds</span></div><ol>{[...review.routing_actions, ...review.native_actions, ...review.sing_box_actions].map((action, index) => <li key={`${action.kind}-${action.resource}-${index}`}><strong>{action.kind}</strong><span>{action.summary}</span><code>{action.resource}</code></li>)}</ol><div className="dialog__actions"><Button variant="ghost" onClick={() => setReview(null)}>Cancel</Button><Button variant="primary" disabled={busy} onClick={apply}>Apply atomically</Button></div></div> : null}
    </Dialog>
  </section>;
}

function RouteCard({ item, outbounds, busy, onEdit, onToggle, onDelete }: { item: StoredRoute; outbounds: StoredOutbound[]; busy: boolean; onEdit: () => void; onToggle: () => void; onDelete: () => void }) {
  const route = item.route;
  const outbound = outbounds.find(({ outbound }) => outbound.id === route.outbound_id)?.outbound;
  const source = route.source.kind === "interface" ? route.source.interface : route.source.subnet;
  return <Card className="outbound-card route-card"><header><div><p className="eyebrow">{route.source.kind.toUpperCase()} · REV {item.revision}</p><h3>{route.name}</h3><code>{source}</code></div><Badge tone={route.enabled ? "success" : "neutral"}>{route.enabled ? "Enabled" : "Disabled"}</Badge></header><dl><div><dt>Outbound</dt><dd>{outbound?.name || route.outbound_id}</dd></div><div><dt>Failure</dt><dd>{route.failure_policy}{route.kill_switch ? " · kill switch" : ""}</dd></div><div><dt>DNS</dt><dd>{route.dns_policy}</dd></div><div><dt>IP families</dt><dd>v4 {route.ipv4_policy} · v6 {route.ipv6_policy}</dd></div></dl><div className="row-actions"><Button size="sm" variant="ghost" disabled={busy} onClick={onEdit}>Edit</Button><Button size="sm" variant="secondary" disabled={busy} onClick={onToggle}>{route.enabled ? "Disable" : "Enable"}</Button><Button size="sm" variant="danger" disabled={busy} onClick={onDelete}>Delete</Button></div></Card>;
}

function PolicyOptions() { return <><option value="follow_outbound">Follow outbound</option><option value="block">Block</option><option value="direct">Direct</option></>; }
function Heading() { return <div><p className="eyebrow">POLICY ROUTING</p><h2>Interface & subnet routes</h2><p>Explicit DNS, IP-family, failover, and leak-control behavior.</p></div>; }
function message(error: unknown) { return error instanceof Error ? error.message : "Request failed."; }

function formToRoute(form: FormState): EgressRoute {
  const route: EgressRoute = {
    id: form.id.trim(), name: form.name.trim(),
    source: form.sourceKind === "interface" ? { kind: "interface", interface: form.sourceValue.trim() } : { kind: "subnet", subnet: form.sourceValue.trim() },
    outbound_id: form.outboundID, failure_policy: form.failurePolicy, dns_policy: form.dnsPolicy,
    ipv4_policy: form.ipv4Policy, ipv6_policy: form.ipv6Policy, kill_switch: form.killSwitch,
    enabled: form.enabled,
  };
  if (form.failurePolicy === "failover") route.fallback_outbound_id = form.fallbackOutboundID;
  if (form.dnsPolicy === "follow_outbound") route.dns_servers = form.dnsServers.split(",").map((value) => value.trim()).filter(Boolean);
  if (form.mtu) route.mtu = Number(form.mtu);
  if (form.tcpMSS) route.tcp_mss = Number(form.tcpMSS);
  return route;
}
