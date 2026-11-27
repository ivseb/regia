// «Piano» del progetto: Gantt con fasi, work package, attività e milestone (replica/progetti.md §6).
// Si parte dal Gantt della richiesta (pages/Plan.tsx): trascinando una barra la si sposta, dai bordi
// la si allunga; chi dipende da lei slitta a cascata già durante il trascinamento, con le regole del
// server (plan-logic.ts). Sul telefono c'è l'elenco per fase: il Gantt si guarda e non si modifica.
// Con una baseline, sotto ogni barra c'è in trasparenza la sua posizione nella baseline in vigore;
// sotto il Gantt, baseline e change request (ProjectBaseline.tsx).
import { useEffect, useMemo, useRef, useState, type PointerEvent as RPointerEvent } from "react";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../../api";
import * as cal from "../../calendar";
import { projectKeys, projectsApi, type DeliveryMode, type Phase, type PlanItem, type ProjectPlan } from "../../projectsApi";
import { PageHeader } from "../../shell";
import { EntitySelect } from "../../select";
import { ErrorNote, Loading } from "../../ui";
import { useT } from "../../i18n/react";
import { Markers, NonWorkdays, PX_PER_DAY, TimeHeader, ZoomSwitch, useNarrow, type Zoom } from "../timeline";
import { BaselinePanel } from "./ProjectBaseline";
import { CrossDepsPanel, XDepChip } from "./CrossDeps";
import { ConflictChip, PendingChip, useConflictText } from "./shared";
import { H, cascadeFrom, criticalPath, envelope, itemsByPhase, proposedDuration, rowHeight, violated, type Row, type Span } from "./plan-logic";

const LABEL = 320;
const MODES: DeliveryMode[] = ["agile", "waterfall", "supplier"];

type Drag = { id: string; mode: "move" | "start" | "end"; x0: number; orig: Span; moved: boolean };
type Adding = { kind: "wp"; phaseId: string | null } | { kind: "ms"; phaseId: string | null } | { kind: "act"; wp: PlanItem };

export function ProjectPlanTab({ refKey }: { refKey: string }) {
  const plan = useQuery({ queryKey: projectKeys.plan(refKey), queryFn: () => projectsApi.plan(refKey) });
  if (plan.isPending) return <Loading className="text-muted" />;
  if (plan.error) return <ErrorNote error={plan.error} />;
  return <PlanView plan={plan.data} refKey={refKey} />;
}

export function ProjectPlanPage({ projectKey }: { projectKey: string }) {
  const t = useT();
  const plan = useQuery({ queryKey: projectKeys.plan(projectKey), queryFn: () => projectsApi.plan(projectKey) });
  if (plan.isPending) return <Loading className="p-8 text-muted" />;
  if (plan.error)
    return (
      <div className="p-8">
        <ErrorNote error={plan.error} />
      </div>
    );
  const p = plan.data;
  return (
    <>
      <PageHeader
        path={t("progetti.piano.path", { key: p.project.key })}
        title={p.project.title}
        actions={
          <Link to="/e/$key" params={{ key: p.project.key }} className="btn">
            {t("progetti.piano.openProject")}
          </Link>
        }
      />
      <div className="flex flex-col gap-4 px-4 pb-10 md:px-7">
        <PlanView plan={p} refKey={projectKey} />
      </div>
    </>
  );
}

/** Rotta /progetti/$key/piano: il piano a tutta pagina. */
export function ProjectPlanRoute() {
  const { key } = useParams({ strict: false }) as { key: string };
  return <ProjectPlanPage key={key} projectKey={key} />;
}

function PlanView({ plan, refKey }: { plan: ProjectPlan; refKey: string }) {
  const t = useT();
  const qc = useQueryClient();
  const narrow = useNarrow();
  const [zoom, setZoom] = useState<Zoom>("settimane");
  const [err, setErr] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  const [showBaseline, setShowBaseline] = useState(true);

  const invalidate = async () => {
    await qc.invalidateQueries({ queryKey: projectKeys.plan(refKey) });
    qc.invalidateQueries({ queryKey: projectKeys.overview(refKey) });
    // Uno spostamento oltre le tolleranze apre una change request.
    qc.invalidateQueries({ queryKey: projectKeys.changeRequests(refKey) });
  };
  /** Esegue un'azione sul server, poi ricarica il piano; l'errore si mostra sotto il Gantt. */
  const run = async (fn: () => Promise<unknown>) => {
    setBusy(true);
    setErr(null);
    try {
      await fn();
    } catch (e) {
      setErr(e);
    } finally {
      await invalidate();
      setBusy(false);
    }
  };

  const undated = plan.items.filter((i) => !i.start && i.state_category !== "cancelled").length;
  const canEdit = plan.can_edit && !narrow;
  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2">
        {!narrow && <ZoomSwitch value={zoom} options={["giorni", "settimane", "mesi"]} onChange={setZoom} />}
        {!narrow && plan.baseline && (
          <label className="flex items-center gap-1.5 text-[13px]">
            <input type="checkbox" className="size-5 accent-[var(--accent)]" checked={showBaseline} onChange={(e) => setShowBaseline(e.target.checked)} />
            {t("progetti.baseline.show", { number: plan.baseline.number })}
          </label>
        )}
        {plan.can_edit && undated > 0 && !narrow && (
          <button className="btn btn-primary ml-auto" disabled={busy} onClick={() => run(() => projectsApi.autoPlan(refKey))}>
            {t("progetti.piano.autoPlan", { count: undated })}
          </button>
        )}
      </div>
      <ErrorNote error={err} />
      {narrow ? (
        <PhoneList plan={plan} />
      ) : (
        <Gantt plan={plan} refKey={refKey} zoom={zoom} canEdit={canEdit} busy={busy} run={run} showBaseline={showBaseline && plan.baseline != null} />
      )}
      <CrossDepsPanel plan={plan} refKey={refKey} run={run} busy={busy} />
      <BaselinePanel plan={plan} refKey={refKey} />
    </div>
  );
}

