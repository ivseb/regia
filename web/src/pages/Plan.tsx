import { useEffect, useMemo, useRef, useState, type PointerEvent as RPointerEvent } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, type Plan, type PlanTask } from "../api";
import * as cal from "../calendar";
import { PageHeader } from "../shell";
import { ErrorNote, Loading } from "../ui";
import { useT } from "../i18n/react";
import { Trans } from "../i18n/Trans";
import { Markers, NonWorkdays, PX_PER_DAY, TimeHeader, ZoomSwitch, useNarrow, type Zoom } from "./timeline";

// Gantt della richiesta (piano, passo "wow"): impatti per team con le dipendenze.
// Trascinando una barra la si sposta, tirando i bordi la si allunga o accorcia; chi
// dipende da lei slitta a cascata già durante il trascinamento, con le stesse regole
// del server (giorni lavorativi, festività italiane). Il percorso critico è in evidenza.

const ROW = 38;
const GROUP = 28;

type Span = { s: number; e: number };
type Drag = { id: string; mode: "move" | "start" | "end"; x0: number; orig: Span; moved: boolean };

function proposedDuration(t: PlanTask): number {
  return t.estimate_effort_days && t.estimate_effort_days > 0 ? Math.ceil(t.estimate_effort_days / (t.team_capacity / 5)) : 5;
}

/** Percorso critico: catene a margine zero che finiscono con la fine del progetto. */
function critical(tasks: PlanTask[], spans: Map<string, Span>): Set<string> {
  const crit = new Set<string>();
  let end = -Infinity;
  for (const t of tasks) {
    const s = spans.get(t.id);
    if (s && t.state_category !== "cancelled") end = Math.max(end, s.e);
  }
  for (const t of tasks) if (spans.get(t.id)?.e === end && t.state_category !== "cancelled") crit.add(t.id);
  let changed = true;
  while (changed) {
    changed = false;
    for (const t of tasks) {
      if (!crit.has(t.id)) continue;
      const ts = spans.get(t.id)!;
      for (const p of t.depends_on) {
        const ps = spans.get(p);
        if (ps && !crit.has(p) && cal.afterEnd(ps.e) >= ts.s) {
          crit.add(p);
          changed = true;
        }
      }
    }
  }
  return crit;
}

export function PlanPage({ requestKey }: { requestKey: string }) {
  const tr = useT();
  const qc = useQueryClient();
  const narrow = useNarrow();
  const [zoom, setZoom] = useState<Zoom>(() => (window.matchMedia("(max-width: 767px)").matches ? "mesi" : "settimane"));
  const plan = useQuery({ queryKey: ["plan", requestKey], queryFn: () => api.plan(requestKey) });
  const auto = useMutation({
    mutationFn: (replan: boolean) => api.autoPlan(requestKey, replan),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["plan", requestKey] });
      qc.invalidateQueries({ queryKey: ["load"] });
    },
  });
  if (plan.isPending) return <Loading className="p-8 text-muted" />;
  if (plan.error) return <div className="p-8"><ErrorNote error={plan.error} /></div>;
  const p = plan.data!;
  const unscheduled = p.tasks.filter((t) => !t.start && t.state_category !== "cancelled").length;
  return (
    <>
      <PageHeader
        path={tr("lavoro.plan.path", { key: p.request.key })}
        title={p.request.title}
        actions={
          <div className="flex flex-wrap items-center gap-2">
            <ZoomSwitch value={zoom} options={["giorni", "settimane", "mesi"]} onChange={setZoom} />
            {p.can_edit && p.tasks.length > 0 && (
              <button className="btn btn-primary" disabled={auto.isPending} onClick={() => auto.mutate(unscheduled === 0)}>
                {unscheduled > 0 ? (unscheduled === 1 ? tr("lavoro.plan.planOne") : tr("lavoro.plan.planMany", { count: unscheduled })) : tr("lavoro.plan.replanAll")}
              </button>
            )}
          </div>
        }
      />
      <div className="flex flex-col gap-4 px-4 md:px-7 pb-10">
        <ErrorNote error={auto.error} />
        {p.tasks.length === 0 ? (
          <p className="text-muted">
            <Trans
              k="lavoro.plan.noImpacts"
              slots={{
                link: (
                  <Link to="/e/$key" params={{ key: p.request.key }} className="text-accent">
                    {tr("lavoro.plan.technicalAnalysis")}
                  </Link>
                ),
              }}
            />
          </p>
        ) : (
          <Gantt plan={p} zoom={zoom} narrow={narrow} />
        )}
      </div>
    </>
  );
}

