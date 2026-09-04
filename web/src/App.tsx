const capabilities = [
  ["NAT", "Port forwarding with guarded management paths"],
  ["HAProxy", "TCP health, failover, and load distribution"],
  ["Outbounds", "Proxy and native interface egress"],
] as const;

function App() {
  return (
    <main className="shell">
      <header className="masthead">
        <div className="brand-mark" aria-hidden="true">
          E
        </div>
        <div>
          <p className="eyebrow">NETWORK CONTROL PLANE</p>
          <h1>Egress Manager</h1>
        </div>
        <span className="status">Bootstrap</span>
      </header>

      <section className="hero" aria-labelledby="hero-title">
        <p className="eyebrow">SAFE BY DEFAULT</p>
        <h2 id="hero-title">Route traffic without losing control.</h2>
        <p>
          Privilege-separated operations, transaction journals, native validation,
          and rollback form the baseline for every network change.
        </p>
      </section>

      <section className="capability-grid" aria-label="Planned capabilities">
        {capabilities.map(([title, description]) => (
          <article className="capability-card" key={title}>
            <span className="card-index">0{capabilities.findIndex(([name]) => name === title) + 1}</span>
            <h3>{title}</h3>
            <p>{description}</p>
          </article>
        ))}
      </section>

      <footer>
        <span className="pulse" aria-hidden="true" />
        Phase 0 workspace ready
      </footer>
    </main>
  );
}

export default App;
