// Progetti, passo 6: dipendenze tra progetti nel piano (replica/progetti.md §6 «Programma e portafoglio»).
// Il progetto, o una sua voce, ha bisogno di una milestone di un altro progetto entro una data. Se la
// milestone è prevista oltre, il server la segna in ritardo (late_days), il semaforo diventa giallo e il PM
// riceve un avviso. Qui l'elenco delle dipendenze, quelle di altri progetti sulle milestone di questo e
// il modulo per aggiungerne. Le parti che l'attore non legge arrivano senza titolo.
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { projectKeys, projectsApi, type CrossDep, type ProjectPlan } from "../../projectsApi";
import { Select } from "../../select";
import { SectionTitle, date } from "../../ui";
import { useT } from "../../i18n/react";
import { ItemLink } from "./shared";

type Run = (fn: () => Promise<unknown>) => Promise<void>;

/** «PRJ-3 IDP pronto» oppure «un altro progetto». */
function milestoneText(d: CrossDep, t: ReturnType<typeof useT>): string {
  if (!d.milestone_project) return t("progetti.restricted");
  return d.milestone ? `${d.milestone_project.key} · ${d.milestone.title}` : `${d.milestone_project.key} · ${t("progetti.restricted")}`;
}

/** Segno sulla riga del Gantt: la voce aspetta una milestone di un altro progetto (in rosso se in ritardo). */
export function XDepChip({ deps }: { deps: CrossDep[] }) {
  const t = useT();
  if (deps.length === 0) return null;
  const late = deps.some((d) => d.late_days > 0);
  const title = deps
    .map((d) => `${t("progetti.passo6.dep.waitsOn", { milestone: milestoneText(d, t) })}${d.late_days > 0 ? ` · ${t("progetti.passo6.dep.late", { count: d.late_days })}` : ""}`)
    .join(" · ");
  return (
    <span
      className={`inline-flex shrink-0 items-center gap-1 border px-1 text-[10.5px] font-semibold whitespace-nowrap ${late ? "border-danger text-danger" : "border-rule text-muted"}`}
      title={title}
      aria-label={title}
      data-testid="dipendenza-chip"
    >
      <span aria-hidden="true">⇠</span>
      {deps[0].milestone_project?.key ?? t("progetti.passo6.dep.other")}
      {deps.length > 1 ? ` +${deps.length - 1}` : ""}
    </span>
  );
}

function Status({ d }: { d: CrossDep }) {
  const t = useT();
  if (d.milestone_state_category === "done") return <span className="text-[12.5px] text-ok">{t("progetti.passo6.dep.reached")}</span>;
  if (d.late_days > 0) return <span className="text-[12.5px] font-semibold text-danger">{t("progetti.passo6.dep.late", { count: d.late_days })}</span>;
  if (!d.need_by) return <span className="text-[12.5px] text-muted">{t("progetti.passo6.dep.noNeed")}</span>;
  return <span className="text-[12.5px]">{t("progetti.passo6.dep.onTime")}</span>;
}

export function CrossDepsPanel({ plan, refKey, run, busy }: { plan: ProjectPlan; refKey: string; run: Run; busy: boolean }) {
  const t = useT();
  const ext = plan.external ?? [];
  const deps = plan.dependents ?? [];
  const [adding, setAdding] = useState(false);
  if (!plan.can_edit && ext.length === 0 && deps.length === 0) return null;
  return (
    <section className="flex flex-col gap-3" aria-label={t("progetti.passo6.dep.title")} data-testid="dipendenze-progetti">
      <SectionTitle>{t("progetti.passo6.dep.title")}</SectionTitle>
      <p className="m-0 text-[12.5px] text-muted">{t("progetti.passo6.dep.help")}</p>
      {ext.length === 0 ? (
        <p className="m-0 text-[13px] text-muted">{t("progetti.passo6.dep.none")}</p>
      ) : (
        <ul className="m-0 flex list-none flex-col p-0" aria-label={t("progetti.passo6.dep.title")}>
          {ext.map((d) => (
            <li key={d.id} className="flex flex-col gap-1 border-b border-hair py-2 sm:flex-row sm:items-start sm:gap-4" data-testid="dipendenza">
              <div className="flex min-w-0 flex-1 flex-col gap-0.5">
                <span className="text-[13px]">
                  <span className="font-semibold">{d.item ? `${d.item.key} ${d.item.title}` : t("progetti.passo6.dep.wholeProject")}</span>{" "}
                  {t("progetti.passo6.dep.needs")}{" "}
                  {d.milestone_project && d.milestone ? <ItemLink itemKey={d.milestone.key} title={d.milestone.title} /> : <span>{milestoneText(d, t)}</span>}
                  {d.milestone_project && <span className="text-muted"> · {d.milestone_project.key} {d.milestone_project.title}</span>}
                </span>
                <span className="tabular text-[12.5px] text-muted">
                  {t("progetti.passo6.dep.dates", { need: d.need_by ? date(d.need_by) : "—", date: d.milestone_date ? date(d.milestone_date) : "—" })}
                  {!d.need_by_set && d.need_by && ` · ${t("progetti.passo6.dep.needFromItem")}`}
                </span>
                {d.note && <span className="text-[12.5px]">{d.note}</span>}
              </div>
              <div className="flex shrink-0 items-center gap-3">
                <Status d={d} />
                {plan.can_edit && (
                  <button type="button" className="btn btn-quiet touch-44 text-[12px]" disabled={busy} onClick={() => run(() => projectsApi.deleteCrossDep(refKey, d.id))}>
                    {t("progetti.passo6.dep.remove")}
                  </button>
                )}
              </div>
            </li>
          ))}
        </ul>
      )}
      {plan.can_edit &&
        (adding ? (
          <AddCrossDep plan={plan} refKey={refKey} busy={busy} run={run} onDone={() => setAdding(false)} />
        ) : (
          <button type="button" className="btn touch-44 self-start" disabled={busy} onClick={() => setAdding(true)}>
            {t("progetti.passo6.dep.add")}
          </button>
        ))}
      {deps.length > 0 && (
        <div className="flex flex-col gap-1.5" data-testid="dipendenti">
          <span className="label text-muted">{t("progetti.passo6.dep.dependents")}</span>
          <ul className="m-0 flex list-none flex-col gap-1 p-0 text-[13px]">
            {deps.map((d) => (
              <li key={d.id} className="flex flex-wrap items-baseline gap-x-2">
                <span>{d.milestone ? `${d.milestone.key} ${d.milestone.title}` : t("progetti.restricted")}</span>
                <span className="text-muted">←</span>
                <span>{d.project ? `${d.project.key} ${d.project.title}` : t("progetti.restricted")}</span>
                <span className="tabular text-[12.5px] text-muted">{t("progetti.passo6.dep.needBy", { date: d.need_by ? date(d.need_by) : "—" })}</span>
                {d.late_days > 0 && <span className="text-[12.5px] font-semibold text-danger">{t("progetti.passo6.dep.late", { count: d.late_days })}</span>}
              </li>
            ))}
          </ul>
        </div>
      )}
    </section>
  );
}

