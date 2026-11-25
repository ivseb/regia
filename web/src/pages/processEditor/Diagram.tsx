import { useMemo, useRef, useState, type PointerEvent as RPointerEvent } from "react";
import type { Definition } from "../../api";
import { ANY, LAYOUT, categoryName, layout, type NodeBox } from "./model";
import { useT } from "../../i18n/react";

// Diagramma delle fasi in SVG (nessuna libreria): blocchi in fila per il percorso
// principale, fasi fuori percorso sotto, passaggi come frecce con etichetta. Si
// trascina un blocco per riordinare (o per portarlo dentro o fuori percorso) e dal
// pallino a destra di un blocco verso un altro per creare un passaggio.

export type Selection = { kind: "phase"; name: string } | { kind: "transition"; index: number } | null;

type Drag = { mode: "move" | "link"; node: NodeBox; x0: number; y0: number; x: number; y: number; moved: boolean; pointer: number };

type Props = {
  def: Definition;
  selection: Selection;
  onSelect: (s: Selection) => void;
  /** Fasi con errori, passaggi con errori (indici), fasi irraggiungibili. */
  phaseErrors: Set<string>;
  transitionErrors: Set<number>;
  unreachable: Set<string>;
  onMove: (name: string, index: number, offPath: boolean) => void;
  onConnect: (from: string, to: string) => void;
  /** Fattore di scala (1 = dimensioni reali): il diagramma si riduce per stare nella pagina. */
  scale?: number;
};

const short = (s: string, n: number) => (s.length > n ? `${s.slice(0, n - 1)}…` : s);

/** Larghezza del diagramma alle dimensioni reali, per calcolare la scala che lo fa entrare. */
export function diagramWidth(def: Definition): number {
  return layout(def).width;
}

