// Verifica i temi di web/src/themes.css (replica/design.md, "Temi e font"):
// - ogni tema definisce le stesse variabili di Atlante, in chiaro e in scuro;
// - il contrasto WCAG 2.x delle coppie testo/fondo è almeno AA (4,5:1), AAA (7:1) per
//   i temi ad alto contrasto; i bordi dei controlli almeno 3:1 (criterio 1.4.11);
// - i colori dei moduli (--m-*) almeno 3:1 su fondo e barra laterale (--side), 4,5:1 per i temi
//   ad alto contrasto; --chip-ink (icone e testo breve in grassetto sui fondi pieni: contatori,
//   iniziali, targhette) almeno 4,5:1 su ciascuno.
// Esce con codice 1 se qualcosa non va. Gira prima della build del frontend.
// Uso: node scripts/check-contrast.mjs [percorso di themes.css] [--quiet]
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const args = process.argv.slice(2);
const quiet = args.includes("--quiet");
const file = args.find((a) => !a.startsWith("--")) ?? resolve(here, "../web/src/themes.css");

// Temi che promettono AAA sul testo.
const AAA_THEMES = new Set(["contrasto"]);
const BASE = "atlante";

// [primo piano, fondo, tipo]: "text" = testo (AA 4,5 / AAA 7), "ui" = componenti (3:1),
// "label" = testo breve in grassetto su un fondo pieno (4,5:1 in tutti i temi).
// Colori dei moduli: servono per fondi, bordi e icone (componenti grafici, 1.4.11).
const MODULES = ["demand", "work", "budget", "knowledge", "devsecops", "grc", "arch", "platform"];

const PAIRS = [
  ["fg", "bg", "text"],
  ["muted", "bg", "text"],
  ["accent", "bg", "text"],
  ["flag", "bg", "text"],
  ["ok", "bg", "text"],
  ["danger", "bg", "text"],
  ["fg", "field", "text"],
  ["muted", "field", "text"],
  ["fg", "sel", "text"],
  ["muted", "sel", "text"],
  ["accent", "sel", "text"],
  ["accent-fg", "accent", "text"],
  ["bg", "rule", "text"], // etichetta "QUERY" su fondo --rule (Requests.tsx)
  ["rule", "field", "ui"],
  ...MODULES.flatMap((m) => [
    [`m-${m}`, "bg", "ui"],
    [`m-${m}`, "side", "ui"],
    ["chip-ink", `m-${m}`, "label"],
  ]),
];

function parse(css) {
  css = css.replace(/\/\*[\s\S]*?\*\//g, "");
  const themes = {}; // nome -> { light: {}, dark: {} }
  for (const m of css.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    const selector = m[1].trim();
    const vars = {};
    for (const d of m[2].matchAll(/(--[a-z0-9-]+|color-scheme)\s*:\s*([^;]+);/g)) vars[d[1]] = d[2].trim();
    for (const part of selector.split(",")) {
      const skin = part.match(/\[data-skin="([a-z0-9-]+)"\]/)?.[1];
      if (!skin) continue; // ":root" è solo l'alias di Atlante prima che parta il JS
      const mode = /\[data-theme="dark"\]/.test(part) ? "dark" : "light";
      themes[skin] ??= { light: {}, dark: {} };
      Object.assign(themes[skin][mode], vars);
    }
  }
  return themes;
}

function luminance(hex) {
  const m = /^#([0-9a-f]{6})$/i.exec(hex);
  if (!m) throw new Error(`colore non esadecimale a 6 cifre: ${hex}`);
  const [r, g, b] = [0, 2, 4].map((i) => {
    const c = parseInt(m[1].slice(i, i + 2), 16) / 255;
    return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

export function contrast(a, b) {
  const [l1, l2] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (l1 + 0.05) / (l2 + 0.05);
}

const themes = parse(readFileSync(file, "utf8"));
const errors = [];
if (!themes[BASE]) errors.push(`manca il tema di base "${BASE}"`);
const keys = (o) => Object.keys(o).sort().join(" ");
const rows = [];

for (const [name, t] of Object.entries(themes)) {
  for (const mode of ["light", "dark"]) {
    if (themes[BASE] && keys(t[mode]) !== keys(themes[BASE][mode])) {
      const want = Object.keys(themes[BASE][mode]);
      const missing = want.filter((k) => !(k in t[mode]));
      const extra = Object.keys(t[mode]).filter((k) => !want.includes(k));
      errors.push(`${name} ${mode}: variabili diverse da ${BASE}` +
        (missing.length ? `; mancano ${missing.join(", ")}` : "") + (extra.length ? `; in più ${extra.join(", ")}` : ""));
      continue;
    }
    const v = mode === "dark" ? { ...t.light, ...t.dark } : t.light;
    const expected = mode === "dark" ? "dark" : "light";
    if (v["color-scheme"] !== expected) errors.push(`${name} ${mode}: color-scheme deve essere ${expected}`);
    const aaa = AAA_THEMES.has(name);
    for (const [fg, bg, kind] of PAIRS) {
      let ratio;
      try {
        ratio = contrast(v[`--${fg}`], v[`--${bg}`]);
      } catch (e) {
        errors.push(`${name} ${mode}: ${e.message}`);
        continue;
      }
      // Nei temi ad alto contrasto anche i componenti grafici chiedono 4,5:1.
      const min = kind === "label" ? 4.5 : kind === "ui" ? (aaa ? 4.5 : 3) : aaa ? 7 : 4.5;
      const level = kind !== "text" ? (ratio >= min ? "ok" : "NO") : ratio >= 7 ? "AAA" : ratio >= 4.5 ? "AA" : "NO";
      rows.push({ tema: name, modo: mode === "dark" ? "scuro" : "chiaro", coppia: `${fg} su ${bg}`, rapporto: ratio.toFixed(2), livello: level });
      if (ratio < min) {
        errors.push(`${name} ${mode === "dark" ? "scuro" : "chiaro"}: ${fg} su ${bg} = ${ratio.toFixed(2)}:1, minimo ${min}:1`);
      }
    }
  }
}

if (!quiet) {
  const w = { tema: 10, modo: 7, coppia: 22, rapporto: 9 };
  console.log("tema".padEnd(w.tema) + "modo".padEnd(w.modo) + "coppia".padEnd(w.coppia) + "rapporto".padStart(w.rapporto) + "  livello");
  for (const r of rows) {
    console.log(r.tema.padEnd(w.tema) + r.modo.padEnd(w.modo) + r.coppia.padEnd(w.coppia) + r.rapporto.padStart(w.rapporto) + "  " + r.livello);
  }
}
if (errors.length) {
  console.error("\nContrasti dei temi: problemi\n- " + errors.join("\n- "));
  process.exit(1);
}
console.log(`Contrasti dei temi: ${Object.keys(themes).length} temi, chiaro e scuro, tutti almeno AA (AAA per ${[...AAA_THEMES].join(", ")}).`);
