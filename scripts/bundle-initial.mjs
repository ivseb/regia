// Peso del pacchetto iniziale: i file che index.html carica subito (script, modulepreload,
// stili), in byte compressi con gzip. Serve a vedere quanto cresce con una modifica.
// Uso: node scripts/bundle-initial.mjs (dopo `pnpm build` nella cartella web).
import { readFileSync } from "node:fs";
import { gzipSync } from "node:zlib";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const dist = resolve(dirname(fileURLToPath(import.meta.url)), "..", "internal", "webui", "dist", "app");
const html = readFileSync(resolve(dist, "index.html"), "utf8");
const files = [...new Set([...html.matchAll(/(?:src|href)="\/?(assets\/[^"]+\.(?:js|css))"/g)].map((m) => m[1]))];
let total = 0;
for (const f of files) {
  const gz = gzipSync(readFileSync(resolve(dist, f)), { level: 9 }).length;
  total += gz;
  console.log(`${f.padEnd(48)} ${(gz / 1024).toFixed(2)} kB gzip`);
}
console.log(`${"totale iniziale".padEnd(48)} ${(total / 1024).toFixed(2)} kB gzip`);