function Summary({ plan, spans, crit }: { plan: Plan; spans: Map<string, Span>; crit: Set<string> }) {
  const tr = useT();
  const live = plan.tasks.filter((t) => t.state_category !== "cancelled" && spans.has(t.id));
  if (live.length === 0) return null;
  const start = Math.min(...live.map((t) => spans.get(t.id)!.s));
  const end = Math.max(...live.map((t) => spans.get(t.id)!.e));
  const days = cal.workdays(start, end);
  const deadline = plan.request.regulatory_deadline ? cal.parse(plan.request.regulatory_deadline) : null;
  const desired = plan.request.desired_date ? cal.parse(plan.request.desired_date) : null;
  const margin = (d: number) => (end <= d ? cal.workdays(cal.addDays(end, 1), d) : -cal.workdays(cal.addDays(d, 1), end));
  const effort = live.reduce((n, t) => n + (t.estimate_effort_days ?? 0), 0);
  const items: { k: string; v: string; tone?: string }[] = [
    { k: tr("lavoro.plan.start"), v: cal.shortYear(start) },
    { k: tr("lavoro.plan.expectedEnd"), v: cal.shortYear(end) },
    { k: tr("lavoro.plan.duration"), v: tr("lavoro.plan.workdays", { count: days }) },
    { k: tr("lavoro.plan.effort"), v: tr("lavoro.plan.personDays", { count: effort }) },
    { k: tr("lavoro.plan.criticalPath"), v: tr("lavoro.plan.impacts", { count: crit.size }) },
  ];
  if (deadline !== null) {
    const m = margin(deadline);
    items.push({ k: tr("lavoro.plan.regDeadline"), v: m >= 0 ? tr("lavoro.plan.marginDays", { count: m }) : tr("lavoro.plan.lateDays", { count: -m }), tone: m >= 0 ? "" : "text-danger" });
  } else if (desired !== null) {
    const m = margin(desired);
    items.push({ k: tr("lavoro.plan.desiredDate"), v: m >= 0 ? tr("lavoro.plan.marginDays", { count: m }) : tr("lavoro.plan.overDays", { count: -m }), tone: m >= 0 ? "" : "text-flag" });
  }
  return (
    <dl className="m-0 flex flex-wrap gap-x-8 gap-y-2 border-y border-hair py-3">
      {items.map((i) => (
        <div key={i.k} className="flex flex-col">
          <dt className="label">{i.k}</dt>
          <dd className={`m-0 text-[15px] font-semibold tabular ${i.tone ?? ""}`}>{i.v}</dd>
        </div>
      ))}
    </dl>
  );
}