function AddCrossDep({ plan, refKey, busy, run, onDone }: { plan: ProjectPlan; refKey: string; busy: boolean; run: Run; onDone: () => void }) {
  const t = useT();
  const cands = useQuery({ queryKey: projectKeys.candidates(refKey), queryFn: () => projectsApi.dependencyCandidates(refKey) });
  const [milestone, setMilestone] = useState("");
  const [item, setItem] = useState("");
  const [need, setNeed] = useState("");
  const [note, setNote] = useState("");
  const items = plan.items.filter((i) => i.state_category !== "cancelled" && !i.restricted);
  const picked = items.find((i) => i.id === item);
  const ok = milestone !== "" && (need !== "" || item !== "");
  const list = cands.data?.items ?? [];
  return (
    <form
      aria-label={t("progetti.passo6.dep.add")}
      className="grid grid-cols-1 gap-2 border border-hair p-3 sm:grid-cols-2"
      onSubmit={(e) => {
        e.preventDefault();
        if (!ok) return;
        void run(() =>
          projectsApi.createCrossDep(refKey, { milestone_id: milestone, ...(item ? { item_id: item } : {}), ...(need ? { need_by: need } : {}), note: note.trim() }),
        ).then(onDone);
      }}
    >
      <label className="flex flex-col gap-1 sm:col-span-2">
        <span className="label">{t("progetti.passo6.dep.milestone")}</span>
        {cands.data && list.length === 0 ? (
          <span className="text-[12.5px] text-muted">{t("progetti.passo6.dep.noCandidates")}</span>
        ) : (
          <Select className="field w-full" aria-label={t("progetti.passo6.dep.milestone")} value={milestone} onChange={(e) => setMilestone(e.target.value)}>
            <option value="">{t("progetti.passo6.dep.choose")}</option>
            {list.map((c) => (
              <option key={c.milestone.id} value={c.milestone.id}>
                {`${c.project.key} ${c.project.title} · ${c.milestone.title}${c.date ? ` · ${date(c.date)}` : ""}${c.same_program ? ` · ${t("progetti.passo6.dep.sameProgram")}` : ""}`}
              </option>
            ))}
          </Select>
        )}
      </label>
      <label className="flex flex-col gap-1">
        <span className="label">{t("progetti.passo6.dep.item")}</span>
        <Select className="field w-full" aria-label={t("progetti.passo6.dep.item")} value={item} onChange={(e) => setItem(e.target.value)}>
          <option value="">{t("progetti.passo6.dep.wholeProject")}</option>
          {items.map((i) => (
            <option key={i.id} value={i.id}>{`${i.key} ${i.title}`}</option>
          ))}
        </Select>
      </label>
      <label className="flex flex-col gap-1">
        <span className="label">{t("progetti.passo6.dep.needLabel")}</span>
        <input type="date" className="field" value={need} onChange={(e) => setNeed(e.target.value)} />
        <span className="text-[12px] text-muted">
          {picked?.start ? t("progetti.passo6.dep.needHintItem", { date: date(picked.start) }) : t("progetti.passo6.dep.needHint")}
        </span>
      </label>
      <label className="flex flex-col gap-1 sm:col-span-2">
        <span className="label">{t("progetti.passo6.dep.note")}</span>
        <input className="field" maxLength={500} value={note} onChange={(e) => setNote(e.target.value)} />
      </label>
      <div className="flex gap-2 sm:col-span-2">
        <button type="submit" className="btn btn-primary touch-44" disabled={busy || !ok}>
          {t("progetti.passo6.dep.save")}
        </button>
        <button type="button" className="btn touch-44" onClick={onDone}>
          {t("progetti.passo6.dep.cancel")}
        </button>
      </div>
    </form>
  );
}
