// Progetti (replica/progetti.md, §3 «Piano» e «Modo di consegna»): scheda «Dettagli» di una voce di
// piano (work package, attività, milestone). Progetto e fase, modo di consegna, team, applicazione,
// fornitore, stima e peso, avanzamento (dichiarato con storico, oppure dalle epic o dalle attività),
// data della milestone e dipendenze con tipo e ritardo. Modifica il piano chi ha `can_edit`; avanzamento
// e date del proprio perimetro li aggiorna chi ha `can_update`. Il server calcola i numeri: qui si mostrano.
import { useEffect, useState, type ReactNode } from "react";
import { Link } from "@tanstack/react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Select, EntitySelect } from "../../select";
import { ErrorNote, Loading, SectionTitle, date } from "../../ui";
import { ProgressBar } from "../work-ui";
import { useT } from "../../i18n/react";
import { projectKeys, projectsApi, type Dep, type DepKind, type DeliveryMode, type ItemPatch, type PlanItemView, type ProgressEntry } from "../../projectsApi";

const MODES: DeliveryMode[] = ["agile", "waterfall", "supplier"];
const DEP_KINDS: DepKind[] = ["fs", "ss", "ff"];

export function PlanItemPanel({ refKey }: { refKey: string }) {
  const t = useT();
  const qc = useQueryClient();
  const q = useQuery({ queryKey: projectKeys.item(refKey), queryFn: () => projectsApi.item(refKey) });
  const item = q.data;
  const plan = useQuery({ queryKey: projectKeys.plan(item?.project.key ?? ""), queryFn: () => projectsApi.plan(item!.project.key), enabled: !!item });
  const [err, setErr] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);

  if (q.error) return <ErrorNote error={q.error} />;
  if (!item) return <Loading className="text-muted" />;

  const refresh = async () => {
    await Promise.all([
      qc.invalidateQueries({ queryKey: projectKeys.item(refKey) }),
      qc.invalidateQueries({ queryKey: projectKeys.item(item.id) }),
      qc.invalidateQueries({ queryKey: projectKeys.plan(item.project.key) }),
      qc.invalidateQueries({ queryKey: projectKeys.overview(item.project.key) }),
      qc.invalidateQueries({ queryKey: ["plan-item-progress", item.id] }),
      qc.invalidateQueries({ queryKey: ["element", refKey] }),
    ]);
  };
  /** Esegue un'azione e ricarica; l'errore del server resta in vista. */
  const run = async (fn: () => Promise<unknown>) => {
    setBusy(true);
    setErr(null);
    try {
      await fn();
      await refresh();
    } catch (e) {
      setErr(e);
    } finally {
      setBusy(false);
    }
  };
  const patch = (p: ItemPatch) => run(() => projectsApi.updateItem(item.id, p));

  const siblings = plan.data?.items ?? [];
  const phases = plan.data?.phases ?? [];
  const canEdit = item.can_edit;
  const activities = siblings.filter((x) => x.kind === "plan_activity" && x.parent_id === item.id);
  const wp = item.kind === "work_package";

  return (
    <div className="flex max-w-[1000px] flex-col gap-8">
      <section className="flex flex-col" aria-label={t("progetti.voci.project")}>
        <Row label={t("progetti.voci.project")}>
          <Link to="/e/$key" params={{ key: item.project.key }} className="text-accent no-underline hover:underline">
            {item.project.key} · {item.project.title}
          </Link>
        </Row>
        <Row label={t("progetti.voci.phase")}>
          {canEdit ? (
            <Select className="field w-full sm:w-72" aria-label={t("progetti.voci.phase")} disabled={busy} value={item.phase_id ?? ""} onChange={(e) => patch({ phase_id: e.target.value })}>
              <option value="">{t("progetti.voci.noPhase")}</option>
              {phases.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
            </Select>
          ) : (
            <span>{item.phase ?? t("progetti.voci.noPhase")}</span>
          )}
        </Row>
        {wp && (
          <>
            <Row label={t("progetti.voci.deliveryMode")}>
              <div role="radiogroup" aria-label={t("progetti.voci.deliveryMode")} className="flex flex-wrap gap-1.5">
                {MODES.map((m) => (
                  <button key={m} type="button" role="radio" aria-checked={item.delivery_mode === m} disabled={!canEdit || busy}
                    className={`btn touch-44 ${item.delivery_mode === m ? "btn-primary" : ""}`}
                    onClick={() => item.delivery_mode !== m && patch({ delivery_mode: m })}>
                    {t(`progetti.voci.mode.${m}`)}
                  </button>
                ))}
              </div>
            </Row>
            <Row label={t("progetti.voci.team")}>
              <Entity kind="team" label={t("progetti.voci.team")} id={item.team_id} name={item.team} canEdit={canEdit} busy={busy} onPick={(v) => patch({ team_id: v })} />
            </Row>
            <Row label={t("progetti.voci.application")}>
              <Entity kind="application" label={t("progetti.voci.application")} id={item.application_id} name={item.application} canEdit={canEdit} busy={busy} onPick={(v) => patch({ application_id: v })} />
            </Row>
            <Row label={t("progetti.voci.supplier")}>
              <Entity kind="supplier" label={t("progetti.voci.supplier")} id={item.supplier_id} name={item.supplier} canEdit={canEdit} busy={busy} onPick={(v) => patch({ supplier_id: v })} />
            </Row>
          </>
        )}
        {item.kind !== "milestone" && (
          <Row label={t("progetti.voci.estimate")}>
            <span className="flex items-center gap-2">
              <NumField label={t("progetti.voci.estimate")} value={item.estimate_effort_days} disabled={!canEdit || busy} onSave={(n) => n != null && patch({ estimate_effort_days: n })} />
              <span className="text-muted">{t("progetti.voci.estimateUnit")}</span>
            </span>
          </Row>
        )}
        {wp && (
          <Row label={t("progetti.voci.weight")} help={t("progetti.voci.weightHelp")}>
            <NumField label={t("progetti.voci.weight")} value={item.weight} disabled={!canEdit || busy} onSave={(n) => patch({ weight: n ?? 0 })} />
          </Row>
        )}
        {item.kind !== "milestone" && (
          <Row label={t("progetti.voci.progress")}>
            <span className="flex min-w-0 flex-col gap-1">
              <ProgressBar percent={item.progress} label={t("progetti.voci.progress")} />
              <span className="text-[12px] text-muted">{t(`progetti.voci.source.${item.progress_source}`)}</span>
            </span>
          </Row>
        )}
        {item.kind === "milestone" && <MilestoneRows item={item} busy={busy} onDate={(d) => run(() => projectsApi.schedule(item.id, d, d))} onContractual={(v) => patch({ contractual: v })} />}
      </section>

      {wp && item.delivery_mode === "agile" && (
        <section className="flex flex-col gap-3" aria-label={t("progetti.voci.epic.title")}>
          <SectionTitle>{t("progetti.voci.epic.title")}</SectionTitle>
          {item.epics.length === 0 && <p className="m-0 text-muted">{t("progetti.voci.epic.none")}</p>}
          {item.epics.map((e) => (
            <Link key={e.id} to="/e/$key" params={{ key: e.key }} className="text-accent no-underline hover:underline">{e.key} · {e.title}</Link>
          ))}
          {canEdit && item.epics.length === 0 && (item.team_id ? (
            <button className="btn self-start touch-44" disabled={busy} onClick={() => run(() => projectsApi.createEpic(item.id))}>{t("progetti.voci.epic.create")}</button>
          ) : (
            <p className="m-0 text-[12px] text-muted">{t("progetti.voci.epic.needTeam")}</p>
          ))}
        </section>
      )}

      {wp && item.delivery_mode === "waterfall" && (
        <section className="flex flex-col gap-3" aria-label={t("progetti.voci.activities.title")}>
          <SectionTitle>{t("progetti.voci.activities.title")}</SectionTitle>
          {activities.length === 0 && <p className="m-0 text-muted">{t("progetti.voci.activities.none")}</p>}
          <ul className="m-0 flex list-none flex-col p-0">
            {activities.map((a) => (
              <li key={a.id} className="grid grid-cols-1 gap-1 border-b border-hair py-2 sm:grid-cols-[minmax(0,1fr)_220px] sm:items-center sm:gap-4">
                <Link to="/e/$key" params={{ key: a.key }} className="min-w-0 truncate text-accent no-underline hover:underline">{a.key} · {a.title}</Link>
                <ProgressBar percent={a.progress} label={a.title} />
              </li>
            ))}
          </ul>
          {canEdit && <AddActivity busy={busy} onAdd={(title) => run(() => projectsApi.createItem(item.project.key, { kind: "plan_activity", parent_id: item.id, title }))} />}
        </section>
      )}

      {(item.kind === "plan_activity" || (wp && item.epics.length === 0 && activities.length === 0)) && (
        <DeclaredProgress item={item} busy={busy} onRecord={(p, n) => run(() => projectsApi.recordProgress(item.id, p, n))} />
      )}

      <Dependencies item={item} others={siblings.filter((x) => x.id !== item.id)} busy={busy} onSave={(deps) => run(() => projectsApi.setDeps(item.id, deps.map((d) => ({ ref: d.id, kind: d.kind, lag: d.lag }))))} />

      {!canEdit && !item.can_update && <p className="m-0 text-[12px] text-muted">{t("progetti.voci.readOnly")}</p>}
      <ErrorNote error={err} />
    </div>
  );
}