// ---------------------------------------------------------------------------------------------
// Telefono: elenco per fase.

function dateRange(it: { start: string | null; end: string | null }, t: ReturnType<typeof useT>): string {
  if (!it.start) return t("progetti.piano.noDates");
  const s = cal.parse(it.start);
  const e = cal.parse(it.end ?? it.start);
  return s === e ? cal.shortYear(s) : t("progetti.piano.range", { from: cal.short(s), to: cal.shortYear(e) });
}

function PhoneList({ plan }: { plan: ProjectPlan }) {
  const t = useT();
  const conflictText = useConflictText();
  const rows = itemsByPhase(plan.phases, plan.items, new Map(), new Map(), false);
  const groups: { phase: Phase | null; items: { it: PlanItem; depth: number }[] }[] = [];
  for (const r of rows) {
    if (r.k === "phase") groups.push({ phase: r.phase, items: [] });
    else if (r.k === "item") groups[groups.length - 1].items.push({ it: r.it, depth: r.depth });
  }
  if (plan.items.length === 0) return <p className="m-0 text-muted">{t("progetti.piano.empty")}</p>;
  return (
    <div className="flex flex-col gap-5">
      {groups.map((g) => (
        <section key={g.phase?.id ?? "none"} aria-label={g.phase?.name ?? t("progetti.piano.noPhase")}>
          <div className="flex items-baseline gap-2 border-b-2 border-rule pb-1.5">
            <h3 className="label m-0">{g.phase?.name ?? t("progetti.piano.noPhase")}</h3>
            {g.phase?.gate_enabled && <span className="font-mono text-[11px] text-muted">{t("progetti.piano.gate", { status: t(`progetti.piano.gateStatus.${g.phase.gate_status}`) })}</span>}
            {g.phase && <span className="tabular ml-auto font-mono text-[12px] text-muted">{Math.round(g.phase.progress)}%</span>}
          </div>
          {g.items.length === 0 && <p className="m-0 py-2 text-[13px] text-muted">{t("progetti.piano.phaseEmpty")}</p>}
          <ul className="m-0 list-none p-0">
            {g.items.map(({ it, depth }) => (
              <li key={it.id} className="border-b border-hair/60" style={{ paddingLeft: depth * 16 }}>
                <Link to="/e/$key" params={{ key: it.key }} className="touch-44 flex flex-col gap-1 py-2 text-fg no-underline">
                  <span className={`flex items-baseline gap-2 text-[14px] font-medium ${it.state_category === "cancelled" ? "text-muted line-through" : ""}`}>
                    {it.kind === "milestone" && <span aria-hidden="true" className="inline-block h-2.5 w-2.5 shrink-0 rotate-45 bg-fg" />}
                    <span className="min-w-0 flex-1 truncate">{it.title}</span>
                    <span className="tabular font-mono text-[12px] text-muted">{Math.round(it.progress)}%</span>
                  </span>
                  {it.kind !== "milestone" && (
                    <span className="block h-1.5 bg-hair" role="progressbar" aria-valuenow={Math.round(it.progress)} aria-valuemin={0} aria-valuemax={100} aria-label={it.title}>
                      <span className="block h-full bg-accent" style={{ width: `${Math.min(100, Math.max(0, it.progress))}%` }} />
                    </span>
                  )}
                  {(it.conflicts?.length || it.claims_pending > 0) && (
                    <span className="flex flex-wrap items-center gap-1.5">
                      <ConflictChip conflicts={it.conflicts ?? []} />
                      <PendingChip count={it.claims_pending} />
                    </span>
                  )}
                  {it.conflicts?.map((c) => <span key={c.item_id + c.application_id} className="text-[12px] text-flag">{conflictText(c)}</span>)}
                  <span className="flex flex-wrap gap-x-3 font-mono text-[11px] text-muted">
                    <span>{it.key}</span>
                    {it.delivery_mode && <span>{t(`progetti.piano.mode.${it.delivery_mode}`)}</span>}
                    <span>{dateRange(it, t)}</span>
                    {plan.baseline && it.in_baseline && (
                      <span>
                        {t("progetti.baseline.phone", { dates: dateRange({ start: it.baseline_start, end: it.baseline_end }, t) })}
                        {it.variance_days ? ` ${t("progetti.baseline.shift", { sign: it.variance_days > 0 ? "+" : "−", count: Math.abs(it.variance_days) })}` : ""}
                      </span>
                    )}
                  </span>
                </Link>
              </li>
            ))}
          </ul>
        </section>
      ))}
    </div>
  );
}

// ---------------------------------------------------------------------------------------------
// Gantt.

