import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { api } from "../lib/api";
import { Icon } from "./Icon";
import { Badge } from "./ui/Badge";
import { Button } from "./ui/Button";
import { Card } from "./ui/Card";
import { Dialog } from "./ui/Dialog";
import { SelectField, TextField } from "./ui/Field";
import { Skeleton } from "./ui/Skeleton";
import { StatePanel } from "./ui/StatePanel";

type Installation = {
  kind: "standalone" | "marzban" | "3x-ui"; config_path: string; config_hash: string; config_bytes: number;
  xray_executable?: string; xray_version?: string; service_name: string; service_loaded: boolean; service_active: boolean;
  loader: string; managed_path?: string; inbound_tags: string[]; outbound_tags: string[]; ownership: "foreign";
  mutation_strategy: "read_only" | "managed_fragment"; limitations: string[];
};
type Discovery = { installations: Installation[]; warnings: string[] };
type Binding = { id: string; inbound_tag: string; outbound_tag: string; enabled: boolean };
type StoredBinding = { binding: Binding; revision: number; created_at?: string; updated_at?: string };
type Action = { kind: string; resource: string; summary: string };
type Review = {
  engine: string; service_name: string; managed_path: string; foreign_state_hash: string; fragment_state_hash: string;
  candidate_hash: string; candidate_exists: boolean; enabled_bindings: number; actions: Action[];
};

const emptyBinding: Binding = { id: "", inbound_tag: "", outbound_tag: "", enabled: true };

