// Diagrammi (replica/architettura-ea.md §6): viste salvate del grafo con le posizioni
// fissate dall'utente e le annotazioni. Nomi, dati e collegamenti arrivano sempre dal
// grafo: se un'applicazione cambia nome, il diagramma lo mostra. Export SVG e PNG.
import { Select } from "../../select";
import { useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError } from "../../api";
import { useMe } from "../../shell";
import { CatalogId } from "../../catalogId";
import { ErrorNote, SectionTitle, ago } from "../../ui";
import { canWriteEA, eaApi, type DiagramLayout, type DiagramNote, type DiagramView, type EdgeType, type GraphNode } from "../../eaApi";
import { forceLayout, short, type Pos } from "./graphModel";
import { COLOR_MODES, LEGENDS, NODE_R, edgeDash, exportPNG, exportSVG, fileName, nodeFill, shapePath, useViewport, type ColorMode } from "./canvas";
import { EaPage } from "./tabs";
import { C4Editor } from "./c4";
import { useT } from "../../i18n/react";
import { tr } from "../../i18n/core";
import { Trans } from "../../i18n/Trans";

// Etichette lette al momento dell'uso, nella lingua corrente.
const KINDS = {
  get dependency() {
    return tr("altro.ea.diag.kindDependency");
  },
  get capability_map() {
    return tr("altro.ea.diag.kindCapabilityMap");
  },
  get c4_context() {
    return tr("altro.ea.diag.kindC4Context");
  },
  get c4_container() {
    return tr("altro.ea.diag.kindC4Container");
  },
  get c4_component() {
    return tr("altro.ea.diag.kindC4Component");
  },
  archimate: "ArchiMate", // i18n-ok: nome proprio
  get free() {
    return tr("altro.ea.diag.kindFree");
  },
} as Record<string, string>;

export function DiagramsPage() {
  const t = useT();
  const perms = useMe().data?.active_tenant?.permissions ?? [];
  const canWrite = canWriteEA(perms);
  const nav = useNavigate();
  const list = useQuery({ queryKey: ["ea", "diagrams"], queryFn: eaApi.diagrams });
  const [name, setName] = useState("");
  const [kind, setKind] = useState("dependency");
  const create = useMutation({
    mutationFn: () => eaApi.createDiagram({ name, kind, layout: { nodes: [], notes: [] } }),
    onSuccess: (d) => nav({ to: "/architettura/diagrammi/$id", params: { id: d.id } }),
  });
  return (
    <EaPage active="diagrammi" title={t("altro.ea.diag.title")}>
      <p className="m-0 max-w-3xl text-muted">{t("altro.ea.diag.intro")}</p>
      {canWrite && (
        <form
          className="flex flex-wrap items-end gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            create.mutate();
          }}
        >
          <label className="flex min-w-0 flex-[1_1_280px] flex-col gap-1">
            <span className="label">{t("altro.ea.diag.new")}</span>
            <input className="field" required value={name} onChange={(e) => setName(e.target.value)} placeholder={t("altro.ea.diag.newPlaceholder")} />
          </label>
          <label className="flex w-52 flex-col gap-1">
            <span className="label">{t("altro.ea.diag.kind")}</span>
            <Select className="field" value={kind} onChange={(e) => setKind(e.target.value)}>
              {Object.entries(KINDS).map(([k, v]) => (
                <option key={k} value={k}>
                  {v}
                </option>
              ))}
            </Select>
          </label>
          <button className="btn btn-primary" disabled={create.isPending}>
            {t("altro.ea.diag.create")}
          </button>
          <div className="basis-full">
            <ErrorNote error={create.error} />
          </div>
        </form>
      )}
      <ErrorNote error={list.error} />
      <table className="w-full max-w-4xl border-collapse">
        <thead>
          <tr className="label h-9 text-left">
            <th scope="col" className="border-b-2 border-rule pr-3">{t("altro.ea.diag.colDiagram")}</th>
            <th scope="col" className="border-b-2 border-rule px-3">{t("altro.ea.diag.kind")}</th>
            <th scope="col" className="border-b-2 border-rule px-3 text-right">{t("altro.ea.diag.colItems")}</th>
            <th scope="col" className="border-b-2 border-rule pl-3">{t("altro.ea.diag.colUpdated")}</th>
          </tr>
        </thead>
        <tbody>
          {list.data?.items.map((d) => (
            <tr key={d.id} className="border-b border-hair hover:bg-sel/50">
              <td className="py-2 pr-3">
                <Link to="/architettura/diagrammi/$id" params={{ id: d.id }} className="font-medium text-fg no-underline hover:underline">
                  {d.name}
                </Link>
                {d.description && <span className="block text-[12px] text-muted">{short(d.description, 120)}</span>}
              </td>
              <td className="px-3 py-2 text-[12.5px]">{KINDS[d.kind] ?? d.kind}</td>
              <td className="px-3 py-2 text-right font-mono text-[12px]">{d.layout.c4 ? t("altro.ea.diag.fromData") : d.node_count}</td>
              <td className="py-2 pl-3 text-[12.5px] text-muted">
                {ago(d.updated_at)}
                {d.updated_by && ` · ${d.updated_by}`}
              </td>
            </tr>
          ))}
          {list.data?.items.length === 0 && (
            <tr>
              <td colSpan={4} className="py-8 text-muted">
                {t("altro.ea.diag.empty")}
              </td>
            </tr>
          )}
        </tbody>
      </table>
    </EaPage>
  );
}

