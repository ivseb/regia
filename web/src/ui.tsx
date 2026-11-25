import type { ReactNode } from "react";
import { ApiError, type State } from "./api";
import { tr } from "./i18n/core";
import { useT } from "./i18n/react";
import { fmtDate, fmtRelative, fmtNumber } from "./i18n/format";
import { useStateName } from "./stateLabels";

// Fasi fuori dal percorso principale dello stage-gate (sospensioni, esiti negativi):
// le decide il processo (off_path); i nomi sono il ripiego per i workflow semplici.
const OFF_PATH = new Set(["Sospesa", "Respinta", "Ritirata"]);

export function mainPath(states: State[] | null | undefined): State[] {
  if (!states) return [];
  const anyFlag = states.some((s) => s.off_path !== undefined && s.off_path);
  return states.filter((s) => !(anyFlag ? !!s.off_path : OFF_PATH.has(s.name)));
}

/** Barra a segmenti dello stage-gate: fasi completate in --fg, corrente in --accent, future in --hair. */
export function StageGate({ states, current, size = "sm", names = false }: { states: State[]; current: string; size?: "sm" | "lg"; names?: boolean }) {
  const t = useT();
  const stateName = useStateName();
  const path = mainPath(states);
  const idx = path.findIndex((s) => s.name === current);
  const offPath = idx < 0;
  const h = size === "lg" ? "h-1.5" : "h-[5px]";
  const w = size === "lg" ? "flex-1" : "w-2.5";
  const showNames = names && size === "lg" && !offPath;
  return (
    <span role="group" className={`${showNames ? "flex w-full" : "inline-flex"} flex-col gap-1`} aria-label={t("common.phaseLabel", { name: stateName(current, path[idx]?.labels) })}>
      <span className="flex gap-0.5" aria-hidden="true">
        {path.map((s, i) => (
          <span
            key={s.id}
            title={stateName(s.name, s.labels)}
            className={`${w} ${h} ${offPath ? "bg-hair" : i < idx ? "bg-fg" : i === idx ? "bg-accent" : "bg-hair"}`}
          />
        ))}
      </span>
      {showNames && (
        <>
          {/* Con spazio: il nome di ogni fase sotto il suo segmento. */}
          <span className="hidden gap-0.5 md:flex" aria-hidden="true">
            {path.map((s, i) => (
              <span key={s.id} className={`min-w-0 flex-1 truncate text-[11.5px] ${i === idx ? "font-semibold text-fg" : "text-muted"}`} title={stateName(s.name, s.labels)}>
                {stateName(s.name, s.labels)}
              </span>
            ))}
          </span>
          {/* Sul telefono: dove sei, quante fasi, e la prossima. */}
          <span className="text-[12.5px] md:hidden">
            {t("common.phase")} <span className="font-semibold tabular">{t("common.phaseOf", { n: idx + 1, total: path.length })}</span>
            {path[idx + 1] && <span className="text-muted"> · {t("common.phaseNext", { name: stateName(path[idx + 1].name, path[idx + 1].labels) })}</span>}
          </span>
        </>
      )}
      {size === "sm" && (
        <span className="text-[12px]">
          {!offPath && <span className="font-mono text-muted tabular">{String(idx + 1).padStart(2, "0")} </span>}
          <span className={offPath ? "font-semibold text-flag" : ""}>{current}</span>
        </span>
      )}
    </span>
  );
}

export function euro(cents: number | null | undefined): string {
  if (cents == null) return "—";
  const v = cents / 100;
  if (v >= 1000) return `${fmtNumber(v / 1000, { maximumFractionDigits: 1 })} k€`;
  return `${fmtNumber(v, { maximumFractionDigits: 0 })} €`;
}

export function euroRange(low: number | null, high: number | null): string {
  if (low == null && high == null) return "—";
  if (low == null || high == null || low === high) return euro(high ?? low);
  return `${euro(low).replace(" k€", "")}–${euro(high)}`;
}

/** «2 ore fa», «2 hours ago»: tempo relativo nella lingua della persona; oltre un mese, la data. */
export function ago(iso: string): string {
  return fmtRelative(iso);
}

export function date(iso: string | null | undefined): string {
  return fmtDate(iso);
}

export const DRIVERS: Record<string, string> = {
  get strategic() {
    return tr("common.driver.strategic");
  },
  get regulatory() {
    return tr("common.driver.regulatory");
  },
  get operational() {
    return tr("common.driver.operational");
  },
  get efficiency() {
    return tr("common.driver.efficiency");
  },
  get risk() {
    return tr("common.driver.risk");
  },
};

export const DECISIONS: Record<string, string> = {
  get pending() {
    return tr("common.decision.pending");
  },
  get approved() {
    return tr("common.decision.approved");
  },
  get approved_with_conditions() {
    return tr("common.decision.approved_with_conditions");
  },
  get rejected() {
    return tr("common.decision.rejected");
  },
  get deferred() {
    return tr("common.decision.deferred");
  },
  get skipped() {
    return tr("common.decision.skipped");
  },
};

