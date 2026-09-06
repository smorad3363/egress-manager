import { useCallback, useEffect, useState } from "react";
import { api } from "../lib/api";
import { Icon } from "./Icon";
import { Badge } from "./ui/Badge";
import { Button } from "./ui/Button";
import { Card } from "./ui/Card";
import { Skeleton } from "./ui/Skeleton";
import { StatePanel } from "./ui/StatePanel";

type Outbound = {
  id: string;
  name: string;
  adapter: "sing-box" | "xray" | "interface";
  type: string;
  server: { host: string; port: number };
  enabled: boolean;
  health: { status: string };
};
type StoredOutbound = { outbound: Outbound; revision: number };
type Relay = {
  id: string;
  name: string;
  listen_address: string;
  listen_port: number;
  network: string;
  destination: { host: string; port: number };
  outbound_id: string;
  enabled: boolean;
};
type StoredRelay = { relay: Relay; revision: number };
type LoadState =
  | { status: "loading" }
  | { status: "ready"; outbounds: StoredOutbound[]; relays: StoredRelay[] };

export function XrayManager({ createRequest: _createRequest }: { createRequest: number }) {
  const [state, setState] = useState<LoadState>({ status: "loading" });

  const load = useCallback(async () => {
    setState({ status: "loading" });
    const [outboundResult, relayResult] = await Promise.allSettled([
      api<{ items: StoredOutbound[] }>("/api/v1/outbounds?limit=128"),
      api<{ items: StoredRelay[] }>("/api/v1/relays?limit=128"),
    ]);

    // Compatibility probe only. Alpha.10 production discovery has an empty candidate catalog,
    // so this endpoint cannot inspect foreign Xray or proxy-panel files/services.
    void api("/api/v1/xray/discovery").catch(() => undefined);

    setState({
      status: "ready",
      outbounds: outboundResult.status === "fulfilled" ? outboundResult.value.items : [],
      relays: relayResult.status === "fulfilled" ? relayResult.value.items : [],
    });
  }, []);

  useEffect(() => { void load(); }, [load]);

  if (state.status === "loading") {
    return <section><Heading /><div className="forward-loading"><Skeleton /><Skeleton /><Skeleton /></div></section>;
  }

  const xrayOutbounds = state.outbounds.filter((item) => item.outbound.adapter === "xray");
  const outboundNames = new Map(xrayOutbounds.map((item) => [item.outbound.id, item.outbound.name]));
  const xrayRelays = state.relays.filter((item) => outboundNames.has(item.relay.outbound_id));
  const enabledOutbounds = xrayOutbounds.filter((item) => item.outbound.enabled).length;
  const enabledRelays = xrayRelays.filter((item) => item.relay.enabled).length;
  const healthyOutbounds = xrayOutbounds.filter((item) => item.outbound.health.status === "healthy").length;
  const configured = enabledRelays > 0;
  const empty = xrayOutbounds.length === 0 && xrayRelays.length === 0;

  return <section>
    <div className="inventory-heading forward-heading">
      <Heading />
      <div className="forward-actions"><Button variant="ghost" aria-label="Scan again" onClick={() => void load()}>Refresh</Button></div>
    </div>

    <div className="inventory-warning" role="status">
      <Icon name="shield" />
      <span>Egress Manager does not discover, read, modify, restart, or integrate with Xray installations owned by other panels. Port collision checks only observe host listeners.</span>
    </div>

    <div className="forward-summary">
      <Card><span>Runtime</span><strong>Owned</strong><small>Egress Manager only</small></Card>
      <Card><span>Xray outbounds</span><strong>{enabledOutbounds} / {xrayOutbounds.length}</strong><small>enabled / saved</small></Card>
      <Card><span>Listener relays</span><strong>{enabledRelays} / {xrayRelays.length}</strong><small>enabled / saved</small></Card>
      <Card><span>Healthy outbounds</span><strong>{healthyOutbounds}</strong><small>latest saved health</small></Card>
    </div>

    {empty ? <Card className="forward-card"><StatePanel title="No Xray service detected" description="No Egress Manager-owned Xray outbound or listener relay is configured yet. Import an Xray outbound, then create a listener relay. Other proxy panels are not inspected." action="Scan again" onAction={() => void load()} /></Card> : null}

    <div className="xray-layout">
      <Card className="xray-installation">
        <header><div><p className="eyebrow">PROJECT-OWNED RUNTIME</p><h3>Egress Manager Xray</h3></div><Badge tone={configured ? "success" : "neutral"}>{configured ? "Configured" : "Idle"}</Badge></header>
        <dl>
          <div><dt>Binary</dt><dd><code>/usr/local/lib/egress-manager/bin/xray</code></dd></div>
          <div><dt>Service</dt><dd><code>egress-manager-xray-relay.service</code></dd></div>
          <div><dt>Private config</dt><dd><code>/var/lib/egress-manager/private/xray-relay.json</code></dd></div>
          <div><dt>Ownership</dt><dd>Egress Manager</dd></div>
        </dl>
        <div className="xray-limitations">
          <p><Icon name="shield" />No writes are made to /etc/xray, /usr/local/etc/xray, or other proxy-panel directories.</p>
          <p><Icon name="shield" />The owned service starts only when an Egress Manager listener-relay configuration exists.</p>
          <p><Icon name="shield" />Relay failure is fail-closed; traffic is never silently sent direct.</p>
        </div>
      </Card>

      <Card className="forward-card xray-bindings">
        <header className="card-header"><div><p className="eyebrow">MANAGED OUTBOUNDS</p><h3>Xray egress profiles</h3></div><Badge tone={xrayOutbounds.length > 0 ? "success" : "neutral"}>{xrayOutbounds.length} saved</Badge></header>
        {xrayOutbounds.length === 0 ? <StatePanel title="No managed Xray outbounds" description="Import a supported VLESS REALITY/XHTTP link from Outbounds. It will use only the Egress Manager owned Xray runtime." action="Refresh" onAction={() => void load()} /> : <div className="table-scroll"><table><thead><tr><th>Outbound</th><th>Endpoint</th><th>Type</th><th>Health</th><th>Status</th></tr></thead><tbody>{xrayOutbounds.map(({ outbound }) => <tr key={outbound.id}><td><strong>{outbound.name}</strong><small className="pid">{outbound.id}</small></td><td><code>{outbound.server.host}:{outbound.server.port}</code></td><td>{outbound.type}</td><td><Badge tone={healthTone(outbound.health.status)}>{outbound.health.status}</Badge></td><td><Badge tone={outbound.enabled ? "success" : "neutral"}>{outbound.enabled ? "Enabled" : "Disabled"}</Badge></td></tr>)}</tbody></table></div>}
      </Card>
    </div>

    <Card className="routes-card">
      <div className="card-header"><div><p className="eyebrow">OWNED LISTENERS</p><h3>Xray listener relays</h3></div><Badge tone={enabledRelays > 0 ? "success" : "neutral"}>{enabledRelays} enabled</Badge></div>
      {xrayRelays.length === 0 ? <StatePanel title="No Xray listener relays" description="Create a relay from the Relays page and select one managed Xray outbound." action="Refresh" onAction={() => void load()} /> : <div className="table-scroll"><table><thead><tr><th>Relay</th><th>Listener</th><th>Outbound</th><th>Destination</th><th>Status</th></tr></thead><tbody>{xrayRelays.map(({ relay }) => <tr key={relay.id}><td><strong>{relay.name}</strong><small className="pid">{relay.network.toUpperCase()}</small></td><td><code>{relay.listen_address}:{relay.listen_port}</code></td><td>{outboundNames.get(relay.outbound_id) || relay.outbound_id}</td><td><code>{relay.destination.host}:{relay.destination.port}</code></td><td><Badge tone={relay.enabled ? "success" : "neutral"}>{relay.enabled ? "Enabled" : "Disabled"}</Badge></td></tr>)}</tbody></table></div>}
    </Card>
  </section>;
}

function Heading() {
  return <div><p className="eyebrow">PROJECT-OWNED XRAY</p><h2>Native Xray routing</h2><p>Xray outbounds and listeners owned exclusively by Egress Manager, isolated from every other proxy panel on the host.</p></div>;
}

function healthTone(status: string): "success" | "warning" | "danger" | "neutral" {
  if (status === "healthy") return "success";
  if (status === "degraded") return "warning";
  if (status === "unhealthy") return "danger";
  return "neutral";
}
