// Pezzi comuni delle pagine della continuità: sotto-navigazione, durata in ore o giorni,
// segno ok/non ok, azione diretta di un'incoerenza, riga di un'incoerenza.
import { useEffect, useState, type ReactNode } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ApiError } from "../../api";
import { useMe } from "../../shell";
import { ErrorNote } from "../../ui";
import { Select } from "../../select";
import { useT } from "../../i18n/react";
import { ProblemActions } from "../ProblemActions";
import { actionLink, continuityApi, continuityCan, fmtMinutes, issueText, splitMinutes, toMinutes, type Can, type DurationUnit, type Issue } from "../../continuityApi";

export type ContinuityView = "panoramica" | "bia" | "test";

/** Sotto-navigazione della scheda Continuità: panoramica, BIA, piano dei test. */
export function ContinuityNav({ active }: { active: ContinuityView }) {
  const t = useT();
  const cls = (on: boolean) => `pb-1 no-underline ${on ? "border-b-2 border-fg font-semibold text-fg" : "text-muted hover:text-fg"}`;
  return (
    <nav aria-label={t("continuita.nav.label")} className="flex flex-wrap gap-x-5 gap-y-1 text-[13px]">
      <Link to="/architettura/continuita" className={cls(active === "panoramica")} aria-current={active === "panoramica" ? "page" : undefined}>
        {t("continuita.nav.overview")}
      </Link>
      <Link to="/architettura/continuita/bia" className={cls(active === "bia")} aria-current={active === "bia" ? "page" : undefined}>
        {t("continuita.nav.bia")}
      </Link>
      <Link to="/architettura/continuita/test" className={cls(active === "test")} aria-current={active === "test" ? "page" : undefined}>
        {t("continuita.nav.tests")}
      </Link>
    </nav>
  );
}

/** Durata scritta in minuti, ore o giorni; il valore esce sempre in minuti (o null). */
export function DurationInput({
  label,
  value,
  onChange,
  disabled,
  id,
}: {
  label: string;
  value: number | null;
  onChange: (m: number | null) => void;
  disabled?: boolean;
  id?: string;
}) {
  const t = useT();
  const [draft, setDraft] = useState(() => splitMinutes(value));
  // Il valore cambiato da fuori (per esempio «Usa la proposta») riscrive il campo.
  useEffect(() => {
    if (toMinutes(draft.value, draft.unit) !== value) setDraft(splitMinutes(value));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value]);
  const set = (v: string, u: DurationUnit) => {
    setDraft({ value: v, unit: u });
    onChange(toMinutes(v, u));
  };
  return (
    <fieldset className="m-0 flex min-w-0 flex-col gap-1 border-0 p-0">
      <legend className="label mb-1">{label}</legend>
      <div className="flex gap-1.5">
        <input
          id={id}
          className="field w-20 tabular"
          inputMode="decimal"
          aria-label={t("continuita.duration.value", { label })}
          value={draft.value}
          disabled={disabled}
          onChange={(e) => set(e.target.value, draft.unit)}
        />
        <Select className="field w-28" aria-label={t("continuita.duration.unit", { label })} value={draft.unit} disabled={disabled} onChange={(e) => set(draft.value, e.target.value as DurationUnit)}>
          <option value="min">{t("continuita.unit.min")}</option>
          <option value="h">{t("continuita.unit.h")}</option>
          <option value="d">{t("continuita.unit.d")}</option>
        </Select>
      </div>
    </fieldset>
  );
}

/** Ok, non ok o non valutabile, sempre con il testo (mai solo il colore). */
export function OkMark({ ok }: { ok: boolean | null | undefined }) {
  const t = useT();
  if (ok == null) return <span className="text-[12px] text-muted">{t("continuita.ok.unknown")}</span>;
  return ok ? (
    <span className="text-[12px] font-semibold text-ok">{t("continuita.ok.yes")}</span>
  ) : (
    <span className="text-[12px] font-semibold text-danger">{t("continuita.ok.no")}</span>
  );
}

/** Minuti con il segno «—» quando mancano, in cifre tabellari. */
export function Minutes({ m, className = "" }: { m: number | null | undefined; className?: string }) {
  return <span className={`tabular ${className}`}>{fmtMinutes(m)}</span>;
}

