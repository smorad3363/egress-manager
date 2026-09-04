import { useCallback, useEffect, useMemo, useState } from "react";
import { Badge } from "./ui/Badge";
import { Button } from "./ui/Button";
import { Card } from "./ui/Card";
import { Dialog } from "./ui/Dialog";
import { SelectField, TextField } from "./ui/Field";
import { Icon } from "./Icon";
import { Skeleton } from "./ui/Skeleton";
import { StatePanel } from "./ui/StatePanel";

type PortRange = { from: number; to: number };
type PortForward = {
  id: string;
  name: string;
  protocols: Array<"tcp" | "udp">;
  listen_address: string;
  listen_ports?: PortRange[];
  all_ports_except?: PortRange[];
  remote_address: string;
  remote_port_start?: number;
  source_cidrs?: string[];
  enabled: boolean;
};
type StoredForward = { forward: PortForward; revision: number; created_at: string; updated_at: string };
type Counter = { forward_id: string; accepted_packets: number; accepted_bytes: number; dropped_packets: number; dropped_bytes: number };
type Plan = { engine: string; owned_table: string; enabled_rules: number; actions: Array<{ kind: string; resource: string; summary: string }>; candidate: string };
type LoadState = { status: "loading" } | { status: "error"; message: string } | { status: "ready"; items: StoredForward[]; counters: Counter[] };

type FormState = {
  name: string;
  listenAddress: string;
  ports: string;
  mode: "ports" | "except";
  remoteAddress: string;
  remotePort: string;
  sourceCIDRs: string;
  tcp: boolean;
  udp: boolean;
};

const emptyForm: FormState = {
  name: "", listenAddress: "", ports: "", mode: "ports", remoteAddress: "", remotePort: "", sourceCIDRs: "", tcp: true, udp: false,
};

