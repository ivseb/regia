// «Tutto ciò che si configura evolve nel tempo»: componente unico del ciclo
// modifica → anteprima → pubblica con data → transizione (docs/sviluppo/versioni-vigore.md).
//
// È generico: non conosce i processi. Un modulo che versiona qualcosa (questionari, scorecard,
// politiche di approvazione, matrici SLA, politiche della pipeline, regole, cataloghi di requisiti,
// budget) gli passa due funzioni (anteprima e pubblicazione) e, se vuole, le etichette dei gruppi di
// elementi da spostare. Lo stato di una versione (in vigore, programmata, deprecata, conclusa) si
// mostra con VersionLifecycle, le date di una versione già pubblicata si cambiano con VigoreDates.
import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Select } from "./select";
import { ErrorNote, SectionTitle, date } from "./ui";
import { useT } from "./i18n/react";

/** Regola di transizione: modo (auto | resta | manuale) per fase, più il predefinito. */
export type FlowRule = { default?: string; phases?: Record<string, string> };
/** Un gruppo di elementi già avviati: stessa versione di partenza, stessa fase. */
export type FlowGroup = { from: string; phase: string; count: number; mode: string; missing?: boolean };
export type FlowPreview = {
  immediate: boolean;
  effectiveFrom: string;
  /** Versione oggi in vigore (testo, es. «v2»), vuoto se non ce n'è. */
  inForce: string;
  /** Versione programmata che questa sostituirebbe. */
  withdrawn: string;
  rule: FlowRule;
  groups: FlowGroup[];
  counts: { auto: number; stay: number; manual: number };
  errors: number;
};
export type FlowOptions = { effectiveFrom: string; effectiveTo: string; rule: FlowRule | null };

type Props = {
  /** Chiave unica della query (es. ["process", key]). */
  scope: unknown[];
  /** C'è una bozza da pubblicare? */
  hasDraft: boolean;
  /** Nome della versione che nasce (es. «v3»). */
  versionLabel: string;
  loadPreview: (o: FlowOptions) => Promise<FlowPreview>;
  publish: (o: FlowOptions) => Promise<unknown>;
  onPublished?: () => void;
  /** Come si chiamano i gruppi (es. «Richieste»). */
  itemsLabel?: string;
};

const MODES = ["auto", "resta", "manuale"] as const;