/** Il collegamento all'azione diretta di un'incoerenza. */
export function IssueAction({ issue }: { issue: Issue }) {
  const t = useT();
  const l = actionLink(issue.action);
  const label = t(`continuita.action.${issue.action.kind}`);
  const cls = "text-[12.5px] text-accent underline";
  switch (l.to) {
    case "/e/$key":
      return (
        <Link to="/e/$key" params={l.params} className={cls}>
          {label}
        </Link>
      );
    case "/documenti":
      return (
        <Link to="/documenti" className={cls}>
          {label}
        </Link>
      );
    case "/architettura/continuita/test":
      return (
        <Link to="/architettura/continuita/test" search={l.search} className={cls}>
          {label}
        </Link>
      );
    case "/architettura/continuita/bia":
      return (
        <Link to="/architettura/continuita/bia" search={l.search} className={cls}>
          {label}
        </Link>
      );
  }
}

/** Una riga del «cosa manca»: frase, azione diretta e, aprendo la riga, il lavoro nel team. */
export function IssueRow({ issue, aside }: { issue: Issue; aside?: ReactNode }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  return (
    <li className="flex flex-col gap-1.5 border-b border-hair py-2.5">
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
        <span className="min-w-0 flex-[1_1_260px] break-words">{issueText(issue)}</span>
        {aside}
        <IssueAction issue={issue} />
        <button type="button" className="bg-transparent p-0 text-[12.5px] text-muted underline" aria-expanded={open} onClick={() => setOpen(!open)}>
          {open ? t("continuita.issue.lessWork") : t("continuita.issue.moreWork")}
        </button>
      </div>
      {(issue.team || issue.source) && (
        <span className="text-[12px] text-muted">
          {issue.team && t("continuita.issue.team", { name: issue.team.name })}
          {issue.team && issue.source && " · "}
          {issue.source && t("continuita.issue.source", { key: issue.source.key })}
        </span>
      )}
      {open && (
        <div className="pl-0 md:pl-3">
          <ProblemActions kind="continuity" refKey={issue.ref} compact />
        </div>
      )}
    </li>
  );
}

/** Etichetta breve della gravità. */
export function SeverityLabel({ severity }: { severity: "high" | "medium" }) {
  const t = useT();
  return (
    <span className={`text-[11.5px] font-semibold ${severity === "high" ? "text-danger" : "text-flag"}`}>
      {severity === "high" ? t("continuita.severity.high") : t("continuita.severity.medium")}
    </span>
  );
}

/** Il modulo non è ancora preparato nel tenant: cosa fa la preparazione e chi la può lanciare. */
export function NotReady({ can }: { can: Can }) {
  const t = useT();
  const qc = useQueryClient();
  const setup = useMutation({
    mutationFn: continuityApi.setup,
    onSuccess: () => qc.invalidateQueries({ queryKey: ["continuity"] }),
  });
  return (
    <section className="flex max-w-3xl flex-col gap-2 border-l-4 border-accent pl-4" aria-label={t("continuita.setup.title")}>
      <h3 className="m-0 text-[16px] font-bold">{t("continuita.setup.title")}</h3>
      <p className="m-0">{t("continuita.setup.text")}</p>
      <ul className="m-0 flex flex-col gap-0.5 pl-5 text-[13px]">
        <li>{t("continuita.setup.item1")}</li>
        <li>{t("continuita.setup.item2")}</li>
        <li>{t("continuita.setup.item3")}</li>
      </ul>
      {can.admin ? (
        <button type="button" className="btn btn-primary self-start" disabled={setup.isPending} onClick={() => setup.mutate()}>
          {setup.isPending ? t("continuita.setup.running") : t("continuita.setup.button")}
        </button>
      ) : (
        <p className="m-0 text-[12.5px] text-muted">{t("continuita.setup.askAdmin")}</p>
      )}
      <ErrorNote error={setup.error} />
    </section>
  );
}

/** Errore di una pagina della continuità: se il modulo non è preparato, la spiegazione e il pulsante. */
export function ContinuityError({ error }: { error: unknown }) {
  const perms = useMe().data?.active_tenant?.permissions ?? [];
  if (error instanceof ApiError && error.code === "validation.continuity.not_ready") return <NotReady can={continuityCan(perms)} />;
  return <ErrorNote error={error} />;
}
