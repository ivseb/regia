// Modello del grafo dell'architettura (replica/architettura-ea.md §4): dipendenze nei due
// versi, vicini fino a N livelli, layout automatico scritto a mano. Solo funzioni pure,
// provate con `pnpm test` (test/eaGraph.test.ts). Nessuna libreria.
import type { EdgeType, GraphEdge, GraphNode } from "../../eaApi";
import { loc } from "../../i18n/format.ts";

export type Pos = { x: number; y: number };
export type Adjacency = { dependents: Map<string, string[]>; dependencies: Map<string, string[]> };

/** Chi dipende da chi, secondo il verso di ogni tipo di relazione. */
export function adjacency(edges: GraphEdge[], types: EdgeType[]): Adjacency {
  const dir = new Map(types.map((t) => [t.key, t.impact_direction]));
  const dependents = new Map<string, string[]>();
  const dependencies = new Map<string, string[]>();
  const push = (m: Map<string, string[]>, k: string, v: string) => {
    const l = m.get(k);
    if (l) l.push(v);
    else m.set(k, [v]);
  };
  for (const e of edges) {
    const d = dir.get(e.type) ?? "forward";
    if (d === "none") continue;
    const [dependent, dependency] = d === "reverse" ? [e.target, e.source] : [e.source, e.target];
    push(dependents, dependency, dependent);
    push(dependencies, dependent, dependency);
  }
  return { dependents, dependencies };
}

/**
 * Nodi raggiungibili da start entro depth livelli (0 = tutti), con il livello più vicino.
 * Visita in ampiezza: i cicli non fanno girare a vuoto. Il nodo di partenza ha livello 0.
 */
export function reach(adj: Map<string, string[]>, start: string, depth: number, visible?: Set<string>): Map<string, number> {
  const seen = new Map<string, number>([[start, 0]]);
  let frontier = [start];
  for (let level = 1; frontier.length > 0 && (depth <= 0 || level <= depth); level++) {
    const next: string[] = [];
    for (const id of frontier)
      for (const to of adj.get(id) ?? []) {
        if (seen.has(to) || (visible && !visible.has(to))) continue;
        seen.set(to, level);
        next.push(to);
      }
    frontier = next;
  }
  return seen;
}

// ---------------------------------------------------------------------------
// Layout a forze (Fruchterman-Reingold con griglia per la repulsione)

export type ForceOptions = { k?: number; iterations?: number; initial?: Map<string, Pos> };

/** Posizioni iniziali deterministiche: spirale con l'angolo aureo, nell'ordine degli id. */
function seed(ids: string[], k: number, initial?: Map<string, Pos>): Float64Array {
  const xy = new Float64Array(ids.length * 2);
  const golden = Math.PI * (3 - Math.sqrt(5));
  ids.forEach((id, i) => {
    const p = initial?.get(id);
    if (p) {
      xy[2 * i] = p.x;
      xy[2 * i + 1] = p.y;
      return;
    }
    const r = k * Math.sqrt(i + 0.5);
    xy[2 * i] = r * Math.cos(i * golden);
    xy[2 * i + 1] = r * Math.sin(i * golden);
  });
  return xy;
}

/**
 * Layout a forze: repulsione tra i nodi vicini (griglia di lato 2k, costo quasi lineare),
 * attrazione lungo gli archi, gravità verso il centro per tenere insieme le componenti.
 * Con 500 nodi e 1000 archi bastano poche decine di millisecondi.
 */
