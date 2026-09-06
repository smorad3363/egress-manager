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

type RelayNetwork = "tcp" | "udp" | "tcp,udp";
type Relay = {
  id: string;
  name: string;
  listen_address: string;
  listen_port: number;
  network: RelayNetwork;
  destination: { host: string; port: number };
  outbound_id: string;
  source_cidrs?: string[];
  enabled: boolean;
};
type StoredRelay = { relay: Relay; revision: number; created_at?: string; updated_at?: string };
type Outbound = {
  id: string;
  name: string;
  adapter: string;
  type: string;
  enabled: boolean;
  capabilities: { tcp: boolean; udp: boolean };
  health: { status: string };
};
type StoredOutbound = { outbound: Outbound; revision: number };
type RelayAction = { kind: string; resource: string; summary: string };
type RelayPlan = {
  engine: string;
  state_hash: string;
  candidate_hash: string;
  candidate_exists: boolean;
  enabled_relays: number;
  enabled_outbounds: number;
  actions: RelayAction[];
};
type FormState = {
  id: string;
  name: string;
  listenAddress: string;
  listenPort: string;
  network: RelayNetwork;
  destinationHost: string;
  destinationPort: string;
  outboundID: string;
  sourceCIDRs: string;
  enabled: boolean;
};

const emptyForm: FormState = {
  id: "",
  name: "",
  listenAddress: "0.0.0.0",
  listenPort: "",
  network: "tcp",
  destinationHost: "",
  destinationPort: "",
  outboundID: "",
  sourceCIDRs: "",
  enabled: true,
};