function today(): string {
  const d = new Date();
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

export function VersionFlow({ scope, hasDraft, versionLabel, loadPreview, publish, onPublished, itemsLabel }: Props) {
  const t = useT();
  const [step, setStep] = useState<1 | 2 | 3>(1);
  const [when, setWhen] = useState<"now" | "date">("now");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [overrides, setOverrides] = useState<Record<string, string>>({});
  const [done, setDone] = useState<{ scheduled: boolean; from: string } | null>(null);
  const effectiveFrom = when === "date" && from ? from : "";
  const q = useQuery({
    queryKey: [...scope, "versionFlow", effectiveFrom, to, overrides],
    queryFn: async () => {
      const first = await loadPreview({ effectiveFrom, effectiveTo: to, rule: null });
      if (!Object.keys(overrides).length) return first;
      return loadPreview({ effectiveFrom, effectiveTo: to, rule: { default: first.rule.default, phases: { ...first.rule.phases, ...overrides } } });
    },
    enabled: hasDraft && step >= 2 && !done,
  });
  useEffect(() => {
    if (!hasDraft) setStep(1);
  }, [hasDraft]);
  const pub = useMutation({
    mutationFn: () => publish({ effectiveFrom, effectiveTo: to, rule: q.data ? { default: q.data.rule.default, phases: { ...q.data.rule.phases, ...overrides } } : null }),
    onSuccess: () => {
      setDone({ scheduled: !!q.data && !q.data.immediate, from: q.data?.effectiveFrom ?? "" });
      setStep(1);
      setOverrides({});
      onPublished?.();
    },
  });
  const phases = useMemo(() => {
    const seen = new Map<string, FlowGroup>();
    for (const g of q.data?.groups ?? []) if (!seen.has(g.phase)) seen.set(g.phase, g);
    return [...seen.keys()];
  }, [q.data]);
  const modeOf = (phase: string) => overrides[phase] ?? q.data?.rule.phases?.[phase] ?? q.data?.rule.default ?? "resta";
  const steps = [t("configurazione.versioni.step.edit"), t("configurazione.versioni.step.preview"), t("configurazione.versioni.step.publish"), t("configurazione.versioni.step.transition")];
  const current = done ? 4 : step;
  const d = q.data;
  const blocked = !d || d.errors > 0;
  return (
    <section id="pubblica-con-data" aria-label={t("configurazione.versioni.title")} className="flex scroll-mt-4 flex-col gap-3">
      <SectionTitle>{t("configurazione.versioni.title")}</SectionTitle>
      <ol className="m-0 flex list-none flex-wrap gap-x-5 gap-y-1 p-0" aria-label={t("configurazione.versioni.stepsLabel")}>
        {steps.map((s, i) => (
          <li key={s} aria-current={i + 1 === current ? "step" : undefined} className={`flex items-center gap-1.5 text-[13px] ${i + 1 === current ? "font-semibold" : i + 1 < current ? "" : "text-muted"}`}>
            <span aria-hidden="true" className={`inline-block h-2.5 w-2.5 ${i + 1 < current ? "bg-fg" : i + 1 === current ? "bg-accent" : "border border-muted"}`} />
            <span className="font-mono text-[11px] text-muted tabular" aria-hidden="true">{String(i + 1).padStart(2, "0")}</span>
            {s}
          </li>
        ))}
      </ol>

      {!hasDraft && !done && <p className="m-0 max-w-3xl text-muted">{t("configurazione.versioni.noDraft")}</p>}

      {hasDraft && step === 1 && !done && (
        <div className="flex max-w-3xl flex-col gap-2">
          <p className="m-0">{t("configurazione.versioni.editHint", { version: versionLabel })}</p>
          <div>
            <button className="btn btn-primary" onClick={() => setStep(2)}>{t("configurazione.versioni.toPreview")}</button>
          </div>
        </div>
      )}

      {hasDraft && step >= 2 && !done && (
        <div className="flex flex-col gap-4">
          <fieldset className="m-0 flex max-w-3xl flex-col gap-2 border-0 p-0">
            <legend className="label mb-1">{t("configurazione.versioni.whenLegend")}</legend>
            <label className="flex items-center gap-2">
              <input type="radio" name="vigore" checked={when === "now"} onChange={() => setWhen("now")} />
              {t("configurazione.versioni.whenNow")}
            </label>
            <label className="flex flex-wrap items-center gap-2">
              <input type="radio" name="vigore" checked={when === "date"} onChange={() => { setWhen("date"); if (!from) setFrom(today()); }} />
              {t("configurazione.versioni.whenFrom")}
              <input
                type="date"
                className="field max-w-[200px] tabular"
                aria-label={t("configurazione.versioni.effectiveFrom")}
                min={today()}
                value={from}
                disabled={when !== "date"}
                onChange={(e) => setFrom(e.target.value)}
              />
            </label>
            <label className="flex flex-wrap items-center gap-2">
              <span className="text-muted">{t("configurazione.versioni.effectiveTo")}</span>
              <input type="date" className="field max-w-[200px] tabular" aria-label={t("configurazione.versioni.effectiveTo")} min={today()} value={to} onChange={(e) => setTo(e.target.value)} />
              <span className="text-[12.5px] text-muted">{t("configurazione.versioni.effectiveToHint")}</span>
            </label>
          </fieldset>

          <ErrorNote error={q.error} />
          {d && (
            <div className="flex flex-col gap-3">
              <p className="m-0 max-w-3xl font-semibold">
                {d.immediate
                  ? t("configurazione.versioni.summaryNow", { version: versionLabel })
                  : t("configurazione.versioni.summaryFrom", { version: versionLabel, date: date(d.effectiveFrom) })}
                {!d.immediate && d.inForce ? ` ${t("configurazione.versioni.untilThen", { current: d.inForce })}` : ""}
              </p>
              {d.withdrawn && <p className="m-0 text-[12.5px] text-flag">{t("configurazione.versioni.withdraws", { version: d.withdrawn })}</p>}
              {d.errors > 0 && <p role="alert" className="m-0 text-danger">{t("configurazione.versioni.draftErrors", { count: d.errors })}</p>}
              <div>
                <h3 className="label m-0 mb-1">{t("configurazione.versioni.transitionTitle")}</h3>
                <p className="m-0 mb-2 max-w-3xl text-[12.5px] text-muted">{t("configurazione.versioni.transitionHelp")}</p>
                {d.groups.length === 0 ? (
                  <p className="m-0 text-muted">{t("configurazione.versioni.noItems")}</p>
                ) : (
                  <div className="overflow-x-auto">
                    <table className="w-full border-collapse tabular">
                      <thead>
                        <tr className="border-b border-hair text-left">
                          <th className="label py-1.5 pr-3 font-normal">{t("configurazione.versioni.col.from")}</th>
                          <th className="label px-3 py-1.5 font-normal">{t("configurazione.versioni.col.phase")}</th>
                          <th className="label px-3 py-1.5 font-normal">{itemsLabel ?? t("configurazione.versioni.itemsDefault")}</th>
                          <th className="label py-1.5 pl-3 font-normal">{t("configurazione.versioni.col.what")}</th>
                        </tr>
                      </thead>
                      <tbody>
                        {d.groups.map((g) => (
                          <tr key={`${g.from}-${g.phase}`} className="border-b border-hair">
                            <td className="py-2 pr-3 font-mono text-[12.5px]">{g.from}</td>
                            <td className="px-3 py-2">{g.phase || "—"}</td>
                            <td className="px-3 py-2 font-mono text-[12.5px]">{g.count}</td>
                            <td className="py-2 pl-3">
                              <Select
                                aria-label={t("configurazione.versioni.modeFor", { phase: g.phase })}
                                className="field py-1"
                                value={modeOf(g.phase)}
                                onChange={(e) => setOverrides({ ...overrides, [g.phase]: e.target.value })}
                              >
                                {MODES.map((m) => (
                                  <option key={m} value={m}>{t(`configurazione.versioni.mode.${m}`)}</option>
                                ))}
                              </Select>
                              {g.missing && <span className="ml-2 text-[12px] text-flag">{t("configurazione.versioni.missingPhase")}</span>}
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                )}
                {phases.length > 0 && (
                  <p className="m-0 mt-2 text-[12.5px] text-muted">
                    {t("configurazione.versioni.counts", { auto: d.counts.auto, stay: d.counts.stay, manual: d.counts.manual })}
                  </p>
                )}
              </div>
            </div>
          )}

          {step === 2 && (
            <div className="flex flex-wrap gap-2">
              <button className="btn" onClick={() => setStep(1)}>{t("configurazione.versioni.back")}</button>
              <button className="btn btn-primary" disabled={blocked || (when === "date" && !from)} onClick={() => setStep(3)}>{t("configurazione.versioni.toPublish")}</button>
            </div>
          )}
          {step === 3 && d && (
            <div className="flex max-w-3xl flex-col gap-2 border-y-2 border-rule py-3">
              <p className="m-0">
                {d.immediate
                  ? t("configurazione.versioni.confirmNow", { version: versionLabel, auto: d.counts.auto })
                  : t("configurazione.versioni.confirmFrom", { version: versionLabel, date: date(d.effectiveFrom), auto: d.counts.auto })}
              </p>
              <div className="flex flex-wrap gap-2">
                <button className="btn" onClick={() => setStep(2)}>{t("configurazione.versioni.back")}</button>
                <button className="btn btn-primary" disabled={pub.isPending} onClick={() => pub.mutate()}>
                  {d.immediate ? t("configurazione.versioni.publishNow") : t("configurazione.versioni.publishWithDate")}
                </button>
              </div>
              <ErrorNote error={pub.error} />
            </div>
          )}
        </div>
      )}

      {done && (
        <p role="status" className="m-0 max-w-3xl text-ok">
          {done.scheduled
            ? t("configurazione.versioni.doneScheduled", { date: date(done.from) })
            : t("configurazione.versioni.doneNow")}{" "}
          <button className="text-accent underline" onClick={() => setDone(null)}>{t("configurazione.versioni.again")}</button>
        </p>
      )}
    </section>
  );
}

/** Stato di una versione: in vigore, programmata dal …, deprecata dal …, conclusa. */
export function VersionLifecycle({ lifecycle, from, to, deprecatedAt }: { lifecycle: string; from?: string | null; to?: string | null; deprecatedAt?: string | null }) {
  const t = useT();
  const cls = lifecycle === "in_force" ? "font-semibold" : lifecycle === "draft" || lifecycle === "scheduled" ? "text-accent" : "text-muted";
  let text: string;
  switch (lifecycle) {
    case "draft":
      text = t("configurazione.versioni.lifecycle.draft");
      break;
    case "scheduled":
      text = t("configurazione.versioni.lifecycle.scheduled", { date: date(from) });
      break;
    case "in_force":
      text = t("configurazione.versioni.lifecycle.in_force");
      break;
    case "deprecated":
      text = t("configurazione.versioni.lifecycle.deprecated", { date: date(deprecatedAt) });
      break;
    case "concluded":
      text = t("configurazione.versioni.lifecycle.concluded");
      break;
    default:
      text = t("configurazione.versioni.lifecycle.withdrawn");
  }
  return (
    <span className={cls}>
      {text}
      {to && lifecycle !== "concluded" ? <span className="block text-[12px] font-normal text-muted">{t("configurazione.versioni.until", { date: date(to) })}</span> : null}
    </span>
  );
}

/** Date di una versione già pubblicata: la fine (sempre) e l'entrata in vigore (finché è programmata). */
export function VigoreDates({
  lifecycle,
  from,
  to,
  onSave,
}: {
  lifecycle: string;
  from?: string | null;
  to?: string | null;
  onSave: (v: { effective_from?: string; effective_to?: string }) => Promise<unknown>;
}) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const [f, setF] = useState((from ?? "").slice(0, 10));
  const [e, setE] = useState((to ?? "").slice(0, 10));
  const save = useMutation({
    mutationFn: () => onSave({ ...(lifecycle === "scheduled" && f ? { effective_from: f } : {}), effective_to: e }),
    onSuccess: () => setOpen(false),
  });
  if (lifecycle === "draft" || lifecycle === "withdrawn") return null;
  if (!open)
    return (
      <button className="text-[12.5px] text-accent underline" onClick={() => setOpen(true)}>
        {t("configurazione.versioni.editDates")}
      </button>
    );
  return (
    <div className="flex flex-col gap-1.5">
      {lifecycle === "scheduled" && (
        <label className="flex flex-wrap items-center gap-2 text-[12.5px]">
          {t("configurazione.versioni.effectiveFrom")}
          <input type="date" className="field max-w-[170px] py-1 tabular" value={f} min={today()} onChange={(ev) => setF(ev.target.value)} />
        </label>
      )}
      <label className="flex flex-wrap items-center gap-2 text-[12.5px]">
        {t("configurazione.versioni.effectiveTo")}
        <input type="date" className="field max-w-[170px] py-1 tabular" value={e} onChange={(ev) => setE(ev.target.value)} />
      </label>
      <div className="flex gap-2">
        <button className="btn" disabled={save.isPending} onClick={() => save.mutate()}>{t("configurazione.versioni.saveDates")}</button>
        <button className="btn" onClick={() => setOpen(false)}>{t("configurazione.versioni.cancel")}</button>
      </div>
      <ErrorNote error={save.error} />
    </div>
  );
}