export function forceLayout(nodes: string[], edges: [string, string][], opts: ForceOptions = {}): Map<string, Pos> {
  const n = nodes.length;
  const out = new Map<string, Pos>();
  if (n === 0) return out;
  const k = opts.k ?? 70;
  const index = new Map(nodes.map((id, i) => [id, i]));
  const links: number[] = [];
  for (const [a, b] of edges) {
    const i = index.get(a);
    const j = index.get(b);
    if (i !== undefined && j !== undefined && i !== j) links.push(i, j);
  }
  const xy = seed(nodes, k, opts.initial);
  const warm = opts.initial && nodes.every((id) => opts.initial!.has(id));
  const iterations = opts.iterations ?? Math.round(Math.max(60, Math.min(300, 1_500_000 / (n * 20 + links.length))));
  const disp = new Float64Array(n * 2);
  const cell = 2 * k;
  const k2 = k * k;
  let temp = warm ? k : k * Math.sqrt(n) * 0.5;
  const cool = temp / iterations;
  const grid = new Map<number, number[]>();
  const key = (cx: number, cy: number) => cx * 73856093 + cy * 19349663;
  for (let it = 0; it < iterations; it++) {
    disp.fill(0);
    grid.clear();
    for (let i = 0; i < n; i++) {
      const g = key(Math.floor(xy[2 * i] / cell), Math.floor(xy[2 * i + 1] / cell));
      const l = grid.get(g);
      if (l) l.push(i);
      else grid.set(g, [i]);
    }
    // Repulsione tra i nodi delle celle adiacenti.
    for (let i = 0; i < n; i++) {
      const xi = xy[2 * i];
      const yi = xy[2 * i + 1];
      const cx = Math.floor(xi / cell);
      const cy = Math.floor(yi / cell);
      for (let dx = -1; dx <= 1; dx++)
        for (let dy = -1; dy <= 1; dy++) {
          const l = grid.get(key(cx + dx, cy + dy));
          if (!l) continue;
          for (const j of l) {
            if (j <= i) continue;
            let ddx = xi - xy[2 * j];
            let ddy = yi - xy[2 * j + 1];
            let d2 = ddx * ddx + ddy * ddy;
            if (d2 < 0.01) {
              // Nodi sovrapposti: una spinta deterministica.
              ddx = ((i * 7919 + j) % 13) - 6 || 1;
              ddy = ((j * 104729 + i) % 11) - 5 || 1;
              d2 = ddx * ddx + ddy * ddy;
            }
            if (d2 > cell * cell) continue;
            const f = k2 / d2;
            disp[2 * i] += ddx * f;
            disp[2 * i + 1] += ddy * f;
            disp[2 * j] -= ddx * f;
            disp[2 * j + 1] -= ddy * f;
          }
        }
    }
    // Attrazione lungo gli archi.
    for (let e = 0; e < links.length; e += 2) {
      const i = links[e];
      const j = links[e + 1];
      const ddx = xy[2 * i] - xy[2 * j];
      const ddy = xy[2 * i + 1] - xy[2 * j + 1];
      const d = Math.sqrt(ddx * ddx + ddy * ddy) || 0.01;
      const f = d / k;
      disp[2 * i] -= ddx * f;
      disp[2 * i + 1] -= ddy * f;
      disp[2 * j] += ddx * f;
      disp[2 * j + 1] += ddy * f;
    }
    // Gravità e spostamento limitato dalla temperatura.
    for (let i = 0; i < n; i++) {
      disp[2 * i] -= xy[2 * i] * 0.02;
      disp[2 * i + 1] -= xy[2 * i + 1] * 0.02;
      const d = Math.sqrt(disp[2 * i] ** 2 + disp[2 * i + 1] ** 2);
      if (d > 0) {
        const m = Math.min(d, temp) / d;
        xy[2 * i] += disp[2 * i] * m;
        xy[2 * i + 1] += disp[2 * i + 1] * m;
      }
    }
    temp = Math.max(temp - cool, k * 0.05);
  }
  nodes.forEach((id, i) => out.set(id, { x: Math.round(xy[2 * i]), y: Math.round(xy[2 * i + 1]) }));
  return out;
}

// ---------------------------------------------------------------------------
// Layout a strati

const TYPE_ORDER = ["capability", "logical_asset", "application"];

/**
 * Layout a strati: capability per livello in alto, poi asset logici, applicazioni e gli
 * altri tipi (fornitori...). Le righe troppo lunghe vanno a capo; dentro una riga i nodi
 * seguono il baricentro dei vicini della riga sopra (due passate), così gli archi si
 * incrociano meno.
 */