export function ErrorNote({ error }: { error: unknown }) {
  const t = useT();
  if (!error) return null;
  const msg = error instanceof ApiError ? error.message : t("errors.network");
  return (
    <p role="alert" className="text-danger">
      <span className="font-semibold">{t("common.errorLabel")}: </span>
      {msg}
    </p>
  );
}

/** Caricamento: righe grigie al posto della pagina vuota; il testo resta per i lettori di schermo. */
export function Loading({ className = "", rows = 3, label }: { className?: string; rows?: number; label?: string }) {
  const t = useT();
  const widths = ["w-2/3", "w-full", "w-5/6", "w-1/2"];
  return (
    <div role="status" className={className}>
      <span className="sr-only">{label ?? t("common.loading")}</span>
      <div aria-hidden="true" className="flex max-w-3xl animate-pulse flex-col gap-2.5">
        {Array.from({ length: rows }, (_, i) => (
          <div key={i} className={`h-3.5 ${widths[i % widths.length]} bg-hair`} />
        ))}
      </div>
    </div>
  );
}

export function SectionTitle({ children, aside }: { children: ReactNode; aside?: ReactNode }) {
  return (
    <div className="flex items-baseline justify-between border-b-2 border-rule pb-1.5">
      <h3 className="label m-0">{children}</h3>
      {aside}
    </div>
  );
}

/**
 * Titolo da mostrare. Il titolo salvato di un impatto è «applicazione · richiesta» (serve alla
 * ricerca): nelle liste e nelle notifiche l'applicazione fa da titolo e la richiesta da
 * etichetta di contesto, senza ripetere la stessa cosa due volte.
 */
export function titleParts(type: string | undefined, title: string): { main: string; context?: string } {
  if (type !== "impact") return { main: title };
  const i = title.indexOf(" · ");
  if (i <= 0) return { main: title };
  return { main: title.slice(0, i), context: title.slice(i + 3) };
}

/** Titolo di un elemento con, se serve, l'etichetta di contesto (vedi titleParts). */
export function ElementTitle({ type, title }: { type?: string; title: string }) {
  const t = useT();
  const { main, context } = titleParts(type, title);
  return (
    <>
      {main}
      {context && <span className="ml-2 text-[12px] font-normal text-muted">{t("common.contextRequest", { name: context })}</span>}
    </>
  );
}

export function Initials({ name }: { name: string }) {
  const ini = name
    .split(/\s+/)
    .slice(0, 2)
    .map((p) => p[0])
    .join("")
    .toUpperCase();
  return (
    <span className="inline-flex size-6 shrink-0 items-center justify-center rounded-full bg-accent text-[10.5px] font-bold text-accent-fg">
      {ini}
    </span>
  );
}

/** Nomi dei ruoli sugli oggetti del pacchetto di partenza (i ruoli del tenant arriveranno dall'API). */
export const ROLE_NAMES: Record<string, string> = {
  get requester() {
    return tr("common.role.requester");
  },
  get business_owner() {
    return tr("common.role.business_owner");
  },
  get sponsor() {
    return tr("common.role.sponsor");
  },
  get demand_manager() {
    return tr("common.role.demand_manager");
  },
  get pmo() {
    return tr("common.role.pmo");
  },
  get project_manager() {
    return tr("common.role.project_manager");
  },
  get architect() {
    return tr("common.role.architect");
  },
  get finance() {
    return tr("common.role.finance");
  },
  get compliance() {
    return tr("common.role.compliance");
  },
  get technical_analyst() {
    return tr("common.role.technical_analyst");
  },
  get team_lead() {
    return tr("common.role.team_lead");
  },
  get review_owner() {
    return tr("common.role.review_owner");
  },
  get review_compiler() {
    return tr("common.role.review_compiler");
  },
  get reviewer() {
    return tr("common.role.reviewer");
  },
  get review_lead() {
    return tr("common.role.review_lead");
  },
};

export const CRITICALITY: Record<string, string> = {
  get low() {
    return tr("common.criticality.low");
  },
  get medium() {
    return tr("common.criticality.medium");
  },
  get high() {
    return tr("common.criticality.high");
  },
  get critical() {
    return tr("common.criticality.critical");
  },
};
export const CHANGE_KINDS: Record<string, string> = {
  get new() {
    return tr("common.changeKind.new");
  },
  get change() {
    return tr("common.changeKind.change");
  },
  get config() {
    return tr("common.changeKind.config");
  },
  get decommission() {
    return tr("common.changeKind.decommission");
  },
  get none() {
    return tr("common.changeKind.none");
  },
};

/** Criticità con il segnale arancione per le applicazioni critiche (regolamentate). */
export function Criticality({ value, critical }: { value: string | null | undefined; critical?: boolean }) {
  const t = useT();
  return (
    <span className="inline-flex items-center gap-1.5 whitespace-nowrap">
      <span className={value === "critical" || value === "high" ? "font-semibold" : ""}>{value ? CRITICALITY[value] ?? value : "—"}</span>
      {critical && (
        <span className="border border-flag px-1 text-[10.5px] font-semibold text-flag" title={t("common.doraTitle")}>
          DORA
        </span>
      )}
    </span>
  );
}
