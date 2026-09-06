import { useCallback, useEffect, useState } from "react";
import { api } from "../lib/api";
import { Icon } from "./Icon";
import { Badge } from "./ui/Badge";
import { Button } from "./ui/Button";
import { Card } from "./ui/Card";
import { Skeleton } from "./ui/Skeleton";
import { StatePanel } from "./ui/StatePanel";

type HealthDocument = { status: string; version: string; checks?: Record<string, string> };
type Route = {
  id: string;
  name: string;
  source: { kind: "interface"; interface: string } | { kind: "subnet"; subnet: string };
  outbound_id: string;
  enabled: boolean;
};
type StoredRoute = { route: Route; revision: number };
type Outbound = { id: string; name: string; enabled: boolean; health: { status: string } };
type StoredOutbound = { outbound: Outbound; revision: number };
type StoredForward = { forward: { id: string; name: string; enabled: boolean }; revision: number };
type Counter = { forward_id: string; accepted_packets: number; accepted_bytes: number; dropped_packets: number; dropped_bytes: number };
type CounterSnapshot = { items: Counter[] };
type HAProxyStats = { info?: { version?: string; pid?: number; current_connections?: number }; stats?: Array<{ status?: string }> };

type Snapshot = {
  health: HealthDocument | null;
  routes: StoredRoute[] | null;
  outbounds: StoredOutbound[] | null;
  forwards: StoredForward[] | null;
  counters4: Counter[] | null;
  counters6: Counter[] | null;
  haproxy: HAProxyStats | null;
  failures: string[];
  refreshedAt: Date;
};

type LoadState = { status: "loading" } | { status: "error"; message: string } | { status: "ready"; snapshot: Snapshot };

type DashboardOverviewProps = {
  onOpenRoutes: () => void;
  onCreateRoute: () => void;
  onOpenNetwork: () => void;
};

