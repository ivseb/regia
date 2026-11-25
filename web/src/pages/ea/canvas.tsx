// Tela SVG del grafo (mappa e diagrammi): zoom e spostamento con mouse, rotella e tocco
// (due dita), forme dei nodi, colori dal tema, export SVG e PNG. Lo zoom non passa da
// React: cambia solo l'attributo transform del gruppo, così anche centinaia di nodi
// restano fluidi.
import { useCallback, useEffect, useRef, useState, type PointerEvent as RPointerEvent } from "react";
import type { Crit, GraphNode, TimeRating } from "../../eaApi";
import { bounds, type Pos } from "./graphModel";
import { tr } from "../../i18n/core";

export type View = { x: number; y: number; k: number };

type Handlers = {
  /** Tocco o clic senza trascinamento: id del nodo (data-id) oppure null sullo sfondo. */
  onTap?: (id: string | null) => void;
  /** Senza elementi per nodo: trova il nodo vicino a un punto del mondo (k = zoom). */
  pick?: (p: Pos, k: number) => string | null;
  /** Se restituisce true per un nodo, il trascinamento sposta il nodo invece della vista. */
  canDrag?: (id: string) => boolean;
  onDrag?: (id: string, p: Pos, phase: "move" | "end") => void;
  /** Zoom sopra il quale i nomi dei nodi sono sempre visibili (predefinito 0,55). */
  labelK?: number;
};

const MIN_K = 0.08;
const MAX_K = 4;
const LABEL_K = 0.55;

