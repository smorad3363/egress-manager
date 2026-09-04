import { useState } from "react";
import { Icon } from "./components/Icon";
import { Badge } from "./components/ui/Badge";
import { Button } from "./components/ui/Button";
import { Card } from "./components/ui/Card";
import { Dialog } from "./components/ui/Dialog";
import { SelectField, TextField } from "./components/ui/Field";
import { Skeleton } from "./components/ui/Skeleton";
import { StatePanel } from "./components/ui/StatePanel";
import { Toast } from "./components/ui/Toast";

export function DesignSystem() {
  const [dialogOpen, setDialogOpen] = useState(false);
  const [toast, setToast] = useState<string | null>(null);
  const notify = () => { setToast(" This is a concise operational notification."); window.setTimeout(() => setToast(null), 2400); };

  return (
    <main className="showcase" id="main-content">
      <header className="showcase__header"><div><p className="eyebrow">EGRESS MANAGER</p><h1>Interface system</h1><p>Reusable primitives and product states.</p></div><a className="text-link" href="/">Return to console <Icon name="arrow" /></a></header>
      <section className="showcase__section"><h2>Color & status</h2><div className="swatches"><span className="swatch swatch--accent">Accent</span><span className="swatch swatch--success">Success</span><span className="swatch swatch--warning">Warning</span><span className="swatch swatch--danger">Danger</span></div><div className="row"><Badge tone="success">Healthy</Badge><Badge tone="warning">Degraded</Badge><Badge tone="danger">Failed</Badge><Badge>Standby</Badge></div></section>
      <section className="showcase__section"><h2>Actions</h2><div className="row"><Button variant="primary"><Icon name="plus" />Primary</Button><Button>Secondary</Button><Button variant="ghost">Ghost</Button><Button variant="danger">Destructive</Button><Button disabled>Disabled</Button></div></section>
      <section className="showcase__section"><h2>Inputs</h2><div className="showcase__fields"><TextField label="Interface" defaultValue="wg0" hint="Linux interface name" /><SelectField label="Failure policy" defaultValue="fail_closed"><option value="fail_closed">Fail closed</option><option value="fail_open">Fail open</option></SelectField><TextField label="Source CIDR" defaultValue="10.20.0.1/16" error="Use canonical CIDR 10.20.0.0/16" /></div></section>
      <section className="showcase__section"><h2>Feedback</h2><div className="row"><Button onClick={() => setDialogOpen(true)}>Open dialog</Button><Button onClick={notify}>Show toast</Button></div></section>
      <section className="showcase__section"><h2>Loading</h2><Card className="skeleton-card"><Skeleton className="skeleton--avatar" /><div><Skeleton className="skeleton--title" /><Skeleton className="skeleton--line" /></div></Card></section>
      <section className="showcase__section"><h2>System states</h2><div className="state-grid"><StatePanel tone="empty" title="No routes yet" description="Create the first policy route for this gateway." action="Create route" /><StatePanel tone="error" title="Health check failed" description="The previous state is preserved. No change was applied." action="View operation" /></div></section>
      <Dialog open={dialogOpen} onOpenChange={setDialogOpen} title="Confirm operation" description="You can inspect the exact plan before applying it."><div className="dialog__form"><div className="notice"><Icon name="shield" /><p><strong>Management path protected</strong><span>SSH and panel access remain allowed.</span></p></div><div className="dialog__actions"><Button variant="ghost" onClick={() => setDialogOpen(false)}>Cancel</Button><Button variant="primary" onClick={() => { setDialogOpen(false); notify(); }}>Continue</Button></div></div></Dialog>
      <Toast message={toast} />
    </main>
  );
}
