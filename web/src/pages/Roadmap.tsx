import { Suspense, lazy, useEffect, useMemo, useRef, useState, type PointerEvent as RPointerEvent } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, type RequestWindow } from "../api";
import * as cal from "../calendar";
import { PageHeader, useMe } from "../shell";
import { DRIVERS, ErrorNote, SectionTitle, date } from "../ui";
import { fmtDate } from "../i18n/format";
import { useT } from "../i18n/react";
import { Markers, NonWorkdays, PX_PER_DAY, TimeHeader, ZoomSwitch, useNarrow, type Zoom } from "./timeline";
import { phaseTint } from "../tinte";
import { crumbs } from "../crumbs";
import { moduleOn } from "../modules";
import { projectKeys, projectsApi } from "../projectsApi";
// La roadmap dei progetti sta con il programma: si carica a parte, come la sua pagina.
const ProjectsRoadmap = lazy(() => import("./progetti/ProgramOverview").then((m) => ({ default: m.Roadmap })));

// Roadmap di portafoglio: ogni richiesta è una barra sulla sua finestra (l'inviluppo dei
// suoi impatti pianificati, o la finestra impostata a mano). Dentro la barra i segmenti
// dello stage-gate; in arancione le scadenze normative. Trascinando una barra si sposta
// tutta la richiesta, impatti compresi.

const ROW = 44;

type Drag = { key: string; x0: number; s: number; e: number; mode: "move" | "end"; moved: boolean };

export function Roadmap() {
  const t = useT();
  const [zoom, setZoom] = useState<Zoom>("mesi");
  const [onlyRegulatory, setOnlyRegulatory] = useState(false);
  const [closed, setClosed] = useState(false);
  const rm = useQuery({ queryKey: ["roadmap", closed], queryFn: () => api.roadmap(closed) });
  // Progetti, passo 6 (progetti.md §4): la roadmap mostra i progetti e le richieste non ancora diventate progetto.
  const projectsOn = moduleOn(useMe().data, "projects");
  const pf = useQuery({ queryKey: projectKeys.portfolio(closed), queryFn: () => projectsApi.portfolio(closed), enabled: projectsOn });
  const projects = pf.data?.projects ?? [];
  const all = (rm.data?.items ?? []).filter((r) => !onlyRegulatory || r.is_mandatory || r.regulatory_deadline);
  const items = projectsOn ? all.filter((r) => !r.in_project) : all;
  const merged = all.length - items.length;
  return (
    <>
      <PageHeader
        path={crumbs("demand", "roadmap")}
        title={t("lavoro.roadmap.title")}
        actions={
          <div className="flex flex-wrap items-center gap-3">
            <label className="flex items-center gap-1.5 text-[12.5px]">
              <input type="checkbox" checked={onlyRegulatory} onChange={(e) => setOnlyRegulatory(e.target.checked)} />
              {t("lavoro.roadmap.onlyRegulatory")}
            </label>
            <label className="flex items-center gap-1.5 text-[12.5px]">
              <input type="checkbox" checked={closed} onChange={(e) => setClosed(e.target.checked)} />
              {t("lavoro.roadmap.includeClosed")}
            </label>
            <ZoomSwitch value={zoom} options={["settimane", "mesi", "trimestri"]} onChange={setZoom} />
          </div>
        }
      />
      <div role="tablist" aria-label={t("lavoro.roadmap.view")} className="flex gap-6 border-b border-hair px-4 md:px-7 pt-1">
        <Link to="/richieste" role="tab" aria-selected="false" className="pb-2.5 text-muted no-underline hover:text-fg">
          {t("lavoro.roadmap.tabList")}
        </Link>
        <Link to="/cabina" role="tab" aria-selected="false" className="pb-2.5 text-muted no-underline hover:text-fg">
          {t("lavoro.cabina.tab")}
        </Link>
        <span role="tab" aria-selected="false" aria-disabled="true" className="pb-2.5 text-muted" title={t("lavoro.roadmap.comingSoon")}>
          {t("lavoro.roadmap.tabBoard")}
        </span>
        <span role="tab" aria-selected="true" className="border-b-2 border-fg pb-2.5 font-semibold">
          {t("lavoro.roadmap.tabRoadmap")}
        </span>
      </div>
      <div className="flex flex-col gap-3 px-4 md:px-7 py-5">
        {projectsOn && (
          <section className="flex flex-col gap-2" aria-label={t("progetti.passo6.roadmap.projects")} data-testid="roadmap-progetti">
            <SectionTitle>{t("progetti.passo6.roadmap.projects")}</SectionTitle>
            <ErrorNote error={pf.error} />
            {pf.data && projects.length === 0 ? (
              <p className="m-0 text-[13px] text-muted">{t("progetti.passo6.roadmap.noProjects")}</p>
            ) : (
              <Suspense fallback={null}>
                <ProjectsRoadmap projects={projects} />
              </Suspense>
            )}
          </section>
        )}
        {projectsOn && <SectionTitle>{t("progetti.passo6.roadmap.requests")}</SectionTitle>}
        <p className="m-0 text-[12.5px] text-muted">
          {t("lavoro.roadmap.help")}
          {projectsOn && merged > 0 && ` ${t("progetti.passo6.roadmap.merged", { count: merged })}`}
        </p>
        <RoadmapChart items={items} zoom={zoom} error={rm.error} />
        {items.length === 0 && rm.data && <p className="text-muted">{t("lavoro.roadmap.empty")}</p>}
      </div>
    </>
  );
}

