import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { lazy, Suspense, useEffect, useRef, useState } from "react";
import type { ReactNode } from "react";
import { DesignSystem } from "./DesignSystem";
import { LanguageSwitcher } from "./i18n";
import { Icon } from "./components/Icon";
import { NetworkInventory } from "./components/NetworkInventory";
import { OutboundsManager } from "./components/OutboundsManager";
import { HAProxyManager } from "./components/HAProxyManager";
import { PortForwards } from "./components/PortForwards";
import { RoutesManager } from "./components/RoutesManager";
import { XrayManager } from "./components/XrayManager";
import { Badge } from "./components/ui/Badge";
import { Button } from "./components/ui/Button";
import { Card } from "./components/ui/Card";
import { Dialog } from "./components/ui/Dialog";
import { SelectField, TextField } from "./components/ui/Field";
import { Toast } from "./components/ui/Toast";

const navigation = [
  ["Dashboard", "dashboard"], ["Outbounds", "globe"], ["Routes", "route"], ["Xray", "network"],
  ["Port Forward", "arrow"], ["HAProxy", "balance"], ["Firewall", "firewall"],
  ["Network", "network"], ["Logs", "logs"], ["Settings", "settings"],
] as const;

type PageName = (typeof navigation)[number][0];

const unfinishedPages = new Set<PageName>(["Firewall", "Logs", "Settings"]);

const searchAliases: Record<PageName, string[]> = {
  Dashboard: ["dashboard", "home", "overview", "داشبورد", "خانه", "وضعیت"],
  Outbounds: ["outbounds", "outbound", "proxy", "vpn", "خروجی", "خروجی‌ها", "پروکسی", "وی پی ان"],
  Routes: ["routes", "route", "routing", "مسیر", "مسیرها", "مسیریابی"],
  Xray: ["xray", "ایکس ری", "ایکس‌ری", "marzban", "3x-ui"],
  "Port Forward": ["port forward", "nat", "forward", "انتقال پورت", "فوروارد", "پورت"],
  HAProxy: ["haproxy", "load balance", "balancer", "توزیع بار", "لود بالانس"],
  Firewall: ["firewall", "دیواره آتش", "فایروال"],
  Network: ["network", "interfaces", "dns", "شبکه", "کارت شبکه", "دی ان اس"],
  Logs: ["logs", "log", "گزارش", "گزارش‌ها", "لاگ"],
  Settings: ["settings", "config", "تنظیمات", "پیکربندی"],
};

const dashboardRoutes = [
  { name: "EU applications", source: "10.20.0.0/16", outbound: "Frankfurt · WG0", state: "Healthy", traffic: "1.8 TB" },
  { name: "Streaming relay", source: "tcp · 443", outbound: "Amsterdam · VLESS", state: "Healthy", traffic: "642 GB" },
  { name: "Backup transit", source: "10.40.8.0/24", outbound: "Helsinki · Direct", state: "Standby", traffic: "88 GB" },
] as const;

const TrafficChart = lazy(async () => {
  const module = await import("./components/TrafficChart");
  return { default: module.TrafficChart };
});