function Row({ label, help, children }: { label: string; help?: string; children: ReactNode }) {
  return (
    <div className="grid grid-cols-1 gap-1 border-b border-hair py-2.5 sm:grid-cols-[180px_minmax(0,1fr)] sm:items-center sm:gap-4">
      <span className="flex flex-col">
        <span className="label">{label}</span>
        {help && <span className="text-[12px] text-muted">{help}</span>}
      </span>
      <div className="min-w-0">{children}</div>
    </div>
  );
}

function Entity({ kind, label, id, name, canEdit, busy, onPick }: {
  kind: "team" | "application" | "supplier"; label: string; id: string | null; name: string | null; canEdit: boolean; busy: boolean; onPick: (v: string) => void;
}) {
  const t = useT();
  if (!canEdit) return <span>{name ?? t("progetti.voci.none")}</span>;
  return (
    <EntitySelect kind={kind} className="field w-full sm:w-72" aria-label={label} noneLabel={t("progetti.voci.none")} disabled={busy}
      hints={id && name ? { [id]: name } : undefined} value={id ?? ""} onChange={onPick} />
  );
}

/** Numero che si salva uscendo dal campo (o con Invio); vuoto = null. */
function NumField({ label, value, disabled, onSave }: { label: string; value: number | null; disabled: boolean; onSave: (n: number | null) => void }) {
  const [text, setText] = useState(value == null ? "" : String(value));
  useEffect(() => setText(value == null ? "" : String(value)), [value]);
  const commit = () => {
    const n = text.trim() === "" ? null : Number(text.replace(",", "."));
    if (n != null && (!Number.isFinite(n) || n < 0)) return setText(value == null ? "" : String(value));
    if (n !== value) onSave(n);
  };
  return (
    <input type="number" inputMode="decimal" min={0} step="any" className="field tabular w-28" aria-label={label} disabled={disabled} value={text}
      onChange={(e) => setText(e.target.value)} onBlur={commit} onKeyDown={(e) => e.key === "Enter" && (e.target as HTMLInputElement).blur()} />
  );
}

