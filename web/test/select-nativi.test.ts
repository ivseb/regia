// Nessun <select> nativo fuori dal selettore comune (src/select.tsx): le tendine senza ricerca sono
// scomode con molte voci. Un'eccezione si dichiara con un commento «select-nativo: motivo» sulla
// riga stessa o su una delle due righe sopra.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative, resolve } from "node:path";

const root = resolve(import.meta.dirname, "..", "src");

function files(dir: string): string[] {
  return readdirSync(dir).flatMap((n) => {
    const p = join(dir, n);
    return statSync(p).isDirectory() ? files(p) : /\.(tsx|jsx|ts)$/.test(n) ? [p] : [];
  });
}

test("nessun <select> nativo fuori da src/select.tsx, salvo eccezioni commentate", () => {
  const bad: string[] = [];
  for (const f of files(root)) {
    if (relative(root, f) === "select.tsx") continue;
    const lines = readFileSync(f, "utf8").split("\n");
    lines.forEach((l, i) => {
      if (!/<select(\s|>|$)/.test(l)) return;
      const near = lines.slice(Math.max(0, i - 2), i + 1).join("\n");
      if (!/select-nativo:\s*\S/.test(near)) bad.push(`${relative(root, f)}:${i + 1}`);
    });
  }
  assert.deepEqual(bad, [], `<select> nativo non ammesso (usare Select/EntitySelect da src/select.tsx): ${bad.join(", ")}`);
});
