import { useEffect, useState } from "react";
import { Badge } from "./ui/Badge";
import { Button } from "./ui/Button";
import { Card } from "./ui/Card";
import { Skeleton } from "./ui/Skeleton";
import { StatePanel } from "./ui/StatePanel";

type Inventory = {
  generated_at: string;
  interfaces: Array<{ name: string; state: string; mtu: number; addresses: Array<{ family: string; cidr: string; scope: string }> }>;
  routes: Array<{ destination: string; gateway?: string; interface?: string; default: boolean }>;
  listeners: Array<{ protocol: string; address: string; port: number; process?: string; pid?: number }>;
  dns: { source: string; servers: string[]; search_domains: string[] };
  capabilities: Array<{ name: string; available: boolean; running: boolean; version?: string; backend?: string }>;
  warnings: string[];
};

type LoadState = { status: "loading" } | { status: "error"; message: string } | { status: "ready"; value: Inventory };

export function NetworkInventory() {
  const [state, setState] = useState<LoadState>({ status: "loading" });
  const [reloadNonce, setReloadNonce] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    setState({ status: "loading" });
    void fetch("/api/v1/network/inventory", { credentials: "same-origin", signal: controller.signal })
      .then(async (response) => {
        if (!response.ok) throw new Error(response.status === 401 ? "Sign in to inspect this gateway." : "Inventory is temporarily unavailable.");
        return response.json() as Promise<Inventory>;
      })
      .then((value) => setState({ status: "ready", value }))
      .catch((error: unknown) => {
        if (!controller.signal.aborted) setState({ status: "error", message: error instanceof Error ? error.message : "Inventory is temporarily unavailable." });
      });
    return () => controller.abort();
  }, [reloadNonce]);

  const reload = () => setReloadNonce((value) => value + 1);

  if (state.status === "loading") {
    return <section aria-label="Loading network inventory"><div className="inventory-heading"><div><p className="eyebrow">READ ONLY</p><h2>Host network inventory</h2></div></div><div className="inventory-loading"><Skeleton /><Skeleton /><Skeleton /></div></section>;
  }
  if (state.status === "error") {
    return <section><div className="inventory-heading"><div><p className="eyebrow">READ ONLY</p><h2>Host network inventory</h2><p>No host changes are made by this view.</p></div></div><StatePanel tone="error" title="Inventory unavailable" description={state.message} action="Try again" onAction={reload} /></section>;
  }

  const inventory = state.value;
  const defaults = inventory.routes.filter((route) => route.default);
  const available = inventory.capabilities.filter((capability) => capability.available).length;
  return (
    <section aria-labelledby="inventory-title">
      <div className="inventory-heading"><div><p className="eyebrow">READ ONLY</p><h2 id="inventory-title">Host network inventory</h2><p>Live interfaces, routes, listeners, DNS, and installed network services.</p></div><div className="page-heading__meta"><span>Observed</span><strong>{new Date(inventory.generated_at).toLocaleTimeString()}</strong><Button size="sm" variant="ghost" onClick={reload}>Refresh</Button></div></div>

      {inventory.warnings.length > 0 ? <div className="inventory-warning" role="status"><strong>Partial inventory</strong><span>{inventory.warnings.join(" · ")}</span></div> : null}

      <div className="inventory-summary">
        <Card><span>Interfaces</span><strong>{inventory.interfaces.length}</strong><small>{inventory.interfaces.filter((item) => item.state === "up").length} up</small></Card>
        <Card><span>Listeners</span><strong>{inventory.listeners.length}</strong><small>occupied ports</small></Card>
        <Card><span>Default routes</span><strong>{defaults.length}</strong><small>{defaults[0]?.gateway ?? "none"}</small></Card>
        <Card><span>Capabilities</span><strong>{available} / {inventory.capabilities.length}</strong><small>available</small></Card>
      </div>

      <div className="inventory-grid">
        <Card className="inventory-card">
          <header><div><p className="eyebrow">INTERFACES</p><h3>Addresses & subnets</h3></div><Badge>{inventory.interfaces.length} detected</Badge></header>
          <ul className="interface-list">{inventory.interfaces.map((item) => <li key={item.name}><div><strong>{item.name}</strong><Badge tone={item.state === "up" ? "success" : "neutral"}>{item.state || "unknown"}</Badge></div><span>MTU {item.mtu}</span>{item.addresses.length ? item.addresses.map((address) => <code key={`${address.family}-${address.cidr}`}>{address.cidr} · {address.scope}</code>) : <small>No address</small>}</li>)}</ul>
        </Card>
        <Card className="inventory-card">
          <header><div><p className="eyebrow">RESOLUTION</p><h3>DNS & gateways</h3></div><Badge tone="success">Observed</Badge></header>
          <dl className="definition-list"><div><dt>DNS source</dt><dd><code>{inventory.dns.source}</code></dd></div><div><dt>Nameservers</dt><dd>{inventory.dns.servers.length ? inventory.dns.servers.map((server) => <code key={server}>{server}</code>) : "None"}</dd></div><div><dt>Search domains</dt><dd>{inventory.dns.search_domains.join(", ") || "None"}</dd></div><div><dt>Default gateway</dt><dd>{defaults.map((route) => <code key={`${route.gateway}-${route.interface}`}>{route.gateway ?? "on-link"} · {route.interface}</code>)}</dd></div></dl>
        </Card>
      </div>

      <Card className="routes-card inventory-table">
        <header className="card-header"><div><p className="eyebrow">SOCKETS</p><h3>Listening ports</h3></div><Badge tone="warning">Review conflicts before binding</Badge></header>
        <div className="table-scroll"><table><thead><tr><th>Process</th><th>Protocol</th><th>Address</th><th>Port</th><th>Service</th></tr></thead><tbody>{inventory.listeners.map((listener, index) => <tr key={`${listener.protocol}-${listener.address}-${listener.port}-${index}`}><td>{listener.process || "Unavailable"}{listener.pid ? <small className="pid">PID {listener.pid}</small> : null}</td><td><Badge>{listener.protocol.toUpperCase()}</Badge></td><td><code>{listener.address}</code></td><td><code>{listener.port}</code></td><td><Badge tone="warning">Occupied</Badge></td></tr>)}</tbody></table></div>
      </Card>

      <Card className="routes-card inventory-table">
        <header className="card-header"><div><p className="eyebrow">CAPABILITIES</p><h3>Network services</h3></div></header>
        <div className="capability-grid">{inventory.capabilities.map((capability) => <article key={capability.name}><span className={capability.available ? "service-dot" : "service-dot service-dot--off"} /><div><strong>{capability.name}</strong><small>{capability.backend || capability.version || (capability.available ? "detected" : "not detected")}</small></div><Badge tone={capability.running ? "success" : "neutral"}>{capability.running ? "Running" : capability.available ? "Available" : "Absent"}</Badge></article>)}</div>
      </Card>
    </section>
  );
}
