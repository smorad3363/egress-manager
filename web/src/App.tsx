import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { useEffect, useRef, useState } from "react";
import type { FormEvent } from "react";
import { DesignSystem } from "./DesignSystem";
import { DashboardOverview } from "./components/DashboardOverview";
import { HAProxyManager } from "./components/HAProxyManager";
import { Icon } from "./components/Icon";
import { NetworkInventory } from "./components/NetworkInventory";
import { OutboundsManager } from "./components/OutboundsManager";
import { PortForwards } from "./components/PortForwards";
import { RoutesManager } from "./components/RoutesManager";
import { XrayManager } from "./components/XrayManager";
import { Button } from "./components/ui/Button";
import { Toast } from "./components/ui/Toast";
import { LanguageSwitcher } from "./i18n";

const navigation = [
  ["Dashboard", "dashboard"],
  ["Outbounds", "globe"],
  ["Routes", "route"],
  ["Xray", "network"],
  ["Port Forward", "arrow"],
  ["HAProxy", "balance"],
  ["Network", "network"],
] as const;

type PageName = (typeof navigation)[number][0];

const searchAliases: Record<PageName, string[]> = {
  Dashboard: ["dashboard", "home", "overview", "داشبورد", "خانه", "وضعیت"],
  Outbounds: ["outbounds", "outbound", "proxy", "vpn", "خروجی", "خروجی‌ها", "پروکسی", "وی پی ان"],
  Routes: ["routes", "route", "routing", "مسیر", "مسیرها", "مسیریابی"],
  Xray: ["xray", "ایکس ری", "ایکس‌ری", "marzban", "3x-ui"],
  "Port Forward": ["port forward", "nat", "forward", "انتقال پورت", "فوروارد", "پورت"],
  HAProxy: ["haproxy", "load balance", "balancer", "توزیع بار", "لود بالانس"],
  Network: ["network", "interfaces", "dns", "شبکه", "کارت شبکه", "دی ان اس"],
};

export function App() {
  if (import.meta.env.DEV && window.location.pathname === "/design-system") return <DesignSystem />;
  return <Dashboard />;
}

function Dashboard() {
  const [active, setActive] = useState<PageName>("Dashboard");
  const [mobileNavigation, setMobileNavigation] = useState(false);
  const [toast, setToast] = useState<string | null>(null);
  const [search, setSearch] = useState("");
  const [newForwardRequest, setNewForwardRequest] = useState(0);
  const [newHAProxyRequest, setNewHAProxyRequest] = useState(0);
  const [newOutboundRequest, setNewOutboundRequest] = useState(0);
  const [newRouteRequest, setNewRouteRequest] = useState(0);
  const searchRef = useRef<HTMLInputElement>(null);
  const reduceMotion = useReducedMotion();

  const notify = (message: string) => {
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

  const openRouteCreate = () => {
    setActive("Routes");
    setNewRouteRequest((value) => value + 1);
  };

  const submitSearch = (event: FormEvent) => {
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
    if (active === "Dashboard") return <Button variant="primary" onClick={openRouteCreate}><Icon name="plus" />New route</Button>;
    if (active === "Port Forward") return <Button variant="primary" onClick={() => setNewForwardRequest((value) => value + 1)}><Icon name="plus" />New forward</Button>;
    if (active === "HAProxy") return <Button variant="primary" onClick={() => setNewHAProxyRequest((value) => value + 1)}><Icon name="plus" />New frontend</Button>;
    if (active === "Outbounds") return <Button variant="primary" onClick={() => setNewOutboundRequest((value) => value + 1)}><Icon name="plus" />Import outbound</Button>;
    if (active === "Routes") return <Button variant="primary" onClick={() => setNewRouteRequest((value) => value + 1)}><Icon name="plus" />New route</Button>;
    return null;
  })();

  return <div className="app-shell">
    <a className="skip-link" href="#main-content">Skip to content</a>
    <aside className="sidebar" aria-label="Primary navigation">
      <Brand />
      <Navigation active={active} onSelect={setActive} />
    </aside>

    <AnimatePresence>
      {mobileNavigation ? <motion.div className="mobile-drawer" initial={reduceMotion ? false : { opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }}>
        <button className="mobile-drawer__backdrop" aria-label="Close navigation" onClick={() => setMobileNavigation(false)} />
        <motion.aside className="mobile-drawer__panel" initial={reduceMotion ? false : { x: -24 }} animate={{ x: 0 }} exit={{ x: -24 }}>
          <div className="mobile-drawer__heading"><Brand /><Button size="icon" variant="ghost" aria-label="Close navigation" onClick={() => setMobileNavigation(false)}><Icon name="close" /></Button></div>
          <Navigation active={active} onSelect={(item) => { setActive(item); setMobileNavigation(false); }} />
        </motion.aside>
      </motion.div> : null}
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
        {active === "Dashboard" ? <DashboardOverview onOpenRoutes={() => setActive("Routes")} onCreateRoute={openRouteCreate} onOpenNetwork={() => setActive("Network")} /> :
          active === "Network" ? <NetworkInventory /> :
          active === "Port Forward" ? <PortForwards createRequest={newForwardRequest} /> :
          active === "HAProxy" ? <HAProxyManager createRequest={newHAProxyRequest} /> :
          active === "Outbounds" ? <OutboundsManager createRequest={newOutboundRequest} /> :
          active === "Routes" ? <RoutesManager createRequest={newRouteRequest} /> :
          <XrayManager createRequest={0} />}
      </div>
    </main>
    <Toast message={toast} />
  </div>;
}

function Brand() {
  return <div className="brand"><span className="brand__mark"><Icon name="terminal" /></span><span><strong>Egress</strong><small>Manager</small></span></div>;
}

function Navigation({ active, onSelect }: { active: PageName; onSelect: (item: PageName) => void }) {
  return <nav className="navigation">{navigation.map(([label, icon]) => <button className={active === label ? "active" : ""} key={label} onClick={() => onSelect(label)}><Icon name={icon} /><span>{label}</span></button>)}</nav>;
}

function findPage(query: string): PageName | null {
  const normalized = query.trim().toLocaleLowerCase().replace(/\s+/g, " ");
  if (!normalized) return null;
  for (const [page, aliases] of Object.entries(searchAliases) as Array<[PageName, string[]]>) {
    if (aliases.some((alias) => alias.toLocaleLowerCase().includes(normalized) || normalized.includes(alias.toLocaleLowerCase()))) return page;
  }
  return null;
}