function Gantt({
  plan,
  refKey,
  zoom,
  canEdit,
  busy,
  run,
  showBaseline,
}: {
  plan: ProjectPlan;
  refKey: string;
  zoom: Zoom;
  canEdit: boolean;
  busy: boolean;
  run: (fn: () => Promise<unknown>) => Promise<void>;
  showBaseline: boolean;
}) {
  const t = useT();
  const nav = useNavigate();
  const px = PX_PER_DAY[zoom];
  const [drag, setDrag] = useState<Drag | null>(null);
  const [delta, setDelta] = useState(0);
  const [pending, setPending] = useState<Map<string, Span> | null>(null);
  const [adding, setAdding] = useState<Adding | null>(null);
  const [involving, setInvolving] = useState<string | null>(null);
  const dragRef = useRef<Drag | null>(null);
  const scroller = useRef<HTMLDivElement>(null);
  const [vw, setVw] = useState(640);

  useEffect(() => {
    const el = scroller.current;
    if (!el) return;
    const on = () => setVw(el.clientWidth);
    on();
    const ro = new ResizeObserver(on);
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const items = plan.items;
  const base = useMemo(() => {
    const m = new Map<string, Span>();
    for (const i of items) if (i.start) m.set(i.id, { s: cal.parse(i.start), e: cal.parse(i.end ?? i.start) });
    return m;
  }, [items]);

  // Voci senza date: barra tratteggiata da oggi, durata dalla stima.
  const ghost = useMemo(() => {
    const m = new Map<string, Span>();
    const s = cal.nextWorkday(cal.today());
    for (const i of items) if (!i.start && i.state_category !== "cancelled") m.set(i.id, { s, e: cal.endAfter(s, proposedDuration(i)) });
    return m;
  }, [items]);

  const byId = useMemo(() => new Map(items.map((i) => [i.id, i])), [items]);

  // Date mostrate: quelle salvate, più l'anteprima del trascinamento con la cascata.
  const spans = useMemo(() => {
    if (pending) return pending;
    if (!drag) return base;
    const all = new Map(base);
    const o = drag.orig;
    const ms = byId.get(drag.id)?.kind === "milestone";
    let next: Span;
    if (drag.mode === "move") {
      const s = cal.nextWorkday(cal.addDays(o.s, delta));
      next = ms ? { s, e: s } : { s, e: cal.endAfter(s, cal.workdays(o.s, o.e)) };
    } else if (drag.mode === "end") {
      next = { s: o.s, e: Math.max(o.s, cal.addDays(o.e, delta)) };
    } else {
      next = { s: Math.min(o.e, cal.nextWorkday(cal.addDays(o.s, delta))), e: o.e };
    }
    all.set(drag.id, next);
    return cascadeFrom(items, all, drag.id);
  }, [base, drag, delta, pending, items, byId]);

  const crit = useMemo(() => criticalPath(items, spans), [items, spans]);

  // Righe e posizioni verticali.
  const rows = useMemo(() => itemsByPhase(plan.phases, items, spans, ghost, canEdit), [plan.phases, items, spans, ghost, canEdit]);
  const { tops, yOf, height, phaseItems } = useMemo(() => {
    const tops: number[] = [];
    const yOf = new Map<string, number>();
    const phaseItems = new Map<string, PlanItem[]>();
    let y = 0;
    let cur = "none";
    for (const r of rows) {
      tops.push(y);
      const h = rowHeight(r);
      if (r.k === "phase") cur = r.id;
      if (r.k === "item") {
        yOf.set(r.it.id, y + h / 2);
        phaseItems.set(cur, [...(phaseItems.get(cur) ?? []), r.it]);
      }
      y += h;
    }
    return { tops, yOf, height: y, phaseItems };
  }, [rows]);

  // Intervallo visibile: dal lunedì prima dell'inizio (o di oggi) a qualche settimana dopo fine e scadenza.
  const { from, days } = useMemo(() => {
    const pts = [cal.today()];
    for (const s of base.values()) pts.push(s.s, s.e);
    for (const s of ghost.values()) pts.push(s.e);
    if (showBaseline) for (const i of items) for (const d of [i.baseline_start, i.baseline_end]) if (d) pts.push(cal.parse(d));
    if (plan.project.target) pts.push(cal.parse(plan.project.target));
    const f = cal.monday(cal.addDays(Math.min(...pts), -7));
    const to = cal.addDays(Math.max(...pts), zoom === "mesi" ? 60 : 28);
    return { from: f, days: cal.diffDays(f, to) };
  }, [base, ghost, plan.project.target, zoom, showBaseline, items]);
  const width = days * px;
  const xOf = (d: number) => cal.diffDays(from, d) * px;

  // All'apertura (e cambiando scala) si scorre fino alla prima barra o a oggi.
  const firstStart = base.size > 0 ? Math.min(...[...base.values()].map((s) => s.s)) : cal.today();
  useEffect(() => {
    const el = scroller.current;
    if (el) el.scrollLeft = Math.max(0, cal.diffDays(from, firstStart) * px - 3 * px - 8);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [zoom, plan.project.id]);

  const markers = plan.project.target
    ? [
        {
          at: plan.project.target,
          label: plan.project.target_kind === "regulatory" ? t("progetti.piano.markerRegulatory") : t("progetti.piano.markerDesired"),
          tone: plan.project.target_kind === "regulatory" ? ("flag" as const) : ("muted" as const),
        },
      ]
    : [];

  function onDown(e: RPointerEvent<HTMLDivElement>, it: PlanItem, span: Span, isGhost: boolean) {
    if (!canEdit || busy) return;
    const r = e.currentTarget.getBoundingClientRect();
    const off = e.clientX - r.left;
    const edge = Math.min(10, r.width / 3);
    const mode: Drag["mode"] = isGhost || it.kind === "milestone" ? "move" : off < edge ? "start" : off > r.width - edge ? "end" : "move";
    e.currentTarget.setPointerCapture(e.pointerId);
    const d = { id: it.id, mode, x0: e.clientX, orig: span, moved: false };
    dragRef.current = d;
    setDrag(d);
    setDelta(0);
  }
  function onMove(e: RPointerEvent<HTMLDivElement>) {
    const d = dragRef.current;
    if (!d) return;
    const dx = e.clientX - d.x0;
    if (Math.abs(dx) > 3) d.moved = true;
    setDelta(Math.round(dx / px));
  }
  function onUp(it: PlanItem) {
    const d = dragRef.current;
    dragRef.current = null;
    if (!d) return;
    if (!d.moved) {
      setDrag(null);
      nav({ to: "/e/$key", params: { key: it.key } });
      return;
    }
    const final = spans.get(d.id) ?? d.orig;
    setDrag(null);
    if (it.start && final.s === d.orig.s && final.e === d.orig.e) return;
    setPending(spans);
    run(() => projectsApi.schedule(d.id, cal.fmt(final.s), cal.fmt(final.e))).finally(() => setPending(null));
  }

  function involve(it: PlanItem) {
    if (it.team_id || it.assignee_id || it.supplier_id) run(() => api.transition(it.id, "Proposto")); // i18n-ok: nome di stato del processo
    else setInvolving(it.id);
  }

  if (items.length === 0 && plan.phases.length === 0 && !canEdit) return <p className="m-0 text-muted">{t("progetti.piano.empty")}</p>;

  return (
    <>
      <p className="m-0 text-[12.5px] text-muted">
        {canEdit ? t("progetti.piano.helpEdit") : t("progetti.piano.helpReadOnly")} {t("progetti.piano.helpCritical")}
        {showBaseline && plan.baseline && ` ${t("progetti.baseline.help", { number: plan.baseline.number })}`}
      </p>
      <div ref={scroller} className="overflow-x-auto border border-hair" style={{ touchAction: "pan-x pan-y" }}>
        <div style={{ width: LABEL + width }}>
          <TimeHeader from={from} days={days} px={px} zoom={zoom} label={LABEL} />
          <div className="relative flex">
            <div className="sticky left-0 z-20 shrink-0 border-r border-hair bg-bg" style={{ width: LABEL }}>
              {rows.map((r, i) => (
                <LabelCell
                  key={rowKey(r, i)}
                  row={r}
                  plan={plan}
                  canEdit={canEdit}
                  busy={busy}
                  vw={vw}
                  adding={adding}
                  setAdding={setAdding}
                  involving={involving}
                  setInvolving={setInvolving}
                  involve={involve}
                  refKey={refKey}
                  run={run}
                />
              ))}
            </div>
            <div className="relative" style={{ width, height }}>
              <NonWorkdays from={from} days={days} px={px} height={height} />
              {rows.map((r, i) => (
                <div key={rowKey(r, i)} className={`absolute left-0 border-b border-hair/60 ${r.k === "phase" ? "bg-hair/25" : ""}`} style={{ top: tops[i], height: rowHeight(r), width }} />
              ))}
              <Markers from={from} px={px} height={height} markers={markers} />
              {showBaseline &&
                rows.map((r) => (r.k === "item" && r.it.in_baseline ? <BaselineMark key={"b" + r.it.id} it={r.it} y={yOf.get(r.it.id)!} depth={r.depth} xOf={xOf} px={px} /> : null))}
              <Arrows items={items} spans={spans} yOf={yOf} xOf={xOf} px={px} width={width} height={height} crit={crit} />
              {rows.map((r, i) => {
                if (r.k === "phase") return <PhaseBar key={rowKey(r, i)} phase={r.phase} y={tops[i] + H.phase / 2} span={envelope(phaseItems.get(r.id) ?? [], spans)} xOf={xOf} px={px} />;
                if (r.k !== "item") return null;
                const it = r.it;
                const real = spans.get(it.id);
                const span = real ?? ghost.get(it.id);
                if (!span) return null;
                return (
                  <Bar
                    key={it.id}
                    it={it}
                    span={span}
                    isGhost={!real}
                    y={yOf.get(it.id)!}
                    x={xOf(span.s)}
                    px={px}
                    depth={r.depth}
                    crit={crit.has(it.id)}
                    dragging={drag?.id === it.id}
                    canEdit={canEdit}
                    onDown={onDown}
                    onMove={onMove}
                    onUp={onUp}
                    onCancel={() => {
                      dragRef.current = null;
                      setDrag(null);
                    }}
                  />
                );
              })}
            </div>
          </div>
        </div>
      </div>
    </>
  );
}

function rowKey(r: Row, i: number): string {
  if (r.k === "phase") return "p" + r.id;
  if (r.k === "item") return r.it.id;
  if (r.k === "add") return "a" + r.id;
  return "aa" + r.wp.id + i;
}

// ---------------------------------------------------------------------------------------------
// Colonna delle etichette.

type LabelProps = {
  row: Row;
  plan: ProjectPlan;
  canEdit: boolean;
  busy: boolean;
  vw: number;
  adding: Adding | null;
  setAdding: (a: Adding | null) => void;
  involving: string | null;
  setInvolving: (id: string | null) => void;
  involve: (it: PlanItem) => void;
  refKey: string;
  run: (fn: () => Promise<unknown>) => Promise<void>;
};

function LabelCell(p: LabelProps) {
  const t = useT();
  const { row: r } = p;
  if (r.k === "phase") {
    const ph = r.phase;
    return (
      <div className="flex items-end gap-2 border-b border-hair bg-hair/25 px-3 pb-1" style={{ height: H.phase }}>
        <span className="label truncate">{ph?.name ?? t("progetti.piano.noPhase")}</span>
        {ph?.gate_enabled && (
          <span className="shrink-0 border border-rule px-1 font-mono text-[10.5px] text-muted" title={t("progetti.piano.gate", { status: t(`progetti.piano.gateStatus.${ph.gate_status}`) })}>
            {t("progetti.piano.gate", { status: t(`progetti.piano.gateStatus.${ph.gate_status}`) })}
          </span>
        )}
        {ph && <span className="tabular ml-auto font-mono text-[11px] text-muted">{Math.round(ph.progress)}%</span>}
      </div>
    );
  }
  if (r.k === "add") {
    const open = p.adding && p.adding.kind !== "act" && p.adding.phaseId === r.phaseId ? p.adding : null;
    return (
      <div className="relative flex items-center gap-1 border-b border-hair/60 px-2" style={{ height: H.add }}>
        <button className="btn btn-quiet px-1.5 text-[12px]" disabled={p.busy} onClick={() => p.setAdding({ kind: "wp", phaseId: r.phaseId })}>
          + {t("progetti.piano.addWp")}
        </button>
        <button className="btn btn-quiet px-1.5 text-[12px]" disabled={p.busy} onClick={() => p.setAdding({ kind: "ms", phaseId: r.phaseId })}>
          + {t("progetti.piano.addMilestone")}
        </button>
        {open && <AddForm {...p} adding={open} />}
      </div>
    );
  }
  if (r.k === "addact") {
    const open = p.adding?.kind === "act" && p.adding.wp.id === r.wp.id ? p.adding : null;
    return (
      <div className="relative flex items-center border-b border-hair/60 pl-8 pr-2" style={{ height: H.addact }}>
        <button className="btn btn-quiet px-1.5 text-[12px]" disabled={p.busy} onClick={() => p.setAdding({ kind: "act", wp: r.wp })}>
          + {t("progetti.piano.addActivity")}
        </button>
        {open && <AddForm {...p} adding={open} />}
      </div>
    );
  }
  const it = r.it;
  const cancelled = it.state_category === "cancelled";
  const title = (
    <Link to="/e/$key" params={{ key: it.key }} className={`flex min-w-0 items-baseline gap-1.5 text-fg no-underline hover:underline ${cancelled ? "text-muted line-through" : ""}`}>
      <span className="shrink-0 font-mono text-[10.5px] text-muted">{it.key}</span>
      <span className={`truncate text-[12.5px] ${it.kind === "work_package" ? "font-semibold" : "font-medium"}`}>{it.title}</span>
    </Link>
  );
  if (it.kind !== "work_package") {
    return (
      <div className="flex items-center border-b border-hair/60 pr-3" style={{ height: rowHeight(r), paddingLeft: r.depth === 1 ? 32 : 12 }}>
        {it.kind === "milestone" && <span aria-hidden="true" className="mr-2 inline-block h-2 w-2 shrink-0 rotate-45 bg-fg" />}
        {title}
        <XDepChip deps={(p.plan.external ?? []).filter((d) => d.item_id === it.id)} />
      </div>
    );
  }
  const mode = it.delivery_mode;
  const planned = it.state === "Da pianificare"; // i18n-ok: nome di stato del processo
  return (
    <div className="relative flex flex-col justify-center gap-1 border-b border-hair/60 px-3" style={{ height: H.wp }}>
      {title}
      <div className="flex items-center gap-2">
        {p.canEdit ? (
          <div role="radiogroup" aria-label={t("progetti.piano.modeLabel")} className="flex border border-rule text-[11px]">
            {MODES.map((m) => (
              <button
                key={m}
                role="radio"
                aria-checked={mode === m}
                disabled={p.busy}
                className={`px-2 py-0.5 ${mode === m ? "bg-fg text-bg" : "text-muted hover:text-fg"}`}
                onClick={() => mode !== m && p.run(() => projectsApi.updateItem(it.id, { delivery_mode: m }))}
              >
                {t(`progetti.piano.mode.${m}`)}
              </button>
            ))}
          </div>
        ) : (
          mode && <span className="border border-rule px-1.5 py-0.5 text-[11px] text-muted">{t(`progetti.piano.mode.${mode}`)}</span>
        )}
        {p.canEdit && planned && (
          <button className="btn btn-quiet px-1.5 text-[11.5px]" disabled={p.busy} onClick={() => p.involve(it)}>
            {t("progetti.piano.involve")}
          </button>
        )}
        <ConflictChip conflicts={it.conflicts ?? []} />
        <XDepChip deps={(p.plan.external ?? []).filter((d) => d.item_id === it.id)} />
        <PendingChip count={it.claims_pending} />
        {mode === "agile" && it.epics.length > 0 && (
          <Link to="/e/$key" params={{ key: it.epics[0].key }} className="truncate font-mono text-[11px] text-accent">
            {t("progetti.piano.epic", { key: it.epics[0].key })}
          </Link>
        )}
      </div>
      {p.involving === it.id && <InvolveForm {...p} it={it} />}
    </div>
  );
}

/** Contenitore di un modulo in linea: si estende sopra il Gantt per tutta la larghezza visibile. */
function Overlay({ vw, children, label }: { vw: number; children: React.ReactNode; label: string }) {
  return (
    <div role="group" aria-label={label} className="absolute top-0 left-0 z-40 flex flex-wrap items-center gap-2 border border-rule bg-bg p-2" style={{ width: Math.max(280, vw - 2), minHeight: H.add }}>
      {children}
    </div>
  );
}

function AddForm(p: LabelProps & { adding: Adding }) {
  const t = useT();
  const a = p.adding;
  const template = p.plan.project.template;
  const [title, setTitle] = useState("");
  const [date, setDate] = useState(cal.fmt(cal.nextWorkday(cal.today())));
  const [mode, setMode] = useState<DeliveryMode>(template === "waterfall" ? "waterfall" : template === "contract" ? "supplier" : "agile");
  const ok = title.trim() !== "" && (a.kind !== "ms" || date !== "");
  const label = a.kind === "wp" ? t("progetti.piano.addWp") : a.kind === "ms" ? t("progetti.piano.addMilestone") : t("progetti.piano.addActivity");

  function submit() {
    if (!ok || p.busy) return;
    const name = title.trim();
    const phase = a.kind !== "act" && a.phaseId ? { phase_id: a.phaseId } : {};
    p.setAdding(null);
    if (a.kind === "wp") p.run(() => projectsApi.createItem(p.refKey, { kind: "work_package", title: name, delivery_mode: mode, ...phase }));
    else if (a.kind === "ms") p.run(() => projectsApi.createItem(p.refKey, { kind: "milestone", title: name, date, ...phase }));
    else p.run(() => projectsApi.createItem(p.refKey, { kind: "plan_activity", title: name, parent_id: a.wp.id }));
  }

  return (
    <Overlay vw={p.vw} label={label}>
      <input
        autoFocus
        className="field min-w-0 flex-1 basis-40"
        aria-label={t("progetti.piano.titleLabel")}
        placeholder={t("progetti.piano.titleLabel")}
        value={title}
        onChange={(e) => setTitle(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") submit();
          if (e.key === "Escape") p.setAdding(null);
        }}
      />
      {a.kind === "wp" && (
        <div role="radiogroup" aria-label={t("progetti.piano.modeLabel")} className="flex border border-rule text-[12px]">
          {MODES.map((m) => (
            <button key={m} role="radio" aria-checked={mode === m} className={`touch-44 px-2.5 py-1.5 ${mode === m ? "bg-fg text-bg" : "text-muted hover:text-fg"}`} onClick={() => setMode(m)}>
              {t(`progetti.piano.mode.${m}`)}
            </button>
          ))}
        </div>
      )}
      {a.kind === "ms" && <input type="date" className="field" aria-label={t("progetti.piano.dateLabel")} value={date} onChange={(e) => setDate(e.target.value)} />}
      <button className="btn btn-primary" disabled={!ok || p.busy} onClick={submit}>
        {t("progetti.piano.add")}
      </button>
      <button className="btn" onClick={() => p.setAdding(null)}>
        {t("progetti.piano.cancel")}
      </button>
    </Overlay>
  );
}

/** «Coinvolgi il team» senza responsabile: si sceglie il team, poi il work package passa a «Proposto». */
function InvolveForm(p: LabelProps & { it: PlanItem }) {
  const t = useT();
  const [team, setTeam] = useState("");
  return (
    <Overlay vw={p.vw} label={t("progetti.piano.involve")}>
      <div className="min-w-0 flex-1 basis-48">
        <EntitySelect kind="team" value={team} onChange={setTeam} aria-label={t("progetti.piano.teamLabel")} placeholder={t("progetti.piano.teamLabel")} />
      </div>
      <button
        className="btn btn-primary"
        disabled={!team || p.busy}
        onClick={() => {
          p.setInvolving(null);
          p.run(async () => {
            await projectsApi.updateItem(p.it.id, { team_id: team });
            await api.transition(p.it.id, "Proposto"); // i18n-ok: nome di stato del processo
          });
        }}
      >
        {t("progetti.piano.involveGo")}
      </button>
      <button className="btn" onClick={() => p.setInvolving(null)}>
        {t("progetti.piano.cancel")}
      </button>
    </Overlay>
  );
}

// ---------------------------------------------------------------------------------------------
// Barre, rombi, frecce.

function PhaseBar({ phase, y, span, xOf, px }: { phase: Phase | null; y: number; span: Span | null; xOf: (d: number) => number; px: number }) {
  const t = useT();
  if (!span) return null;
  const x = xOf(span.s);
  const w = (cal.diffDays(span.s, span.e) + 1) * px;
  const pct = phase ? Math.min(100, Math.max(0, phase.progress)) : 0;
  return (
    <div
      className="pointer-events-none absolute z-10"
      style={{ left: x, width: Math.max(w, 6), top: y - 5, height: 10 }}
      title={t("progetti.piano.phaseTitle", { name: phase?.name ?? t("progetti.piano.noPhase"), from: cal.shortYear(span.s), to: cal.shortYear(span.e) })}
    >
      <div className="h-full border border-fg bg-bg">
        <div className="h-full bg-fg" style={{ width: `${pct}%` }} />
      </div>
      <span className="absolute left-full ml-1.5 font-mono text-[10.5px] whitespace-nowrap text-muted">
        {cal.short(span.s)} – {cal.short(span.e)}
      </span>
    </div>
  );
}

function Bar({
  it,
  span,
  isGhost,
  y,
  x,
  px,
  depth,
  crit,
  dragging,
  canEdit,
  onDown,
  onMove,
  onUp,
  onCancel,
}: {
  it: PlanItem;
  span: Span;
  isGhost: boolean;
  y: number;
  x: number;
  px: number;
  depth: 0 | 1;
  crit: boolean;
  dragging: boolean;
  canEdit: boolean;
  onDown: (e: RPointerEvent<HTMLDivElement>, it: PlanItem, span: Span, isGhost: boolean) => void;
  onMove: (e: RPointerEvent<HTMLDivElement>) => void;
  onUp: (it: PlanItem) => void;
  onCancel: () => void;
}) {
  const t = useT();
  const nav = useNavigate();
  const conflictText = useConflictText();
  const conflicts = it.conflicts ?? [];
  const ms = it.kind === "milestone";
  const wd = cal.workdays(span.s, span.e);
  const dur = ms ? cal.short(span.s) : isGhost ? t("progetti.piano.toPlan") : t("progetti.piano.daysShort", { count: wd });
  const w = (cal.diffDays(span.s, span.e) + 1) * px;
  const tone =
    it.state_category === "cancelled"
      ? "bg-hair text-muted" // i18n-ok: classi CSS
      : it.state_category === "done"
        ? "bg-muted text-bg" // i18n-ok: classi CSS
        : crit
          ? "bg-fg text-bg" // i18n-ok: classi CSS
          : "bg-accent text-accent-fg"; // i18n-ok: classi CSS
  const common = {
    role: "button" as const,
    tabIndex: 0,
    "aria-label": t("progetti.piano.barAria", { title: it.title, from: cal.short(span.s), to: cal.short(span.e), what: dur }),
    title: t("progetti.piano.barTitle", { key: it.key, title: it.title, from: cal.shortYear(span.s), to: cal.shortYear(span.e), state: it.state, pct: Math.round(it.progress) }) + (conflicts.length ? `\n▲ ${conflicts.map(conflictText).join("\n▲ ")}` : ""),
    onPointerDown: (e: RPointerEvent<HTMLDivElement>) => onDown(e, it, span, isGhost),
    onPointerMove: onMove,
    onPointerUp: () => onUp(it),
    onPointerCancel: onCancel,
    onKeyDown: (e: React.KeyboardEvent) => {
      if (e.key === "Enter") nav({ to: "/e/$key", params: { key: it.key } });
    },
  };
  const cursor = canEdit ? (dragging ? "cursor-grabbing" : "cursor-grab") : "cursor-pointer";
  const ring = dragging ? "shadow-[0_0_0_2px_var(--accent)]" : "";
  const motion = { transition: dragging ? "none" : "left .18s ease, width .18s ease", touchAction: "none" as const };

  if (ms) {
    const done = it.state_category === "done";
    return (
      <div
        {...common}
        className={`absolute z-10 flex items-center justify-center select-none ${cursor}`}
        style={{ left: x + px / 2 - 11, top: y - 11, width: 22, height: 22, ...motion }}
      >
        <span
          aria-hidden="true"
          className={`block h-3.5 w-3.5 rotate-45 ${ring} ${isGhost ? "border border-dashed border-accent bg-sel" : done ? "bg-muted" : crit ? "bg-fg" : "bg-accent"}`}
        />
        <span className="pointer-events-none absolute left-full ml-1 font-mono text-[10.5px] whitespace-nowrap text-muted">
          {it.title} · {dur}
        </span>
      </div>
    );
  }
  const h = depth === 1 ? 16 : 22;
  return (
    <div
      {...common}
      className={`group absolute z-10 flex items-center overflow-visible select-none ${isGhost ? "border border-dashed border-accent bg-sel text-accent" : tone} ${cursor} ${ring}`}
      style={{ left: x, width: Math.max(w, 6), top: y - h / 2, height: h, ...motion }}
    >
      {!isGhost && it.progress > 0 && <span aria-hidden="true" className="pointer-events-none absolute bottom-0 left-0 h-[3px] bg-bg/70" style={{ width: `${Math.min(100, it.progress)}%` }} />}
      {canEdit && !isGhost && <span className="absolute inset-y-0 left-0 w-1.5 cursor-ew-resize opacity-0 group-hover:bg-bg/40 group-hover:opacity-100" />}
      {conflicts.length > 0 && !isGhost && <span aria-hidden="true" className="shrink-0 pl-1 text-[10px] font-bold">▲</span>}
      <span className="truncate px-1.5 text-[11px] font-semibold whitespace-nowrap">{w > 70 ? dur : ""}</span>
      {canEdit && !isGhost && <span className="absolute inset-y-0 right-0 w-1.5 cursor-ew-resize opacity-0 group-hover:bg-bg/40 group-hover:opacity-100" />}
      {w <= 70 && <span className="pointer-events-none absolute left-full ml-1.5 font-mono text-[10.5px] whitespace-nowrap text-muted">{dur}</span>}
      {dragging && (
        <span className="pointer-events-none absolute -top-6 left-0 z-30 bg-fg px-1.5 py-0.5 font-mono text-[10.5px] whitespace-nowrap text-bg">
          {t("progetti.piano.dragLabel", { from: cal.short(span.s), to: cal.short(span.e), days: wd })}
        </span>
      )}
    </div>
  );
}

/** La voce nella baseline in vigore, in trasparenza: una striscia sotto la barra, un rombo vuoto per la milestone. */
function BaselineMark({ it, y, depth, xOf, px }: { it: PlanItem; y: number; depth: 0 | 1; xOf: (d: number) => number; px: number }) {
  const t = useT();
  if (!it.baseline_start && !it.baseline_end) return null;
  const s = cal.parse(it.baseline_start ?? it.baseline_end!);
  const e = cal.parse(it.baseline_end ?? it.baseline_start!);
  const v = it.variance_days ?? 0;
  const title = t("progetti.baseline.markTitle", {
    from: cal.shortYear(s),
    to: cal.shortYear(e),
    shift: v === 0 ? t("progetti.baseline.onBaseline") : t("progetti.baseline.shift", { sign: v > 0 ? "+" : "−", count: Math.abs(v) }),
  });
  if (it.kind === "milestone") {
    return (
      <span
        aria-hidden="true"
        title={title}
        className="pointer-events-none absolute z-[9] block h-3.5 w-3.5 rotate-45 border border-dashed border-muted bg-hair/40"
        style={{ left: xOf(s) + px / 2 - 7, top: y - 7 }}
      />
    );
  }
  const h = depth === 1 ? 16 : 22;
  return (
    <span
      aria-hidden="true"
      title={title}
      className="pointer-events-none absolute z-[9] block border border-muted bg-muted/25"
      style={{ left: xOf(s), width: Math.max((cal.diffDays(s, e) + 1) * px, 6), top: y + h / 2 + 2, height: 5 }}
    />
  );
}

/** Frecce: fine→inizio (fs), inizio→inizio (ss), fine→fine (ff); in rosso tratteggiato quelle non rispettate. */
function Arrows({
  items,
  spans,
  yOf,
  xOf,
  px,
  width,
  height,
  crit,
}: {
  items: PlanItem[];
  spans: Map<string, Span>;
  yOf: Map<string, number>;
  xOf: (d: number) => number;
  px: number;
  width: number;
  height: number;
  crit: Set<string>;
}) {
  const paths: { d: string; bad: boolean; hot: boolean; k: string }[] = [];
  for (const it of items) {
    const ts = spans.get(it.id);
    if (!ts || !yOf.has(it.id)) continue;
    const dms = it.kind === "milestone";
    for (const dep of it.deps) {
      const ps = spans.get(dep.id);
      const pit = items.find((x) => x.id === dep.id);
      if (!ps || !pit || !yOf.has(dep.id)) continue;
      const pms = pit.kind === "milestone";
      // Punti di attacco: i rombi stanno al centro del giorno.
      const startX = (s: Span, ms: boolean) => xOf(s.s) + (ms ? px / 2 : 0);
      const endX = (s: Span, ms: boolean) => (ms ? xOf(s.s) + px / 2 : xOf(s.e) + px);
      const y1 = yOf.get(dep.id)!;
      const y2 = yOf.get(it.id)!;
      let d: string;
      if (dep.kind === "ss") {
        const x1 = startX(ps, pms);
        const x2 = startX(ts, dms);
        const lx = Math.min(x1, x2) - 10;
        d = `M${x1},${y1} H${lx} V${y2} H${x2 - 2}`;
      } else if (dep.kind === "ff") {
        const x1 = endX(ps, pms);
        const x2 = endX(ts, dms);
        const rx = Math.max(x1, x2) + 10;
        d = `M${x1},${y1} H${rx} V${y2} H${x2 + 2}`;
      } else {
        const x1 = endX(ps, pms);
        const x2 = startX(ts, dms);
        const bend = Math.max(x1 + 8, Math.min(x2 - 8, x1 + 16));
        d = x2 - 8 >= x1 + 8 ? `M${x1},${y1} H${bend} V${y2} H${x2 - 2}` : `M${x1},${y1} h8 V${(y1 + y2) / 2} H${x2 - 10} V${y2} H${x2 - 2}`;
      }
      paths.push({ d, bad: violated(ps, ts, dms, dep.kind, dep.lag), hot: crit.has(it.id) && crit.has(dep.id), k: dep.id + it.id + dep.kind });
    }
  }
  return (
    <svg className="pointer-events-none absolute top-0 left-0 z-[5]" width={width} height={height} aria-hidden="true">
      <defs>
        {["muted", "fg", "danger"].map((c) => (
          <marker key={c} id={`parrow-${c}`} viewBox="0 0 8 8" refX="7" refY="4" markerWidth="7" markerHeight="7" orient="auto">
            <path d="M0,0 L8,4 L0,8 z" fill={`var(--${c})`} />
          </marker>
        ))}
      </defs>
      {paths.map((p) => (
        <path
          key={p.k}
          d={p.d}
          fill="none"
          stroke={p.bad ? "var(--danger)" : p.hot ? "var(--fg)" : "var(--muted)"}
          strokeWidth={p.hot ? 1.75 : 1.25}
          strokeDasharray={p.bad ? "4 3" : undefined}
          markerEnd={`url(#parrow-${p.bad ? "danger" : p.hot ? "fg" : "muted"})`}
        />
      ))}
    </svg>
  );
}