/** La roadmap come componente: la usano la pagina Roadmap e la cabina di portafoglio (con la linea di taglio). */
export function RoadmapChart({ items, zoom, cutAfter, error }: { items: RequestWindow[]; zoom: Zoom; cutAfter?: number | null; error?: unknown }) {
  const t = useT();
  const qc = useQueryClient();
  const nav = useNavigate();
  const narrow = useNarrow();
  const [drag, setDrag] = useState<Drag | null>(null);
  const [delta, setDelta] = useState(0);
  const dragRef = useRef<Drag | null>(null);
  const shift = useMutation({
    mutationFn: ({ key, s, e }: { key: string; s: number; e: number }) => api.shiftRequest(key, cal.fmt(s), cal.fmt(e)),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: ["roadmap"] });
      qc.invalidateQueries({ queryKey: ["plan"] });
      qc.invalidateQueries({ queryKey: ["cabina"] });
      setDrag(null);
    },
  });
  const px = PX_PER_DAY[zoom];
  const label = narrow ? 168 : 320;
  const { from, days } = useMemo(() => {
    const pts = [cal.today()];
    for (const r of items) for (const d of [r.start, r.end, r.regulatory_deadline, r.desired_date]) if (d) pts.push(cal.parse(d));
    const f = cal.monday(cal.addDays(Math.min(...pts), -14));
    const t = cal.addDays(Math.max(...pts), 45);
    return { from: f, days: Math.max(cal.diffDays(f, t), 180) };
  }, [items]);
  const xOf = (t: number) => cal.diffDays(from, t) * px;
  const scroller = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const el = scroller.current;
    if (el) el.scrollLeft = Math.max(0, cal.diffDays(from, cal.today()) * px - 40);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [zoom, items.length > 0]);
  const height = items.length * ROW;

  function span(r: RequestWindow): { s: number; e: number } | null {
    if (!r.start || !r.end) return null;
    let s = cal.parse(r.start);
    let e = cal.parse(r.end);
    if (drag?.key === r.key) {
      if (drag.mode === "move") {
        s = cal.addDays(drag.s, delta);
        e = cal.addDays(drag.e, delta);
      } else {
        e = Math.max(s, cal.addDays(drag.e, delta));
      }
    }
    return { s, e };
  }

  function onDown(ev: RPointerEvent<HTMLDivElement>, r: RequestWindow, s: number, e: number) {
    const rect = ev.currentTarget.getBoundingClientRect();
    const mode = r.scheduled === 0 && ev.clientX > rect.right - 10 ? "end" : "move";
    ev.currentTarget.setPointerCapture(ev.pointerId);
    const d = { key: r.key, x0: ev.clientX, s, e, mode, moved: false } as Drag;
    dragRef.current = d;
    setDrag(d);
    setDelta(0);
  }
  function onMove(ev: RPointerEvent<HTMLDivElement>) {
    const d = dragRef.current;
    if (!d) return;
    if (Math.abs(ev.clientX - d.x0) > 3) d.moved = true;
    setDelta(Math.round((ev.clientX - d.x0) / px));
  }
  function onUp(r: RequestWindow) {
    const d = dragRef.current;
    dragRef.current = null;
    if (!d) return;
    if (!d.moved || delta === 0) {
      setDrag(null);
      if (!d.moved) nav({ to: "/richieste/$key/piano", params: { key: r.key } });
      return;
    }
    const sp = span(r)!;
    shift.mutate({ key: r.key, s: sp.s, e: sp.e });
  }

  if (narrow) return <RoadmapMonths items={items} cutAfter={cutAfter} error={error} />;
  return (
    <>
      <ErrorNote error={error ?? shift.error} />
        <div ref={scroller} className="overflow-x-auto border border-hair">
          <div style={{ width: label + days * px }}>
            <TimeHeader from={from} days={days} px={px} zoom={zoom} label={label} />
            <div className="relative flex">
              <div className="sticky left-0 z-20 shrink-0 border-r border-hair bg-bg" style={{ width: label }}>
                {items.map((r, i) => (
                  <div key={r.id} className={`relative flex flex-col justify-center border-b border-hair/60 px-3 ${cutAfter != null && i >= cutAfter ? "opacity-60" : ""}`} style={{ height: ROW }}>
                    {cutAfter != null && i === cutAfter && <span className="absolute -top-px left-0 z-30 bg-accent px-1 font-mono text-[10px] leading-[14px] font-semibold text-accent-fg">{t("lavoro.cabina.cutLabel")}</span>}
                    <Link to="/richieste/$key/piano" params={{ key: r.key }} title={r.title} className={`font-medium text-fg no-underline hover:underline ${narrow ? "line-clamp-2 text-[11.5px] leading-[1.15]" : "truncate text-[12.5px]"}`}>
                      {r.title}
                    </Link>
                    <span className="truncate font-mono text-[10.5px] text-muted" title={`${r.key} · ${r.state}`}>
                      {r.key} · {r.state}
                      {r.driver && !narrow ? ` · ${DRIVERS[r.driver] ?? r.driver}` : ""}
                    </span>
                  </div>
                ))}
              </div>
              <div className="relative" style={{ width: days * px, height }}>
                <NonWorkdays from={from} days={days} px={px} height={height} />
                <Markers from={from} px={px} height={height} markers={[]} />
                {cutAfter != null && items.length > 0 && (
                  <div
                    data-cut-line
                    className="pointer-events-none absolute left-0 z-30 h-0.5 bg-accent"
                    style={{ top: Math.min(cutAfter, items.length) * ROW - 1, width: days * px }}
                  />
                )}
                {items.map((r, i) => {
                  const top = i * ROW;
                  const sp = span(r);
                  const deadline = r.regulatory_deadline ? cal.parse(r.regulatory_deadline) : null;
                  const desired = r.desired_date ? cal.parse(r.desired_date) : null;
                  const late = deadline !== null && sp !== null && sp.e > deadline;
                  return (
                    <div key={r.id} className={`absolute left-0 border-b border-hair/60 ${cutAfter != null && i >= cutAfter ? "opacity-45" : ""}`} style={{ top, height: ROW, width: days * px }}>
                      {deadline !== null && (
                        <div
                          title={t("lavoro.roadmap.deadline", { date: cal.shortYear(deadline) })}
                          className="pointer-events-none absolute top-0.5 bottom-0.5 z-20 border-l-2 border-flag"
                          style={{ left: xOf(deadline) + px / 2 }}
                        >
                          <span className="absolute -top-0.5 left-1 bg-bg px-0.5 font-mono text-[10px] font-semibold whitespace-nowrap text-flag">{cal.short(deadline)}</span>
                        </div>
                      )}
                      {desired !== null && !sp && (
                        <div title={t("lavoro.roadmap.desired", { date: cal.shortYear(desired) })} className="absolute top-1/2 size-2.5 -translate-y-1/2 rotate-45 border border-muted" style={{ left: xOf(desired) }} />
                      )}
                      {sp ? (
                        <div
                          role="button"
                          tabIndex={0}
                          aria-label={`${r.title}: ${cal.short(sp.s)} – ${cal.short(sp.e)}`}
                          title={t("lavoro.roadmap.barTitle", { key: r.key, title: r.title, from: cal.shortYear(sp.s), to: cal.shortYear(sp.e) }) + (r.scheduled ? t("lavoro.roadmap.barScheduled", { count: r.scheduled }) : "")}
                          onPointerDown={(e) => onDown(e, r, cal.parse(r.start!), cal.parse(r.end!))}
                          onPointerMove={onMove}
                          onPointerUp={() => onUp(r)}
                          onPointerCancel={() => {
                            dragRef.current = null;
                            setDrag(null);
                          }}
                          onKeyDown={(e) => {
                            if (e.key === "Enter") nav({ to: "/richieste/$key/piano", params: { key: r.key } });
                          }}
                          className={`absolute top-[11px] z-[15] flex h-[22px] cursor-grab overflow-hidden select-none ${drag?.key === r.key ? "shadow-[0_0_0_2px_var(--accent)]" : ""} ${late ? "outline-2 outline-danger" : ""}`}
                          style={{
                            left: xOf(sp.s),
                            width: Math.max((cal.diffDays(sp.s, sp.e) + 1) * px, 8),
                            touchAction: "none",
                            transition: drag?.key === r.key ? "none" : "left .2s ease, width .2s ease",
                          }}
                        >
                          <PhaseSegments r={r} />
                          {drag?.key === r.key && (
                            <span className="pointer-events-none absolute inset-0 flex items-center justify-center bg-fg/80 font-mono text-[10.5px] whitespace-nowrap text-bg">
                              {cal.short(sp.s)} → {cal.short(sp.e)}
                            </span>
                          )}
                        </div>
                      ) : (
                        <span className="absolute top-[14px] font-mono text-[10.5px] text-muted" style={{ left: xOf(cal.today()) + 8 }}>
                          {r.impacts > 0 ? t("lavoro.roadmap.impactsToPlan") : t("lavoro.roadmap.toPlan")}
                        </span>
                      )}
                    </div>
                  );
                })}
              </div>
            </div>
          </div>
        </div>
    </>
  );
}

