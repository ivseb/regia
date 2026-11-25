// Diagrammi C4 (contesto, container, componenti): il server calcola riquadri e frecce dal
// grafo a ogni apertura; qui c'è la disposizione a strati, le posizioni scelte dall'utente
// (salvate col diagramma), la notazione C4 con la legenda e l'export SVG e PNG.
import { Select } from "../../select";
import { useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ApiError } from "../../api";
import { useMe } from "../../shell";
import { ErrorNote, SectionTitle, ago } from "../../ui";
import { canWriteEA, eaApi, type C4Actor, type C4Edge, type C4Model, type C4Node, type DiagramLayout, type DiagramView } from "../../eaApi";
import { short, type Pos } from "./graphModel";
import { exportPNG, exportSVG, fileName, useViewport } from "./canvas";
import { useT } from "../../i18n/react";
import { tr } from "../../i18n/core";
import { Trans } from "../../i18n/Trans";

const W = 212;
const H = 104;
const GX = 270;
const GY = 190;

const LEVELS = {
  get context() {
    return tr("altro.ea.c4.levelContext");
  },
  get container() {
    return tr("altro.ea.c4.levelContainer");
  },
  get component() {
    return tr("altro.ea.c4.levelComponent");
  },
} as Record<string, string>;

const KIND_LABEL = {
  get person() {
    return tr("altro.ea.c4.kindPerson");
  },
  get system() {
    return tr("altro.ea.c4.kindSystem");
  },
  get external() {
    return tr("altro.ea.c4.kindExternal");
  },
  get container() {
    return tr("altro.ea.c4.kindContainer");
  },
  get database() {
    return tr("altro.ea.c4.kindDatabase");
  },
  get component() {
    return tr("altro.ea.c4.kindComponent");
  },
  get asset() {
    return tr("altro.ea.c4.kindAsset");
  },
} as Record<C4Node["kind"], string>;

/** Classi del tema per riempimento, bordo e testo di ogni tipo di riquadro. */
const KIND_STYLE: Record<C4Node["kind"], { fill: string; stroke: string; text: string; sub: string }> = {
  person: { fill: "fill-fg", stroke: "stroke-fg", text: "fill-bg", sub: "fill-bg" },
  system: { fill: "fill-accent", stroke: "stroke-accent", text: "fill-bg", sub: "fill-bg" },
  external: { fill: "fill-hair", stroke: "stroke-muted", text: "fill-fg", sub: "fill-muted" },
  container: { fill: "fill-sel", stroke: "stroke-accent", text: "fill-fg", sub: "fill-muted" },
  database: { fill: "fill-sel", stroke: "stroke-accent", text: "fill-fg", sub: "fill-muted" },
  component: { fill: "fill-bg", stroke: "stroke-accent", text: "fill-fg", sub: "fill-muted" },
  asset: { fill: "fill-bg", stroke: "stroke-muted", text: "fill-fg", sub: "fill-muted" },
};

const ORDER: C4Node["kind"][] = ["person", "external", "asset", "system", "container", "component", "database"];

/**
 * Disposizione a strati: le frecce vanno dall'alto al basso (archi all'indietro ignorati),
 * dentro ogni strato i riquadri seguono il baricentro dei vicini. I riquadri dentro il
 * confine del sistema stanno al centro, quelli fuori ai lati: il confine non li copre.
 */