export function RelaysManager({ createRequest }: { createRequest: number }) {
  const [relays, setRelays] = useState<StoredRelay[] | null>(null);
  const [outbounds, setOutbounds] = useState<StoredOutbound[]>([]);
  const [editing, setEditing] = useState<StoredRelay | null>(null);
  const [form, setForm] = useState<FormState>(emptyForm);
  const [editorOpen, setEditorOpen] = useState(false);
  const [review, setReview] = useState<RelayPlan | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setError(null);
    try {
      const [relayResponse, outboundResponse] = await Promise.all([
        api<{ items: StoredRelay[] }>("/api/v1/relays?limit=100"),
        api<{ items: StoredOutbound[] }>("/api/v1/outbounds?limit=128"),
      ]);
      setRelays(relayResponse.items);
      setOutbounds(outboundResponse.items);
    } catch (caught) {
      setError(message(caught));
      setRelays([]);
    }
  }, []);

  useEffect(() => { void load(); }, [load]);
  useEffect(() => { if (createRequest > 0) openCreate(); }, [createRequest]);

  const xrayOutbounds = useMemo(() => outbounds.filter((item) => item.outbound.adapter === "xray"), [outbounds]);
  const enabledRelays = useMemo(() => (relays || []).filter((item) => item.relay.enabled).length, [relays]);
  const restrictedRelays = useMemo(() => (relays || []).filter((item) => (item.relay.source_cidrs || []).length > 0).length, [relays]);

  const run = async (work: () => Promise<void>) => {
    setBusy(true);
    setError(null);
    try { await work(); } catch (caught) { setError(message(caught)); } finally { setBusy(false); }
  };

  const openCreate = () => {
    setEditing(null);
    setForm(emptyForm);
    setEditorOpen(true);
  };

  const openEdit = (item: StoredRelay) => {
    const relay = item.relay;
    setEditing(item);
    setForm({
      id: relay.id,
      name: relay.name,
      listenAddress: relay.listen_address,
      listenPort: String(relay.listen_port),
      network: relay.network,
      destinationHost: relay.destination.host,
      destinationPort: String(relay.destination.port),
      outboundID: relay.outbound_id,
      sourceCIDRs: (relay.source_cidrs || []).join(", "),
      enabled: relay.enabled,
    });
    setEditorOpen(true);
  };

  const save = () => void run(async () => {
    const relay = formToRelay(form);
    if (editing) {
      await api("/api/v1/relays", { method: "PUT", body: JSON.stringify({ relay, expected_revision: editing.revision }) });
    } else {
      await api("/api/v1/relays", { method: "POST", body: JSON.stringify(relay) });
    }
    setEditorOpen(false);
    await load();
  });

  const toggle = (item: StoredRelay) => void run(async () => {
    await api("/api/v1/relays", {
      method: "PUT",
      body: JSON.stringify({ relay: { ...item.relay, enabled: !item.relay.enabled }, expected_revision: item.revision }),
    });
    await load();
  });

  const remove = (item: StoredRelay) => {
    if (!window.confirm(`Delete ${item.relay.name}?`)) return;
    void run(async () => {
      await api("/api/v1/relays", { method: "DELETE", body: JSON.stringify({ id: item.relay.id, expected_revision: item.revision }) });
      await load();
    });
  };

  const preview = () => void run(async () => setReview(await api<RelayPlan>("/api/v1/relays/plan", { method: "POST", body: "{}" })));
  const apply = () => void run(async () => {
    if (!review) return;
    await api("/api/v1/relays/apply", {
      method: "POST",
      body: JSON.stringify({ expected_state_hash: review.state_hash, expected_candidate_hash: review.candidate_hash }),
    });
    setReview(null);
    await load();
  });

  if (relays === null) return <section><Heading /><div className="forward-loading"><Skeleton /><Skeleton /></div></section>;

  return <section>
    <div className="inventory-heading forward-heading">
      <Heading />
      <div className="forward-actions">
        <Button variant="secondary" onClick={openCreate}>New relay</Button>
        <Button variant="primary" disabled={busy} onClick={preview}>Review & apply</Button>
      </div>
    </div>

    {error ? <div className="inventory-warning" role="alert"><Icon name="shield" /><span>{error}</span></div> : null}

    <div className="forward-summary">
      <Card><span>Total</span><strong>{relays.length}</strong><small>Saved relays</small></Card>
      <Card><span>Enabled</span><strong>{enabledRelays}</strong><small>Desired listeners</small></Card>
      <Card><span>Xray outbounds</span><strong>{xrayOutbounds.filter((item) => item.outbound.enabled).length}</strong><small>Enabled relay targets</small></Card>
      <Card><span>Source restricted</span><strong>{restrictedRelays}</strong><small>CIDR allow-list enabled</small></Card>
    </div>

    {relays.length === 0 ? <Card className="forward-card"><StatePanel
      title="No listener relays"
      description={xrayOutbounds.length === 0 ? "Import a supported VLESS REALITY/XHTTP outbound first, then create a fixed listener-to-destination relay." : "Create a listener that sends traffic to a fixed destination through a selected Xray outbound."}
      action="Create relay"
      onAction={openCreate}
    /></Card> : <div className="outbound-grid">{relays.map((item) => <RelayCard
      key={item.relay.id}
      item={item}
      outbounds={outbounds}
      busy={busy}
      onEdit={() => openEdit(item)}
      onToggle={() => toggle(item)}
      onDelete={() => remove(item)}
    />)}</div>}

    <Dialog open={editorOpen} onOpenChange={setEditorOpen} title={editing ? "Edit listener relay" : "Create listener relay"} description="Traffic accepted on this listener reaches only the fixed destination through the selected Xray outbound. There is no direct fallback.">
      <form className="dialog__form" onSubmit={(event) => { event.preventDefault(); save(); }}>
        <div className="form-grid">
          <TextField autoFocus label="Relay ID" required disabled={editing !== null} value={form.id} onChange={(event) => setForm({ ...form, id: event.target.value })} placeholder="ssh_gateway" />
          <TextField label="Display name" required value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} placeholder="SSH gateway" />
        </div>
        <div className="form-grid">
          <TextField label="Listen address" required value={form.listenAddress} onChange={(event) => setForm({ ...form, listenAddress: event.target.value })} hint="Canonical IP address, for example 0.0.0.0" />
          <TextField label="Listen port" type="number" min="1" max="65535" required value={form.listenPort} onChange={(event) => setForm({ ...form, listenPort: event.target.value })} />
        </div>
        <div className="form-grid">
          <SelectField label="Network" value={form.network} onChange={(event) => setForm({ ...form, network: event.target.value as RelayNetwork })}>
            <option value="tcp">TCP</option>
            <option value="udp">UDP</option>
            <option value="tcp,udp">TCP + UDP</option>
          </SelectField>
          <SelectField label="Selected outbound" required value={form.outboundID} onChange={(event) => setForm({ ...form, outboundID: event.target.value })}>
            <option value="" disabled>Select Xray outbound</option>
            {xrayOutbounds.map(({ outbound }) => <option key={outbound.id} value={outbound.id} disabled={!outbound.enabled || !supportsNetwork(outbound, form.network)}>{outbound.name} · {outbound.type}{outbound.enabled ? "" : " · disabled"}</option>)}
          </SelectField>
        </div>
        <div className="form-grid">
          <TextField label="Destination host" required value={form.destinationHost} onChange={(event) => setForm({ ...form, destinationHost: event.target.value })} placeholder="91.107.220.12" />
          <TextField label="Destination port" type="number" min="1" max="65535" required value={form.destinationPort} onChange={(event) => setForm({ ...form, destinationPort: event.target.value })} />
        </div>
        <TextField label="Source CIDRs" value={form.sourceCIDRs} onChange={(event) => setForm({ ...form, sourceCIDRs: event.target.value })} hint="Optional comma-separated allow-list. All other sources are blocked." placeholder="198.51.100.0/24" />
        <div className="route-toggles"><label><input type="checkbox" checked={form.enabled} onChange={(event) => setForm({ ...form, enabled: event.target.checked })} /> Enabled</label></div>
        <div className="dialog__actions"><Button type="button" variant="ghost" onClick={() => setEditorOpen(false)}>Cancel</Button><Button type="submit" variant="primary" disabled={busy || xrayOutbounds.length === 0}>Save relay</Button></div>
      </form>
    </Dialog>

    <Dialog open={review !== null} onOpenChange={(open) => { if (!open) setReview(null); }} title="Review listener relay plan" description="Only safe actions and authenticated hashes are shown. Xray credentials and the generated candidate stay inside egressd.">
      {review ? <div className="plan-review">
        <div className="plan-meta"><Badge tone="warning">Atomic mutation</Badge><span>{review.enabled_relays} relays · {review.enabled_outbounds} outbounds</span><code>{shortHash(review.candidate_hash)}</code></div>
        {review.actions.length === 0 ? <StatePanel title="No runtime changes" description="Saved relay state already matches the project-owned Xray relay runtime." action="Close" onAction={() => setReview(null)} /> : <ol>{review.actions.map((action, index) => <li key={`${action.kind}-${action.resource}-${index}`}><strong>{action.kind}</strong><span>{action.summary}</span><code>{action.resource}</code></li>)}</ol>}
        <div className="dialog__actions"><Button variant="ghost" onClick={() => setReview(null)}>Cancel</Button><Button variant="primary" disabled={busy} onClick={apply}>Apply atomically</Button></div>
      </div> : null}
    </Dialog>
  </section>;
}