export function Diagram({ def, selection, onSelect, phaseErrors, transitionErrors, unreachable, onMove, onConnect, scale = 1 }: Props) {
  const t = useT();
  const l = useMemo(() => layout(def), [def]);
  const svg = useRef<SVGSVGElement>(null);
  const [drag, setDrag] = useState<Drag | null>(null);
  // Con la cattura del puntatore il clic che segue un trascinamento arriva allo sfondo:
  // non deve togliere la selezione appena fatta.
  const swallowClick = useRef(false);
  const { W, H, P, PAD } = LAYOUT;
  const colors = useMemo(() => new Map(def.phases.map((p) => [p.name, p.color])), [def.phases]);

  const point = (e: { clientX: number; clientY: number }) => {
    const r = svg.current!.getBoundingClientRect();
    return { x: (e.clientX - r.left) / scale, y: (e.clientY - r.top) / scale };
  };
  const nodeAt = (x: number, y: number) => l.nodes.find((n) => x >= n.x && x <= n.x + n.w && y >= n.y - 6 && y <= n.y + n.h + 6);

  const start = (e: RPointerEvent, node: NodeBox, mode: Drag["mode"]) => {
    if (e.button !== 0) return;
    e.stopPropagation();
    const p = point(e);
    svg.current!.setPointerCapture(e.pointerId);
    setDrag({ mode, node, x0: p.x, y0: p.y, x: p.x, y: p.y, moved: false, pointer: e.pointerId });
  };
  const move = (e: RPointerEvent) => {
    if (!drag) return;
    const p = point(e);
    setDrag({ ...drag, x: p.x, y: p.y, moved: drag.moved || Math.hypot(p.x - drag.x0, p.y - drag.y0) > 4 });
  };
  const end = (e: RPointerEvent) => {
    if (!drag) return;
    svg.current!.releasePointerCapture(drag.pointer);
    const { node, moved } = drag;
    setDrag(null);
    swallowClick.current = true;
    setTimeout(() => (swallowClick.current = false), 0);
    const p = point(e);
    if (drag.mode === "link") {
      const target = nodeAt(p.x, p.y);
      if (moved && target && !target.pseudo && target.name !== node.name) onConnect(node.name, target.name);
      return;
    }
    if (!moved) {
      onSelect(node.pseudo ? null : { kind: "phase", name: node.name });
      return;
    }
    if (node.pseudo) return;
    const cx = node.x + W / 2 + (p.x - drag.x0);
    const cy = node.y + H / 2 + (p.y - drag.y0);
    const off = cy > (l.mainY + H + l.offY) / 2;
    const row = l.nodes.filter((n) => !n.pseudo && n.offPath === off && n.name !== node.name);
    const index = row.filter((n) => n.x + W / 2 < cx).length;
    onMove(node.name, index, off);
  };

  const selT = selection?.kind === "transition" ? selection.index : -1;
  const selP = selection?.kind === "phase" ? selection.name : "";
  const related = (t: number) => {
    if (selT >= 0) return t === selT;
    if (!selP) return true;
    const tr = def.transitions[t];
    return tr.to === selP || tr.from.includes(selP);
  };
  const dx = drag?.mode === "move" && drag.moved ? drag.x - drag.x0 : 0;
  const dy = drag?.mode === "move" && drag.moved ? drag.y - drag.y0 : 0;

  return (
    <svg
      ref={svg}
      width={l.width * scale}
      height={Math.max(l.height, drag?.mode === "move" ? l.offY + H + 12 : 0) * scale}
      viewBox={`0 0 ${l.width} ${Math.max(l.height, drag?.mode === "move" ? l.offY + H + 12 : 0)}`}
      className="block touch-none select-none font-sans"
      role="group"
      aria-label={t("configurazione.diagram.diagrammaDelleFasiE")}
      onPointerMove={move}
      onPointerUp={end}
      onPointerCancel={() => setDrag(null)}
      onClick={(e) => {
        if (swallowClick.current) swallowClick.current = false;
        else if (e.target === svg.current) onSelect(null);
      }}
    >
      {/* Linee dei passaggi: zona larga invisibile per il clic, poi la linea. */}
      {l.edges.map((e) => {
        const sel = e.transition === selT;
        const dim = !related(e.transition);
        return (
          <g key={e.id} className={dim ? "opacity-30" : ""} onClick={() => onSelect({ kind: "transition", index: e.transition })} style={{ cursor: "pointer" }}>
            <path d={e.d} fill="none" stroke="transparent" strokeWidth={10} />
            <path
              d={e.d}
              fill="none"
              className={sel ? "stroke-accent" : transitionErrors.has(e.transition) ? "stroke-danger" : selP && !dim ? "stroke-fg" : "stroke-muted"}
              strokeWidth={sel ? 2 : 1.25}
              strokeDasharray={e.system ? "5 4" : undefined}
            />
            <polygon points={e.arrow} className={sel ? "fill-accent" : transitionErrors.has(e.transition) ? "fill-danger" : selP && !dim ? "fill-fg" : "fill-muted"} />
          </g>
        );
      })}

      {/* Blocchi delle fasi. */}
      {l.nodes.map((n) => {
        const moving = drag?.mode === "move" && drag.node.name === n.name;
        const sel = !n.pseudo && n.name === selP;
        const err = phaseErrors.has(n.name);
        const lost = unreachable.has(n.name);
        const color = colors.get(n.name);
        const initial = n.name.toLowerCase() === def.initial.toLowerCase();
        const status = [n.pseudo ? t("configurazione.diagram.nonConclusa") : categoryName(def.phases.find((p) => p.name === n.name)?.category ?? ""), initial && t("configurazione.diagram.start"), lost && t("configurazione.diagram.unreachable")]
          .filter(Boolean)
          .join(" · ");
        return (
          <g
            key={`n-${n.name}`}
            transform={`translate(${n.x + (moving ? dx : 0)},${n.y + (moving ? dy : 0)})`}
            className={moving ? "opacity-80" : ""}
            tabIndex={n.pseudo ? -1 : 0}
            role={n.pseudo ? undefined : "button"}
            aria-label={n.pseudo ? undefined : `${n.name} ${status}`}
            aria-pressed={n.pseudo ? undefined : sel}
            onKeyDown={(e) => {
              if (!n.pseudo && (e.key === "Enter" || e.key === " ")) {
                e.preventDefault();
                onSelect({ kind: "phase", name: n.name });
              }
            }}
            style={{ cursor: n.pseudo ? "default" : drag ? "grabbing" : "grab", outline: "none" }}
            onPointerDown={(e) => start(e, n, "move")}
          >
            <title>{n.pseudo ? t("configurazione.diagram.qualunqueFaseNonConclusa") : n.name}</title>
            <rect
              width={n.w}
              height={n.h}
              className={`fill-bg ${sel ? "stroke-accent" : err || lost ? "stroke-danger" : n.pseudo ? "stroke-muted" : "stroke-rule"}`}
              strokeWidth={sel ? 2 : 1}
              strokeDasharray={n.pseudo || lost ? "4 3" : undefined}
            />
            {color && !n.pseudo && <rect x={sel ? 1 : 0.5} y={sel ? 1 : 0.5} width={4} height={n.h - (sel ? 2 : 1)} fill={color} />}
            <text x={12} y={21} className={n.pseudo ? "fill-muted" : "fill-fg"} fontSize={13} fontWeight={600} fontStyle={n.pseudo ? "italic" : undefined}>
              {n.pseudo ? t("configurazione.diagram.qualunqueFase") : short(n.name, 17)}
              {/* Spazio tra nome e stato nel testo letto dalle tecnologie assistive (etichetta nel nome). */}
              {" "}
            </text>
            <text x={12} y={38} className={lost || err ? "fill-danger" : "fill-muted"} fontSize={11}>
              {short(status, 24)}
            </text>
            {initial && <polygon points={`-9,${n.h / 2 - 5} -2,${n.h / 2} -9,${n.h / 2 + 5}`} className="fill-fg" aria-hidden="true" />}
            {/* Pallino da trascinare verso un'altra fase per creare un passaggio. */}
            <g onPointerDown={(e) => start(e, n, "link")} style={{ cursor: "crosshair" }} aria-hidden="true">
              <circle cx={n.w} cy={n.h / 2} r={11} fill="transparent" />
              <circle cx={n.w} cy={n.h / 2} r={4.5} className={sel ? "fill-accent" : "fill-bg stroke-muted"} strokeWidth={1.25} />
            </g>
          </g>
        );
      })}

      {/* Etichette sopra a tutto, con fondo per leggerle sulle linee. */}
      {l.edges.map((e) => {
        const sel = e.transition === selT;
        const err = transitionErrors.has(e.transition);
        const w = e.label.text.length * 6.4 + 12;
        return (
          <g
            key={`l-${e.id}`}
            transform={`translate(${e.label.x - w / 2},${e.label.y - 9})`}
            className={!related(e.transition) ? "opacity-40" : ""}
            tabIndex={0}
            role="button"
            // Il nome comincia con il testo visibile (anche se accorciato), poi il dettaglio completo.
            aria-label={t("configurazione.diagram.transitionLabel", { text: e.label.text, full: e.label.full, to: def.transitions[e.transition].to })}
            aria-pressed={sel}
            style={{ cursor: "pointer", outline: "none" }}
            onClick={() => onSelect({ kind: "transition", index: e.transition })}
            onKeyDown={(ev) => {
              if (ev.key === "Enter" || ev.key === " ") {
                ev.preventDefault();
                onSelect({ kind: "transition", index: e.transition });
              }
            }}
          >
            <title>{e.label.full}</title>
            <rect width={w} height={18} className={`fill-bg ${sel ? "stroke-accent" : "stroke-hair"}`} strokeWidth={1} />
            <text x={6} y={13} fontSize={11.5} fontStyle={e.system ? "italic" : undefined} className={sel ? "fill-accent" : err ? "fill-danger" : "fill-fg"}>
              {err ? "! " : ""}
              {e.label.text}
            </text>
          </g>
        );
      })}

      {/* Trascinamenti in corso. */}
      {drag?.mode === "link" && drag.moved && (
        <line x1={drag.node.x + drag.node.w} y1={drag.node.y + drag.node.h / 2} x2={drag.x} y2={drag.y} className="stroke-accent" strokeWidth={1.5} strokeDasharray="4 3" />
      )}
      {drag?.mode === "move" && drag.moved && <MoveHint l={l} drag={drag} P={P} PAD={PAD} W={W} H={H} />}
    </svg>
  );
}

