// Mappa dell'architettura (piano, passo 20; replica/architettura-ea.md §4): grafo
// interattivo di applicazioni, asset e capability con filtri, layout automatico, zoom e
// spostamento anche con il tocco, vicini fino a N livelli e analisi d'impatto esportabile.
import { Select } from "../../select";
import { memo, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useMe } from "../../shell";
import { CatalogId } from "../../catalogId";
import { CRITICALITY, ErrorNote, SectionTitle } from "../../ui";
import { TIME, canWriteEA, eaApi, type EdgeType, type Graph, type GraphEdge, type GraphNode } from "../../eaApi";
import { adjacency, forceLayout, layeredLayout, reach, short, type Pos } from "./graphModel";
import { COLOR_MODES, LEGENDS, NODE_R, edgeDash, exportPNG, exportSVG, nodeFill, shapeAt, shapePath, useViewport, type ColorMode } from "./canvas";
import { EaPage } from "./tabs";
import { useT } from "../../i18n/react";
import { loc } from "../../i18n/format";

type Direction = "dependents" | "dependencies" | "both";
type LayoutKind = "forze" | "strati";
const CRIT_RANK: Record<string, number> = { low: 1, medium: 2, high: 3, critical: 4 };

export function MapPage({ focus }: { focus?: string }) {
  const t = useT();
  const graph = useQuery({ queryKey: ["ea", "graph"], queryFn: eaApi.graph, staleTime: 30_000 });
  return (
    <EaPage active="mappa" wide>
      <ErrorNote error={graph.error} />
      {graph.isPending && <p className="px-4 py-6 text-muted md:px-7">{t("altro.ea.map.loading")}</p>}
      {graph.data && <GraphExplorer graph={graph.data} focus={focus} />}
    </EaPage>
  );
}