export function XrayManager({ createRequest }: { createRequest: number }) {
  const [discovery, setDiscovery] = useState<Discovery | null>(null);
  const [bindings, setBindings] = useState<StoredBinding[] | null>(null);
  const [form, setForm] = useState<Binding>(emptyBinding);
  const [editing, setEditing] = useState<StoredBinding | null>(null);
  const [editorOpen, setEditorOpen] = useState(false);
  const [review, setReview] = useState<Review | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const handledCreateRequest = useRef(0);

  const load = useCallback(async () => {
    setError(null);
    try {
      const [discovered, saved] = await Promise.all([
        api<Discovery>("/api/v1/xray/discovery"),
        api<{ items: StoredBinding[] }>("/api/v1/xray/bindings?limit=256"),
      ]);
      setDiscovery(discovered); setBindings(saved.items);
    } catch (caught) { setError(message(caught)); setDiscovery({ installations: [], warnings: [] }); setBindings([]); }
  }, []);
  const writable = useMemo(() => discovery?.installations.find((item) => item.mutation_strategy === "managed_fragment"), [discovery]);
  useEffect(() => { void load(); }, [load]);
  useEffect(() => {
    if (createRequest <= handledCreateRequest.current) return;
    handledCreateRequest.current = createRequest;
    if (!writable) { setError("No standalone Xray installation has a proven managed-fragment boundary."); return; }
    setEditing(null); setForm({ ...emptyBinding, inbound_tag: writable.inbound_tags[0] || "", outbound_tag: writable.outbound_tags[0] || "" }); setEditorOpen(true);
  }, [createRequest, writable]);

  const run = async (work: () => Promise<void>) => {
    setBusy(true); setError(null);
    try { await work(); } catch (caught) { setError(message(caught)); } finally { setBusy(false); }
  };
  const openCreate = () => {
    if (!writable) { setError("No standalone Xray installation has a proven managed-fragment boundary."); return; }
    setEditing(null); setForm({ ...emptyBinding, inbound_tag: writable.inbound_tags[0] || "", outbound_tag: writable.outbound_tags[0] || "" }); setEditorOpen(true);
  };
  const openEdit = (item: StoredBinding) => { setEditing(item); setForm({ ...item.binding }); setEditorOpen(true); };
  const save = () => void run(async () => {
    if (editing) await api("/api/v1/xray/bindings", { method: "PUT", body: JSON.stringify({ binding: form, expected_revision: editing.revision }) });
    else await api("/api/v1/xray/bindings", { method: "POST", body: JSON.stringify(form) });
    setEditorOpen(false); setReview(null); await load();
  });
  const toggle = (item: StoredBinding) => void run(async () => {
    await api("/api/v1/xray/bindings", { method: "PUT", body: JSON.stringify({ binding: { ...item.binding, enabled: !item.binding.enabled }, expected_revision: item.revision }) });
    setReview(null); await load();
  });
  const remove = (item: StoredBinding) => {
    if (!window.confirm(`Delete ${item.binding.id}?`)) return;
    void run(async () => { await api("/api/v1/xray/bindings", { method: "DELETE", body: JSON.stringify({ id: item.binding.id, expected_revision: item.revision }) }); setReview(null); await load(); });
  };
  const preview = () => void run(async () => setReview(await api<Review>("/api/v1/xray/plan", { method: "POST", body: "{}" })));
  const apply = () => void run(async () => {
    if (!review) return;
    await api("/api/v1/xray/apply", { method: "POST", body: JSON.stringify({
      expected_foreign_state_hash: review.foreign_state_hash,
      expected_fragment_state_hash: review.fragment_state_hash,
      expected_candidate_hash: review.candidate_hash,
    }) });
    setReview(null); await load();
  });

  if (discovery === null || bindings === null) return <section><Heading /><div className="forward-loading"><Skeleton /><Skeleton /></div></section>;
  const enabled = bindings.filter((item) => item.binding.enabled).length;
  return <section>
    <div className="inventory-heading forward-heading"><Heading /><div className="forward-actions"><Button variant="secondary" disabled={busy || !writable} onClick={openCreate}>New binding</Button><Button variant="primary" disabled={busy || !writable} onClick={preview}>Review & apply</Button></div></div>
    {error ? <div className="inventory-warning" role="alert"><Icon name="shield" /><span>{error}</span></div> : null}
    {discovery.warnings.map((warning) => <div className="inventory-warning" role="status" key={warning}><Icon name="activity" /><span>{warning}</span></div>)}
    <div className="forward-summary"><Card><span>Installations</span><strong>{discovery.installations.length}</strong><small>Detected safely</small></Card><Card><span>Writable</span><strong>{writable ? 1 : 0}</strong><small>Proven confdir</small></Card><Card><span>Bindings</span><strong>{bindings.length}</strong><small>Saved policies</small></Card><Card><span>Enabled</span><strong>{enabled}</strong><small>Native routes</small></Card></div>

    <div className="xray-layout">
      <div className="xray-installations">
        <header className="card-header"><div><p className="eyebrow">DISCOVERY</p><h3>Compatible installations</h3></div><Button size="sm" variant="ghost" disabled={busy} onClick={() => void load()}>Refresh</Button></header>
        {discovery.installations.length === 0 ? <Card><StatePanel title="No Xray service detected" description="The Xray runtime bundled with Egress Manager is installed for validation and integration. This page manages an existing standalone Xray, Marzban, or 3x-ui service when one is found." action="Scan again" onAction={() => void load()} /></Card> : discovery.installations.map((installation) => <InstallationCard key={`${installation.kind}-${installation.config_path}`} installation={installation} />)}
      </div>
      <Card className="forward-card xray-bindings">
        <header className="card-header"><div><p className="eyebrow">NATIVE ROUTING</p><h3>Inbound bindings</h3></div><Badge tone={writable ? "success" : "warning"}>{writable ? "Managed fragment" : "Read only"}</Badge></header>
        {bindings.length === 0 ? <StatePanel title="No native bindings" description={writable ? "Bind one discovered inbound tag directly to an Xray outbound tag." : "Bindings require a proven standalone confdir. Panel-managed configurations remain read-only."} action={writable ? "Create binding" : "Scan again"} onAction={writable ? openCreate : () => void load()} /> : <div className="table-scroll"><table><thead><tr><th>Binding</th><th>Inbound</th><th>Outbound</th><th>Status</th><th /></tr></thead><tbody>{bindings.map((item) => <tr key={item.binding.id}><td><strong className="forward-name">{item.binding.id}</strong><small className="pid">REV {item.revision}</small></td><td><code>{item.binding.inbound_tag}</code></td><td><code>{item.binding.outbound_tag}</code></td><td><Badge tone={item.binding.enabled ? "success" : "neutral"}>{item.binding.enabled ? "Enabled" : "Disabled"}</Badge></td><td><div className="row-actions"><Button size="sm" variant="ghost" disabled={busy} onClick={() => openEdit(item)}>Edit</Button><Button size="sm" variant="secondary" disabled={busy} onClick={() => toggle(item)}>{item.binding.enabled ? "Disable" : "Enable"}</Button><Button size="sm" variant="danger" disabled={busy} onClick={() => remove(item)}>Delete</Button></div></td></tr>)}</tbody></table></div>}
      </Card>
    </div>

    <Dialog open={editorOpen} onOpenChange={setEditorOpen} title={editing ? "Edit Xray binding" : "Create Xray binding"} description="Traffic stays inside Xray: one inboundTag is routed directly to one outboundTag.">
      <form className="dialog__form" onSubmit={(event) => { event.preventDefault(); save(); }}>
        <TextField autoFocus label="Binding ID" required disabled={editing !== null} value={form.id} onChange={(event) => setForm({ ...form, id: event.target.value })} placeholder="vless_to_proxy" />
        <div className="form-grid"><SelectField label="Inbound tag" required value={form.inbound_tag} onChange={(event) => setForm({ ...form, inbound_tag: event.target.value })}><option value="" disabled>Select inbound</option>{writable?.inbound_tags.map((tag) => <option key={tag}>{tag}</option>)}</SelectField><SelectField label="Outbound tag" required value={form.outbound_tag} onChange={(event) => setForm({ ...form, outbound_tag: event.target.value })}><option value="" disabled>Select outbound</option>{writable?.outbound_tags.map((tag) => <option key={tag}>{tag}</option>)}</SelectField></div>
        <div className="route-toggles"><label><input type="checkbox" checked={form.enabled} onChange={(event) => setForm({ ...form, enabled: event.target.checked })} /> Enabled</label></div>
        <div className="dialog__actions"><Button type="button" variant="ghost" onClick={() => setEditorOpen(false)}>Cancel</Button><Button type="submit" variant="primary" disabled={busy}>Save binding</Button></div>
      </form>
    </Dialog>
    <Dialog open={review !== null} onOpenChange={(open) => { if (!open) setReview(null); }} title="Review native Xray plan" description="Only owned actions and authenticated hashes are reviewed. Foreign configuration and the candidate remain private.">
      {review ? <div className="plan-review"><div className="plan-meta"><Badge tone="warning">Service restart</Badge><span>{review.enabled_bindings} enabled bindings · {review.candidate_exists ? "install fragment" : "remove fragment"}</span></div><ol>{review.actions.map((action, index) => <li key={`${action.kind}-${action.resource}-${index}`}><strong>{action.kind}</strong><span>{action.summary}</span><code>{action.resource}</code></li>)}</ol><div className="notice"><Icon name="shield" /><p><strong>Foreign files stay untouched</strong><span>Apply aborts if the confdir or owned fragment changed since this review.</span></p></div><div className="dialog__actions"><Button variant="ghost" onClick={() => setReview(null)}>Cancel</Button><Button variant="primary" disabled={busy} onClick={apply}>Apply & restart Xray</Button></div></div> : null}
    </Dialog>
  </section>;
}

