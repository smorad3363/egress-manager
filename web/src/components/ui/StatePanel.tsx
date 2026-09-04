import { Icon } from "../Icon";
import { Button } from "./Button";

type StatePanelProps = {
  tone: "empty" | "error";
  title: string;
  description: string;
  action: string;
};

export function StatePanel({ tone, title, description, action }: StatePanelProps) {
  return (
    <div className={`state-panel state-panel--${tone}`}>
      <span className="state-panel__icon"><Icon name={tone === "error" ? "activity" : "route"} /></span>
      <h3>{title}</h3>
      <p>{description}</p>
      <Button size="sm" variant="ghost">{action}</Button>
    </div>
  );
}