export function DashboardOverview({ onOpenRoutes, onCreateRoute, onOpenNetwork }: DashboardOverviewProps) {
  const [state, setState] = useState<LoadState>({ status: "loading" });

  const load = useCallback(async () => {
    setState({ status: "loading" });
    const [health, routes, outbounds, forwards, counters4, counters6, haproxy] = await Promise.all([
      settle<HealthDocument>("health", "/api/v1/control/health"),
      settle<{ items: StoredRoute[] }>("routes", "/api/v1/routes?limit=100"),
      settle<{ items: StoredOutbound[] }>("outbounds", "/api/v1/outbounds?limit=100"),
      settle<{ items: StoredForward[] }>("port forwards", "/api/v1/port-forwards?limit=100"),
      settle<CounterSnapshot>("IPv4 counters", "/api/v1/port-forwards/counters?family=ipv4"),
      settle<CounterSnapshot>("IPv6 counters", "/api/v1/port-forwards/counters?family=ipv6"),
      settle<HAProxyStats>("HAProxy", "/api/v1/haproxy/stats"),
    ]);

    const results = [health, routes, outbounds, forwards, counters4, counters6, haproxy];
    const successful = results.filter((result) => result.ok).length;
    if (successful === 0) {
      setState({ status: "error", message: "The server did not return live dashboard data." });
      return;
    }

    setState({
      status: "ready",
      snapshot: {
        health: health.ok ? health.value : null,
        routes: routes.ok ? routes.value.items : null,
        outbounds: outbounds.ok ? outbounds.value.items : null,
        forwards: forwards.ok ? forwards.value.items : null,
        counters4: counters4.ok ? counters4.value.items : null,
        counters6: counters6.ok ? counters6.value.items : null,
        haproxy: haproxy.ok ? haproxy.value : null,
        failures: results.filter((result) => !result.ok).map((result) => result.label),
        refreshedAt: new Date(),
      },
    });
  }, []);

  useEffect(() => { void load(); }, [load]);

  if (state.status === "loading") {
    return <section aria-label="Live server overview"><div className="page-heading"><div><p className="eyebrow">LIVE SERVER DATA</p><h2>Loading current server data…</h2><p>Reading saved configuration and current runtime status.</p></div></div><div className="forward-loading"><Skeleton /><Skeleton /><Skeleton /></div></section>;
  }
  if (state.status === "error") {
    return <section><StatePanel tone="error" title="Live dashboard unavailable" description={state.message} action="Try again" onAction={() => void load()} /></section>;
  }

  const snapshot = state.snapshot;
  const routes = snapshot.routes ?? [];
  const outbounds = snapshot.outbounds ?? [];
  const forwards = snapshot.forwards ?? [];
  const counters = [...(snapshot.counters4 ?? []), ...(snapshot.counters6 ?? [])];
  const enabledRoutes = routes.filter((item) => item.route.enabled).length;
  const enabledOutbounds = outbounds.filter((item) => item.outbound.enabled).length;
  const healthyOutbounds = outbounds.filter((item) => item.outbound.health.status === "healthy").length;
  const enabledForwards = forwards.filter((item) => item.forward.enabled).length;
  const totals = counters.reduce((current, item) => ({
    acceptedPackets: current.acceptedPackets + item.accepted_packets,
    acceptedBytes: current.acceptedBytes + item.accepted_bytes,
    droppedPackets: current.droppedPackets + item.dropped_packets,
    droppedBytes: current.droppedBytes + item.dropped_bytes,
  }), { acceptedPackets: 0, acceptedBytes: 0, droppedPackets: 0, droppedBytes: 0 });
  const outboundNames = new Map(outbounds.map((item) => [item.outbound.id, item.outbound.name]));
  const overallHealthy = snapshot.health?.status === "ok";

  return <section aria-labelledby="overview-title">
    <div className="page-heading">
      <div>
        <p className="eyebrow">LIVE SERVER DATA</p>
        <h2 id="overview-title">Current server overview</h2>
        <p>Everything on this page comes from the server API. No sample traffic, routes, or service values are shown.</p>
      </div>
      <div className="page-heading__meta"><span>Last update</span><strong>{formatTime(snapshot.refreshedAt)}</strong><Button size="sm" variant="ghost" onClick={() => void load()}>Refresh</Button></div>
    </div>

    {snapshot.failures.length > 0 ? <div className="inventory-warning" role="status"><Icon name="activity" /><span>Some live data could not be loaded: {snapshot.failures.join(", ")}.</span></div> : null}

    <div className="forward-summary" aria-label="Current configuration summary">
      <SummaryCard label="Routes" value={snapshot.routes ? `${enabledRoutes} / ${routes.length}` : "—"} detail="enabled / saved" />
      <SummaryCard label="Outbounds" value={snapshot.outbounds ? `${enabledOutbounds} / ${outbounds.length}` : "—"} detail={snapshot.outbounds ? `${healthyOutbounds} healthy` : "unavailable"} />
      <SummaryCard label="Port forwards" value={snapshot.forwards ? `${enabledForwards} / ${forwards.length}` : "—"} detail="enabled / saved" />
      <SummaryCard label="Forwarded traffic" value={snapshot.counters4 || snapshot.counters6 ? formatBytes(totals.acceptedBytes) : "—"} detail={snapshot.counters4 || snapshot.counters6 ? `${formatNumber(totals.acceptedPackets)} accepted packets` : "counters unavailable"} />
    </div>

    <div className="dashboard-grid">
      <Card className="health-card">
        <div className="card-header"><div><p className="eyebrow">SERVER</p><h3>Runtime status</h3></div><Button size="sm" variant="ghost" onClick={onOpenNetwork}>View network</Button></div>
        <ul className="service-list">
          <StatusRow name="Control service" detail={snapshot.health ? `version ${snapshot.health.version || "unknown"}` : "status unavailable"} status={snapshot.health ? (overallHealthy ? "healthy" : "attention") : "unavailable"} />
          <StatusRow name="Database" detail={snapshot.health?.checks?.database === "ok" ? "connected" : snapshot.health?.checks?.database || "reported by control service"} status={snapshot.health?.checks?.database === "failed" ? "attention" : snapshot.health ? "healthy" : "unavailable"} />
          <StatusRow name="HAProxy" detail={snapshot.haproxy?.info?.pid ? `PID ${snapshot.haproxy.info.pid}${snapshot.haproxy.info.version ? ` · ${snapshot.haproxy.info.version}` : ""}` : snapshot.haproxy ? "runtime reachable" : "not running or unavailable"} status={snapshot.haproxy ? "healthy" : "unavailable"} />
          <StatusRow name="Outbound health" detail={snapshot.outbounds ? `${healthyOutbounds} healthy of ${outbounds.length} saved` : "status unavailable"} status={snapshot.outbounds ? (healthyOutbounds === enabledOutbounds ? "healthy" : "attention") : "unavailable"} />
        </ul>
      </Card>

      <Card className="health-card">
        <div className="card-header"><div><p className="eyebrow">PORT FORWARD</p><h3>Current NAT counters</h3></div><Button size="sm" variant="ghost" onClick={() => void load()}>Refresh</Button></div>
        <dl className="inventory-dl">
          <div><dt>Accepted packets</dt><dd>{snapshot.counters4 || snapshot.counters6 ? formatNumber(totals.acceptedPackets) : "—"}</dd></div>
          <div><dt>Accepted bytes</dt><dd>{snapshot.counters4 || snapshot.counters6 ? formatBytes(totals.acceptedBytes) : "—"}</dd></div>
          <div><dt>Dropped packets</dt><dd>{snapshot.counters4 || snapshot.counters6 ? formatNumber(totals.droppedPackets) : "—"}</dd></div>
          <div><dt>Dropped bytes</dt><dd>{snapshot.counters4 || snapshot.counters6 ? formatBytes(totals.droppedBytes) : "—"}</dd></div>
        </dl>
        <p className="field__hint">These are live Egress Manager port-forward counters, not a made-up 24-hour traffic estimate.</p>
      </Card>
    </div>

    <Card className="routes-card">
      <div className="card-header"><div><p className="eyebrow">ROUTES</p><h3>Saved egress routes</h3></div><div className="row-actions"><Button size="sm" variant="ghost" onClick={onCreateRoute}>Create route</Button><Button size="sm" variant="ghost" onClick={onOpenRoutes}>View all <Icon name="arrow" /></Button></div></div>
      {!snapshot.routes ? <StatePanel tone="error" title="Routes unavailable" description="The route list could not be loaded from the server." action="Try again" onAction={() => void load()} /> : routes.length === 0 ? <StatePanel title="No routes yet" description="No egress routes are stored in the database." action="Create route" onAction={onCreateRoute} /> : <div className="table-scroll"><table><thead><tr><th>Route</th><th>Source</th><th>Outbound</th><th>Status</th></tr></thead><tbody>{routes.slice(0, 5).map((item) => <tr key={item.route.id}><td><span className="route-name"><Icon name="route" />{item.route.name}</span></td><td><code>{sourceLabel(item.route)}</code></td><td>{outboundNames.get(item.route.outbound_id) || item.route.outbound_id}</td><td><Badge tone={item.route.enabled ? "success" : "neutral"}>{item.route.enabled ? "Enabled" : "Disabled"}</Badge></td></tr>)}</tbody></table></div>}
    </Card>
  </section>;
}

