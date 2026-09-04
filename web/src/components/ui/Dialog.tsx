import { motion, useReducedMotion } from "motion/react";
import { useEffect, useRef, type ReactNode } from "react";
import { Icon } from "../Icon";
import { Button } from "./Button";

type DialogProps = {
  open: boolean;
  title: string;
  description: string;
  children: ReactNode;
  onOpenChange: (open: boolean) => void;
};

export function Dialog({ open, title, description, children, onOpenChange }: DialogProps) {
  const dialogRef = useRef<HTMLDialogElement>(null);
  const reduceMotion = useReducedMotion();

  useEffect(() => {
    const dialog = dialogRef.current;
    if (!dialog) return;
    if (open && !dialog.open) {
      dialog.showModal();
      window.requestAnimationFrame(() => {
        dialog.querySelector<HTMLElement>("[autofocus], input, select, textarea")?.focus();
      });
    }
    if (!open && dialog.open) dialog.close();
  }, [open]);

  return (
    <dialog
      className="dialog"
      ref={dialogRef}
      onCancel={(event) => { event.preventDefault(); onOpenChange(false); }}
      onClose={() => onOpenChange(false)}
      onClick={(event) => { if (event.target === event.currentTarget) onOpenChange(false); }}
    >
      <motion.div
        className="dialog__panel"
        initial={reduceMotion ? false : { opacity: 0, y: 10, scale: 0.985 }}
        animate={{ opacity: 1, y: 0, scale: 1 }}
        transition={{ duration: reduceMotion ? 0 : 0.16 }}
      >
        <div className="dialog__header">
          <div><h2>{title}</h2><p>{description}</p></div>
          <Button aria-label="Close dialog" size="icon" variant="ghost" onClick={() => onOpenChange(false)}><Icon name="close" /></Button>
        </div>
        {children}
      </motion.div>
    </dialog>
  );
}
