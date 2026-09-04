import { Icon } from "../Icon";
import { Button } from "./Button";

type StatePanelProps = {
  tone?: "empty" | "error";
  title: string;
  description: string;
  action: string;
  onAction?: () => void;
};

export function StatePanel({ tone = "empty", title, description, action, onAction }: StatePanelProps) {
  return (
    <div className={`state-panel state-panel--${tone}`}>
      <span className="state-panel__icon"><Icon name={tone === "error" ? "activity" : "route"} /></span>
      <h3>{title}</h3>
      <p>{description}</p>
      <Button size="sm" variant="ghost" onClick={onAction}>{action}</Button>
    </div>
  );
}
