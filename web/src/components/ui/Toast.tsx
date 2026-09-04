import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { Icon } from "../Icon";

export function Toast({ message }: { message: string | null }) {
  const reduceMotion = useReducedMotion();
  return (
    <div className="toast-region" aria-live="polite" aria-atomic="true">
      <AnimatePresence>
        {message ? (
          <motion.div
            className="toast"
            initial={reduceMotion ? false : { opacity: 0, y: 8 }}
            animate={{ opacity: 1, y: 0 }}
            exit={reduceMotion ? { opacity: 0 } : { opacity: 0, y: 8 }}
            transition={{ duration: reduceMotion ? 0 : 0.14 }}
            role="status"
          >
            <span className="toast__icon"><Icon name="shield" /></span>
            <span><strong>Request accepted</strong>{message}</span>
          </motion.div>
        ) : null}
      </AnimatePresence>
    </div>
  );
}