function SummaryCard({ label, value, detail }: { label: string; value: string; detail: string }) {
  return <Card><span>{label}</span><strong className="tabular">{value}</strong><small>{detail}</small></Card>;
}

function StatusRow({ name, detail, status }: { name: string; detail: string; status: "healthy" | "attention" | "unavailable" }) {
  return <li><span className={`service-dot${status === "healthy" ? "" : " service-dot--warning"}`} aria-hidden="true" /><span><strong>{name}</strong><small>{detail}</small></span><Badge tone={status === "healthy" ? "success" : status === "attention" ? "warning" : "neutral"}>{status === "healthy" ? "Available" : status === "attention" ? "Attention" : "Unavailable"}</Badge></li>;
}

function sourceLabel(route: Route) { return route.source.kind === "subnet" ? route.source.subnet : route.source.interface; }
function formatNumber(value: number) { return new Intl.NumberFormat().format(value); }
function formatBytes(value: number) {
  if (value <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let amount = value;
  let unit = 0;
  while (amount >= 1024 && unit < units.length - 1) { amount /= 1024; unit += 1; }
  return `${amount >= 10 || unit === 0 ? amount.toFixed(0) : amount.toFixed(1)} ${units[unit]}`;
}
function formatTime(value: Date) { return value.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" }); }

type Settled<T> = { ok: true; label: string; value: T } | { ok: false; label: string };
async function settle<T>(label: string, path: string): Promise<Settled<T>> {
  try { return { ok: true, label, value: await api<T>(path) }; }
  catch { return { ok: false, label }; }
}