/** Dove andrà la fase trascinata: una barra verticale tra i blocchi. */
function MoveHint({ l, drag, P, PAD, W, H }: { l: ReturnType<typeof layout>; drag: Drag; P: number; PAD: number; W: number; H: number }) {
  const t = useT();
  const cx = drag.node.x + W / 2 + (drag.x - drag.x0);
  const cy = drag.node.y + H / 2 + (drag.y - drag.y0);
  const offY = l.offY;
  const off = cy > (l.mainY + H + offY) / 2;
  const row = l.nodes.filter((n) => !n.pseudo && n.offPath === off && n.name !== drag.node.name);
  const before = row.filter((n) => n.x + W / 2 < cx);
  const x = before.length ? before[before.length - 1].x + W + (P - W) / 2 : row.length ? row[0].x - (P - W) / 2 : PAD - 8;
  const y = off ? offY : l.mainY;
  return (
    <g aria-hidden="true">
      <line x1={x} x2={x} y1={y - 6} y2={y + H + 6} className="stroke-accent" strokeWidth={2} />
      {off && !row.some((n) => n.name !== ANY) && (
        <text x={x + 8} y={y + H / 2 + 4} fontSize={11.5} className="fill-accent">
          {t("configurazione.common.fuoriPercorso")}
        </text>
      )}
    </g>
  );
}