export function PortForwards({ createRequest }: { createRequest: number }) {
  const [state, setState] = useState<LoadState>({ status: "loading" });
  const [formOpen, setFormOpen] = useState(false);
  const [plan, setPlan] = useState<Plan | null>(null);
  const [form, setForm] = useState<FormState>(emptyForm);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [family, setFamily] = useState<"ipv4" | "ipv6">("ipv4");

  const load = useCallback(async () => {
    setState({ status: "loading" });
    try {
      const [list, counters] = await Promise.all([
        api<{ items: StoredForward[] }>("/api/v1/port-forwards?limit=100"),
        api<{ items: Counter[] }>(`/api/v1/port-forwards/counters?family=${family}`),
      ]);
      setState({ status: "ready", items: list.items, counters: counters.items });
    } catch (caught) {
      setState({ status: "error", message: message(caught) });
    }
  }, [family]);

  useEffect(() => { void load(); }, [load]);
  useEffect(() => { if (createRequest > 0) setFormOpen(true); }, [createRequest]);

  const counterByID = useMemo(() => new Map(state.status === "ready" ? state.counters.map((item) => [item.forward_id, item]) : []), [state]);

  const create = async () => {
    setBusy(true);
    setError(null);
    try {
      await api("/api/v1/port-forwards", { method: "POST", body: JSON.stringify(toForward(form)) });
      setFormOpen(false);
      setForm(emptyForm);
      await load();
    } catch (caught) {
      setError(message(caught));
    } finally {
      setBusy(false);
    }
  };

  const mutate = async (path: string, method: "POST" | "PUT" | "DELETE", body: unknown) => {
    setBusy(true);
    setError(null);
    try {
      await api(path, { method, body: JSON.stringify(body) });
      await load();
    } catch (caught) {
      setError(message(caught));
    } finally {
      setBusy(false);
    }
  };

  const preview = async () => {
    if (state.status !== "ready") return;
    setBusy(true);
    setError(null);
    try {
      const result = await api<Plan>("/api/v1/port-forwards/plan", {
        method: "POST", body: JSON.stringify({ family, forwards: state.items.map((item) => item.forward) }),
      });
      setPlan(result);
    } catch (caught) {
      setError(message(caught));
    } finally {
      setBusy(false);
    }
  };

  const apply = async () => {
    setBusy(true);
    setError(null);
    try {
      await api("/api/v1/port-forwards/apply", { method: "POST", body: JSON.stringify({ family }) });
      setPlan(null);
      await load();
    } catch (caught) {
      setError(message(caught));
    } finally {
      setBusy(false);
    }
  };

  if (state.status === "loading") return <section><div className="inventory-heading"><div><p className="eyebrow">TRANSACTIONAL NAT</p><h2>Port forwarding</h2></div></div><div className="forward-loading"><Skeleton /><Skeleton /></div></section>;
  if (state.status === "error") return <section><div className="inventory-heading"><div><p className="eyebrow">TRANSACTIONAL NAT</p><h2>Port forwarding</h2></div></div><StatePanel tone="error" title="Port forwards unavailable" description={state.message} action="Try again" onAction={() => void load()} /></section>;

  return <section>
    <div className="inventory-heading forward-heading">
      <div><p className="eyebrow">TRANSACTIONAL NAT</p><h2>Port forwarding</h2><p>Desired rules stay staged until review and atomic apply.</p></div>
      <div className="forward-actions"><select className="input family-select" aria-label="Address family" value={family} onChange={(event) => setFamily(event.target.value as "ipv4" | "ipv6")}><option value="ipv4">IPv4</option><option value="ipv6">IPv6</option></select><Button variant="secondary" disabled={busy} onClick={() => void load()}>Refresh</Button><Button variant="primary" disabled={busy} onClick={() => void preview()}>Review & apply</Button></div>
    </div>
    {error ? <div className="inventory-warning" role="alert"><Icon name="shield" /><span>{error}</span></div> : null}
    <div className="forward-summary">
      <Card><span>Configured</span><strong>{state.items.length}</strong><small>Maximum 16 per apply</small></Card>
      <Card><span>Enabled</span><strong>{state.items.filter((item) => item.forward.enabled && (family === "ipv6" ? item.forward.listen_address.includes(":") : !item.forward.listen_address.includes(":"))).length}</strong><small>{family.toUpperCase()} desired rules</small></Card>
      <Card><span>Accepted</span><strong>{formatCount(state.counters.reduce((total, item) => total + item.accepted_packets, 0))}</strong><small>Packets since last apply</small></Card>
      <Card><span>Dropped</span><strong>{formatCount(state.counters.reduce((total, item) => total + item.dropped_packets, 0))}</strong><small>Source policy rejects</small></Card>
    </div>
    {state.items.length === 0 ? <StatePanel title="No port forwards" description="Create desired NAT intent, review native changes, then apply." action="Create forward" onAction={() => setFormOpen(true)} /> :
      <Card className="forward-card"><div className="table-scroll"><table><thead><tr><th>Name</th><th>Listener</th><th>Destination</th><th>Protocol</th><th>Status</th><th className="align-right">Packets</th><th><span className="sr-only">Actions</span></th></tr></thead><tbody>
        {state.items.map((item) => {
          const forward = item.forward;
          const counters = counterByID.get(forward.id);
          return <tr key={forward.id}><td><strong className="forward-name">{forward.name}</strong><small className="pid">{forward.source_cidrs?.join(", ") || "Any source"}</small></td><td><code>{forward.listen_address}:{portLabel(forward)}</code></td><td><code>{forward.remote_address}:{forward.remote_port_start || "same"}</code></td><td>{forward.protocols.map((protocol) => <Badge key={protocol}>{protocol.toUpperCase()}</Badge>)}</td><td><Badge tone={forward.enabled ? "success" : "neutral"}>{forward.enabled ? "Enabled" : "Disabled"}</Badge></td><td className="align-right tabular">{formatCount((counters?.accepted_packets || 0) + (counters?.dropped_packets || 0))}</td><td><div className="row-actions"><Button size="sm" variant="ghost" disabled={busy} onClick={() => void mutate("/api/v1/port-forwards", "PUT", { forward: { ...forward, enabled: !forward.enabled }, expected_revision: item.revision })}>{forward.enabled ? "Disable" : "Enable"}</Button><Button size="sm" variant="ghost" disabled={busy} onClick={() => void mutate("/api/v1/port-forwards", "POST", { ...forward, id: cloneID(forward.id), name: `${forward.name} copy`, enabled: false })}>Clone</Button><Button size="sm" variant="danger" disabled={busy} onClick={() => void mutate("/api/v1/port-forwards", "DELETE", { id: forward.id, expected_revision: item.revision })}>Delete</Button></div></td></tr>;
        })}
      </tbody></table></div></Card>}

    <Dialog open={formOpen} onOpenChange={setFormOpen} title="Create port forward" description="Define desired intent. Host safety checks run during plan.">
      <form className="dialog__form" onSubmit={(event) => { event.preventDefault(); void create(); }}>
        <div className="form-grid"><TextField autoFocus required label="Name" placeholder="HTTPS relay" value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} /><TextField required label="Listen address" placeholder="203.0.113.10" value={form.listenAddress} onChange={(event) => setForm({ ...form, listenAddress: event.target.value })} /></div>
        <div className="form-grid"><SelectField label="Port mode" value={form.mode} onChange={(event) => setForm({ ...form, mode: event.target.value as FormState["mode"] })}><option value="ports">Selected ports</option><option value="except">All ports except</option></SelectField><TextField required label={form.mode === "ports" ? "Ports" : "Excluded ports"} placeholder="443, 8443-8446" value={form.ports} onChange={(event) => setForm({ ...form, ports: event.target.value })} /></div>
        <div className="form-grid"><TextField required label="Remote address" placeholder="10.10.0.5" value={form.remoteAddress} onChange={(event) => setForm({ ...form, remoteAddress: event.target.value })} /><TextField label="Remote port start" inputMode="numeric" placeholder="Same ports" value={form.remotePort} onChange={(event) => setForm({ ...form, remotePort: event.target.value })} /></div>
        <TextField label="Source CIDRs" placeholder="198.51.100.0/24, 2001:db8::/48" hint="Optional comma-separated canonical CIDRs" value={form.sourceCIDRs} onChange={(event) => setForm({ ...form, sourceCIDRs: event.target.value })} />
        <fieldset className="protocol-field"><legend>Protocols</legend><label><input type="checkbox" checked={form.tcp} onChange={(event) => setForm({ ...form, tcp: event.target.checked })} />TCP</label><label><input type="checkbox" checked={form.udp} onChange={(event) => setForm({ ...form, udp: event.target.checked })} />UDP</label></fieldset>
        {error ? <p className="field__error" role="alert">{error}</p> : null}
        <div className="dialog__actions"><Button variant="ghost" type="button" onClick={() => setFormOpen(false)}>Cancel</Button><Button variant="primary" type="submit" disabled={busy}>Save intent</Button></div>
      </form>
    </Dialog>

    <Dialog open={plan !== null} onOpenChange={(open) => { if (!open) setPlan(null); }} title="Review NAT plan" description="Only Egress Manager-owned nftables objects will change.">
      {plan ? <div className="plan-review"><div className="plan-meta"><Badge tone="success">{plan.engine}</Badge><code>{plan.owned_table}</code><span>{plan.enabled_rules} enabled</span></div><ol>{plan.actions.map((action, index) => <li key={`${action.kind}-${action.resource}-${index}`}><strong>{action.kind}</strong><span>{action.summary}</span><code>{action.resource}</code></li>)}</ol><details><summary>Generated native changes</summary><pre>{plan.candidate}</pre></details><div className="dialog__actions"><Button variant="ghost" onClick={() => setPlan(null)}>Cancel</Button><Button variant="primary" disabled={busy} onClick={() => void apply()}>Apply atomically</Button></div></div> : null}
    </Dialog>
  </section>;
}

