// Livello «Continuità» sopra la mappa dell'architettura: sotto ogni nodo l'RTO dichiarato e il
// richiesto, e gli archi incoerenti (dipendenza più lenta) nel colore di pericolo del tema.
// Si disegna sopra archi e nodi della mappa senza toccarne il disegno.
import { memo } from "react";
import { tr } from "../../i18n/core";
import { fmtMinutes, type ContinuityGraph } from "../../continuityApi";
import type { GraphEdge } from "../../eaApi";
import type { Pos } from "../ea/graphModel";

const R = 10; // raggio dei nodi della mappa (canvas.tsx, NODE_R)

/** Coppie (in un verso o nell'altro) degli archi incoerenti. */
export function incoherentPairs(g: ContinuityGraph | undefined): Set<string> {
  const s = new Set<string>();
  for (const e of g?.edges ?? []) {
    if (!e.incoherent) continue;
    s.add(`${e.dependent}|${e.dependency}`);
    s.add(`${e.dependency}|${e.dependent}`);
  }
  return s;
}

/** Testo sotto il nodo: «RTO 4 h · richiesto 1 h»; null se non c'è nulla da dire. */
export function nodeContinuityLabel(n: ContinuityGraph["nodes"][string] | undefined): string | null {
  if (!n || (n.rto_declared == null && n.rto_required == null)) return null;
  if (n.rto_required == null) return tr("continuita.map.declaredOnly", { rto: fmtMinutes(n.rto_declared) });
  return tr("continuita.map.node", { rto: fmtMinutes(n.rto_declared), required: fmtMinutes(n.rto_required) });
}

export const ContinuityLayer = memo(function ContinuityLayer({
  data,
  edges,
  positions,
  visible,
  labels,
  nodeR = R,
}: {
  data: ContinuityGraph;
  edges: GraphEdge[];
  positions: Map<string, Pos>;
  visible: Set<string>;
  labels: boolean;
  nodeR?: number;
}) {
  const bad = incoherentPairs(data);
  const lines: string[] = [];
  const hot = new Set<string>();
  for (const e of edges) {
    if (!bad.has(`${e.source}|${e.target}`)) continue;
    const a = positions.get(e.source);
    const b = positions.get(e.target);
    if (!a || !b) continue;
    lines.push(`M${a.x.toFixed(1)},${a.y.toFixed(1)}L${b.x.toFixed(1)},${b.y.toFixed(1)}`);
    hot.add(e.source);
    hot.add(e.target);
  }
  const texts: { id: string; x: number; y: number; text: string; warn: boolean }[] = [];
  for (const [id, n] of Object.entries(data.nodes)) {
    if (!visible.has(id)) continue;
    const p = positions.get(id);
    if (!p) continue;
    const warn = hot.has(id) || (n.rto_declared != null && n.rto_required != null && n.rto_declared > n.rto_required);
    if (!labels && !warn) continue;
    const text = nodeContinuityLabel(n);
    if (text) texts.push({ id, x: p.x, y: p.y + nodeR + 27, text, warn });
  }
  return (
    <g className="pointer-events-none" data-testid="livello-continuita">
      {lines.length > 0 && <path d={lines.join("")} className="fill-none stroke-danger" strokeWidth={2.6} />}
      {texts.map((x) => (
        <text key={x.id} x={x.x} y={x.y} textAnchor="middle" className={`font-mono text-[10px] ${x.warn ? "fill-danger font-bold" : "fill-muted"}`}>
          {x.text}
        </text>
      ))}
    </g>
  );
});
