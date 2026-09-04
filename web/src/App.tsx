import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { lazy, Suspense, useState } from "react";
import type { ReactNode } from "react";
import { DesignSystem } from "./DesignSystem";
import { Icon } from "./components/Icon";
import { NetworkInventory } from "./components/NetworkInventory";
import { PortForwards } from "./components/PortForwards";
import { Badge } from "./components/ui/Badge";
import { Button } from "./components/ui/Button";
import { Card } from "./components/ui/Card";
import { Dialog } from "./components/ui/Dialog";
import { SelectField, TextField } from "./components/ui/Field";
import { Toast } from "./components/ui/Toast";

const navigation = [
  ["Dashboard", "dashboard"], ["Outbounds", "globe"], ["Routes", "route"],
  ["Port Forward", "arrow"], ["HAProxy", "balance"], ["Firewall", "firewall"],
  ["Network", "network"], ["Logs", "logs"], ["Settings", "settings"],
] as const;

const routes = [
  { name: "EU applications", source: "10.20.0.0/16", outbound: "Frankfurt · WG0", state: "Healthy", traffic: "1.8 TB" },
  { name: "Streaming relay", source: "tcp · 443", outbound: "Amsterdam · VLESS", state: "Healthy", traffic: "642 GB" },
  { name: "Backup transit", source: "10.40.8.0/24", outbound: "Helsinki · Direct", state: "Standby", traffic: "88 GB" },
] as const;

const TrafficChart = lazy(async () => {
  const module = await import("./components/TrafficChart");
  return { default: module.TrafficChart };
});