async function api<T = unknown>(path: string, init?: RequestInit): Promise<T> {
  const headers = new Headers(init?.headers);
  if (init?.body) headers.set("Content-Type", "application/json");
  if (init?.method && !["GET", "HEAD"].includes(init.method)) {
    const token = window.sessionStorage.getItem("egress.csrf") || document.querySelector<HTMLMetaElement>('meta[name="csrf-token"]')?.content;
    if (token) headers.set("X-CSRF-Token", token);
  }
  const response = await fetch(path, { ...init, headers, credentials: "same-origin" });
  if (!response.ok) {
    const body = await response.json().catch(() => null) as { message?: string } | null;
    throw new Error(body?.message || (response.status === 401 ? "Sign in to manage port forwards." : "Request failed."));
  }
  if (response.status === 204) return undefined as T;
  return response.json() as Promise<T>;
}

function toForward(form: FormState): PortForward {
  const ranges = parseRanges(form.ports);
  const protocols = ([form.tcp && "tcp", form.udp && "udp"].filter(Boolean)) as Array<"tcp" | "udp">;
  if (protocols.length === 0) throw new Error("Select TCP, UDP, or both.");
  const idBase = form.name.toLowerCase().replace(/[^a-z0-9]+/g, "_").replace(/^_+|_+$/g, "") || "forward";
  const suffix = Date.now().toString(36);
  return {
    id: `${idBase.slice(0, 63-suffix.length)}_${suffix}`, name: form.name.trim(), protocols,
    listen_address: form.listenAddress.trim(), ...(form.mode === "ports" ? { listen_ports: ranges } : { all_ports_except: ranges }),
    remote_address: form.remoteAddress.trim(), ...(form.remotePort ? { remote_port_start: Number(form.remotePort) } : {}),
    source_cidrs: form.sourceCIDRs.split(",").map((value) => value.trim()).filter(Boolean), enabled: true,
  };
}

function parseRanges(value: string): PortRange[] {
  const ranges = value.split(",").map((item) => item.trim()).filter(Boolean).map((item) => {
    const [fromText, toText = fromText] = item.split("-").map((part) => part.trim());
    const from = Number(fromText); const to = Number(toText);
    if (!Number.isInteger(from) || !Number.isInteger(to) || from < 1 || to > 65535 || from > to) throw new Error(`Invalid port range: ${item}`);
    return { from, to };
  });
  if (ranges.length === 0) throw new Error("Enter at least one port or range.");
  return ranges;
}

function cloneID(id: string) { const suffix = `_copy_${Date.now().toString(36)}`; return `${id.slice(0, 64-suffix.length)}${suffix}`; }
function portLabel(forward: PortForward) { const ranges = forward.listen_ports || forward.all_ports_except || []; return `${forward.all_ports_except ? "except " : ""}${ranges.map((range) => range.from === range.to ? range.from : `${range.from}-${range.to}`).join(", ")}`; }
function formatCount(value: number) { return new Intl.NumberFormat("en", { notation: value >= 10_000 ? "compact" : "standard" }).format(value); }
function message(error: unknown) { return error instanceof Error ? error.message : "Request failed."; }