export function useViewport(h: Handlers) {
  const svgRef = useRef<SVGSVGElement>(null);
  const gRef = useRef<SVGGElement>(null);
  const view = useRef<View>({ x: 0, y: 0, k: 1 });
  const [labels, setLabels] = useState(true);
  const frame = useRef(0);
  const handlers = useRef(h);
  handlers.current = h;

  // Durante un gesto la tela ha la classe "moving": le etichette spariscono finché si
  // muove (meno testo da ridisegnare a ogni fotogramma) e tornano 150 ms dopo.
  const still = useRef(0);
  const apply = useCallback(() => {
    const svg = svgRef.current;
    if (svg) {
      svg.classList.add("moving");
      clearTimeout(still.current);
      still.current = window.setTimeout(() => svg.classList.remove("moving"), 150);
    }
    if (frame.current) return;
    frame.current = requestAnimationFrame(() => {
      frame.current = 0;
      const v = view.current;
      gRef.current?.setAttribute("transform", `translate(${v.x.toFixed(1)} ${v.y.toFixed(1)}) scale(${v.k.toFixed(4)})`);
      const lk = handlers.current.labelK ?? LABEL_K;
      setLabels((old) => (old !== v.k >= lk ? v.k >= lk : old));
    });
  }, []);

  const toWorld = useCallback((clientX: number, clientY: number): Pos => {
    const r = svgRef.current!.getBoundingClientRect();
    const v = view.current;
    return { x: (clientX - r.left - v.x) / v.k, y: (clientY - r.top - v.y) / v.k };
  }, []);

  /** Zoom di un fattore attorno a un punto dello schermo (di default il centro). */
  const zoomAt = useCallback(
    (f: number, sx?: number, sy?: number) => {
      const svg = svgRef.current;
      if (!svg) return;
      const r = svg.getBoundingClientRect();
      const cx = sx ?? r.width / 2;
      const cy = sy ?? r.height / 2;
      const v = view.current;
      const k = Math.min(MAX_K, Math.max(MIN_K, v.k * f));
      view.current = { k, x: cx - ((cx - v.x) * k) / v.k, y: cy - ((cy - v.y) * k) / v.k };
      apply();
    },
    [apply],
  );

  /** Adatta la vista a un insieme di punti. */
  const fit = useCallback(
    (ps: Iterable<Pos>, maxK = 1.4) => {
      const svg = svgRef.current;
      if (!svg) return;
      const r = svg.getBoundingClientRect();
      const b = bounds(ps);
      const k = Math.min(maxK, Math.max(MIN_K, Math.min(r.width / b.w, r.height / b.h)));
      view.current = { k, x: (r.width - b.w * k) / 2 - b.x * k, y: (r.height - b.h * k) / 2 - b.y * k };
      apply();
    },
    [apply],
  );

  /** Porta un punto del mondo al centro, senza cambiare lo zoom (o con almeno minK). */
  const center = useCallback(
    (p: Pos, minK = 0.8) => {
      const svg = svgRef.current;
      if (!svg) return;
      const r = svg.getBoundingClientRect();
      const k = Math.max(view.current.k, minK);
      view.current = { k, x: r.width / 2 - p.x * k, y: r.height / 2 - p.y * k };
      apply();
    },
    [apply],
  );

  // Rotella: zoom attorno al puntatore (listener non passivo per fermare lo scorrimento).
  useEffect(() => {
    const svg = svgRef.current;
    if (!svg) return;
    const onWheel = (e: WheelEvent) => {
      e.preventDefault();
      const r = svg.getBoundingClientRect();
      if (e.ctrlKey || Math.abs(e.deltaY) >= Math.abs(e.deltaX)) {
        zoomAt(Math.exp(-e.deltaY * (e.ctrlKey ? 0.01 : 0.0015)), e.clientX - r.left, e.clientY - r.top);
      } else {
        view.current = { ...view.current, x: view.current.x - e.deltaX };
        apply();
      }
    };
    svg.addEventListener("wheel", onWheel, { passive: false });
    return () => svg.removeEventListener("wheel", onWheel);
  }, [zoomAt, apply]);
  useEffect(
    () => () => {
      cancelAnimationFrame(frame.current);
      clearTimeout(still.current);
    },
    [],
  );

  // Puntatori: uno sposta la vista (o il nodo), due fanno lo zoom (pizzico).
  const pointers = useRef(new Map<number, { x: number; y: number }>());
  const gesture = useRef<{
    startX: number;
    startY: number;
    view: View;
    moved: boolean;
    node: string | null;
    drag: boolean;
    offset: Pos;
    pinch?: { d: number; cx: number; cy: number; view: View };
  } | null>(null);

  const local = (e: { clientX: number; clientY: number }) => {
    const r = svgRef.current!.getBoundingClientRect();
    return { x: e.clientX - r.left, y: e.clientY - r.top };
  };

  const onPointerDown = (e: RPointerEvent<SVGSVGElement>) => {
    if (e.button !== 0 && e.pointerType === "mouse") return;
    const p = local(e);
    pointers.current.set(e.pointerId, p);
    svgRef.current?.setPointerCapture(e.pointerId);
    if (pointers.current.size === 2) {
      const [a, b] = [...pointers.current.values()];
      gesture.current = {
        ...(gesture.current ?? { startX: p.x, startY: p.y, node: null, drag: false, offset: { x: 0, y: 0 } }),
        moved: true,
        view: view.current,
        pinch: { d: Math.hypot(a.x - b.x, a.y - b.y) || 1, cx: (a.x + b.x) / 2, cy: (a.y + b.y) / 2, view: view.current },
      };
      return;
    }
    const node =
      (e.target as Element).closest?.("[data-id]")?.getAttribute("data-id") ?? handlers.current.pick?.(toWorld(e.clientX, e.clientY), view.current.k) ?? null;
    const drag = !!node && !!handlers.current.canDrag?.(node);
    let offset = { x: 0, y: 0 };
    if (drag) {
      const w = toWorld(e.clientX, e.clientY);
      const t = (e.target as Element).closest("[data-id]")!.getAttribute("data-pos")?.split(",").map(Number) ?? [w.x, w.y];
      offset = { x: w.x - t[0], y: w.y - t[1] };
    }
    gesture.current = { startX: p.x, startY: p.y, view: view.current, moved: false, node, drag, offset };
  };

  const onPointerMove = (e: RPointerEvent<SVGSVGElement>) => {
    const g = gesture.current;
    if (!g || !pointers.current.has(e.pointerId)) return;
    const p = local(e);
    pointers.current.set(e.pointerId, p);
    if (g.pinch && pointers.current.size >= 2) {
      const [a, b] = [...pointers.current.values()];
      const d = Math.hypot(a.x - b.x, a.y - b.y) || 1;
      const cx = (a.x + b.x) / 2;
      const cy = (a.y + b.y) / 2;
      const v0 = g.pinch.view;
      const k = Math.min(MAX_K, Math.max(MIN_K, (v0.k * d) / g.pinch.d));
      // Il punto del mondo sotto il centro iniziale segue il centro attuale.
      const wx = (g.pinch.cx - v0.x) / v0.k;
      const wy = (g.pinch.cy - v0.y) / v0.k;
      view.current = { k, x: cx - wx * k, y: cy - wy * k };
      apply();
      return;
    }
    if (!g.moved && Math.hypot(p.x - g.startX, p.y - g.startY) < 5) return;
    g.moved = true;
    if (g.drag && g.node) {
      const w = toWorld(e.clientX, e.clientY);
      handlers.current.onDrag?.(g.node, { x: w.x - g.offset.x, y: w.y - g.offset.y }, "move");
      return;
    }
    view.current = { ...g.view, x: g.view.x + p.x - g.startX, y: g.view.y + p.y - g.startY };
    apply();
  };

  const onPointerUp = (e: RPointerEvent<SVGSVGElement>) => {
    pointers.current.delete(e.pointerId);
    const g = gesture.current;
    if (!g) return;
    if (pointers.current.size > 0) {
      // Fine del pizzico: si riparte con lo spostamento dal dito rimasto.
      const [rest] = [...pointers.current.values()];
      gesture.current = { ...g, pinch: undefined, startX: rest.x, startY: rest.y, view: view.current, drag: false };
      return;
    }
    gesture.current = null;
    if (g.drag && g.node && g.moved) {
      const w = toWorld(e.clientX, e.clientY);
      handlers.current.onDrag?.(g.node, { x: w.x - g.offset.x, y: w.y - g.offset.y }, "end");
      return;
    }
    if (!g.moved) handlers.current.onTap?.(g.node);
  };

  const onPointerCancel = (e: RPointerEvent<SVGSVGElement>) => {
    pointers.current.delete(e.pointerId);
    if (pointers.current.size === 0) gesture.current = null;
  };

  return { svgRef, gRef, view, labels, fit, center, zoomAt, toWorld, bind: { onPointerDown, onPointerMove, onPointerUp, onPointerCancel } };
}