function Dashboard() {
  const [active, setActive] = useState("Dashboard");
  const [mobileNavigation, setMobileNavigation] = useState(false);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [toast, setToast] = useState<string | null>(null);
  const [newForwardRequest, setNewForwardRequest] = useState(0);
  const reduceMotion = useReducedMotion();

  const notify = () => {
    setToast(" The privileged service will validate the plan before any change.");
    window.setTimeout(() => setToast(null), 3200);
  };

  return (
    <div className="app-shell">
      <a className="skip-link" href="#main-content">Skip to content</a>
      <aside className="sidebar" aria-label="Primary navigation">
        <Brand />
        <Navigation active={active} onSelect={setActive} />
        <div className="sidebar__footer">
          <span className="service-dot" aria-hidden="true" />
          <span><strong>All systems operational</strong><small>egressd connected</small></span>
        </div>
      </aside>

      <AnimatePresence>
        {mobileNavigation ? (
          <motion.div className="mobile-drawer" initial={reduceMotion ? false : { opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }}>
            <button className="mobile-drawer__backdrop" aria-label="Close navigation" onClick={() => setMobileNavigation(false)} />
            <motion.aside className="mobile-drawer__panel" initial={reduceMotion ? false : { x: -24 }} animate={{ x: 0 }} exit={{ x: -24 }}>
              <div className="mobile-drawer__heading"><Brand /><Button size="icon" variant="ghost" aria-label="Close navigation" onClick={() => setMobileNavigation(false)}><Icon name="close" /></Button></div>
              <Navigation active={active} onSelect={(item) => { setActive(item); setMobileNavigation(false); }} />
            </motion.aside>
          </motion.div>
        ) : null}
      </AnimatePresence>

      <main id="main-content" className="workspace">
        <header className="topbar">
          <Button className="mobile-menu" size="icon" variant="ghost" aria-label="Open navigation" onClick={() => setMobileNavigation(true)}><Icon name="menu" /></Button>
          <div><p className="breadcrumb">Console / {active}</p><h1>{active}</h1></div>
          <label className="command-search"><Icon name="search" /><span className="sr-only">Search console</span><input placeholder="Search" /><kbd>⌘ K</kbd></label>
          <Button variant="primary" onClick={() => active === "Port Forward" ? setNewForwardRequest((value) => value + 1) : setDialogOpen(true)}><Icon name="plus" />{active === "Port Forward" ? "New forward" : "New route"}</Button>
        </header>

        <div className="content">
          {active === "Network" ? <NetworkInventory /> : active === "Port Forward" ? <PortForwards createRequest={newForwardRequest} /> : <>
          <section className="page-heading" aria-labelledby="overview-title">
            <div><p className="eyebrow">LIVE OVERVIEW</p><h2 id="overview-title">Traffic is flowing normally.</h2><p>Policy and transport health across this gateway.</p></div>
            <div className="page-heading__meta"><span>Last reconciled</span><strong>12 seconds ago</strong></div>
          </section>

          <section className="metric-grid" aria-label="Network summary">
            <Metric label="Active routes" value="24" change="+2 this week" icon="route" />
            <Metric label="Traffic · 24h" value="4.6 TB" change="8.2% below limit" icon="activity" />
            <Metric label="Healthy outbounds" value="7 / 8" change="1 in standby" icon="globe" tone="warning" />
            <Metric label="Blocked requests" value="1,284" change="Last 24 hours" icon="shield" />
          </section>

          <section className="dashboard-grid">
            <Card className="chart-card">
              <CardHeader eyebrow="THROUGHPUT" title="Gateway traffic" action={<Badge tone="success">Live</Badge>} />
              <div className="chart-legend"><strong>4.6 TB</strong><span>total · 24 hours</span></div>
              <Suspense fallback={<div className="traffic-chart chart-loading" aria-label="Loading traffic chart" />}><TrafficChart /></Suspense>
            </Card>
            <Card className="health-card">
              <CardHeader eyebrow="SERVICES" title="System health" action={<Button size="sm" variant="ghost" onClick={notify}>Run checks</Button>} />
              <div className="health-score"><span>99.98<small>%</small></span><p>30-day availability</p></div>
              <ul className="service-list">
                <Service name="Privileged agent" detail="egressd · 4 ms" />
                <Service name="Firewall engine" detail="nftables · synced" />
                <Service name="HAProxy" detail="8 backends · healthy" />
                <Service name="SQLite" detail="WAL · 12 MB" />
              </ul>
            </Card>
          </section>

          <Card className="routes-card">
            <CardHeader eyebrow="POLICY" title="Active routes" action={<Button size="sm" variant="ghost">View all <Icon name="arrow" /></Button>} />
            <div className="table-scroll">
              <table>
                <thead><tr><th>Route</th><th>Source</th><th>Outbound</th><th>Status</th><th className="align-right">Traffic</th></tr></thead>
                <tbody>{routes.map((route) => <tr key={route.name}><td><span className="route-name"><Icon name="route" />{route.name}</span></td><td><code>{route.source}</code></td><td>{route.outbound}</td><td><Badge tone={route.state === "Healthy" ? "success" : "neutral"}>{route.state}</Badge></td><td className="align-right tabular">{route.traffic}</td></tr>)}</tbody>
              </table>
            </div>
          </Card>
          </>}
        </div>
      </main>

      <Dialog open={dialogOpen} onOpenChange={setDialogOpen} title="Create a route" description="Define intent now. The validation plan appears before apply.">
        <form className="dialog__form" onSubmit={(event) => { event.preventDefault(); setDialogOpen(false); notify(); }}>
          <TextField autoFocus label="Route name" placeholder="Production services" />
          <TextField label="Source CIDR" placeholder="10.20.0.0/16" hint="Canonical IPv4 or IPv6 CIDR" />
          <SelectField label="Outbound" defaultValue=""><option value="" disabled>Select an outbound</option><option>Frankfurt · WG0</option><option>Amsterdam · VLESS</option></SelectField>
          <div className="dialog__actions"><Button variant="ghost" onClick={() => setDialogOpen(false)}>Cancel</Button><Button variant="primary" type="submit">Review plan <Icon name="arrow" /></Button></div>
        </form>
      </Dialog>
      <Toast message={toast} />
    </div>
  );
}

function Brand() {
  return <div className="brand"><span className="brand__mark"><Icon name="terminal" /></span><span><strong>Egress</strong><small>Manager</small></span></div>;
}

function Navigation({ active, onSelect }: { active: string; onSelect: (item: string) => void }) {
  return <nav className="navigation">{navigation.map(([label, icon], index) => <button className={active === label ? "active" : ""} key={label} onClick={() => onSelect(label)}><Icon name={icon} /><span>{label}</span>{index === 2 ? <small>24</small> : null}</button>)}</nav>;
}

function Metric({ label, value, change, icon, tone }: { label: string; value: string; change: string; icon: "route" | "activity" | "globe" | "shield"; tone?: "warning" }) {
  return <Card className="metric"><span className={`metric__icon ${tone ? `metric__icon--${tone}` : ""}`}><Icon name={icon} /></span><div><p>{label}</p><strong>{value}</strong><small>{change}</small></div></Card>;
}

function CardHeader({ eyebrow, title, action }: { eyebrow: string; title: string; action: ReactNode }) {
  return <header className="card-header"><div><p className="eyebrow">{eyebrow}</p><h3>{title}</h3></div>{action}</header>;
}

function Service({ name, detail }: { name: string; detail: string }) {
  return <li><span className="service-dot" aria-hidden="true" /><span><strong>{name}</strong><small>{detail}</small></span><Icon name="chevron" /></li>;
}

function App() {
  return window.location.pathname === "/design-system" ? <DesignSystem /> : <Dashboard />;
}

export default App;