function RelayCard({ item, outbounds, busy, onEdit, onToggle, onDelete }: { item: StoredRelay; outbounds: StoredOutbound[]; busy: boolean; onEdit: () => void; onToggle: () => void; onDelete: () => void }) {
  const relay = item.relay;
  const outbound = outbounds.find((candidate) => candidate.outbound.id === relay.outbound_id)?.outbound;
  const sources = relay.source_cidrs || [];
  return <Card className="outbound-card route-card">
    <header><div><p className="eyebrow">{relay.network.toUpperCase()} · REV {item.revision}</p><h3>{relay.name}</h3><code>{relay.listen_address}:{relay.listen_port}</code></div><Badge tone={relay.enabled ? "success" : "neutral"}>{relay.enabled ? "Enabled" : "Disabled"}</Badge></header>
    <dl>
      <div><dt>Fixed destination</dt><dd><code>{relay.destination.host}:{relay.destination.port}</code></dd></div>
      <div><dt>Selected outbound</dt><dd>{outbound?.name || relay.outbound_id}</dd></div>
      <div><dt>Adapter</dt><dd>{outbound ? `${outbound.adapter} · ${outbound.type}` : "Unavailable"}</dd></div>
      <div><dt>Source policy</dt><dd>{sources.length > 0 ? `${sources.length} allowed CIDRs` : "All sources"}</dd></div>
    </dl>
    {sources.length > 0 ? <p className="field__hint"><code>{sources.join(", ")}</code></p> : null}
    <div className="row-actions"><Button size="sm" variant="ghost" disabled={busy} onClick={onEdit}>Edit</Button><Button size="sm" variant="secondary" disabled={busy} onClick={onToggle}>{relay.enabled ? "Disable" : "Enable"}</Button><Button size="sm" variant="danger" disabled={busy} onClick={onDelete}>Delete</Button></div>
  </Card>;
}

function Heading() {
  return <div><p className="eyebrow">PROJECT-OWNED XRAY RELAY</p><h2>Listener relays</h2><p>Listen on a fixed port and reach one fixed destination through a selected managed Xray outbound, with fail-closed behavior.</p></div>;
}

function formToRelay(form: FormState): Relay {
  const sourceCIDRs = form.sourceCIDRs.split(",").map((value) => value.trim()).filter(Boolean);
  return {
    id: form.id.trim(),
    name: form.name.trim(),
    listen_address: form.listenAddress.trim(),
    listen_port: Number(form.listenPort),
    network: form.network,
    destination: { host: form.destinationHost.trim(), port: Number(form.destinationPort) },
    outbound_id: form.outboundID,
    source_cidrs: sourceCIDRs,
    enabled: form.enabled,
  };
}

function supportsNetwork(outbound: Outbound, network: RelayNetwork) {
  const needsTCP = network === "tcp" || network === "tcp,udp";
  const needsUDP = network === "udp" || network === "tcp,udp";
  return (!needsTCP || outbound.capabilities.tcp) && (!needsUDP || outbound.capabilities.udp);
}
function shortHash(value: string) { return value ? `${value.slice(0, 12)}…` : "pending"; }
function message(value: unknown) { return value instanceof Error ? value.message : "Request failed."; }
