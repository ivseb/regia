// Pezzi comuni delle schermate del PM: semaforo, barra di avanzamento, link alla voce.
import { Link } from "@tanstack/react-router";
import { useT } from "../../i18n/react";
import { useTypeNames } from "../../typesApi";
import { date } from "../../ui";
import type { Conflict, Signal } from "../../projectsApi";

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

/** Nome (o plurale) del SAL come lo ha configurato l'organizzazione. */
export function useClaimName() {
  const t = useT();
  const tn = useTypeNames();
  return {
    one: tn.name("progress_claim", t("progetti.fornitori.name.claim")),
    many: tn.plural("progress_claim", t("progetti.fornitori.name.claims")),
  };
}

/** La frase del conflitto: «Anche il progetto PRJ-7 «Titolo» modifica IDP fino al 30/04/2027». */
export function useConflictText() {
  const t = useT();
  return (c: Conflict): string => {
    const params = { key: c.project?.key ?? "", title: c.project?.title ?? "", application: c.application, date: c.end ? date(c.end) : "" };
    if (c.restricted) return t(c.end ? "progetti.fornitori.conflict.restrictedUntil" : "progetti.fornitori.conflict.restrictedOpen", params);
    return t(c.end ? "progetti.fornitori.conflict.until" : "progetti.fornitori.conflict.open", params);
  };
}

/** Segno «Conflitto» (testo e triangolo, non solo colore). */
export function ConflictChip({ conflicts, className = "" }: { conflicts: Conflict[]; className?: string }) {
  const t = useT();
  const text = useConflictText();
  if (conflicts.length === 0) return null;
  const title = `${t("progetti.fornitori.conflict.markTitle", { count: conflicts.length })}: ${conflicts.map(text).join(" · ")}`;
  return (
    <span className={`inline-flex shrink-0 items-center gap-1 border border-flag px-1 text-[10.5px] font-semibold whitespace-nowrap text-flag ${className}`} title={title} aria-label={title}>
      <span aria-hidden="true">▲</span>
      {t("progetti.fornitori.conflict.mark")}
    </span>
  );
}

/** Segno «SAL da accettare: n». */
export function PendingChip({ count, className = "" }: { count: number; className?: string }) {
  const t = useT();
  const name = useClaimName();
  if (count <= 0) return null;
  const title = t("progetti.fornitori.pending.markTitle", { name: count === 1 ? name.one : name.many, count });
  return (
    <span className={`inline-flex shrink-0 items-center gap-1 border border-accent px-1 text-[10.5px] font-semibold whitespace-nowrap text-accent ${className}`} title={title} aria-label={title}>
      <span aria-hidden="true">●</span>
      {name.one} · {count}
    </span>
  );
}