function InstallationCard({ installation }: { installation: Installation }) {
  const writable = installation.mutation_strategy === "managed_fragment";
  return <Card className="xray-installation"><header><div><p className="eyebrow">{installation.kind.toUpperCase()}</p><h3>{installation.service_name}</h3></div><Badge tone={writable ? "success" : "warning"}>{writable ? "Managed fragment" : "Read only"}</Badge></header><code className="xray-path">{installation.config_path}</code><dl><div><dt>Service</dt><dd>{installation.service_loaded ? (installation.service_active ? "Active" : "Loaded") : "Unavailable"}</dd></div><div><dt>Xray</dt><dd>{installation.xray_version || "Version unavailable"}</dd></div><div><dt>Inbounds</dt><dd>{installation.inbound_tags.length}</dd></div><div><dt>Outbounds</dt><dd>{installation.outbound_tags.length}</dd></div></dl><div className="xray-limitations">{installation.limitations.map((value) => <p key={value}><Icon name="shield" />{value}</p>)}</div></Card>;
}

function Heading() { return <div><p className="eyebrow">XRAY ADAPTER</p><h2>Native Xray routing</h2><p>Discover Xray, Marzban, and 3x-ui; bind tags without double proxying.</p></div>; }
function message(error: unknown) { return error instanceof Error ? error.message : "Request failed."; }