/** Sul telefono la roadmap è una lista per mese: niente scorrimento orizzontale, bersagli da 44 px. */
function RoadmapMonths({ items, cutAfter, error }: { items: RequestWindow[]; cutAfter?: number | null; error?: unknown }) {
  const t = useT();
  const groups = new Map<string, { label: string; rows: { r: RequestWindow; out: boolean }[] }>();
  items.forEach((r, i) => {
    const iso = r.start ?? r.desired_date ?? r.regulatory_deadline;
    const k = iso ? iso.slice(0, 7) : "~";
    if (!groups.has(k)) groups.set(k, { label: iso ? fmtDate(`${k}-01`, { month: "long", year: "numeric" }) : t("lavoro.roadmap.noMonth"), rows: [] });
    groups.get(k)!.rows.push({ r, out: cutAfter != null && i >= cutAfter });
  });
  const keys = [...groups.keys()].sort();
  return (
    <div className="flex flex-col gap-4" data-roadmap-months>
      <ErrorNote error={error} />
      {keys.map((k) => (
        <section key={k} aria-label={groups.get(k)!.label}>
          <h3 className="label m-0 border-b-2 border-rule pb-1.5 capitalize">{groups.get(k)!.label}</h3>
          <ul className="m-0 flex list-none flex-col p-0">
            {groups.get(k)!.rows.map(({ r, out }) => (
              <li key={r.id} className={`flex min-h-[44px] items-center gap-3 border-b border-hair/60 py-1.5 ${out ? "text-muted" : ""}`}>
                <Link to="/richieste/$key/piano" params={{ key: r.key }} className="min-w-0 flex-1 py-1 text-[13px] break-words font-medium text-fg no-underline hover:underline">
                  {r.title}
                </Link>
                <span className="flex shrink-0 flex-col items-end text-[11px]">
                  <span className="font-mono text-muted">{r.key}</span>
                  <span className="font-mono tabular">{r.start && r.end ? `${date(r.start)} – ${date(r.end)}` : t("lavoro.roadmap.toPlan")}</span>
                  {r.regulatory_deadline && <span className="font-mono text-flag">{t("lavoro.roadmap.deadline", { date: date(r.regulatory_deadline) })}</span>}
                  {out && <span className="font-semibold">{t("lavoro.cabina.outBadge")}</span>}
                </span>
              </li>
            ))}
          </ul>
        </section>
      ))}
    </div>
  );
}


/** Segmenti dello stage-gate dentro la barra: fasi superate, fase attuale, fasi future. */
function PhaseSegments({ r }: { r: RequestWindow }) {
  const n = Math.max(r.phase_count, 1);
  const off = r.phase_index < 0;
  const col = `var(--m-${phaseTint(r.state, r.phase_index)})`;
  return (
    <span className="flex h-full w-full gap-px bg-bg">
      {Array.from({ length: n }, (_, i) => (
        <span
          key={i}
          className={`h-full flex-1 ${off ? "bg-muted/50" : i < r.phase_index ? "bg-fg" : ""}`}
          style={off || i < r.phase_index ? undefined : { background: i === r.phase_index ? col : `color-mix(in srgb, ${col} 28%, transparent)` }}
        />
      ))}
    </span>
  );
}