export function c4Layout(m: C4Model): Map<string, Pos> {
  const ids = m.nodes.map((n) => n.id);
  const idx = new Map(ids.map((id, i) => [id, i]));
  const adj: number[][] = ids.map(() => []);
  for (const e of m.edges) {
    const a = idx.get(e.source);
    const b = idx.get(e.target);
    if (a !== undefined && b !== undefined && a !== b) adj[a].push(b);
  }
  // Archi in avanti: si scartano quelli che chiudono un ciclo.
  const state = new Array<number>(ids.length).fill(0);
  const fwd: [number, number][] = [];
  const dfs = (u: number) => {
    state[u] = 1;
    for (const v of adj[u]) {
      if (state[v] === 1) continue;
      fwd.push([u, v]);
      if (state[v] === 0) dfs(v);
    }
    state[u] = 2;
  };
  const start = [...ids.keys()].sort((a, b) => ORDER.indexOf(m.nodes[a].kind) - ORDER.indexOf(m.nodes[b].kind));
  for (const u of start) if (state[u] === 0) dfs(u);
  const layer = new Array<number>(ids.length).fill(0);
  for (let pass = 0; pass < ids.length; pass++) {
    let changed = false;
    for (const [u, v] of fwd) if (layer[v] < layer[u] + 1) (layer[v] = layer[u] + 1), (changed = true);
    if (!changed) break;
  }
  const rows = new Map<number, number[]>();
  ids.forEach((_, i) => rows.set(layer[i], [...(rows.get(layer[i]) ?? []), i]));
  const depth = Math.max(0, ...rows.keys());
  const x = new Array<number>(ids.length).fill(0);
  const rank = (i: number) => ORDER.indexOf(m.nodes[i].kind) * 1000 + i;
  for (const [, r] of rows) r.sort((a, b) => rank(a) - rank(b));
  // Baricentro: due passate, giù e su.
  const nb: number[][] = ids.map(() => []);
  for (const [u, v] of fwd) (nb[u].push(v), nb[v].push(u));
  const place = (r: number[]) => r.forEach((i, k) => (x[i] = k - (r.length - 1) / 2));
  rows.forEach(place);
  for (let pass = 0; pass < 2; pass++) {
    for (let l = pass === 0 ? 1 : depth - 1; pass === 0 ? l <= depth : l >= 0; l += pass === 0 ? 1 : -1) {
      const r = rows.get(l);
      if (!r) continue;
      const bary = (i: number) => {
        const ns = nb[i].filter((j) => layer[j] !== l);
        return ns.length ? ns.reduce((s, j) => s + x[j], 0) / ns.length : x[i];
      };
      r.sort((a, b) => bary(a) - bary(b) || rank(a) - rank(b));
      place(r);
    }
  }
  // Posizioni: i riquadri interni al centro, gli altri ai lati negli strati del confine.
  const inside = (i: number) => !!m.nodes[i].inside;
  const insideLayers = [...rows.entries()].filter(([, r]) => r.some(inside)).map(([l]) => l);
  const lo = Math.min(...insideLayers, Infinity);
  const hi = Math.max(...insideLayers, -Infinity);
  const half = Math.max(0, ...[...rows.values()].map((r) => ((r.filter(inside).length - 1) * GX) / 2)) + W / 2 + 50;
  const out = new Map<string, Pos>();
  for (const [l, r] of rows) {
    const y = l * GY;
    if (l >= lo && l <= hi) {
      const ins = r.filter(inside);
      const outs = r.filter((i) => !inside(i));
      ins.forEach((i, k) => out.set(ids[i], { x: (k - (ins.length - 1) / 2) * GX, y }));
      const left = outs.filter((_, k) => k % 2 === 0);
      const right = outs.filter((_, k) => k % 2 === 1);
      left.forEach((i, k) => out.set(ids[i], { x: -(half + W / 2 + 20 + k * GX), y }));
      right.forEach((i, k) => out.set(ids[i], { x: half + W / 2 + 20 + k * GX, y }));
    } else {
      r.forEach((i, k) => out.set(ids[i], { x: (k - (r.length - 1) / 2) * GX, y }));
    }
  }
  return out;
}

/** Punto del bordo del riquadro (centro c) in direzione d. */
function clip(c: Pos, dx: number, dy: number): Pos {
  const t = Math.min(dx ? W / 2 / Math.abs(dx) : Infinity, dy ? H / 2 / Math.abs(dy) : Infinity);
  return { x: c.x + dx * t, y: c.y + dy * t };
}

/** Righe di testo di un riquadro a capo per parole (stima di 6.4 px per carattere a 12 px). */
function wrap(s: string, max: number, lines: number): string[] {
  const out: string[] = [];
  let cur = "";
  for (const w of s.split(/\s+/)) {
    if ((cur + " " + w).trim().length > max && cur) {
      out.push(cur);
      cur = w;
    } else cur = (cur + " " + w).trim();
  }
  if (cur) out.push(cur);
  if (out.length > lines) {
    out.length = lines;
    out[lines - 1] = short(out[lines - 1] + "…", max);
  }
  return out;
}

