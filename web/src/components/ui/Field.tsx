import type { InputHTMLAttributes, ReactNode, SelectHTMLAttributes } from "react";
import { cn } from "../../lib/cn";

type FieldShellProps = { label: string; hint?: string; error?: string; children: ReactNode };

function FieldShell({ label, hint, error, children }: FieldShellProps) {
  return (
    <label className="field">
      <span className="field__label">{label}</span>
      {children}
      {error ? <span className="field__error">{error}</span> : hint ? <span className="field__hint">{hint}</span> : null}
    </label>
  );
}

export function TextField({ label, hint, error, className, ...props }: InputHTMLAttributes<HTMLInputElement> & Omit<FieldShellProps, "children">) {
  return <FieldShell label={label} hint={hint} error={error}><input className={cn("input", error && "input--error", className)} {...props} /></FieldShell>;
}

export function SelectField({ label, hint, error, className, children, ...props }: SelectHTMLAttributes<HTMLSelectElement> & Omit<FieldShellProps, "children">) {
  return <FieldShell label={label} hint={hint} error={error}><select className={cn("input", error && "input--error", className)} {...props}>{children}</select></FieldShell>;
}