// ---------------------------------------------------------------------------
// Forme e colori

export const NODE_R = 10;

/** Forma del nodo per tipo: applicazione quadrato, asset rombo, capability esagono, altri cerchio. */
export function shapePath(type: string, r = NODE_R): string {
  switch (type) {
    case "application":
      return `M${-r},${-r}h${2 * r}v${2 * r}h${-2 * r}z`;
    case "logical_asset":
      return `M0,${-r * 1.3}L${r * 1.3},0L0,${r * 1.3}L${-r * 1.3},0z`;
    case "capability": {
      const w = r * 1.5;
      const hh = r * 0.95;
      return `M${-w},0L${-w / 2},${-hh}L${w / 2},${-hh}L${w},0L${w / 2},${hh}L${-w / 2},${hh}z`;
    }
    default:
      return `M${-r},0a${r},${r} 0 1,0 ${2 * r},0a${r},${r} 0 1,0 ${-2 * r},0`;
  }
}

/** La stessa forma in coordinate assolute, per unire più nodi in un solo path. */
export function shapeAt(type: string, x: number, y: number, r = NODE_R): string {
  switch (type) {
    case "application":
      return `M${x - r},${y - r}h${2 * r}v${2 * r}h${-2 * r}z`;
    case "logical_asset":
      return `M${x},${y - r * 1.3}l${r * 1.3},${r * 1.3}l${-r * 1.3},${r * 1.3}l${-r * 1.3},${-r * 1.3}z`;
    case "capability": {
      const w = r * 1.5;
      const hh = r * 0.95;
      return `M${x - w},${y}l${w / 2},${-hh}h${w}l${w / 2},${hh}l${-w / 2},${hh}h${-w}z`;
    }
    default:
      return `M${x - r},${y}a${r},${r} 0 1,0 ${2 * r},0a${r},${r} 0 1,0 ${-2 * r},0`;
  }
}