export function C4Editor({ diagram, onReload }: { diagram: DiagramView; onReload: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const nav = useNavigate();
  const perms = useMe().data?.active_tenant?.permissions ?? [];
  const canWrite = canWriteEA(perms);
  const [model, setModel] = useState<C4Model>(diagram.c4!);
  const [layout, setLayout] = useState<DiagramLayout>(diagram.layout);
  const [name, setName] = useState(diagram.name);
  const [description, setDescription] = useState(diagram.description);
  const [version, setVersion] = useState(diagram.version);
  const [dirty, setDirty] = useState(false);
  const [selected, setSelected] = useState<string | null>(null);
  const spec = layout.c4!;
  const [actors, setActors] = useState<C4Actor[]>(spec.actors);

  const auto = useMemo(() => c4Layout(model), [model]);
  const saved = layout.pos ?? {};
  const moving = useRef<{ id: string; p: Pos } | null>(null);
  const [, force] = useState(0);
  const posOf = (id: string): Pos => (moving.current?.id === id ? moving.current.p : (saved[id] ?? auto.get(id) ?? { x: 0, y: 0 }));

  const vp = useViewport({
    onTap: (id) => setSelected(id),
    canDrag: () => canWrite,
    onDrag: (id, p, phase) => {
      const q = { x: Math.round(p.x), y: Math.round(p.y) };
      if (phase === "move") {
        moving.current = { id, p: q };
        force((n) => n + 1);
        return;
      }
      moving.current = null;
      setLayout((l) => ({ ...l, pos: { ...(l.pos ?? {}), [id]: q } }));
      setDirty(true);
    },
  });

  const points = (): Pos[] => {
    const out: Pos[] = [];
    for (const n of model.nodes) {
      const p = posOf(n.id);
      out.push({ x: p.x - W / 2 - 40, y: p.y - H / 2 - 50 }, { x: p.x + W / 2 + 40, y: p.y + H / 2 + 170 });
    }
    return out;
  };
  const fitted = useRef(false);
  useEffect(() => {
    if (fitted.current) return;
    fitted.current = true;
    requestAnimationFrame(() => vp.fit(points(), 1.5));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const save = useMutation({
    mutationFn: (next?: DiagramLayout) => eaApi.updateDiagram(diagram.id, { name, description, layout: next ?? layout, version }),
    onSuccess: (d) => {
      setDirty(false);
      setVersion(d.version);
      if (d.c4) setModel(d.c4);
      setLayout(d.layout);
      setActors(d.layout.c4?.actors ?? []);
      qc.invalidateQueries({ queryKey: ["ea", "diagrams"] });
    },
  });
  const remove = useMutation({
    mutationFn: () => eaApi.deleteDiagram(diagram.id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["ea", "diagrams"] });
      nav({ to: "/architettura/diagrammi" });
    },
  });
  const conflict = save.error instanceof ApiError && save.error.status === 409;
  /** Cambia la specifica (attori, nodi nascosti) e salva subito: il modello si ricalcola sul server. */
  const commitSpec = (patch: Partial<typeof spec>) => save.mutate({ ...layout, c4: { ...spec, ...patch } });

  const byID = new Map(model.nodes.map((n) => [n.id, n]));
  const sel = selected ? byID.get(selected) : undefined;
  // Confine del sistema: riquadro attorno ai riquadri interni.
  const inner = model.nodes.filter((n) => n.inside);
  const box = inner.length
    ? (() => {
        const ps = inner.map((n) => posOf(n.id));
        const x0 = Math.min(...ps.map((p) => p.x)) - W / 2 - 36;
        const x1 = Math.max(...ps.map((p) => p.x)) + W / 2 + 36;
        const y0 = Math.min(...ps.map((p) => p.y)) - H / 2 - 44;
        const y1 = Math.max(...ps.map((p) => p.y)) + H / 2 + 36;
        return { x: x0, y: y0, w: x1 - x0, h: y1 - y0 };
      })()
    : null;
  const present = ORDER.filter((k) => model.nodes.some((n) => n.kind === k));
  const legendY = Math.max(0, ...model.nodes.map((n) => posOf(n.id).y)) + H / 2 + 70;
  const legendX = Math.min(0, ...model.nodes.map((n) => posOf(n.id).x)) - W / 2;
  // Frecce tra la stessa coppia: scostate per non sovrapporsi.
  const lane = new Map<string, number>();
  const seen = new Map<string, number>();
  for (const e of model.edges) {
    const k = [e.source, e.target].sort().join("|");
    const n = seen.get(k) ?? 0;
    seen.set(k, n + 1);
    lane.set(e.id + e.source, n);
  }

  return (
    <div className="flex min-w-0 flex-col lg:flex-row">
      <div className="flex min-w-0 flex-1 flex-col">
        <div className="flex flex-wrap items-center gap-2 px-4 py-2 md:px-7">
          <Link to="/architettura/diagrammi" className="text-[12.5px] text-accent underline">
            {t("altro.ea.c4.all")}
          </Link>
          <span className="text-[12.5px] text-muted">
            {t("altro.ea.c4.header", { level: LEVELS[spec.level] })}
          </span>
          <span className="ml-auto flex flex-wrap gap-1.5">
            <button className="btn px-2.5 py-1" aria-label={t("altro.ea.c4.zoomIn")} onClick={() => vp.zoomAt(1.3)}>
              +
            </button>
            <button className="btn px-2.5 py-1" aria-label={t("altro.ea.c4.zoomOut")} onClick={() => vp.zoomAt(1 / 1.3)}>
              −
            </button>
            <button className="btn px-2.5 py-1" onClick={() => vp.fit(points(), 1.5)}>
              {t("altro.ea.c4.fit")}
            </button>
            <button className="btn px-2.5 py-1" onClick={() => vp.svgRef.current && vp.gRef.current && exportSVG(vp.svgRef.current, vp.gRef.current, fileName(name))}>
              SVG
            </button>
            <button className="btn px-2.5 py-1" onClick={() => vp.svgRef.current && vp.gRef.current && exportPNG(vp.svgRef.current, vp.gRef.current, fileName(name))}>
              PNG
            </button>
            {canWrite && (
              <button className="btn btn-primary px-3 py-1" disabled={!dirty || save.isPending} onClick={() => save.mutate(undefined)}>
                {dirty ? t("altro.ea.c4.save") : t("altro.ea.c4.saved")}
              </button>
            )}
          </span>
        </div>
        {conflict && (
          <p className="m-0 px-4 pb-2 text-[12.5px] text-danger md:px-7">
            <Trans
              k="altro.ea.c4.conflict"
              slots={{
                reload: (
                  <button className="underline" onClick={onReload}>
                    {t("altro.ea.c4.reload")}
                  </button>
                ),
              }}
            />
          </p>
        )}
        {!conflict && <ErrorNote error={save.error} />}
        <div className="relative h-[60vh] min-h-[360px] border-y border-hair lg:h-[calc(100vh-240px)]">
          <svg
            ref={vp.svgRef}
            className="block h-full w-full touch-none select-none bg-bg font-sans"
            role="img"
            aria-label={t("altro.ea.c4.ariaDiagram", { level: LEVELS[spec.level], boxes: model.nodes.length, arrows: model.edges.length })}
            {...vp.bind}
          >
            <defs>
              <marker id="c4-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="8" markerHeight="8" orient="auto-start-reverse">
                <path d="M0,0 L10,5 L0,10 z" className="fill-muted" />
              </marker>
            </defs>
            <g ref={vp.gRef}>
              {box && (
                <g>
                  <rect x={box.x} y={box.y} width={box.w} height={box.h} rx={6} className="fill-none stroke-muted" strokeWidth={1.5} strokeDasharray="8 5" />
                  <text x={box.x + 14} y={box.y + 22} className="fill-fg text-[13px] font-bold">
                    {short(model.boundary ?? model.root_name, 60)}
                  </text>
                  <text x={box.x + 14} y={box.y + 37} className="fill-muted text-[10.5px]">
                    {t("altro.ea.c4.boundary", { kind: spec.level === "component" ? t("altro.ea.c4.kindAsset") : t("altro.ea.c4.application") })}
                  </text>
                </g>
              )}
              {model.edges.map((e) => (
                <Arrow key={e.id + e.source} e={e} a={posOf(e.source)} b={posOf(e.target)} lane={lane.get(e.id + e.source) ?? 0} hot={selected === e.source || selected === e.target} labels={vp.labels} />
              ))}
              {model.nodes.map((n) => (
                <Box key={n.id} n={n} p={posOf(n.id)} selected={selected === n.id} canWrite={canWrite} />
              ))}
              <g transform={`translate(${legendX} ${legendY})`}>
                <text className="fill-fg text-[11px] font-bold">{t("altro.ea.c4.legend")}</text>
                {present.map((k, i) => {
                  const st = KIND_STYLE[k];
                  return (
                    <g key={k} transform={`translate(${(i % 4) * 150} ${14 + Math.floor(i / 4) * 20})`}>
                      <rect width={20} height={12} rx={2} className={`${st.fill} ${st.stroke}`} strokeWidth={1.2} />
                      <text x={28} y={10} className="fill-fg text-[10.5px]">
                        {KIND_LABEL[k]}
                      </text>
                    </g>
                  );
                })}
                <g transform={`translate(0 ${14 + Math.ceil(present.length / 4) * 20})`}>
                  <line x1={0} y1={6} x2={20} y2={6} className="stroke-muted" strokeWidth={1.4} markerEnd="url(#c4-arrow)" />
                  <text x={28} y={10} className="fill-fg text-[10.5px]">
                    {t("altro.ea.c4.arrowLegend")}
                  </text>
                </g>
              </g>
            </g>
          </svg>
        </div>
        {model.missing && <p className="m-0 px-4 py-2 text-[12.5px] text-danger md:px-7">{t("altro.ea.c4.rootMissing")}</p>}
      </div>
      <aside className="flex w-full shrink-0 flex-col gap-4 border-hair px-4 py-4 md:px-7 lg:w-[360px] lg:border-l lg:px-5" aria-label={t("altro.ea.c4.panel")}>
        <SectionTitle>{t("altro.ea.c4.title")}</SectionTitle>
        {canWrite ? (
          <>
            <label className="flex flex-col gap-1">
              <span className="label">{t("altro.ea.c4.name")}</span>
              <input
                className="field"
                value={name}
                onChange={(e) => {
                  setName(e.target.value);
                  setDirty(true);
                }}
              />
            </label>
            <label className="flex flex-col gap-1">
              <span className="label">{t("altro.ea.c4.description")}</span>
              <textarea
                className="field min-h-16"
                value={description}
                onChange={(e) => {
                  setDescription(e.target.value);
                  setDirty(true);
                }}
              />
            </label>
          </>
        ) : (
          <p className="m-0 text-[12.5px] text-muted">
            {t("altro.ea.c4.levelUpdated", { level: LEVELS[spec.level], when: ago(diagram.updated_at) })}
          </p>
        )}
        <p className="m-0 text-[12.5px] text-muted">
          {t("altro.ea.c4.help")}
        </p>
        {canWrite && (
          <button
            className="btn self-start"
            onClick={() => {
              setLayout((l) => ({ ...l, pos: {} }));
              setDirty(true);
              requestAnimationFrame(() => vp.fit(points(), 1.5));
            }}
          >
            {t("altro.ea.c4.autoLayout")}
          </button>
        )}
        {sel && (
          <section className="flex flex-col gap-2 border-t-2 border-rule pt-3">
            {sel.key && sel.element_id ? (
              <Link to="/e/$key" params={{ key: sel.key }} className="font-mono text-[12px] text-accent no-underline hover:underline">
                {sel.key}
              </Link>
            ) : (
              <span className="font-mono text-[12px] text-muted">{sel.type_label}</span>
            )}
            <h2 className="m-0 text-[18px] leading-tight font-bold">{sel.name}</h2>
            <p className="m-0 text-[12.5px] text-muted">
              {sel.type_label}
              {sel.tech ? ` · ${sel.tech}` : ""}
            </p>
            {sel.description && <p className="m-0 text-[12.5px]">{sel.description}</p>}
            {canWrite && sel.kind !== "system" && !(sel.kind === "container" && sel.id === `e:${spec.root_id}`) && !sel.id.startsWith("a:") && (
              <button className="btn self-start" onClick={() => commitSpec({ hidden: [...(spec.hidden ?? []), sel.id] })}>
                {t("altro.ea.c4.hide")}
              </button>
            )}
          </section>
        )}
        {(spec.hidden ?? []).length > 0 && canWrite && (
          <section className="flex flex-col gap-1 border-t border-hair pt-3">
            <span className="label">{t("altro.ea.c4.hidden")}</span>
            <button className="btn self-start" onClick={() => commitSpec({ hidden: [] })}>
              {t("altro.ea.c4.showHidden", { count: (spec.hidden ?? []).length })}
            </button>
          </section>
        )}
        <section className="flex flex-col gap-2 border-t border-hair pt-3">
          <span className="label">{t("altro.ea.c4.actors")}</span>
          <p className="m-0 text-[12.5px] text-muted">{t("altro.ea.c4.actorsHelp")}</p>
          {actors.map((a, i) => (
            <div key={a.id} className="flex flex-col gap-1 border border-hair p-2">
              <div className="flex gap-1.5">
                <input
                  className="field min-w-0 flex-1"
                  aria-label={t("altro.ea.c4.actorName")}
                  value={a.name}
                  disabled={!canWrite}
                  onChange={(e) => setActors(actors.map((x, j) => (j === i ? { ...x, name: e.target.value } : x)))}
                />
                <Select
                  className="field w-auto"
                  aria-label={t("altro.ea.c4.actorKind")}
                  value={a.kind}
                  disabled={!canWrite}
                  onChange={(e) => setActors(actors.map((x, j) => (j === i ? { ...x, kind: e.target.value as C4Actor["kind"] } : x)))}
                >
                  <option value="person">{t("altro.ea.c4.person")}</option>
                  <option value="role">{t("altro.ea.c4.role")}</option>
                </Select>
              </div>
              <input
                className="field"
                aria-label={t("altro.ea.c4.actorDoes")}
                placeholder={t("altro.ea.c4.actorDoesPlaceholder")}
                value={a.label ?? ""}
                disabled={!canWrite}
                onChange={(e) => setActors(actors.map((x, j) => (j === i ? { ...x, label: e.target.value } : x)))}
              />
              {canWrite && (
                <button className="self-start text-[12px] text-muted underline hover:text-danger" onClick={() => setActors(actors.filter((_, j) => j !== i))}>
                  {t("altro.ea.c4.remove")}
                </button>
              )}
            </div>
          ))}
          {canWrite && (
            <div className="flex flex-wrap gap-2">
              <button className="btn" onClick={() => setActors([...actors, { id: `n${Date.now().toString(36)}`, name: t("altro.ea.c4.newActor"), kind: "role", label: t("altro.ea.c4.newActorLabel") }])}>
                {t("altro.ea.c4.addActor")}
              </button>
              <button className="btn btn-primary" disabled={save.isPending || JSON.stringify(actors) === JSON.stringify(spec.actors)} onClick={() => commitSpec({ actors })}>
                {t("altro.ea.c4.apply")}
              </button>
            </div>
          )}
        </section>
        {canWrite && (
          <div className="mt-auto flex flex-col gap-1 border-t border-hair pt-3">
            <button className="btn btn-danger self-start" disabled={remove.isPending} onClick={() => confirm(t("altro.ea.c4.confirmDelete", { name: diagram.name })) && remove.mutate()}>
              {t("altro.ea.c4.delete")}
            </button>
            <ErrorNote error={remove.error} />
          </div>
        )}
      </aside>
    </div>
  );
}

function Arrow({ e, a, b, lane, hot, labels }: { e: C4Edge; a: Pos; b: Pos; lane: number; hot: boolean; labels: boolean }) {
  const dx = b.x - a.x;
  const dy = b.y - a.y;
  const len = Math.hypot(dx, dy) || 1;
  // Scostamento perpendicolare per le frecce parallele.
  const off = lane === 0 ? 0 : (lane % 2 ? 1 : -1) * Math.ceil(lane / 2) * 22;
  const nx = (-dy / len) * off;
  const ny = (dx / len) * off;
  const p1 = clip({ x: a.x + nx, y: a.y + ny }, dx, dy);
  const p2 = clip({ x: b.x + nx, y: b.y + ny }, -dx, -dy);
  const mx = (p1.x + p2.x) / 2;
  const my = (p1.y + p2.y) / 2;
  const tail = [e.protocol ? `[${short(e.protocol, 28)}]` : "", e.data ? short(e.data, 34) : ""].filter(Boolean);
  const text = short(e.label, 34);
  const w = Math.max(text.length, ...tail.map((x) => x.length)) * 6 + 12;
  const rows = 1 + tail.length;
  return (
    <g>
      <line x1={p1.x} y1={p1.y} x2={p2.x} y2={p2.y} className={hot ? "stroke-accent" : "stroke-muted"} strokeWidth={hot ? 2 : 1.4} markerEnd="url(#c4-arrow)" />
      {labels && (
        <g>
          <rect x={mx - w / 2} y={my - 9} width={w} height={rows * 13 + 4} rx={2} className="fill-bg" opacity={0.9} />
          <text x={mx} y={my + 2} textAnchor="middle" className="fill-fg text-[11px]">
            {text}
          </text>
          {tail.map((x, i) => (
            <text key={i} x={mx} y={my + 15 + i * 13} textAnchor="middle" className="fill-muted text-[10px]">
              {x}
            </text>
          ))}
        </g>
      )}
    </g>
  );
}

function Box({ n, p, selected, canWrite }: { n: C4Node; p: Pos; selected: boolean; canWrite: boolean }) {
  const st = KIND_STYLE[n.kind];
  const x = -W / 2;
  const y = -H / 2;
  const title = wrap(n.name, 26, 2);
  const type = `[${n.kind === "person" ? n.type_label : n.type_label}${n.tech ? `: ${short(n.tech, 26)}` : ""}]`;
  const desc = n.description ? wrap(n.description, 32, 2) : [];
  const sw = selected ? 3.5 : 1.6;
  return (
    <g data-id={n.id} data-pos={`${p.x},${p.y}`} transform={`translate(${p.x} ${p.y})`} className={canWrite ? "cursor-move" : "cursor-pointer"}>
      {n.kind === "database" ? (
        <g className={`${st.fill} ${selected ? "stroke-fg" : st.stroke}`} strokeWidth={sw}>
          <path d={`M${x},${y + 12} v${H - 24} a${W / 2},12 0 0 0 ${W},0 v${-(H - 24)}`} />
          <ellipse cx={0} cy={y + 12} rx={W / 2} ry={12} />
        </g>
      ) : (
        <rect x={x} y={y} width={W} height={H} rx={n.kind === "person" ? 14 : 4} className={`${st.fill} ${selected ? "stroke-fg" : st.stroke}`} strokeWidth={sw} />
      )}
      {n.kind === "person" && <circle cx={0} cy={y - 8} r={9} className="fill-fg stroke-bg" strokeWidth={2} />}
      {n.criticality === "critical" && <circle cx={x + W - 10} cy={y + 10} r={4} className="fill-danger" />}
      {title.map((l, i) => (
        <text key={i} x={0} y={y + 24 + i * 16 + (n.kind === "database" ? 14 : 0)} textAnchor="middle" className={`${st.text} text-[13px] font-bold`}>
          {l}
        </text>
      ))}
      <text x={0} y={y + 24 + title.length * 16 + (n.kind === "database" ? 14 : 0)} textAnchor="middle" className={`${st.sub} text-[10.5px]`}>
        {type}
      </text>
      {desc.map((l, i) => (
        <text key={i} x={0} y={y + 28 + title.length * 16 + 12 + i * 13 + (n.kind === "database" ? 14 : 0)} textAnchor="middle" className={`${st.sub} text-[10.5px]`}>
          {l}
        </text>
      ))}
    </g>
  );
}
