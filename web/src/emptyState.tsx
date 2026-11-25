import type { ReactNode } from "react";

/**
 * Stato vuoto: dice che cosa manca, a cosa serve e qual è il primo passo (il pulsante o il link).
 * Piccolo e senza decorazioni: sta dentro tabelle, elenchi e sezioni.
 */
export function EmptyState({ title, text, action }: { title: string; text: ReactNode; action?: ReactNode }) {
  return (
    <div className="flex max-w-xl flex-col items-start gap-2 py-2" data-testid="stato-vuoto">
      <p className="m-0 font-semibold text-fg">{title}</p>
      <p className="m-0 text-[13px] text-muted">{text}</p>
      {action}
    </div>
  );
}