export const SHAPE_NAMES = {
  get application() {
    return tr("altro.ea.canvas.shapeSquare");
  },
  get logical_asset() {
    return tr("altro.ea.canvas.shapeDiamond");
  },
  get capability() {
    return tr("altro.ea.canvas.shapeHexagon");
  },
} as Record<string, string>;

export type ColorMode = "criticita" | "time" | "supporto" | "tipo";

export const COLOR_MODES = {
  get criticita() {
    return tr("altro.ea.canvas.modeCriticality");
  },
  get time() {
    return tr("altro.ea.canvas.modeTime");
  },
  get supporto() {
    return tr("altro.ea.canvas.modeSupport");
  },
  get tipo() {
    return tr("altro.ea.canvas.modeType");
  },
} as Record<ColorMode, string>;

const CRIT_FILL: Record<Crit, string> = { critical: "fill-danger", high: "fill-flag", medium: "fill-accent", low: "fill-muted" };
const TIME_FILL: Record<TimeRating, string> = { invest: "fill-ok", tolerate: "fill-accent", migrate: "fill-flag", eliminate: "fill-danger" };

export function daysTo(date?: string | null): number | null {
  if (!date) return null;
  const d = new Date(`${date}T00:00:00`);
  const now = new Date();
  now.setHours(0, 0, 0, 0);
  return Math.round((d.getTime() - now.getTime()) / 86_400_000);
}

/** Classe di riempimento del nodo nel modo di colore scelto. */
export function nodeFill(n: GraphNode, mode: ColorMode, eolDays = 180): string {
  switch (mode) {
    case "criticita":
      return n.criticality ? CRIT_FILL[n.criticality] : "fill-hair";
    case "time":
      return n.time ? TIME_FILL[n.time] : "fill-hair";
    case "supporto": {
      const d = daysTo(n.end_of_support);
      if (d === null) return "fill-hair";
      return d < 0 ? "fill-danger" : d <= eolDays ? "fill-flag" : "fill-ok";
    }
    default:
      return n.type === "application" ? "fill-fg" : n.type === "capability" ? "fill-accent" : "fill-muted";
  }
}

// Le legende si leggono al momento dell'uso, nella lingua corrente.
export const LEGENDS = {
  get criticita(): [string, string][] {
    return [
      ["fill-danger", tr("altro.ea.canvas.legCritical")],
      ["fill-flag", tr("altro.ea.canvas.legHigh")],
      ["fill-accent", tr("altro.ea.canvas.legMedium")],
      ["fill-muted", tr("altro.ea.canvas.legLow")],
      ["fill-hair", tr("altro.ea.canvas.legUnset")],
    ];
  },
  get time(): [string, string][] {
    return [
      ["fill-ok", tr("altro.ea.time.invest")],
      ["fill-accent", tr("altro.ea.time.tolerate")],
      ["fill-flag", tr("altro.ea.time.migrate")],
      ["fill-danger", tr("altro.ea.time.eliminate")],
      ["fill-hair", tr("altro.ea.canvas.legNotRated")],
    ];
  },
  get supporto(): [string, string][] {
    return [
      ["fill-danger", tr("altro.ea.canvas.legExpired")],
      ["fill-flag", tr("altro.ea.canvas.legExpiring")],
      ["fill-ok", tr("altro.ea.canvas.legSupported")],
      ["fill-hair", tr("altro.ea.canvas.legNoDate")],
    ];
  },
  get tipo(): [string, string][] {
    return [
      ["fill-fg", tr("altro.ea.canvas.legApplication")],
      ["fill-accent", tr("altro.ea.canvas.legCapability")],
      ["fill-muted", tr("altro.ea.canvas.legOther")],
    ];
  },
} as Record<ColorMode, [string, string][]>;