export function layeredLayout(nodes: GraphNode[], edges: GraphEdge[], perRow = 24, gapX = 150, gapY = 110): Map<string, Pos> {
  const out = new Map<string, Pos>();
  const ids = new Set(nodes.map((n) => n.id));
  const capLevel = new Map<string, number>();
  const byID = new Map(nodes.map((n) => [n.id, n]));
  const level = (n: GraphNode): number => {
    if (n.type !== "capability") return 0;
    const c = capLevel.get(n.id);
    if (c !== undefined) return c;
    let l = 1;
    let p = n.parent_id ? byID.get(n.parent_id) : undefined;
    const guard = new Set([n.id]);
    while (p && !guard.has(p.id) && l < 20) {
      guard.add(p.id);
      l++;
      p = p.parent_id ? byID.get(p.parent_id) : undefined;
    }
    capLevel.set(n.id, l);
    return l;
  };
  const layerKey = (n: GraphNode) => {
    const t = TYPE_ORDER.indexOf(n.type);
    return n.type === "capability" ? level(n) - 1 : 100 + (t < 0 ? TYPE_ORDER.length : t);
  };
  const layers = new Map<number, GraphNode[]>();
  for (const n of nodes) {
    const k = layerKey(n);
    const l = layers.get(k);
    if (l) l.push(n);
    else layers.set(k, [n]);
  }
  const neighbors = new Map<string, string[]>();
  for (const e of edges) {
    if (!ids.has(e.source) || !ids.has(e.target)) continue;
    (neighbors.get(e.source) ?? neighbors.set(e.source, []).get(e.source)!).push(e.target);
    (neighbors.get(e.target) ?? neighbors.set(e.target, []).get(e.target)!).push(e.source);
  }
  const order = [...layers.keys()].sort((a, b) => a - b);
  let y = 0;
  const xOf = new Map<string, number>();
  for (let pass = 0; pass < 2; pass++) {
    y = 0;
    for (const k of order) {
      const row = layers.get(k)!;
      const bary = (n: GraphNode) => {
        const xs = (neighbors.get(n.id) ?? []).map((m) => xOf.get(m)).filter((v): v is number => v !== undefined);
        return xs.length ? xs.reduce((a, b) => a + b, 0) / xs.length : Number.POSITIVE_INFINITY;
      };
      if (pass === 0 && xOf.size === 0) row.sort((a, b) => a.name.localeCompare(b.name, loc()));
      else {
        const b = new Map(row.map((n) => [n.id, bary(n)]));
        row.sort((a, c) => b.get(a.id)! - b.get(c.id)! || a.name.localeCompare(c.name, loc()));
      }
      const lines = Math.ceil(row.length / perRow);
      row.forEach((n, i) => {
        const line = Math.floor(i / perRow);
        const inLine = Math.min(perRow, row.length - line * perRow);
        const x = (i % perRow) * gapX - ((inLine - 1) * gapX) / 2 + (line % 2) * (gapX / 2);
        xOf.set(n.id, x);
        out.set(n.id, { x, y: y + line * gapY * 0.8 });
      });
      y += lines * gapY * 0.8 + gapY * 0.6;
    }
  }
  return out;
}

/** Riquadro che contiene tutte le posizioni, con un margine. */
export function bounds(ps: Iterable<Pos>, margin = 60): { x: number; y: number; w: number; h: number } {
  let x0 = Infinity,
    y0 = Infinity,
    x1 = -Infinity,
    y1 = -Infinity;
  for (const p of ps) {
    x0 = Math.min(x0, p.x);
    y0 = Math.min(y0, p.y);
    x1 = Math.max(x1, p.x);
    y1 = Math.max(y1, p.y);
  }
  if (x0 === Infinity) return { x: -200, y: -150, w: 400, h: 300 };
  return { x: x0 - margin, y: y0 - margin, w: x1 - x0 + 2 * margin, h: y1 - y0 + 2 * margin };
}

/** Testo accorciato con i puntini. */
export function short(s: string, n: number): string {
  return s.length > n ? `${s.slice(0, n - 1)}…` : s;
}

/** Elenco CSV (separatore ";") da righe di testo, con il BOM per Excel. */
export function toCSV(rows: string[][]): string {
  const cell = (v: string) => (/[;"\n]/.test(v) ? `"${v.replace(/"/g, '""')}"` : v);
  return "\ufeff" + rows.map((r) => r.map(cell).join(";")).join("\r\n") + "\r\n";
}