function GraphExplorer({ graph, focus }: { graph: Graph; focus?: string }) {
  const t = useT();
  const perms = useMe().data?.active_tenant?.permissions ?? [];
  const canWrite = canWriteEA(perms);
  const nav = useNavigate();
  const [types, setTypes] = useState<Set<string>>(() => new Set(graph.node_types.map((nt) => nt.key)));
  const [team, setTeam] = useState("");
  const [minCrit, setMinCrit] = useState(0);
  const [doraOnly, setDoraOnly] = useState(false);
  const [layoutKind, setLayoutKind] = useState<LayoutKind>("forze");
  const [colorMode, setColorMode] = useState<ColorMode>("criticita");
  const [selected, setSelected] = useState<string | null>(null);
  const [direction, setDirection] = useState<Direction>("both");
  const [depth, setDepth] = useState(2);
  const [search, setSearch] = useState("");
  const [relayout, setRelayout] = useState(0);
  const [showFilters, setShowFilters] = useState(false); // sotto i 768 px i filtri si aprono a richiesta
  // Nomi dei nodi: sempre visibili sopra una soglia di zoom (0,35); con «Etichette sempre» anche più lontano.
  const [alwaysLabels, setAlwaysLabels] = useState(false);

  const byID = useMemo(() => new Map(graph.nodes.map((n) => [n.id, n])), [graph.nodes]);
  const typeOf = useMemo(() => new Map(graph.edge_types.map((et) => [et.key, et])), [graph.edge_types]);
  const adj = useMemo(() => adjacency(graph.edges, graph.edge_types), [graph.edges, graph.edge_types]);

  // Filtri: i nodi nascosti spariscono con i loro archi.
  const visible = useMemo(() => {
    const s = new Set<string>();
    for (const n of graph.nodes) {
      if (!types.has(n.type)) continue;
      if (n.type === "application") {
        if (team && n.team_id !== team) continue;
        if (minCrit && (CRIT_RANK[n.criticality ?? ""] ?? 0) < minCrit) continue;
        if (doraOnly && !n.dora) continue;
      }
      s.add(n.id);
    }
    return s;
  }, [graph.nodes, types, team, minCrit, doraOnly]);
  const nodes = useMemo(() => graph.nodes.filter((n) => visible.has(n.id)), [graph.nodes, visible]);
  const edges = useMemo(() => graph.edges.filter((e) => visible.has(e.source) && visible.has(e.target)), [graph.edges, visible]);

  // Layout: si ricalcola sui nodi visibili, partendo dalle posizioni precedenti perché il
  // disegno resti riconoscibile quando cambiano i filtri.
  const last = useRef<Map<string, Pos>>(new Map());
  const positions = useMemo(() => {
    const ids = nodes.map((n) => n.id).sort();
    const p =
      layoutKind === "strati"
        ? layeredLayout(nodes, edges)
        : forceLayout(
            ids,
            edges.map((e) => [e.source, e.target] as [string, string]),
            { initial: relayout ? undefined : last.current, k: ids.length > 400 ? 85 : 125 },
          );
    last.current = p;
    return p;
    // relayout forza un layout da zero.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [nodes, edges, layoutKind, relayout]);

  // Evidenza: il nodo scelto e i vicini fino a depth livelli nel verso scelto.
  const highlight = useMemo(() => {
    if (!selected || !byID.has(selected)) return null;
    const d = direction === "dependencies" ? new Map<string, number>() : reach(adj.dependents, selected, depth, visible);
    const u = direction === "dependents" ? new Map<string, number>() : reach(adj.dependencies, selected, depth, visible);
    return { dependents: d, dependencies: u };
  }, [selected, direction, depth, adj, visible, byID]);

  // Tocco: il nodo visibile più vicino entro un raggio che non scende sotto il dito.
  const vp = useViewport({
    labelK: 0.35,
    onTap: (id) => setSelected(id),
    pick: (p, k) => {
      let best: string | null = null;
      let bd = Math.max(NODE_R * 1.8, 18 / k) ** 2;
      for (const n of nodes) {
        const q = positions.get(n.id);
        if (!q) continue;
        const d = (q.x - p.x) ** 2 + (q.y - p.y) ** 2;
        if (d < bd) {
          bd = d;
          best = n.id;
        }
      }
      return best;
    },
  });

  // Prima apertura e cambi di layout: vista adattata; con ?nodo= si parte dal nodo.
  const fitted = useRef("");
  useEffect(() => {
    const sig = `${layoutKind}:${relayout}`;
    if (fitted.current === sig || positions.size === 0) return;
    fitted.current = sig;
    vp.fit(positions.values());
  }, [positions, layoutKind, relayout, vp]);
  useEffect(() => {
    if (!focus) return;
    const f = focus.toLowerCase();
    const n = graph.nodes.find((x) => x.key.toLowerCase() === f || x.id === f || x.code?.toLowerCase() === f);
    if (n) {
      setSelected(n.id);
      const p = positions.get(n.id);
      if (p) vp.center(p);
    }
    // Solo all'arrivo con il parametro.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [focus]);

  const sel = selected ? byID.get(selected) : undefined;
  const findNode = (text: string) => {
    const q = text.trim().toLowerCase();
    if (!q) return;
    const n =
      graph.nodes.find((x) => x.name.toLowerCase() === q || x.key.toLowerCase() === q || x.code?.toLowerCase() === q) ??
      graph.nodes.find((x) => x.name.toLowerCase().includes(q));
    if (!n) return;
    if (!visible.has(n.id)) setTypes(new Set([...types, n.type]));
    setSelected(n.id);
    const p = positions.get(n.id);
    if (p) vp.center(p);
  };

  const saveDiagram = useMutation({
    mutationFn: () => {
      const ids = highlight ? [...new Set([...highlight.dependents.keys(), ...highlight.dependencies.keys()])] : nodes.map((n) => n.id);
      const name = sel ? t("altro.ea.map.around", { name: sel.name }) : t("altro.ea.map.mapView");
      return eaApi.createDiagram({
        name,
        kind: "dependency",
        layout: { nodes: ids.slice(0, 2000).map((id) => ({ id, x: positions.get(id)?.x ?? 0, y: positions.get(id)?.y ?? 0 })), notes: [] },
      });
    },
    onSuccess: (d) => nav({ to: "/architettura/diagrammi/$id", params: { id: d.id } }),
  });

  return (
    <div className="flex min-w-0 flex-col lg:flex-row">
      <div className="flex min-w-0 flex-1 flex-col">
        {/* Filtri */}
        <button className="btn mx-4 mt-3 self-start md:hidden" aria-expanded={showFilters} onClick={() => setShowFilters(!showFilters)}>
          {showFilters ? t("altro.ea.map.hideFilters") : t("altro.ea.map.filters")}
        </button>
        <div className={`${showFilters ? "flex" : "hidden"} flex-wrap items-end gap-x-4 gap-y-2 border-b border-hair px-4 py-3 md:flex md:px-7`}>
          <fieldset className="m-0 flex flex-wrap items-center gap-x-3 gap-y-1 border-0 p-0">
            <legend className="label mb-1">{t("altro.ea.map.types")}</legend>
            {graph.node_types.map((nt) => (
              <label key={nt.key} className="flex items-center gap-1.5 text-[12.5px]">
                <input
                  type="checkbox"
                  checked={types.has(nt.key)}
                  onChange={(e) => {
                    const s = new Set(types);
                    if (e.target.checked) s.add(nt.key);
                    else s.delete(nt.key);
                    setTypes(s);
                  }}
                />
                <svg width="14" height="14" viewBox="-14 -14 28 28" aria-hidden="true">
                  <path d={shapePath(nt.key, 9)} className="fill-none stroke-fg" strokeWidth={2.5} />
                </svg>
                {nt.name}
              </label>
            ))}
          </fieldset>
          <label className="flex w-40 flex-col gap-1">
            <span className="label">{t("altro.ea.map.team")}</span>
            <Select className="field py-1" value={team} onChange={(e) => setTeam(e.target.value)}>
              <option value="">{t("altro.ea.map.allTeams")}</option>
              {graph.teams.map((tm) => (
                <option key={tm.key} value={tm.key}>
                  {tm.name}
                </option>
              ))}
            </Select>
          </label>
          <label className="flex w-36 flex-col gap-1">
            <span className="label">{t("altro.ea.map.criticality")}</span>
            <Select className="field py-1" value={minCrit} onChange={(e) => setMinCrit(Number(e.target.value))}>
              <option value={0}>{t("altro.ea.map.critAll")}</option>
              <option value={2}>{t("altro.ea.map.critMedium")}</option>
              <option value={3}>{t("altro.ea.map.critHigh")}</option>
              <option value={4}>{t("altro.ea.map.critCritical")}</option>
            </Select>
          </label>
          <label className="flex items-center gap-1.5 pb-1.5 text-[12.5px]">
            <input type="checkbox" checked={doraOnly} onChange={(e) => setDoraOnly(e.target.checked)} />
            {t("altro.ea.map.doraOnly")}
          </label>
          <form
            className="flex min-w-0 flex-[1_1_200px] flex-col gap-1"
            onSubmit={(e) => {
              e.preventDefault();
              findNode(search);
            }}
          >
            <label className="label" htmlFor="ea-cerca">
              {t("altro.ea.map.find")}
            </label>
            <input id="ea-cerca" className="field py-1" list="ea-nodi" value={search} onChange={(e) => setSearch(e.target.value)} placeholder={t("altro.ea.map.findPlaceholder")} />
            <datalist id="ea-nodi">
              {graph.nodes.slice(0, 3000).map((n) => (
                <option key={n.id} value={n.name}>
                  {n.key}
                </option>
              ))}
            </datalist>
          </form>
        </div>
        {/* Comandi della tela */}
        <div className="flex flex-wrap items-center gap-2 px-4 py-2 md:px-7">
          <label className="flex items-center gap-1.5 text-[12.5px]">
            <span className="text-muted">{t("altro.ea.map.layout")}</span>
            <Select className="field w-auto py-0.5" value={layoutKind} onChange={(e) => setLayoutKind(e.target.value as LayoutKind)}>
              <option value="forze">{t("altro.ea.map.layoutForce")}</option>
              <option value="strati">{t("altro.ea.map.layoutLayers")}</option>
            </Select>
          </label>
          <label className="flex items-center gap-1.5 text-[12.5px]">
            <span className="text-muted">{t("altro.ea.map.color")}</span>
            <Select className="field w-auto py-0.5" value={colorMode} onChange={(e) => setColorMode(e.target.value as ColorMode)}>
              {Object.entries(COLOR_MODES).map(([k, v]) => (
                <option key={k} value={k}>
                  {v}
                </option>
              ))}
            </Select>
          </label>
          <label className="flex items-center gap-1.5 text-[12.5px]">
            <input type="checkbox" checked={alwaysLabels} onChange={(e) => setAlwaysLabels(e.target.checked)} />
            {t("altro.ea.map.alwaysLabels")}
          </label>
          <span className="ml-auto flex flex-wrap gap-1.5">
            <button className="btn px-2.5 py-1" aria-label={t("altro.ea.map.zoomIn")} onClick={() => vp.zoomAt(1.3)}>
              +
            </button>
            <button className="btn px-2.5 py-1" aria-label={t("altro.ea.map.zoomOut")} onClick={() => vp.zoomAt(1 / 1.3)}>
              −
            </button>
            <button className="btn px-2.5 py-1" onClick={() => vp.fit(positions.values())}>
              {t("altro.ea.map.fit")}
            </button>
            <button className="btn px-2.5 py-1" onClick={() => setRelayout((n) => n + 1)} title={t("altro.ea.map.relayoutTitle")}>
              {t("altro.ea.map.relayout")}
            </button>
            <button className="btn px-2.5 py-1" onClick={() => vp.svgRef.current && vp.gRef.current && exportSVG(vp.svgRef.current, vp.gRef.current, "mappa-architettura")}>
              SVG
            </button>
            <button className="btn px-2.5 py-1" onClick={() => vp.svgRef.current && vp.gRef.current && exportPNG(vp.svgRef.current, vp.gRef.current, "mappa-architettura")}>
              PNG
            </button>
          </span>
        </div>
        <div className="relative h-[62vh] min-h-[360px] border-y border-hair lg:h-[calc(100vh-300px)]">
          <svg
            ref={vp.svgRef}
            className="block h-full w-full touch-none select-none bg-bg font-sans [&.moving_text]:hidden"
            role="img"
            aria-label={t("altro.ea.map.graphLabel", { nodes: nodes.length, edges: edges.length })}
            {...vp.bind}
          >
            <g ref={vp.gRef}>
              <EdgeLayer edges={edges} positions={positions} typeOf={typeOf} highlight={highlight} />
              <NodeLayer nodes={nodes} positions={positions} colorMode={colorMode} highlight={highlight} selected={selected} labels={alwaysLabels || vp.labels} />
            </g>
          </svg>
          <Legend colorMode={colorMode} />
          <p className="pointer-events-none absolute top-2 right-3 m-0 font-mono text-[11px] text-muted">
            {t("altro.ea.map.counts", { nodes: nodes.length, edges: edges.length })}
          </p>
        </div>
        <p className="m-0 px-4 py-2 text-[12px] text-muted md:px-7">
          {t("altro.ea.map.help")}
        </p>
      </div>
      <aside className="flex w-full shrink-0 flex-col gap-4 border-hair px-4 py-4 md:px-7 lg:w-[380px] lg:border-l lg:px-5" aria-label={t("altro.ea.map.selectedLabel")}>
        {!sel && (
          <>
            <SectionTitle>{t("altro.ea.map.impactAnalysis")}</SectionTitle>
            <p className="m-0 text-muted">{t("altro.ea.map.impactIntro")}</p>
          </>
        )}
        {sel && (
          <SelectionPanel
            node={sel}
            graph={graph}
            adj={adj}
            direction={direction}
            setDirection={setDirection}
            depth={depth}
            setDepth={setDepth}
            onPick={(id) => {
              setSelected(id);
              const p = positions.get(id);
              if (p) vp.center(p);
            }}
            onClose={() => setSelected(null)}
          />
        )}
        {canWrite && (
          <div className="flex flex-col gap-1">
            <button className="btn self-start" disabled={saveDiagram.isPending || nodes.length === 0} onClick={() => saveDiagram.mutate()}>
              {sel ? t("altro.ea.map.saveAround") : t("altro.ea.map.saveView")}
            </button>
            <ErrorNote error={saveDiagram.error} />
          </div>
        )}
      </aside>
    </div>
  );
}

type Highlight = { dependents: Map<string, number>; dependencies: Map<string, number> } | null;

// Per restare fluidi con centinaia di nodi, forme e archi con lo stesso stile stanno in
// un solo <path>: pochi elementi da disegnare a ogni zoom. Il tocco trova il nodo più
// vicino (pick), le etichette sono testi separati solo quando servono.
const NodeLayer = memo(function NodeLayer({
  nodes,
  positions,
  colorMode,
  highlight,
  selected,
  labels,
}: {
  nodes: GraphNode[];
  positions: Map<string, Pos>;
  colorMode: ColorMode;
  highlight: Highlight;
  selected: string | null;
  labels: boolean;
}) {
  const groups = new Map<string, string[]>();
  const add = (k: string, d: string) => {
    const l = groups.get(k);
    if (l) l.push(d);
    else groups.set(k, [d]);
  };
  const texts: { id: string; p: Pos; name: string; sel: boolean; dim: boolean }[] = [];
  for (const n of nodes) {
    const p = positions.get(n.id);
    if (!p) continue;
    const on = !highlight || highlight.dependents.has(n.id) || highlight.dependencies.has(n.id);
    const dim = on ? "" : " opacity-15";
    add(`${nodeFill(n, colorMode)} stroke-bg${dim}`, shapeAt(n.type, p.x, p.y));
    if (n.dora) add(`fill-none stroke-flag${dim}`, shapeAt(n.type, p.x, p.y, NODE_R + 4));
    if (labels || n.id === selected || (highlight !== null && on)) texts.push({ id: n.id, p, name: n.code ? `${n.code} · ${n.name}` : n.name, sel: n.id === selected, dim: !on });
  }
  const sel = selected ? nodes.find((n) => n.id === selected) : undefined;
  const sp = sel ? positions.get(sel.id) : undefined;
  return (
    <g>
      {[...groups.entries()].map(([cls, ds]) => (
        <path key={cls} d={ds.join("")} className={cls} strokeWidth={cls.includes("stroke-flag") ? 2 : 1.5} />
      ))}
      {sel && sp && <path d={shapeAt(sel.type, sp.x, sp.y)} className={`${nodeFill(sel, colorMode)} stroke-accent`} strokeWidth={4} />}
      {texts.map((t) => (
        <text key={t.id} x={t.p.x} y={t.p.y + NODE_R + 14} textAnchor="middle" className={`pointer-events-none fill-fg text-[11px] ${t.sel ? "font-bold" : ""} ${t.dim ? "opacity-20" : ""}`}>
          {short(t.name, 32)}
        </text>
      ))}
    </g>
  );
});

const EdgeLayer = memo(function EdgeLayer({
  edges,
  positions,
  typeOf,
  highlight,
}: {
  edges: GraphEdge[];
  positions: Map<string, Pos>;
  typeOf: Map<string, EdgeType>;
  highlight: Highlight;
}) {
  const lines = new Map<string, { cls: string; dash?: string; hot: boolean; d: string[] }>();
  const heads = new Map<string, string[]>();
  for (const e of edges) {
    const a = positions.get(e.source);
    const b = positions.get(e.target);
    if (!a || !b) continue;
    const dash = edgeDash(typeOf.get(e.type)?.category);
    let cls = "stroke-muted opacity-45";
    let hot = false;
    if (highlight) {
      const inDep = highlight.dependents.has(e.source) && highlight.dependents.has(e.target);
      const inUp = highlight.dependencies.has(e.source) && highlight.dependencies.has(e.target);
      hot = inDep || inUp;
      cls = inDep ? "stroke-accent" : inUp ? "stroke-fg" : "stroke-muted opacity-15";
    }
    const dx = b.x - a.x;
    const dy = b.y - a.y;
    const len = Math.hypot(dx, dy) || 1;
    // Punta della freccia a filo del nodo di destinazione.
    const ex = b.x - (dx / len) * (NODE_R + 4);
    const ey = b.y - (dy / len) * (NODE_R + 4);
    const k = `${cls}|${dash ?? ""}`;
    const g = lines.get(k) ?? lines.set(k, { cls, dash, hot, d: [] }).get(k)!;
    g.d.push(`M${a.x},${a.y}L${ex.toFixed(1)},${ey.toFixed(1)}`);
    if (hot) {
      const ux = dx / len;
      const uy = dy / len;
      const h = heads.get(cls) ?? heads.set(cls, []).get(cls)!;
      h.push(`M${ex.toFixed(1)},${ey.toFixed(1)}L${(ex - ux * 9 - uy * 4.5).toFixed(1)},${(ey - uy * 9 + ux * 4.5).toFixed(1)}L${(ex - ux * 9 + uy * 4.5).toFixed(1)},${(ey - uy * 9 - ux * 4.5).toFixed(1)}z`);
    }
  }
  return (
    <g className="pointer-events-none">
      {[...lines.entries()].map(([k, g]) => (
        <path key={k} d={g.d.join("")} className={`fill-none ${g.cls}`} strokeWidth={g.hot ? 2 : 1.2} strokeDasharray={g.dash} />
      ))}
      {[...heads.entries()].map(([cls, d]) => (
        <path key={cls} d={d.join("")} className={cls.replace("stroke-", "fill-")} />
      ))}
    </g>
  );
});

function Legend({ colorMode }: { colorMode: ColorMode }) {
  return (
    <div className="pointer-events-none absolute bottom-2 left-3 flex flex-wrap gap-x-3 gap-y-1 bg-bg/80 px-2 py-1 text-[11px]">
      {LEGENDS[colorMode].map(([cls, label]) => (
        <span key={label} className="flex items-center gap-1">
          <svg width="10" height="10" aria-hidden="true">
            <rect width="10" height="10" className={cls} />
          </svg>
          {label}
        </span>
      ))}
      <span className="flex items-center gap-1">
        <svg width="12" height="12" aria-hidden="true">
          <rect x="1" y="1" width="10" height="10" className="fill-none stroke-flag" strokeWidth="2" />
        </svg>
        {/* i18n-ok: sigla */}
        DORA
      </span>
    </div>
  );
}

function SelectionPanel({
  node,
  graph,
  adj,
  direction,
  setDirection,
  depth,
  setDepth,
  onPick,
  onClose,
}: {
  node: GraphNode;
  graph: Graph;
  adj: ReturnType<typeof adjacency>;
  direction: Direction;
  setDirection: (d: Direction) => void;
  depth: number;
  setDepth: (n: number) => void;
  onPick: (id: string) => void;
  onClose: () => void;
}) {
  const t = useT();
  const byID = useMemo(() => new Map(graph.nodes.map((n) => [n.id, n])), [graph.nodes]);
  // L'elenco è sul grafo intero (non solo sui nodi filtrati), come l'export.
  const dependents = useMemo(() => reach(adj.dependents, node.id, depth), [adj, node.id, depth]);
  const dependencies = useMemo(() => reach(adj.dependencies, node.id, depth), [adj, node.id, depth]);
  const facts: [string, ReactNode][] = [
    [t("altro.ea.map.factType"), node.type_name],
    [t("altro.ea.map.factPhase"), node.state || "—"],
  ];
  if (node.type === "application") {
    facts.push([t("altro.ea.map.team"), node.team ?? "—"]);
    facts.push([t("altro.ea.map.criticality"), `${node.criticality ? CRITICALITY[node.criticality] : "—"}${node.dora ? " · DORA" : ""}`]);
    facts.push(["TIME", node.time ? TIME[node.time] : t("altro.ea.map.notRated")]); // i18n-ok: sigla
    if (node.end_of_support) facts.push([t("altro.ea.map.factEndOfSupport"), node.end_of_support]);
  }
  return (
    <>
      <div className="flex items-start gap-2">
        <div className="min-w-0 flex-1">
          <Link to="/e/$key" params={{ key: node.key }} className="text-[12px] text-accent no-underline hover:underline">
            <CatalogId code={node.code} itemKey={node.key} />
          </Link>
          <h2 className="m-0 text-[20px] leading-tight font-bold">{node.name}</h2>
        </div>
        <button className="btn px-2 py-0.5" aria-label={t("altro.ea.map.closeSelection")} onClick={onClose}>
          ×
        </button>
      </div>
      <dl className="m-0 grid grid-cols-[110px_minmax(0,1fr)] text-[12.5px]">
        {facts.map(([k, v]) => (
          <div key={k} className="contents">
            <dt className="border-b border-hair py-1 text-muted">{k}</dt>
            <dd className="m-0 border-b border-hair py-1">{v}</dd>
          </div>
        ))}
      </dl>
      <fieldset className="m-0 flex flex-col gap-1.5 border-0 p-0">
        <legend className="label mb-1">{t("altro.ea.map.highlight")}</legend>
        {(
          [
            ["dependents", t("altro.ea.map.dirDependents", { name: short(node.name, 30) })],
            ["dependencies", t("altro.ea.map.dirDependencies", { name: short(node.name, 30) })],
            ["both", t("altro.ea.map.dirBoth")],
          ] as const
        ).map(([v, label]) => (
          <label key={v} className="flex items-center gap-2 text-[12.5px]">
            <input type="radio" name="ea-verso" checked={direction === v} onChange={() => setDirection(v)} />
            {label}
          </label>
        ))}
        <label className="mt-1 flex items-center gap-2 text-[12.5px]">
          <span className="text-muted">{t("altro.ea.map.levels")}</span>
          <Select className="field w-auto py-0.5" value={depth} onChange={(e) => setDepth(Number(e.target.value))}>
            {[1, 2, 3, 4, 5].map((n) => (
              <option key={n} value={n}>
                {n}
              </option>
            ))}
            <option value={0}>{t("altro.ea.map.allLevels")}</option>
          </Select>
        </label>
      </fieldset>
      <ImpactList
        title={t("altro.ea.map.listDependentsTitle")}
        hint={t("altro.ea.map.listDependentsHint")}
        levels={dependents}
        byID={byID}
        onPick={onPick}
        csv={eaApi.impactCsvUrl(node.key || node.id, "dependents", depth)}
      />
      <ImpactList
        title={t("altro.ea.map.listDependenciesTitle")}
        hint={t("altro.ea.map.listDependenciesHint")}
        levels={dependencies}
        byID={byID}
        onPick={onPick}
        csv={eaApi.impactCsvUrl(node.key || node.id, "dependencies", depth)}
      />
    </>
  );
}

function ImpactList({
  title,
  hint,
  levels,
  byID,
  onPick,
  csv,
}: {
  title: string;
  hint: string;
  levels: Map<string, number>;
  byID: Map<string, GraphNode>;
  onPick: (id: string) => void;
  csv: string;
}) {
  const t = useT();
  const rows = [...levels.entries()].filter(([, l]) => l > 0).sort((a, b) => a[1] - b[1] || (byID.get(a[0])?.name ?? "").localeCompare(byID.get(b[0])?.name ?? "", loc()));
  const apps = rows.filter(([id]) => byID.get(id)?.type === "application").length;
  return (
    <section className="flex flex-col gap-1.5">
      <SectionTitle
        aside={
          rows.length > 0 && (
            <a className="text-[12px] text-accent underline" href={csv} download>
              {t("altro.ea.map.downloadCsv")}
            </a>
          )
        }
      >
        {title}
      </SectionTitle>
      <p className="m-0 text-[12px] text-muted">
        {hint} {rows.length === 0 ? t("altro.ea.map.noneMasc") : t("altro.ea.map.itemsSummary", { count: rows.length, apps })}
      </p>
      {rows.length > 0 && (
        <ol className="m-0 flex max-h-72 list-none flex-col overflow-y-auto p-0">
          {rows.slice(0, 300).map(([id, l]) => {
            const n = byID.get(id)!;
            return (
              <li key={id} className="flex items-baseline gap-2 border-b border-hair py-1 text-[12.5px]">
                <span className="w-5 shrink-0 text-right font-mono text-[11px] text-muted" title={t("altro.ea.map.level")}>
                  {l}
                </span>
                <button className="min-w-0 flex-1 break-words text-left hover:text-accent" onClick={() => onPick(id)}>
                  {n.name}
                </button>
                <span className="shrink-0 text-[11px] text-muted">{n.type_name}</span>
                {n.dora && <span className="shrink-0 border border-flag px-1 text-[10px] font-semibold text-flag">DORA</span>}
              </li>
            );
          })}
        </ol>
      )}
    </section>
  );
}