/** Tratteggio dell'arco per categoria: dipendenza piena, capability a trattini, composizione a punti. */
export function edgeDash(category: string | undefined): string | undefined {
  return category === "capability" ? "6 4" : category === "composition" ? "2 3" : undefined;
}

// ---------------------------------------------------------------------------
// Export

const STYLE_PROPS = ["fill", "stroke", "stroke-width", "stroke-dasharray", "opacity", "font-size", "font-weight", "font-family", "text-anchor", "dominant-baseline"];

/**
 * Copia autonoma di un SVG: gli stili calcolati (colori del tema compresi) diventano
 * attributi, la vista si adatta al contenuto e sotto c'è il fondo del tema.
 */
export function standaloneSVG(svg: SVGSVGElement, content: SVGGElement): { text: string; w: number; h: number } {
  const box = content.getBBox();
  const pad = 24;
  const w = Math.ceil(box.width + 2 * pad);
  const h = Math.ceil(box.height + 2 * pad);
  const clone = content.cloneNode(true) as SVGGElement;
  const src = [content, ...content.querySelectorAll("*")];
  const dst = [clone, ...clone.querySelectorAll("*")];
  src.forEach((el, i) => {
    const cs = getComputedStyle(el);
    const out = dst[i] as Element;
    for (const p of STYLE_PROPS) {
      const v = cs.getPropertyValue(p);
      if (v && v !== "none" && !(p === "opacity" && v === "1")) out.setAttribute(p, v);
      else if (p === "fill" && v === "none") out.setAttribute("fill", "none");
    }
    out.removeAttribute("class");
    out.removeAttribute("data-id");
    out.removeAttribute("data-pos");
  });
  clone.setAttribute("transform", `translate(${pad - box.x} ${pad - box.y})`);
  const bg = getComputedStyle(svg).getPropertyValue("--bg").trim() || "#ffffff";
  const ns = "http://www.w3.org/2000/svg";
  const text =
    `<svg xmlns="${ns}" width="${w}" height="${h}" viewBox="0 0 ${w} ${h}">` +
    `<rect width="100%" height="100%" fill="${bg}"/>` +
    new XMLSerializer().serializeToString(clone) +
    `</svg>`;
  return { text, w, h };
}

function download(blob: Blob, name: string) {
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

export function exportSVG(svg: SVGSVGElement, content: SVGGElement, name: string) {
  const { text } = standaloneSVG(svg, content);
  download(new Blob([text], { type: "image/svg+xml" }), `${name}.svg`);
}

export function exportPNG(svg: SVGSVGElement, content: SVGGElement, name: string): Promise<void> {
  const { text, w, h } = standaloneSVG(svg, content);
  const scale = Math.min(2, 8000 / Math.max(w, h));
  return new Promise((resolve, reject) => {
    const img = new Image();
    const url = URL.createObjectURL(new Blob([text], { type: "image/svg+xml" }));
    img.onload = () => {
      const c = document.createElement("canvas");
      c.width = Math.round(w * scale);
      c.height = Math.round(h * scale);
      const ctx = c.getContext("2d")!;
      ctx.scale(scale, scale);
      ctx.drawImage(img, 0, 0);
      URL.revokeObjectURL(url);
      c.toBlob((b) => {
        if (b) {
          download(b, `${name}.png`);
          resolve();
        } else reject(new Error(tr("altro.ea.canvas.imageFailed")));
      }, "image/png");
    };
    img.onerror = () => {
      URL.revokeObjectURL(url);
      reject(new Error(tr("altro.ea.canvas.imageFailed")));
    };
    img.src = url;
  });
}

/** Nome di file dal titolo. */
export function fileName(s: string): string {
  return (
    s
      .toLowerCase()
      .normalize("NFD")
      .replace(/[̀-ͯ]/g, "")
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-|-$/g, "") || tr("altro.ea.canvas.defaultFileName")
  );
}