function Gantt({ plan, zoom, narrow }: { plan: Plan; zoom: Zoom; narrow: boolean }) {
  const tr = useT();
  const noTeam = tr("lavoro.plan.noTeam");
  const qc = useQueryClient();
  const nav = useNavigate();
  const px = PX_PER_DAY[zoom];
  const label = narrow ? 128 : 236;
  const [drag, setDrag] = useState<Drag | null>(null);
  const [delta, setDelta] = useState(0);
  const [pending, setPending] = useState<Map<string, Span> | null>(null);
  const dragRef = useRef<Drag | null>(null);

  const base = useMemo(() => {
    const m = new Map<string, Span>();
    for (const t of plan.tasks) if (t.start && t.end) m.set(t.id, { s: cal.parse(t.start), e: cal.parse(t.end) });
    return m;
  }, [plan]);

  // Proposta per gli impatti senza date: barra tratteggiata da oggi, durata dalla stima.
  const ghost = useMemo(() => {
    const m = new Map<string, Span>();
    const s = cal.nextWorkday(cal.today());
    for (const t of plan.tasks) if (!t.start && t.state_category !== "cancelled") m.set(t.id, { s, e: cal.endAfter(s, proposedDuration(t)) });
    return m;
  }, [plan]);

  // Date mostrate: quelle salvate, più l'anteprima del trascinamento con la cascata.
  const spans = useMemo(() => {
    if (pending) return pending;
    if (!drag) return base;
    const all = new Map(base);
    const o = drag.orig;
    let next: Span;
    if (drag.mode === "move") {
      const s = cal.nextWorkday(cal.addDays(o.s, delta));
      next = { s, e: cal.endAfter(s, cal.workdays(o.s, o.e)) };
    } else if (drag.mode === "end") {
      next = { s: o.s, e: Math.max(o.s, cal.addDays(o.e, delta)) };
    } else {
      next = { s: Math.min(o.e, cal.nextWorkday(cal.addDays(o.s, delta))), e: o.e };
    }
    all.set(drag.id, next);
    return cascadeFrom(plan.tasks, all, drag.id);
  }, [base, drag, delta, pending, plan.tasks]);

  const crit = useMemo(() => critical(plan.tasks, spans), [plan.tasks, spans]);

  const save = useMutation({
    mutationFn: ({ id, s }: { id: string; s: Span }) => api.scheduleImpact(id, cal.fmt(s.s), cal.fmt(s.e)),
    onSettled: async () => {
      await qc.invalidateQueries({ queryKey: ["plan", plan.request.key] });
      qc.invalidateQueries({ queryKey: ["load"] });
      setPending(null);
    },
  });

  // Intervallo visibile: dal lunedì prima dell'inizio (o di oggi) a qualche settimana dopo la fine e le scadenze.
  const { from, days } = useMemo(() => {
    const points = [cal.today()];
    for (const s of base.values()) points.push(s.s, s.e);
    for (const s of ghost.values()) points.push(s.e);
    if (plan.request.regulatory_deadline) points.push(cal.parse(plan.request.regulatory_deadline));
    if (plan.request.desired_date) points.push(cal.parse(plan.request.desired_date));
    const f = cal.monday(cal.addDays(Math.min(...points), -7));
    const t = cal.addDays(Math.max(...points), zoom === "mesi" ? 60 : 28);
    return { from: f, days: cal.diffDays(f, t) };
  }, [base, ghost, plan.request, zoom]);

  // Righe: gruppi per team, impatti in ordine di inizio.
  type Row = { kind: "group"; team: string } | { kind: "task"; t: PlanTask };
  const rows: Row[] = [];
  const groups = new Map<string, PlanTask[]>();
  for (const t of plan.tasks) groups.set(t.team ?? noTeam, [...(groups.get(t.team ?? noTeam) ?? []), t]);
  for (const [team, ts] of [...groups.entries()].sort((a, b) => a[0].localeCompare(b[0]))) {
    rows.push({ kind: "group", team });
    ts.sort((a, b) => (spans.get(a.id)?.s ?? Infinity) - (spans.get(b.id)?.s ?? Infinity));
    for (const t of ts) rows.push({ kind: "task", t });
  }
  const yOf = new Map<string, number>();
  let y = 0;
  for (const r of rows) {
    if (r.kind === "task") yOf.set(r.t.id, y + ROW / 2);
    y += r.kind === "group" ? GROUP : ROW;
  }
  const height = y;
  const width = days * px;
  const xOf = (t: number) => cal.diffDays(from, t) * px;

  // All'apertura (e cambiando scala) si scorre fino alla prima barra o a oggi.
  const scroller = useRef<HTMLDivElement>(null);
  const firstStart = Math.min(...[...base.values()].map((s) => s.s), cal.today());
  useEffect(() => {
    const el = scroller.current;
    if (el) el.scrollLeft = Math.max(0, cal.diffDays(from, firstStart) * px - 3 * px - 8);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [zoom, plan.request.id]);

  function onDown(e: RPointerEvent<HTMLDivElement>, t: PlanTask, span: Span) {
    if (!plan.can_edit || save.isPending) return;
    const r = e.currentTarget.getBoundingClientRect();
    const off = e.clientX - r.left;
    const edge = Math.min(10, r.width / 3);
    const mode: Drag["mode"] = !t.start ? "move" : off < edge ? "start" : off > r.width - edge ? "end" : "move";
    e.currentTarget.setPointerCapture(e.pointerId);
    const d = { id: t.id, mode, x0: e.clientX, orig: span, moved: false };
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
  function onUp(t: PlanTask) {
    const d = dragRef.current;
    dragRef.current = null;
    if (!d) return;
    if (!d.moved) {
      setDrag(null);
      nav({ to: "/e/$key", params: { key: t.key } });
      return;
    }
    const final = spans.get(d.id) ?? d.orig;
    setPending(spans);
    setDrag(null);
    if (t.start && final.s === d.orig.s && final.e === d.orig.e) {
      setPending(null);
      return;
    }
    save.mutate({ id: d.id, s: final });
  }

  const markers = [
    ...(plan.request.regulatory_deadline ? [{ at: plan.request.regulatory_deadline, label: tr("lavoro.plan.markerDeadline"), tone: "flag" as const }] : []),
    ...(plan.request.desired_date ? [{ at: plan.request.desired_date, label: tr("lavoro.plan.markerDesired"), tone: "muted" as const }] : []),
  ];

  return (
    <>
      <Summary plan={plan} spans={spans} crit={crit} />
      <p className="m-0 text-[12.5px] text-muted">
        {plan.can_edit
          ? tr("lavoro.plan.helpEdit")
          : tr("lavoro.plan.helpReadOnly")}{" "}
        {tr("lavoro.plan.helpCritical")}
      </p>
      <div ref={scroller} className="overflow-x-auto border border-hair" style={{ touchAction: "pan-x pan-y" }}>
        <div style={{ width: label + width }}>
          <TimeHeader from={from} days={days} px={px} zoom={zoom} label={label} />
          <div className="relative flex">
            <div className="sticky left-0 z-20 shrink-0 border-r border-hair bg-bg" style={{ width: label }}>
              {rows.map((r) =>
                r.kind === "group" ? (
                  <div key={"g" + r.team} className="label flex items-end border-b border-hair px-3 pb-1" style={{ height: GROUP }}>
                    {r.team}
                  </div>
                ) : (
                  <div key={r.t.id} className="flex flex-col justify-center border-b border-hair/60 px-3" style={{ height: ROW }}>
                    <span className={`truncate text-[12.5px] font-medium ${r.t.state_category === "cancelled" ? "text-muted line-through" : ""}`}>
                      {r.t.application}
                    </span>
                    <span className="truncate font-mono text-[10.5px] text-muted">
                      {r.t.key} · {r.t.state}
                    </span>
                  </div>
                ),
              )}
            </div>
            <div className="relative" style={{ width, height }}>
              <NonWorkdays from={from} days={days} px={px} height={height} />
              {rows.map((r, i) => (
                <div key={i} className="absolute left-0 border-b border-hair/60" style={{ top: rowTop(rows, i), height: r.kind === "group" ? GROUP : ROW, width }} />
              ))}
              <Markers from={from} px={px} height={height} markers={markers} />
              <Arrows tasks={plan.tasks} spans={spans} yOf={yOf} xOf={xOf} px={px} width={width} height={height} crit={crit} />
              {plan.tasks.map((t) => {
                const real = spans.get(t.id);
                const span = real ?? ghost.get(t.id);
                if (!span) return null;
                const isGhost = !real;
                const x = xOf(span.s);
                const w = (cal.diffDays(span.s, span.e) + 1) * px;
                const top = yOf.get(t.id)! - 11;
                const dragging = drag?.id === t.id;
                const isCrit = crit.has(t.id);
                const tone =
                  t.state_category === "cancelled"
                    ? "bg-hair text-muted" // i18n-ok: classi CSS
                    : t.state_category === "done"
                      ? "bg-muted text-bg" // i18n-ok: classi CSS
                      : isCrit
                        ? "bg-fg text-bg" // i18n-ok: classi CSS
                        : "bg-accent text-accent-fg"; // i18n-ok: classi CSS
                const wd = cal.workdays(span.s, span.e);
                const dur = isGhost ? tr("lavoro.plan.toPlan") : tr("lavoro.plan.daysShort", { count: wd });
                return (
                  <div
                    key={t.id}
                    role="button"
                    tabIndex={0}
                    aria-label={tr("lavoro.plan.barAria", { app: t.application, from: cal.short(span.s), to: cal.short(span.e), what: dur })}
                    title={tr("lavoro.plan.barTitle", { key: t.key, app: t.application, from: cal.shortYear(span.s), to: cal.shortYear(span.e), days: wd }) + (t.estimate_effort_days != null ? tr("lavoro.plan.barEstimate", { days: t.estimate_effort_days }) : "")}
                    onPointerDown={(e) => onDown(e, t, span)}
                    onPointerMove={onMove}
                    onPointerUp={() => onUp(t)}
                    onPointerCancel={() => {
                      dragRef.current = null;
                      setDrag(null);
                    }}
                    onKeyDown={(e) => {
                      if (e.key === "Enter") nav({ to: "/e/$key", params: { key: t.key } });
                    }}
                    className={`group absolute z-10 flex items-center overflow-visible select-none ${isGhost ? "border border-dashed border-accent bg-sel text-accent" : tone} ${
                      plan.can_edit ? (dragging ? "cursor-grabbing" : "cursor-grab") : "cursor-pointer"
                    } ${dragging ? "shadow-[0_0_0_2px_var(--accent)]" : ""}`}
                    style={{ left: x, width: Math.max(w, 6), top, height: 22, touchAction: "none", transition: dragging ? "none" : "left .18s ease, width .18s ease" }}
                  >
                    {plan.can_edit && !isGhost && <span className="absolute inset-y-0 left-0 w-1.5 cursor-ew-resize opacity-0 group-hover:bg-bg/40 group-hover:opacity-100" />}
                    <span className="truncate px-1.5 text-[11px] font-semibold whitespace-nowrap">{w > 70 ? dur : ""}</span>
                    {plan.can_edit && !isGhost && <span className="absolute inset-y-0 right-0 w-1.5 cursor-ew-resize opacity-0 group-hover:bg-bg/40 group-hover:opacity-100" />}
                    {w <= 70 && (
                      <span className="pointer-events-none absolute left-full ml-1.5 font-mono text-[10.5px] whitespace-nowrap text-muted">
                        {dur}
                      </span>
                    )}
                    {dragging && (
                      <span className="pointer-events-none absolute -top-6 left-0 z-30 bg-fg px-1.5 py-0.5 font-mono text-[10.5px] whitespace-nowrap text-bg">
                        {tr("lavoro.plan.dragLabel", { from: cal.short(span.s), to: cal.short(span.e), days: wd })}
                      </span>
                    )}
                  </div>
                );
              })}
            </div>
          </div>
        </div>
      </div>
      <ErrorNote error={save.error} />
      <LoadStrip plan={plan} from={from} days={days} px={px} label={label} />
    </>
  );
}

function rowTop(rows: { kind: string }[], i: number): number {
  let y = 0;
  for (let j = 0; j < i; j++) y += rows[j].kind === "group" ? GROUP : ROW;
  return y;
}

/** Slittamento a cascata (solo in avanti), come planning.graph.cascade sul server. */
function cascadeFrom(tasks: PlanTask[], spans: Map<string, Span>, id: string): Map<string, Span> {
  const out = new Map(spans);
  const dependents = new Map<string, string[]>();
  for (const t of tasks) for (const p of t.depends_on) dependents.set(p, [...(dependents.get(p) ?? []), t.id]);
  const queue = [id];
  for (let steps = 0; queue.length && steps < 5000; steps++) {
    const cur = queue.shift()!;
    const cs = out.get(cur);
    if (!cs) continue;
    for (const d of dependents.get(cur) ?? []) {
      const ds = out.get(d);
      const task = tasks.find((x) => x.id === d);
      if (!ds || !task || task.state_category === "cancelled") continue;
      const earliest = cal.afterEnd(cs.e);
      if (ds.s >= earliest) continue;
      out.set(d, { s: earliest, e: cal.endAfter(earliest, cal.workdays(ds.s, ds.e)) });
      queue.push(d);
    }
  }
  return out;
}

/** Frecce delle dipendenze (fine → inizio); in rosso tratteggiato quelle non rispettate. */
function Arrows({
  tasks,
  spans,
  yOf,
  xOf,
  px,
  width,
  height,
  crit,
}: {
  tasks: PlanTask[];
  spans: Map<string, Span>;
  yOf: Map<string, number>;
  xOf: (t: number) => number;
  px: number;
  width: number;
  height: number;
  crit: Set<string>;
}) {
  const paths: { d: string; bad: boolean; hot: boolean; k: string }[] = [];
  for (const t of tasks) {
    const ts = spans.get(t.id);
    if (!ts) continue;
    for (const p of t.depends_on) {
      const ps = spans.get(p);
      if (!ps || !yOf.has(p)) continue;
      const x1 = xOf(ps.e) + px;
      const y1 = yOf.get(p)!;
      const x2 = xOf(ts.s);
      const y2 = yOf.get(t.id)!;
      const bend = Math.max(x1 + 8, Math.min(x2 - 8, x1 + 16));
      const d =
        x2 - 8 >= x1 + 8
          ? `M${x1},${y1} H${bend} V${y2} H${x2 - 2}`
          : `M${x1},${y1} h8 V${(y1 + y2) / 2} H${x2 - 10} V${y2} H${x2 - 2}`;
      paths.push({ d, bad: ts.s <= ps.e, hot: crit.has(t.id) && crit.has(p), k: p + t.id });
    }
  }
  return (
    <svg className="pointer-events-none absolute top-0 left-0 z-[5]" width={width} height={height} aria-hidden="true">
      <defs>
        {["muted", "fg", "danger"].map((c) => (
          <marker key={c} id={`arrow-${c}`} viewBox="0 0 8 8" refX="7" refY="4" markerWidth="7" markerHeight="7" orient="auto">
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
          markerEnd={`url(#arrow-${p.bad ? "danger" : p.hot ? "fg" : "muted"})`}
        />
      ))}
    </svg>
  );
}

/** Carico dei team del piano, settimana per settimana, allineato al Gantt. */
function LoadStrip({ plan, from, days, px, label }: { plan: Plan; from: number; days: number; px: number; label: number }) {
  const tr = useT();
  const to = cal.addDays(from, days);
  const load = useQuery({
    queryKey: ["load", cal.fmt(from), cal.fmt(to)],
    queryFn: () => api.teamLoad(cal.fmt(from), cal.fmt(to)),
    placeholderData: keepPreviousData,
  });
  const teams = new Set(plan.tasks.map((t) => t.team_id).filter(Boolean));
  const rows = load.data?.items.filter((t) => teams.has(t.id)) ?? [];
  if (rows.length === 0) return null;
  return (
    <section className="flex flex-col gap-2">
      <div className="flex items-baseline justify-between border-b-2 border-rule pb-1.5">
        <h3 className="label m-0">{tr("lavoro.plan.teamLoad")}</h3>
        <span className="hidden text-[12px] text-muted md:inline">{tr("lavoro.plan.teamLoadHelp")}</span>
      </div>
      <div className="overflow-x-auto">
        <div style={{ width: label + days * px }}>
          {rows.map((t) => (
            <div key={t.id} className="flex border-b border-hair/60" style={{ height: 30 }}>
              <div className="sticky left-0 z-10 flex shrink-0 flex-col justify-center border-r border-hair bg-bg px-3" style={{ width: label }}>
                <span className="truncate text-[12.5px] font-medium">{t.name}</span>
                <span className="font-mono text-[10.5px] text-muted">{tr("lavoro.plan.perWeek", { count: t.capacity_days_week })}</span>
              </div>
              <div className="relative" style={{ width: days * px }}>
                {t.weeks.map((w) => {
                  const ws = cal.parse(w.week);
                  const x = cal.diffDays(from, ws) * px;
                  const r = w.capacity_days > 0 ? w.load_days / w.capacity_days : 0;
                  if (w.load_days <= 0) return null;
                  const over = r > 1.0001;
                  return (
                    <div
                      key={w.week}
                      title={tr("lavoro.plan.weekTitle", { date: cal.short(ws), load: w.load_days, capacity: w.capacity_days, pct: Math.round(r * 100) })}
                      className={`absolute top-1 bottom-1 flex items-center justify-center font-mono text-[10.5px] font-semibold ${over ? "bg-danger text-bg" : "text-fg"}`}
                      style={{
                        left: x + 1,
                        width: 7 * px - 2,
                        background: over ? undefined : `color-mix(in srgb, var(--accent) ${Math.round(12 + r * 50)}%, transparent)`,
                      }}
                    >
                      {7 * px >= 34 ? `${Math.round(r * 100)}%` : ""}
                    </div>
                  );
                })}
              </div>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}
