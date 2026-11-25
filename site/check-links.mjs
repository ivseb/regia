// Controlla i link interni di site/dist: ogni href, src e srcset che punta al sito deve
// arrivare a un file esistente; un rimando a #ancora deve trovare l'id nella pagina.
// Esce con codice 1 se un link interno è rotto. Le ancore mancanti sono avvertenze,
// a meno di STRICT_ANCHORS=1 (i documenti li modifica chi sviluppa).
//
//   node check-links.mjs            (dopo node build.mjs)
//   SITE_BASE=/ node check-links.mjs

import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const DIST = path.join(HERE, "dist");
let BASE = process.env.SITE_BASE ?? "/regia/";
if (!BASE.startsWith("/")) BASE = "/" + BASE;
if (!BASE.endsWith("/")) BASE += "/";
const STRICT = process.env.STRICT_ANCHORS === "1";

if (!fs.existsSync(DIST)) {
  console.error("site/dist non esiste: esegui prima node build.mjs");
  process.exit(1);
}

function walk(dir) {
  return fs.readdirSync(dir, { withFileTypes: true }).flatMap((e) => (e.isDirectory() ? walk(path.join(dir, e.name)) : [path.join(dir, e.name)]));
}
const files = walk(DIST);
const htmlFiles = files.filter((f) => f.endsWith(".html"));
const idCache = new Map();
function idsOf(file) {
  if (!idCache.has(file)) {
    const html = fs.readFileSync(file, "utf8");
    idCache.set(file, new Set([...html.matchAll(/\sid="([^"]+)"/g)].map((m) => m[1])));
  }
  return idCache.get(file);
}

// Da un percorso del sito (già senza base) al file in dist.
function toFile(p) {
  const clean = decodeURI(p);
  const abs = path.join(DIST, clean);
  if (!abs.startsWith(DIST)) return null;
  if (clean === "" || clean.endsWith("/")) {
    const idx = path.join(abs, "index.html");
    return fs.existsSync(idx) ? idx : null;
  }
  if (fs.existsSync(abs) && fs.statSync(abs).isFile()) return abs;
  const idx = path.join(abs, "index.html");
  return fs.existsSync(idx) ? idx : null;
}

const errors = [];
const warnings = [];
let checked = 0;

for (const file of htmlFiles) {
  const html = fs.readFileSync(file, "utf8");
  const rel = "/" + path.relative(DIST, file).split(path.sep).join("/");
  const pageDir = path.posix.dirname(BASE + rel.slice(1));
  const refs = [];
  for (const m of html.matchAll(/\s(?:href|src)="([^"]*)"/g)) refs.push(m[1]);
  for (const m of html.matchAll(/\ssrcset="([^"]*)"/g)) for (const part of m[1].split(",")) refs.push(part.trim().split(/\s+/)[0]);
  for (const raw of refs) {
    const ref = raw.replace(/&amp;/g, "&");
    if (!ref || /^[a-z][a-z0-9+.-]*:/i.test(ref) || ref.startsWith("//")) continue; // esterni
    checked++;
    const [pathPart, hash] = ref.split("#");
    let target; // percorso assoluto sul server
    if (pathPart === "") target = BASE + rel.slice(1);
    else if (pathPart.startsWith("/")) target = pathPart;
    else target = path.posix.join(pageDir, pathPart) + (pathPart.endsWith("/") ? "/" : "");
    target = target.split("?")[0];
    if (!target.startsWith(BASE)) {
      errors.push(`${rel}: «${raw}» punta fuori dalla base ${BASE}`);
      continue;
    }
    const dest = toFile(target.slice(BASE.length));
    if (!dest) {
      errors.push(`${rel}: «${raw}» non esiste`);
      continue;
    }
    if (hash && dest.endsWith(".html") && !idsOf(dest).has(decodeURIComponent(hash))) {
      (STRICT ? errors : warnings).push(`${rel}: «${raw}» ancora non trovata`);
    }
  }
}

console.log(`controllati ${checked} link in ${htmlFiles.length} pagine`);
for (const w of warnings) console.warn("avvertenza: " + w);
if (errors.length) {
  for (const e of errors) console.error("errore: " + e);
  console.error(`${errors.length} link interni rotti`);
  process.exit(1);
}
console.log("nessun link interno rotto");