function Dashboard() {
  const [active, setActive] = useState<PageName>("Dashboard");
  const [mobileNavigation, setMobileNavigation] = useState(false);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [toast, setToast] = useState<string | null>(null);
  const [search, setSearch] = useState("");
  const [newForwardRequest, setNewForwardRequest] = useState(0);
  const [newHAProxyRequest, setNewHAProxyRequest] = useState(0);
  const [newOutboundRequest, setNewOutboundRequest] = useState(0);
  const [newRouteRequest, setNewRouteRequest] = useState(0);
  const [newXrayRequest, setNewXrayRequest] = useState(0);
  const searchRef = useRef<HTMLInputElement>(null);
  const reduceMotion = useReducedMotion();

  const notify = (message = "The change was prepared. Review it before applying.") => {
    setToast(message);
    window.setTimeout(() => setToast(null), 3200);
  };

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        searchRef.current?.focus();
      }
      if (event.key === "Escape" && document.activeElement === searchRef.current) {
        setSearch("");
        searchRef.current?.blur();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);

  const submitSearch = (event: React.FormEvent) => {
    event.preventDefault();
    const match = findPage(search);
    if (!match) {
      notify("No matching page was found.");
      return;
    }
    setActive(match);
    setSearch("");
    searchRef.current?.blur();
  };

  const primaryAction = (() => {
    if (active === "Dashboard") return <Button variant="primary" onClick={() => setDialogOpen(true)}><Icon name="plus" />New route</Button>;
    if (active === "Port Forward") return <Button variant="primary" onClick={() => setNewForwardRequest((value) => value + 1)}><Icon name="plus" />New forward</Button>;
    if (active === "HAProxy") return <Button variant="primary" onClick={() => setNewHAProxyRequest((value) => value + 1)}><Icon name="plus" />New frontend</Button>;
    if (active === "Outbounds") return <Button variant="primary" onClick={() => setNewOutboundRequest((value) => value + 1)}><Icon name="plus" />Import outbound</Button>;
    if (active === "Routes") return <Button variant="primary" onClick={() => setNewRouteRequest((value) => value + 1)}><Icon name="plus" />New route</Button>;
    if (active === "Xray") return <Button variant="primary" onClick={() => setNewXrayRequest((value) => value + 1)}><Icon name="plus" />New binding</Button>;
    return null;
  })();

  return (
    <div className="app-shell">
      <a className="skip-link" href="#main-content">Skip to content</a>
      <aside className="sidebar" aria-label="Primary navigation">
        <Brand />
        <Navigation active={active} onSelect={setActive} />
        <div className="sidebar__footer">
          <span className="service-dot" aria-hidden="true" />
          <span><strong>Console connected</strong><small>Server management</small></span>
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
          <form className="command-search" role="search" onSubmit={submitSearch}>
            <Icon name="search" />
            <span className="sr-only">Search console</span>
            <input ref={searchRef} placeholder="Search" value={search} onChange={(event) => setSearch(event.target.value)} />
            <kbd>⌘ K</kbd>
          </form>
          <LanguageSwitcher compact />
          {primaryAction}
        </header>

        <div className="content">
          {active === "Network" ? <NetworkInventory /> :
            active === "Port Forward" ? <PortForwards createRequest={newForwardRequest} /> :
            active === "HAProxy" ? <HAProxyManager createRequest={newHAProxyRequest} /> :
            active === "Outbounds" ? <OutboundsManager createRequest={newOutboundRequest} /> :
            active === "Routes" ? <RoutesManager createRequest={newRouteRequest} /> :
            active === "Xray" ? <XrayManager createRequest={newXrayRequest} /> :
            unfinishedPages.has(active) ? <UnavailablePage page={active} /> : <>
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
              <CardHeader eyebrow="SERVICES" title="System health" action={<Button size="sm" variant="ghost" onClick={() => setActive("Network")}>View network</Button>} />
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
            <CardHeader eyebrow="POLICY" title="Active routes" action={<Button size="sm" variant="ghost" onClick={() => setActive("Routes")}>View all <Icon name="arrow" /></Button>} />
            <div className="table-scroll">
              <table>
                <thead><tr><th>Route</th><th>Source</th><th>Outbound</th><th>Status</th><th className="align-right">Traffic</th></tr></thead>
                <tbody>{dashboardRoutes.map((route) => <tr key={route.name}><td><span className="route-name"><Icon name="route" />{route.name}</span></td><td><code>{route.source}</code></td><td>{route.outbound}</td><td><Badge tone={route.state === "Healthy" ? "success" : "neutral"}>{route.state}</Badge></td><td className="align-right tabular">{route.traffic}</td></tr>)}</tbody>
              </table>
            </div>
          </Card>
          </>}
        </div>
      </main>

      <Dialog open={dialogOpen} onOpenChange={setDialogOpen} title="Create a route" description="Define where traffic should leave this server. You can review everything before it is applied.">
        <form className="dialog__form" onSubmit={(event) => { event.preventDefault(); setDialogOpen(false); setActive("Routes"); notify(); }}>
          <TextField autoFocus label="Route name" placeholder="Production services" />
          <TextField label="Source CIDR" placeholder="10.20.0.0/16" hint="Canonical IPv4 or IPv6 CIDR" />
          <SelectField label="Outbound" defaultValue=""><option value="" disabled>Select an outbound</option><option>Frankfurt · WG0</option><option>Amsterdam · VLESS</option></SelectField>
          <div className="dialog__actions"><Button variant="ghost" type="button" onClick={() => setDialogOpen(false)}>Cancel</Button><Button variant="primary" type="submit">Review <Icon name="arrow" /></Button></div>
        </form>
      </Dialog>
      <Toast message={toast} />
    </div>
  );
}

function UnavailablePage({ page }: { page: PageName }) {
  const copy = page === "Firewall"
    ? ["Firewall controls are not exposed in this version. Existing firewall rules are left untouched.", "Use Port Forward or Routes for Egress Manager-owned networking rules."]
    : page === "Logs"
      ? ["The log viewer is not available in the browser yet.", "Run sudo egress-manager logs on the server to view service logs."]
      : ["Browser settings are not available yet.", "Run sudo egress-manager config on the server to change service settings safely."];
  return <Card className="unavailable-page"><p className="eyebrow">Soon</p><h2>This page is not available yet</h2><p>{copy[0]}</p><p>{copy[1]}</p></Card>;
}

function Brand() {
  return <div className="brand"><span className="brand__mark"><Icon name="terminal" /></span><span><strong>Egress</strong><small>Manager</small></span></div>;
}

function Navigation({ active, onSelect }: { active: PageName; onSelect: (item: PageName) => void }) {
  return <nav className="navigation">{navigation.map(([label, icon], index) => <button className={active === label ? "active" : ""} key={label} onClick={() => onSelect(label)}><Icon name={icon} /><span>{label}</span>{index === 2 ? <small>24</small> : unfinishedPages.has(label) ? <small>Soon</small> : null}</button>)}</nav>;
}

function findPage(query: string): PageName | null {
  const normalized = query.trim().toLocaleLowerCase().replace(/\s+/g, " ");
  if (!normalized) return null;
  for (const [page, aliases] of Object.entries(searchAliases) as Array<[PageName, string[]]>) {
    if (aliases.some((alias) => alias.toLocaleLowerCase().includes(normalized) || normalized.includes(alias.toLocaleLowerCase()))) return page;
  }
  return null;
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
