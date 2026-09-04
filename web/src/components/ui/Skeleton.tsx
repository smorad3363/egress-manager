import { cn } from "../../lib/cn";

export function Skeleton({ className }: { className?: string }) {
  return <span className={cn("skeleton", className)} aria-hidden="true" data-slot="skeleton" />;
}