function AddActivity({ busy, onAdd }: { busy: boolean; onAdd: (title: string) => void }) {
  const t = useT();
  const [title, setTitle] = useState("");
  return (
    <form className="flex flex-wrap gap-2" onSubmit={(e) => { e.preventDefault(); if (title.trim()) { onAdd(title.trim()); setTitle(""); } }}>
      <input className="field min-w-0 flex-1" maxLength={200} aria-label={t("progetti.voci.activities.newTitle")} placeholder={t("progetti.voci.activities.newTitle")} value={title} onChange={(e) => setTitle(e.target.value)} />
      <button type="submit" className="btn touch-44" disabled={busy || !title.trim()}>{t("progetti.voci.activities.add")}</button>
    </form>
  );
}

function MilestoneRows({ item, busy, onDate, onContractual }: { item: PlanItemView; busy: boolean; onDate: (d: string) => void; onContractual: (v: boolean) => void }) {
  const t = useT();
  const day = item.end ?? item.start ?? "";
  const reached = item.state_category === "done";
  return (
    <>
      <Row label={t("progetti.voci.milestone.date")}>
        <input type="date" className="field w-44" aria-label={t("progetti.voci.milestone.date")} disabled={!(item.can_edit || item.can_update) || busy} value={day}
          onChange={(e) => e.target.value && e.target.value !== day && onDate(e.target.value)} />
      </Row>
      <Row label={t("progetti.voci.milestone.contractual")}>
        <input type="checkbox" className="size-5 accent-[var(--accent)]" aria-label={t("progetti.voci.milestone.contractual")} disabled={!item.can_edit || busy} checked={item.contractual} onChange={(e) => onContractual(e.target.checked)} />
      </Row>
      <Row label={item.state}>
        <span className={reached ? "text-ok" : "text-muted"}>{reached ? t("progetti.voci.milestone.reached", { date: date(day) }) : t("progetti.voci.milestone.notReached")}</span>
      </Row>
    </>
  );
}

