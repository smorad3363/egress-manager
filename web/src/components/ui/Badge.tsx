import type { HTMLAttributes } from "react";
import { cn } from "../../lib/cn";

export function Badge({ className, tone = "neutral", ...props }: HTMLAttributes<HTMLSpanElement> & { tone?: "success" | "warning" | "danger" | "neutral" }) {
  return <span className={cn("badge", `badge--${tone}`, className)} data-slot="badge" {...props} />;
}