// ---------------------------------------------------------------------------
// Editor

type Draft = { layout: DiagramLayout; name: string; description: string; kind: string };

export function DiagramPage({ id }: { id: string }) {
  const t = useT();
  const d = useQuery({ queryKey: ["ea", "diagram", id], queryFn: () => eaApi.diagram(id) });
  // gen riparte l'editor dai dati del server (dopo un conflitto di versione).
  const [gen, setGen] = useState(0);
  return (
    <EaPage active="diagrammi" title={d.data?.name ?? t("altro.ea.diag.single")} wide>
      <ErrorNote error={d.error} />
      {d.data?.c4 && (
        <C4Editor
          key={`${d.data.id}:${gen}`}
          diagram={d.data}
          onReload={async () => {
            await d.refetch();
            setGen((g) => g + 1);
          }}
        />
      )}
      {d.data && !d.data.c4 && (
        <DiagramEditor
          key={`${d.data.id}:${gen}`}
          diagram={d.data}
          onReload={async () => {
            await d.refetch();
            setGen((g) => g + 1);
          }}
        />
      )}
    </EaPage>
  );
}

function DiagramEditor({ diagram, onReload }: { diagram: DiagramView; onReload: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const nav = useNavigate();
  const perms = useMe().data?.active_tenant?.permissions ?? [];
  const canWrite = canWriteEA(perms);
  const graph = useQuery({ queryKey: ["ea", "graph"], queryFn: eaApi.graph, staleTime: 30_000, enabled: canWrite });
  const [draft, setDraft] = useState<Draft>({ layout: diagram.layout, name: diagram.name, description: diagram.description, kind: diagram.kind });
  const [dirty, setDirty] = useState(false);
  const [selected, setSelected] = useState<string | null>(null);
  const [colorMode, setColorMode] = useState<ColorMode>("criticita");
  const [adding, setAdding] = useState("");
  const [version, setVersion] = useState(diagram.version);

  // Dati dei nodi: quelli del diagramma, più quelli del grafo per i nodi appena aggiunti.
  const nodeData = useMemo(() => {
    const m = new Map<string, GraphNode>(diagram.nodes.map((n) => [n.id, n]));
    for (const n of graph.data?.nodes ?? []) if (!m.has(n.id)) m.set(n.id, n);
    return m;
  }, [diagram.nodes, graph.data]);
  const edgeTypes = useMemo(() => new Map<string, EdgeType>((graph.data?.edge_types ?? diagram.edge_types).map((et) => [et.key, et])), [graph.data, diagram.edge_types]);
  const pos = useMemo(() => new Map(draft.layout.nodes.map((n) => [n.id, { x: n.x, y: n.y }])), [draft.layout.nodes]);
  const shownTypes = draft.layout.edge_types ?? [];
  const edges = useMemo(() => {
    const src = graph.data?.edges ?? diagram.edges;
    return src.filter((e) => pos.has(e.source) && pos.has(e.target) && (shownTypes.length === 0 || shownTypes.includes(e.type)));
  }, [graph.data, diagram.edges, pos, shownTypes]);

  const update = (fn: (l: DiagramLayout) => DiagramLayout) => {
    setDraft((d) => ({ ...d, layout: fn(d.layout) }));
    setDirty(true);
  };
  const moving = useRef<{ id: string; p: Pos } | null>(null);
  const [, force] = useState(0);
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
      if (id.startsWith("nota:")) {
        const nid = id.slice(5);
        update((l) => ({ ...l, notes: l.notes.map((n) => (n.id === nid ? { ...n, ...q } : n)) }));
      } else update((l) => ({ ...l, nodes: l.nodes.map((n) => (n.id === id ? { ...n, ...q } : n)) }));
    },
  });
  const posOf = (id: string): Pos | undefined => (moving.current?.id === id ? moving.current.p : pos.get(id));

  const fitted = useRef(false);
  useEffect(() => {
    if (fitted.current) return;
    fitted.current = true;
    vp.fit(extent(pos, draft.layout.notes), 1.2);
  }, [pos, draft.layout.notes, vp]);

  const save = useMutation({
    mutationFn: () => eaApi.updateDiagram(diagram.id, { ...draft, version }),
    onSuccess: (d) => {
      setDirty(false);
      setVersion(d.version);
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

  const center = (): Pos => {
    const r = vp.svgRef.current?.getBoundingClientRect();
    return r ? vp.toWorld(r.left + r.width / 2, r.top + r.height / 2) : { x: 0, y: 0 };
  };
  const addNode = (id: string) => {
    if (pos.has(id)) return;
    const c = center();
    update((l) => ({ ...l, nodes: [...l.nodes, { id, x: Math.round(c.x + (l.nodes.length % 5) * 30), y: Math.round(c.y) }] }));
  };
  const addNeighbors = (id: string) => {
    const p = pos.get(id) ?? center();
    const near = (graph.data?.edges ?? [])
      .filter((e) => e.source === id || e.target === id)
      .map((e) => (e.source === id ? e.target : e.source))
      .filter((x, i, a) => !pos.has(x) && a.indexOf(x) === i);
    update((l) => ({
      ...l,
      nodes: [
        ...l.nodes,
        ...near.map((x, i) => {
          const a = (2 * Math.PI * i) / Math.max(near.length, 1);
          return { id: x, x: Math.round(p.x + 160 * Math.cos(a)), y: Math.round(p.y + 160 * Math.sin(a)) };
        }),
      ],
    }));
  };
  const autoLayout = () => {
    const ids = draft.layout.nodes.map((n) => n.id).sort();
    const p = forceLayout(
      ids,
      edges.map((e) => [e.source, e.target] as [string, string]),
      { k: 110 },
    );
    update((l) => ({ ...l, nodes: l.nodes.map((n) => ({ ...n, ...(p.get(n.id) ?? {}) })) }));
    requestAnimationFrame(() => vp.fit(p.values(), 1.2));
  };
  const selNode = selected && !selected.startsWith("nota:") ? nodeData.get(selected) : undefined;
  const selNote = selected?.startsWith("nota:") ? draft.layout.notes.find((n) => n.id === selected.slice(5)) : undefined;
  const missing = draft.layout.nodes.filter((n) => !nodeData.has(n.id)).length;

  return (
    <div className="flex min-w-0 flex-col lg:flex-row">
      <div className="flex min-w-0 flex-1 flex-col">
        <div className="flex flex-wrap items-center gap-2 px-4 py-2 md:px-7">
          <Link to="/architettura/diagrammi" className="text-[12.5px] text-accent underline">
            {t("altro.ea.diag.all")}
          </Link>
          <label className="flex items-center gap-1.5 text-[12.5px]">
            <span className="text-muted">{t("altro.ea.diag.color")}</span>
            <Select className="field w-auto py-0.5" value={colorMode} onChange={(e) => setColorMode(e.target.value as ColorMode)}>
              {Object.entries(COLOR_MODES).map(([k, v]) => (
                <option key={k} value={k}>
                  {v}
                </option>
              ))}
            </Select>
          </label>
          <span className="ml-auto flex flex-wrap gap-1.5">
            <button className="btn px-2.5 py-1" aria-label={t("altro.ea.diag.zoomIn")} onClick={() => vp.zoomAt(1.3)}>
              +
            </button>
            <button className="btn px-2.5 py-1" aria-label={t("altro.ea.diag.zoomOut")} onClick={() => vp.zoomAt(1 / 1.3)}>
              −
            </button>
            <button className="btn px-2.5 py-1" onClick={() => vp.fit(extent(pos, draft.layout.notes), 1.2)}>
              {t("altro.ea.diag.fit")}
            </button>
            <button className="btn px-2.5 py-1" onClick={() => vp.svgRef.current && vp.gRef.current && exportSVG(vp.svgRef.current, vp.gRef.current, fileName(draft.name))}>
              SVG
            </button>
            <button className="btn px-2.5 py-1" onClick={() => vp.svgRef.current && vp.gRef.current && exportPNG(vp.svgRef.current, vp.gRef.current, fileName(draft.name))}>
              PNG
            </button>
            {canWrite && (
              <button className="btn btn-primary px-3 py-1" disabled={!dirty || save.isPending} onClick={() => save.mutate()}>
                {dirty ? t("altro.ea.diag.save") : t("altro.ea.diag.saved")}
              </button>
            )}
          </span>
        </div>
        {conflict && (
          <p className="m-0 px-4 pb-2 text-[12.5px] text-danger md:px-7">
            <Trans
              k="altro.ea.diag.conflict"
              slots={{
                reload: (
                  <button className="underline" onClick={onReload}>
                    {t("altro.ea.diag.reload")}
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
            aria-label={t("altro.ea.diag.ariaDiagram", { name: draft.name, count: draft.layout.nodes.length })}
            {...vp.bind}
          >
            <g ref={vp.gRef}>
              {edges.map((e) => {
                const a = posOf(e.source);
                const b = posOf(e.target);
                if (!a || !b) return null;
                const et = edgeTypes.get(e.type);
                const dx = b.x - a.x;
                const dy = b.y - a.y;
                const len = Math.hypot(dx, dy) || 1;
                const ex = b.x - (dx / len) * (NODE_R + 6);
                const ey = b.y - (dy / len) * (NODE_R + 6);
                const hot = selected === e.source || selected === e.target;
                const label = e.type === "_parent" ? t("altro.ea.diag.partOf") : (et?.outward_name ?? e.type);
                return (
                  <g key={e.id}>
                    <line x1={a.x} y1={a.y} x2={ex} y2={ey} className={hot ? "stroke-accent" : "stroke-muted"} strokeWidth={hot ? 2 : 1.2} strokeDasharray={edgeDash(et?.category)} />
                    <polygon
                      points={`${ex},${ey} ${ex - (dx / len) * 9 - (dy / len) * 4.5},${ey - (dy / len) * 9 + (dx / len) * 4.5} ${ex - (dx / len) * 9 + (dy / len) * 4.5},${ey - (dy / len) * 9 - (dx / len) * 4.5}`}
                      className={hot ? "fill-accent" : "fill-muted"}
                    />
                    {vp.labels && len > 120 && (
                      <text x={(a.x + ex) / 2} y={(a.y + ey) / 2 - 4} textAnchor="middle" className="fill-muted text-[10px]">
                        {short(label, 24)}
                      </text>
                    )}
                  </g>
                );
              })}
              {draft.layout.nodes.map((dn) => {
                const n = nodeData.get(dn.id);
                const p = posOf(dn.id)!;
                const isSel = selected === dn.id;
                return (
                  <g key={dn.id} data-id={dn.id} data-pos={`${p.x},${p.y}`} transform={`translate(${p.x} ${p.y})`} className={canWrite ? "cursor-move" : "cursor-pointer"}>
                    <circle r={NODE_R * 2} className="fill-transparent" />
                    {n?.dora && <path d={shapePath(n.type, NODE_R + 4)} className="fill-none stroke-flag" strokeWidth={2} />}
                    <path
                      d={shapePath(n?.type ?? "other")}
                      className={`${n ? nodeFill(n, colorMode) : "fill-hair"} ${isSel ? "stroke-accent" : "stroke-bg"}`}
                      strokeWidth={isSel ? 4 : 1.5}
                    />
                    <text y={NODE_R + 15} textAnchor="middle" className={`text-[12px] ${n ? "fill-fg" : "fill-danger"} ${isSel ? "font-bold" : "font-medium"}`}>
                      {n ? short(n.name, 30) : t("altro.ea.diag.gone")}
                    </text>
                    {n && (
                      <text y={NODE_R + 28} textAnchor="middle" className="fill-muted text-[10px]">
                        {n.code ?? n.key}
                        {n.team ? ` · ${n.team}` : ""}
                      </text>
                    )}
                  </g>
                );
              })}
              {draft.layout.notes.map((nt) => (
                <NoteShape key={nt.id} note={nt} pos={posOf(`nota:${nt.id}`) ?? nt} selected={selected === `nota:${nt.id}`} canWrite={canWrite} />
              ))}
            </g>
          </svg>
          <div className="pointer-events-none absolute bottom-2 left-3 flex flex-wrap gap-x-3 gap-y-1 bg-bg/80 px-2 py-1 text-[11px]">
            {LEGENDS[colorMode].map(([cls, label]) => (
              <span key={label} className="flex items-center gap-1">
                <svg width="10" height="10" aria-hidden="true">
                  <rect width="10" height="10" className={cls} />
                </svg>
                {label}
              </span>
            ))}
          </div>
        </div>
        {draft.layout.nodes.length === 0 && (
          <p className="m-0 px-4 py-2 text-muted md:px-7">{canWrite ? t("altro.ea.diag.emptyDiagram") : t("altro.ea.diag.emptyDiagramNoPerm")}</p>
        )}
        {(missing > 0 || diagram.missing > 0) && (
          <p className="m-0 px-4 py-2 text-[12.5px] text-flag md:px-7">
            {t("altro.ea.diag.missing", { count: Math.max(missing, diagram.missing) })}
          </p>
        )}
      </div>
      <aside className="flex w-full shrink-0 flex-col gap-4 border-hair px-4 py-4 md:px-7 lg:w-[360px] lg:border-l lg:px-5" aria-label={t("altro.ea.diag.panel")}>
        {canWrite ? (
          <>
            <SectionTitle>{t("altro.ea.diag.single")}</SectionTitle>
            <label className="flex flex-col gap-1">
              <span className="label">{t("altro.ea.diag.name")}</span>
              <input
                className="field"
                value={draft.name}
                onChange={(e) => {
                  setDraft({ ...draft, name: e.target.value });
                  setDirty(true);
                }}
              />
            </label>
            <label className="flex flex-col gap-1">
              <span className="label">{t("altro.ea.diag.kind")}</span>
              <Select
                className="field"
                value={draft.kind}
                onChange={(e) => {
                  setDraft({ ...draft, kind: e.target.value });
                  setDirty(true);
                }}
              >
                {Object.entries(KINDS).map(([k, v]) => (
                  <option key={k} value={k}>
                    {v}
                  </option>
                ))}
              </Select>
            </label>
            <label className="flex flex-col gap-1">
              <span className="label">{t("altro.ea.diag.description")}</span>
              <textarea
                className="field min-h-16"
                value={draft.description}
                onChange={(e) => {
                  setDraft({ ...draft, description: e.target.value });
                  setDirty(true);
                }}
              />
            </label>
            <form
              className="flex flex-col gap-1"
              onSubmit={(e) => {
                e.preventDefault();
                const q = adding.trim().toLowerCase();
                const n = [...(graph.data?.nodes ?? [])].find((x) => x.name.toLowerCase() === q || x.key.toLowerCase() === q || x.code?.toLowerCase() === q);
                if (n) {
                  addNode(n.id);
                  setSelected(n.id);
                  setAdding("");
                }
              }}
            >
              <label className="label" htmlFor="ea-aggiungi">
                {t("altro.ea.diag.addItem")}
              </label>
              <div className="flex gap-2">
                <input id="ea-aggiungi" className="field min-w-0 flex-1" list="ea-diag-nodi" value={adding} onChange={(e) => setAdding(e.target.value)} placeholder={t("altro.ea.diag.addPlaceholder")} />
                <button className="btn">{t("altro.ea.diag.add")}</button>
              </div>
              <datalist id="ea-diag-nodi">
                {(graph.data?.nodes ?? [])
                  .filter((n) => !pos.has(n.id))
                  .slice(0, 3000)
                  .map((n) => (
                    <option key={n.id} value={n.name}>
                      {n.key}
                    </option>
                  ))}
              </datalist>
            </form>
            <div className="flex flex-wrap gap-2">
              <button
                className="btn"
                onClick={() => {
                  const c = center();
                  const id = `n${Date.now().toString(36)}`;
                  update((l) => ({ ...l, notes: [...l.notes, { id, x: Math.round(c.x), y: Math.round(c.y), w: 200, text: t("altro.ea.diag.defaultNote") }] }));
                  setSelected(`nota:${id}`);
                }}
              >
                {t("altro.ea.diag.addNote")}
              </button>
              <button className="btn" disabled={draft.layout.nodes.length < 2} onClick={autoLayout}>
                {t("altro.ea.diag.autoLayout")}
              </button>
            </div>
            <fieldset className="m-0 flex flex-col gap-1 border-0 p-0">
              <legend className="label mb-1">{t("altro.ea.diag.linksToShow")}</legend>
              <label className="flex items-center gap-2 text-[12.5px]">
                <input
                  type="checkbox"
                  checked={shownTypes.length === 0}
                  onChange={(e) => update((l) => ({ ...l, edge_types: e.target.checked ? [] : [...edgeTypes.keys()] }))}
                />
                {t("altro.ea.diag.allLinks")}
              </label>
              {[...edgeTypes.values()].map((et) => (
                <label key={et.key} className="flex items-center gap-2 text-[12.5px]">
                  <input
                    type="checkbox"
                    checked={shownTypes.length === 0 || shownTypes.includes(et.key)}
                    onChange={(e) => {
                      const cur = shownTypes.length === 0 ? [...edgeTypes.keys()] : shownTypes;
                      const next = e.target.checked ? [...cur, et.key] : cur.filter((k) => k !== et.key);
                      update((l) => ({ ...l, edge_types: next.length === edgeTypes.size ? [] : next }));
                    }}
                  />
                  {et.outward_name}
                </label>
              ))}
            </fieldset>
          </>
        ) : (
          <>
            <SectionTitle>{t("altro.ea.diag.single")}</SectionTitle>
            {diagram.description && <p className="m-0 whitespace-pre-wrap">{diagram.description}</p>}
            <p className="m-0 text-[12.5px] text-muted">
              {t("altro.ea.diag.summary", { kind: KINDS[diagram.kind] ?? diagram.kind, count: diagram.layout.nodes.length, when: ago(diagram.updated_at) })}
            </p>
          </>
        )}
        {selNode && (
          <section className="flex flex-col gap-2 border-t-2 border-rule pt-3">
            <Link to="/e/$key" params={{ key: selNode.key }} className="text-[12px] text-accent no-underline hover:underline">
              <CatalogId code={selNode.code} itemKey={selNode.key} />
            </Link>
            <h2 className="m-0 text-[18px] leading-tight font-bold">{selNode.name}</h2>
            <p className="m-0 text-[12.5px] text-muted">
              {selNode.type_name}
              {selNode.team ? ` · ${selNode.team}` : ""}
            </p>
            <span className="flex flex-wrap gap-2">
              <Link to="/architettura/mappa" search={{ nodo: selNode.key }} className="btn no-underline">
                {t("altro.ea.diag.onMap")}
              </Link>
              {canWrite && (
                <>
                  <button className="btn" onClick={() => addNeighbors(selNode.id)}>
                    {t("altro.ea.diag.addNeighbors")}
                  </button>
                  <button
                    className="btn btn-danger"
                    onClick={() => {
                      update((l) => ({ ...l, nodes: l.nodes.filter((n) => n.id !== selNode.id) }));
                      setSelected(null);
                    }}
                  >
                    {t("altro.ea.diag.remove")}
                  </button>
                </>
              )}
            </span>
          </section>
        )}
        {selected && !selNode && !selNote && canWrite && (
          <button
            className="btn btn-danger self-start"
            onClick={() => {
              update((l) => ({ ...l, nodes: l.nodes.filter((n) => n.id !== selected) }));
              setSelected(null);
            }}
          >
            {t("altro.ea.diag.removeMissing")}
          </button>
        )}
        {selNote && canWrite && (
          <section className="flex flex-col gap-2 border-t-2 border-rule pt-3">
            <label className="flex flex-col gap-1">
              <span className="label">{t("altro.ea.diag.noteText")}</span>
              <textarea
                className="field min-h-20"
                value={selNote.text}
                onChange={(e) => update((l) => ({ ...l, notes: l.notes.map((n) => (n.id === selNote.id ? { ...n, text: e.target.value } : n)) }))}
              />
            </label>
            <button
              className="btn btn-danger self-start"
              onClick={() => {
                update((l) => ({ ...l, notes: l.notes.filter((n) => n.id !== selNote.id) }));
                setSelected(null);
              }}
            >
              {t("altro.ea.diag.deleteNote")}
            </button>
          </section>
        )}
        {canWrite && (
          <div className="mt-auto flex flex-col gap-1 border-t border-hair pt-3">
            <button className="btn btn-danger self-start" disabled={remove.isPending} onClick={() => confirm(t("altro.ea.diag.confirmDelete", { name: diagram.name })) && remove.mutate()}>
              {t("altro.ea.diag.delete")}
            </button>
            <ErrorNote error={remove.error} />
          </div>
        )}
      </aside>
    </div>
  );
}

/** Punti da tenere in vista: i nodi con l'etichetta e le note per intero. */
function extent(pos: Map<string, Pos>, notes: DiagramNote[]): Pos[] {
  const out: Pos[] = [];
  for (const p of pos.values()) out.push({ x: p.x - 60, y: p.y }, { x: p.x + 60, y: p.y + 30 });
  for (const n of notes) out.push({ x: n.x, y: n.y }, { x: n.x + (n.w ?? 200), y: n.y + 80 });
  return out;
}

function NoteShape({ note, pos, selected, canWrite }: { note: DiagramNote; pos: Pos; selected: boolean; canWrite: boolean }) {
  const w = note.w ?? 200;
  // A capo semplice per parole, al massimo 8 righe.
  const lines: string[] = [];
  for (const para of note.text.split("\n")) {
    let cur = "";
    for (const word of para.split(/\s+/)) {
      if ((cur + " " + word).trim().length * 6.2 > w - 16) {
        if (cur) lines.push(cur);
        cur = word;
      } else cur = (cur + " " + word).trim();
    }
    lines.push(cur);
  }
  const shown = lines.slice(0, 8);
  const h = 14 + shown.length * 15;
  return (
    <g data-id={`nota:${note.id}`} data-pos={`${pos.x},${pos.y}`} transform={`translate(${pos.x} ${pos.y})`} className={canWrite ? "cursor-move" : ""}>
      <rect width={w} height={h} className={`fill-sel ${selected ? "stroke-accent" : "stroke-hair"}`} strokeWidth={selected ? 2 : 1} />
      <rect width={3} height={h} className="fill-flag" />
      {shown.map((l, i) => (
        <text key={i} x={10} y={20 + i * 15} className="fill-fg text-[12px]">
          {l}
        </text>
      ))}
    </g>
  );
}
