// Test del modello del grafo dell'architettura: `pnpm test` (node:test, nessuna dipendenza).
import { test } from "node:test";
import assert from "node:assert/strict";
import { adjacency, bounds, forceLayout, layeredLayout, reach, toCSV } from "../src/pages/ea/graphModel.ts";

const types = [
  { key: "calls_api", outward_name: "chiama le API di", inward_name: "espone API a", category: "dependency", impact_direction: "forward" as const },
  { key: "supports", outward_name: "supporta", inward_name: "è supportata da", category: "capability", impact_direction: "reverse" as const },
  { key: "note", outward_name: "nota", inward_name: "nota", category: "dependency", impact_direction: "none" as const },
];
const edge = (id: string, type: string, source: string, target: string) => ({ id, type, source, target });

test("dipendenze nei due versi, cicli e livelli", () => {
  // A chiama B, B chiama C, C chiama A (ciclo); A supporta la capability K.
  const edges = [edge("1", "calls_api", "A", "B"), edge("2", "calls_api", "B", "C"), edge("3", "calls_api", "C", "A"), edge("4", "supports", "A", "K"), edge("5", "note", "A", "Z")];
  const adj = adjacency(edges, types);
  // Da A dipendono: C (chiama A), K (supportata da A), poi B.
  const dep = reach(adj.dependents, "A", 0);
  assert.deepEqual(Object.fromEntries(dep), { A: 0, C: 1, K: 1, B: 2 });
  assert.deepEqual(Object.fromEntries(reach(adj.dependencies, "A", 1)), { A: 0, B: 1 });
  assert.equal(reach(adj.dependencies, "A", 0).has("Z"), false); // verso "none"
  // Solo i nodi visibili.
  assert.deepEqual([...reach(adj.dependents, "A", 0, new Set(["A", "K"])).keys()], ["A", "K"]);
});

test("layout a forze: deterministico, nodi separati, 600 nodi in fretta", () => {
  const ids = Array.from({ length: 600 }, (_, i) => `n${i}`);
  const links: [string, string][] = ids.slice(1).map((id, i) => [id, ids[Math.floor(i / 3)]]);
  const t0 = performance.now();
  const a = forceLayout(ids, links);
  const ms = performance.now() - t0;
  assert.ok(ms < 3000, `layout troppo lento: ${ms} ms`);
  const b = forceLayout(ids, links);
  assert.deepEqual(a.get("n42"), b.get("n42"));
  // Nessuna coppia sovrapposta tra i primi nodi.
  const ps = ids.slice(0, 80).map((id) => a.get(id)!);
  for (let i = 0; i < ps.length; i++)
    for (let j = i + 1; j < ps.length; j++) assert.ok(Math.hypot(ps[i].x - ps[j].x, ps[i].y - ps[j].y) > 5, `n${i} e n${j} sovrapposti`);
  // Ripartendo dalle posizioni precedenti il disegno resta riconoscibile.
  const warm = forceLayout(ids, links, { initial: a });
  const moved = ids.filter((id) => Math.hypot(warm.get(id)!.x - a.get(id)!.x, warm.get(id)!.y - a.get(id)!.y) > 200).length;
  assert.ok(moved < ids.length / 4, `${moved} nodi spostati molto`);
});

test("layout a strati: capability sopra, applicazioni sotto", () => {
  const nodes = [
    { id: "K1", key: "ARC-1", name: "Pagamenti", type: "capability", type_name: "", state: "", state_category: "" },
    { id: "K2", key: "ARC-2", name: "Bonifici", type: "capability", type_name: "", state: "", state_category: "", parent_id: "K1" },
    { id: "A", key: "ARC-3", name: "Home banking", type: "application", type_name: "", state: "", state_category: "" },
    { id: "P", key: "GRC-1", name: "Fornitore", type: "ict_provider", type_name: "", state: "", state_category: "" },
  ];
  const l = layeredLayout(nodes, [edge("1", "supports", "A", "K2")]);
  assert.ok(l.get("K1")!.y < l.get("K2")!.y && l.get("K2")!.y < l.get("A")!.y && l.get("A")!.y < l.get("P")!.y);
  const b = bounds(l.values(), 10);
  assert.ok(b.w > 0 && b.h > 0);
});

test("CSV con separatore e virgolette", () => {
  assert.equal(toCSV([["a;b", 'c"d']]), '\ufeff"a;b";"c""d"\r\n');
});
