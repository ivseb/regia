// Pezzi comuni delle pagine della conformità: stato di copertura (quadratino e parola, mai
// solo il colore), barra di copertura, sigla della normativa (arancio: elemento normativo),
// stato di un'evidenza e nome di un perimetro.
import { Link } from "@tanstack/react-router";
import { useMe } from "../shell";
import { date } from "../ui";
import { EVIDENCE_STATUS, STATUS_NAMES, type Counts, type CoverageStatus, type EvidenceRef, type EvidenceStatus, type Scope } from "../grcApi";
import { tr } from "../i18n/core";

const STATUS_STYLE: Record<CoverageStatus, { dot: string; text: string }> = {
  covered: { dot: "bg-ok", text: "text-ok" },
  partial: { dot: "border border-ok bg-transparent", text: "text-fg" },
  no_evidence: { dot: "border border-danger bg-transparent", text: "text-danger" },
  no_control: { dot: "bg-danger", text: "text-danger font-semibold" },
  not_applicable: { dot: "bg-hair", text: "text-muted line-through" },
};

export function CoverageMark({ status, expiring }: { status: CoverageStatus | ""; expiring?: boolean }) {
  if (!status) return <span className="text-muted">—</span>;
  const s = STATUS_STYLE[status];
  return (
    <span className="inline-flex items-center gap-1.5 whitespace-nowrap text-[12.5px]">
      <span className={`size-2 shrink-0 ${s.dot}`} aria-hidden="true" />
      <span className={s.text}>{STATUS_NAMES[status]}</span>
      {expiring && <span className="text-flag">{tr("governance.grcui.in_scadenza")}</span>}
    </span>
  );
}

const EV_STYLE: Record<EvidenceStatus, string> = {
  valid: "text-ok",
  expiring: "text-flag",
  expired: "text-danger",
  draft: "text-accent",
  rejected: "text-muted line-through",
  revoked: "text-muted line-through",
};

export function EvidenceMark({ status }: { status: EvidenceStatus }) {
  return <span className={`whitespace-nowrap text-[12.5px] ${EV_STYLE[status]}`}>{EVIDENCE_STATUS[status]}</span>;
}

/** Barra di copertura: coperti, parziali, senza evidenza, senza controllo. */
export function CoverageBar({ c, wide }: { c: Counts; wide?: boolean }) {
  const total = Math.max(c.applicable, 1);
  const w = (n: number) => `${(100 * n) / total}%`;
  return (
    <div className={`flex items-center gap-2 ${wide ? "min-w-[180px]" : "min-w-[120px]"}`}>
      <div
        className="flex h-2 flex-1 gap-px bg-hair"
        role="img"
        aria-label={tr("governance.grcui.coperti_parziali_senza_evidenza_senza", { covered: c.covered, partial: c.partial, no_evidence: c.no_evidence, no_control: c.no_control, applicable: c.applicable })}
      >
        {c.covered > 0 && <span className="h-full bg-ok" style={{ width: w(c.covered) }} />}
        {c.partial > 0 && <span className="h-full bg-muted" style={{ width: w(c.partial) }} />}
        {c.no_evidence > 0 && <span className="h-full bg-danger opacity-50" style={{ width: w(c.no_evidence) }} />}
        {c.no_control > 0 && <span className="h-full bg-danger" style={{ width: w(c.no_control) }} />}
      </div>
      <span className="w-10 text-right font-mono text-[12.5px] font-semibold tabular">{c.applicable ? `${c.percent}%` : "—"}</span>
    </div>
  );
}

/** Sigla della normativa: l'arancio è riservato agli elementi normativi. */
export function FrameworkTag({ k, title }: { k: string; title?: string }) {
  return (
    <span className="inline-block border border-flag px-1 font-mono text-[10.5px] font-semibold text-flag uppercase" title={title}>
      {k}
    </span>
  );
}

export function scopeLabel(s: Scope): string {
  if (s.kind === "tenant") return tr("governance.grcui.tutta_l_organizzazione");
  return s.key ? `${s.key} · ${s.name}` : s.name;
}

/** Collegamento a un'evidenza: alla sua pagina, o al requisito di progetto verificato. */
export function EvidenceLink({ e }: { e: EvidenceRef }) {
  return (
    <span className="inline-flex flex-wrap items-baseline gap-x-2">
      <Link to="/e/$key" params={{ key: e.key }} search={{ scheda: "dettagli" }} className="font-mono text-[12px] font-medium">
        {e.key}
      </Link>
      <span className="min-w-0">{e.title}</span>
      <EvidenceMark status={e.status} />
      {e.valid_until && <span className="text-[12px] text-muted tabular">{tr("governance.requirements.fino_al", { date: date(e.valid_until) })}</span>}
    </span>
  );
}

export function useGrcPerms() {
  const perms = useMe().data?.active_tenant?.permissions ?? [];
  const admin = perms.includes("*") || perms.includes("grc.admin");
  return { admin, read: admin || perms.includes("grc.read") };
}
