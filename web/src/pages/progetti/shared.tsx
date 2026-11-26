// Pezzi comuni delle schermate del PM: semaforo, barra di avanzamento, link alla voce.
import { Link } from "@tanstack/react-router";
import { useT } from "../../i18n/react";
import type { Signal } from "../../projectsApi";

/** Semaforo: pieno verde, mezzo pieno giallo, pieno rosso; il nome è sempre scritto accanto. */
export function SignalMark({ value, size = 10 }: { value: Signal; size?: number }) {
  const cls = value === "green" ? "bg-ok border-ok" : value === "none" ? "border-muted" : "border-danger"; // i18n-ok: classi CSS
  return (
    <span aria-hidden="true" className={`relative inline-block shrink-0 border-2 ${cls}`} style={{ width: size, height: size }}>
      {value === "amber" && <span className="absolute inset-y-0 left-0 w-1/2 bg-danger" />}
      {value === "red" && <span className="absolute inset-0 bg-danger" />}
    </span>
  );
}

export function SignalName({ value, className = "" }: { value: Signal; className?: string }) {
  const t = useT();
  return (
    <span className={`inline-flex items-center gap-1.5 whitespace-nowrap ${className}`}>
      <SignalMark value={value} />
      <span className={value === "green" || value === "none" ? "" : "font-semibold"}>{t(`progetti.common.signal.${value}`)}</span>
    </span>
  );
}

/** Barra sottile 0..100 con il valore leggibile da chi usa uno screen reader. */
export function ProgressBar({ percent, className = "" }: { percent: number; className?: string }) {
  const t = useT();
  const p = Math.max(0, Math.min(100, Math.round(percent)));
  return (
    <div
      role="progressbar"
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={p}
      aria-label={t("progetti.common.progress", { percent: p })}
      className={`h-1.5 w-full bg-hair ${className}`}
    >
      <div className="h-full bg-accent" style={{ width: `${p}%` }} />
    </div>
  );
}

export function ItemLink({ itemKey, title, className = "" }: { itemKey: string; title?: string; className?: string }) {
  return (
    <Link to="/e/$key" params={{ key: itemKey }} className={`inline-flex min-h-[44px] items-center gap-1.5 text-accent no-underline hover:underline md:min-h-0 ${className}`}>
      <span className="font-mono text-[11.5px] font-medium">{itemKey}</span>
      {title && <span className="text-fg">{title}</span>}
    </Link>
  );
}
