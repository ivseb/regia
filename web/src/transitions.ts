import type { TransitionView } from "./api";

/** Il passaggio in avanti per primo (e principale); gli altri restano nell'ordine del processo. */
export function primaryFirst(ts: TransitionView[]): TransitionView[] {
  const i = ts.findIndex((t) => t.forward);
  return i > 0 ? [ts[i], ...ts.slice(0, i), ...ts.slice(i + 1)] : ts;
}