function DeclaredProgress({ item, busy, onRecord }: { item: PlanItemView; busy: boolean; onRecord: (percent: number, note: string) => void }) {
  const t = useT();
  const [percent, setPercent] = useState("");
  const [note, setNote] = useState("");
  const hist = useQuery({ queryKey: ["plan-item-progress", item.id], queryFn: () => projectsApi.progress(item.id) });
  const entries: ProgressEntry[] = hist.data?.items ?? [];
  const n = Number(percent);
  const valid = percent.trim() !== "" && Number.isFinite(n) && n >= 0 && n <= 100;
  return (
    <section className="flex flex-col gap-3" aria-label={t("progetti.voci.declared.title")}>
      <SectionTitle>{t("progetti.voci.declared.title")}</SectionTitle>
      {item.can_update && (
        <form className="grid grid-cols-1 gap-2 sm:grid-cols-[110px_minmax(0,1fr)_auto] sm:items-end" onSubmit={(e) => { e.preventDefault(); if (valid) { onRecord(n, note.trim()); setPercent(""); setNote(""); } }}>
          <label className="flex flex-col gap-1">
            <span className="label">{t("progetti.voci.declared.percent")}</span>
            <input type="number" inputMode="numeric" min={0} max={100} step={1} className="field tabular" value={percent} onChange={(e) => setPercent(e.target.value)} />
          </label>
          <label className="flex flex-col gap-1">
            <span className="label">{t("progetti.voci.declared.note")}</span>
            <input className="field" maxLength={500} placeholder={t("progetti.voci.declared.notePlaceholder")} value={note} onChange={(e) => setNote(e.target.value)} />
          </label>
          <button type="submit" className="btn btn-primary touch-44" disabled={busy || !valid}>{t("progetti.voci.declared.record")}</button>
        </form>
      )}
      <h4 className="label m-0 mt-1">{t("progetti.voci.history.title")}</h4>
      {entries.length === 0 ? (
        <p className="m-0 text-muted">{t("progetti.voci.history.empty")}</p>
      ) : (
        <ul className="m-0 flex list-none flex-col p-0" aria-label={t("progetti.voci.history.title")}>
          {entries.map((e) => (
            <li key={e.id} className="grid grid-cols-[auto_auto_minmax(0,1fr)] gap-x-4 gap-y-0.5 border-b border-hair py-2 sm:grid-cols-[100px_60px_minmax(0,1fr)_160px]">
              <span className="tabular text-muted">{date(e.date)}</span>
              <span className="tabular font-semibold">{e.percent}%</span>
              <span className="min-w-0 break-words">{e.note}</span>
              <span className="col-span-3 text-[12px] text-muted sm:col-span-1">{e.member ?? ""}</span>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function Dependencies({ item, others, busy, onSave }: { item: PlanItemView; others: { id: string; key: string; title: string }[]; busy: boolean; onSave: (deps: Dep[]) => void }) {
  const t = useT();
  const sig = JSON.stringify(item.deps);
  const [deps, setDeps] = useState<Dep[]>(item.deps);
  useEffect(() => setDeps(JSON.parse(sig) as Dep[]), [sig]);
  const [pick, setPick] = useState("");
  const [kind, setKind] = useState<DepKind>("fs");
  const [lag, setLag] = useState("0");
  const dirty = JSON.stringify(deps) !== sig;
  const label = (id: string) => others.find((o) => o.id === id);
  const free = others.filter((o) => !deps.some((d) => d.id === o.id));
  const lagN = Math.max(0, Math.min(365, Math.trunc(Number(lag) || 0)));
  const setDep = (id: string, p: Partial<Dep>) => setDeps(deps.map((d) => (d.id === id ? { ...d, ...p } : d)));
  const canEdit = item.can_edit;
  return (
    <section className="flex flex-col gap-3" aria-label={t("progetti.voci.deps.title")}>
      <SectionTitle>{t("progetti.voci.deps.title")}</SectionTitle>
      {deps.length === 0 && <p className="m-0 text-muted">{t("progetti.voci.deps.empty")}</p>}
      <ul className="m-0 flex list-none flex-col p-0">
        {deps.map((d) => {
          const o = label(d.id);
          return (
            <li key={d.id} className="grid grid-cols-1 gap-2 border-b border-hair py-2 sm:grid-cols-[minmax(0,1fr)_170px_90px_auto] sm:items-center sm:gap-3">
              {o ? <Link to="/e/$key" params={{ key: o.key }} className="min-w-0 truncate text-accent no-underline hover:underline">{o.key} · {o.title}</Link> : <span className="text-muted">{d.id}</span>}
              {canEdit ? (
                <>
                  <Select className="field" aria-label={t("progetti.voci.deps.kindLabel")} value={d.kind} onChange={(e) => setDep(d.id, { kind: e.target.value as DepKind })}>
                    {DEP_KINDS.map((k) => <option key={k} value={k}>{t(`progetti.voci.deps.kind.${k}`)}</option>)}
                  </Select>
                  <input type="number" min={0} max={365} className="field tabular" aria-label={t("progetti.voci.deps.lag")} title={t("progetti.voci.deps.lag")} value={d.lag}
                    onChange={(e) => setDep(d.id, { lag: Math.max(0, Math.min(365, Math.trunc(Number(e.target.value) || 0))) })} />
                  <button type="button" className="btn btn-quiet touch-44" aria-label={t("progetti.voci.deps.remove", { key: o?.key ?? d.id })} onClick={() => setDeps(deps.filter((x) => x.id !== d.id))}>×</button>
                </>
              ) : (
                <>
                  <span>{t(`progetti.voci.deps.kind.${d.kind}`)}</span>
                  <span className="tabular">{d.lag > 0 ? `+${d.lag}` : ""}</span>
                </>
              )}
            </li>
          );
        })}
      </ul>
      {canEdit && (
        <>
          <div className="grid grid-cols-1 gap-2 sm:grid-cols-[minmax(0,1fr)_170px_90px_auto] sm:items-end sm:gap-3">
            <label className="flex flex-col gap-1">
              <span className="label">{t("progetti.voci.deps.pick")}</span>
              <Select className="field" value={pick} onChange={(e) => setPick(e.target.value)}>
                <option value="">{t("progetti.voci.deps.pickNone")}</option>
                {free.map((o) => <option key={o.id} value={o.id}>{o.key} · {o.title}</option>)}
              </Select>
            </label>
            <label className="flex flex-col gap-1">
              <span className="label">{t("progetti.voci.deps.kindLabel")}</span>
              <Select className="field" value={kind} onChange={(e) => setKind(e.target.value as DepKind)}>
                {DEP_KINDS.map((k) => <option key={k} value={k}>{t(`progetti.voci.deps.kind.${k}`)}</option>)}
              </Select>
            </label>
            <label className="flex flex-col gap-1">
              <span className="label">{t("progetti.voci.deps.lag")}</span>
              <input type="number" min={0} max={365} className="field tabular" value={lag} onChange={(e) => setLag(e.target.value)} />
            </label>
            <button type="button" className="btn touch-44" disabled={!pick} onClick={() => { setDeps([...deps, { id: pick, kind, lag: lagN }]); setPick(""); setLag("0"); }}>{t("progetti.voci.deps.add")}</button>
          </div>
          <button type="button" className="btn btn-primary touch-44 self-start" disabled={busy || !dirty} onClick={() => onSave(deps)}>{t("progetti.voci.deps.save")}</button>
        </>
      )}
    </section>
  );
}
