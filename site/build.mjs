// Genera site/dist: home e installazione in italiano e inglese, e la documentazione resa
// dai file docs/**/*.md letti al momento della build (niente testo copiato a mano).
//
//   node build.mjs                 base /regia/ (GitHub Pages)
//   SITE_BASE=/ node build.mjs     base alla radice, per provare in locale
//
// Dipendenza: solo `marked`.

import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { Marked } from "marked";
import { it, en, REPO } from "./content.mjs";
import { landing, MODULE_KEYS } from "./landing.mjs";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const ROOT = path.resolve(HERE, "..");
const DOCS = path.join(ROOT, "docs");
const DIST = path.join(HERE, "dist");
const STATIC = path.join(HERE, "static");

// Base dei percorsi: il sito sta sotto /regia/ su GitHub Pages.
const BASE = normalizeBase(process.env.SITE_BASE ?? "/regia/");
const ORIGIN = "https://ivseb.github.io";
const EXCLUDED_DOCS = new Set(["sso-kanidm"]);
const DOCS_DIR = "documentazione";

function normalizeBase(b) {
  if (!b.startsWith("/")) b = "/" + b;
  if (!b.endsWith("/")) b += "/";
  return b;
}
const url = (p = "") => BASE + p;
const esc = (s) => String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
// `codice` nei testi del sito.
const inline = (s) => esc(s).replace(/`([^`]+)`/g, "<code>$1</code>");

/* ------------------------------------------------------------------ documenti */

function walk(dir) {
  const out = [];
  for (const ent of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, ent.name);
    if (ent.isDirectory()) out.push(...walk(p));
    else out.push(p);
  }
  return out;
}

function plainText(md) {
  return md
    .replace(/!\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/[`*_>#]/g, "")
    .replace(/\s+/g, " ")
    .trim();
}

function loadDocs() {
  const docs = new Map(); // chiave -> { key, file, md, title, description }
  const assets = []; // file non markdown di docs/ (immagini)
  for (const file of walk(DOCS)) {
    const rel = path.relative(DOCS, file).split(path.sep).join("/");
    if (!rel.endsWith(".md")) {
      if (/\.(png|jpe?g|gif|svg|webp)$/i.test(rel)) assets.push(rel);
      continue;
    }
    const key = rel.slice(0, -3);
    if (EXCLUDED_DOCS.has(key)) continue;
    const md = fs.readFileSync(file, "utf8");
    const h1 = md.match(/^#\s+(.+?)\s*$/m);
    const title = plainText(h1 ? h1[1] : key);
    const para = md
      .split(/\n{2,}/)
      .map((b) => b.trim())
      .find((b) => b && !/^(#|\||```|>|[-*]\s|\d+\.\s)/.test(b));
    const description = para ? plainText(para).slice(0, 180) : "";
    docs.set(key, { key, file, rel, md, title, description });
  }
  return { docs, assets };
}

const { docs, assets: docAssets } = loadDocs();

const GUIDE_ORDER = [
  "operativo", "import", "devsecops-github", "devsecops-gitlab", "devsecops-gitea", "devsecops-api",
  "budget-api", "benchmark", "licenza", "licenze-dipendenze",
];
function ordered() {
  const guides = [], dev = [];
  for (const d of docs.values()) (d.key === "sviluppo" || d.key.startsWith("sviluppo/") ? dev : guides).push(d);
  const gi = (k) => { const i = GUIDE_ORDER.indexOf(k); return i < 0 ? 999 : i; };
  guides.sort((a, b) => gi(a.key) - gi(b.key) || a.title.localeCompare(b.title, "it"));
  // «Per sviluppatori»: prima l'indice (sviluppo.md), poi gli argomenti nell'ordine in cui lo stesso indice li elenca.
  const index = docs.get("sviluppo");
  const seen = new Map();
  if (index) {
    let n = 0;
    for (const m of index.md.matchAll(/\]\((sviluppo\/[^)#\s]+)\.md/g)) if (!seen.has(m[1])) seen.set(m[1], n++);
  }
  const di = (k) => (k === "sviluppo" ? -1 : seen.has(k) ? seen.get(k) : 999);
  dev.sort((a, b) => di(a.key) - di(b.key) || a.title.localeCompare(b.title, "it"));
  return { guides, dev };
}
const { guides: GUIDES, dev: DEVDOCS } = ordered();

const docUrl = (key) => url(`${DOCS_DIR}/${key}/`);
const sourceUrl = (rel) => `${REPO}/blob/main/${rel}`;

// Risolve un link o un'immagine di un documento. Restituisce { kind, href }.
function resolveHref(href, fromKey) {
  if (/^[a-z][a-z0-9+.-]*:/i.test(href)) return { kind: "external", href };
  if (href.startsWith("#")) return { kind: "anchor", href };
  const [p, hash] = splitHash(href);
  const fromDir = path.posix.dirname(`docs/${fromKey}.md`);
  const target = path.posix.normalize(path.posix.join(fromDir, decodeURI(p)));
  if (target.startsWith("..")) return { kind: "none" };
  if (target === "replica" || target.startsWith("replica/")) return { kind: "none" };
  if (target.startsWith("docs/") && target.endsWith(".md")) {
    const key = target.slice(5, -3);
    return docs.has(key) ? { kind: "page", href: docUrl(key) + hash } : { kind: "none" };
  }
  if (target.startsWith("docs/")) {
    const rel = target.slice(5);
    if (docAssets.includes(rel)) return { kind: "asset", href: url(`${DOCS_DIR}/${rel}`) };
    return { kind: "none" };
  }
  const isDir = p.endsWith("/") || (fs.existsSync(path.join(ROOT, target)) && fs.statSync(path.join(ROOT, target)).isDirectory());
  return { kind: "repo", href: `${REPO}/${isDir ? "tree" : "blob"}/main/${target.replace(/\/$/, "")}${hash}` };
}
function splitHash(h) {
  const i = h.indexOf("#");
  return i < 0 ? [h, ""] : [h.slice(0, i), h.slice(i)];
}

// Identificativo di un titolo, come lo fa GitHub: così i rimandi `file.md#sezione` restano validi.
function slugify(text) {
  return text.toLowerCase().replace(/[^\p{L}\p{N}\s_-]/gu, "").trim().replace(/\s/g, "-");
}

function renderDoc(doc) {
  const used = new Map();
  const m = new Marked({ gfm: true });
  m.use({
    renderer: {
      html({ text }) { return esc(text); }, // l'HTML dei documenti è testo
      heading({ tokens, depth }) {
        const inner = this.parser.parseInline(tokens);
        const plain = inner.replace(/<[^>]+>/g, "").replace(/&amp;/g, "&").replace(/&lt;/g, "<").replace(/&gt;/g, ">").replace(/&quot;/g, '"').replace(/&#39;/g, "'");
        let id = slugify(plain) || "sezione";
        const n = used.get(id) ?? 0;
        used.set(id, n + 1);
        if (n) id = `${id}-${n}`;
        const anchor = depth > 1 ? ` <a class="anchor" href="#${id}" aria-label="Link a questa sezione">#</a>` : "";
        return `<h${depth} id="${id}">${inner}${anchor}</h${depth}>\n`;
      },
      link({ href, title, tokens }) {
        const inner = this.parser.parseInline(tokens);
        const r = resolveHref(href, doc.key);
        if (r.kind === "none") return inner; // niente pagina pubblicata: solo testo
        const t = title ? ` title="${esc(title)}"` : "";
        const ext = r.kind === "external" || r.kind === "repo" ? ' rel="noopener"' : "";
        return `<a href="${esc(r.href)}"${t}${ext}>${inner}</a>`;
      },
      image({ href, title, text }) {
        const r = resolveHref(href, doc.key);
        if (r.kind !== "asset" && r.kind !== "external") return esc(text);
        const t = title ? ` title="${esc(title)}"` : "";
        return `<img src="${esc(r.href)}" alt="${esc(text)}"${t} loading="lazy">`;
      },
    },
  });
  let html = m.parse(doc.md);
  html = html
    .replace(/<table>/g, '<div class="table-wrap" tabindex="0" role="region" aria-label="Tabella scorrevole"><table>')
    .replace(/<\/table>/g, "</table></div>")
    .replace(/<pre>/g, '<pre tabindex="0">');
  return html;
}

/* ------------------------------------------------------------------ schermate */

/* ------------------------------------------------------------------ disegni */

// Dimensioni delle schermate (static/img/*.webp, tutte 2000×1250).
const SHOT_W = 2000, SHOT_H = 1250;
const shotExists = (id) => fs.existsSync(path.join(STATIC, "img", `${id}.webp`));

// Icone dei moduli: tratti da 2 px, angoli vivi, griglia 24×24.
const ICONS = {
  demand: `<path d="M3 4h18l-7 8.5V19l-4 2v-8.5z"/>`,
  projects: `<path d="M3 4h10v4H3zM8 10h9v4H8zM13 16h8v4h-8z"/>`,
  work: `<path d="M3 3h5v18H3zM10 3h5v11h-5zM17 3h4v7h-4z"/>`,
  budget: `<path d="M11 4a8 8 0 1 0 8 8h-8z"/><path d="M14 2v7h7a7 7 0 0 0-7-7z"/>`,
  knowledge: `<path d="M3 4h6a3 3 0 0 1 3 3v14a2.5 2.5 0 0 0-2.5-2.5H3zM21 4h-6a3 3 0 0 0-3 3v14a2.5 2.5 0 0 1 2.5-2.5H21z"/>`,
  devsecops: `<path d="M12 2.5l8 3v6c0 5-3.4 8.6-8 10-4.6-1.4-8-5-8-10v-6z"/><path d="M8.5 12l2.5 2.5 4.5-5"/>`,
  grc: `<circle cx="12" cy="12" r="9"/><circle cx="12" cy="12" r="4.5"/><path d="M12 12h.01"/>`,
  arch: `<path d="M4 4h5v5H4zM15 4h5v5h-5zM9.5 15h5v5h-5z"/><path d="M6.5 9v3h11V9M12 12v3"/>`,
  platform: `<path d="M12 3l9 4.5-9 4.5-9-4.5z"/><path d="M3 12l9 4.5 9-4.5M3 16.5l9 4.5 9-4.5"/>`,
};
const icon = (m, cls = "ico") =>
  `<svg class="${cls}" viewBox="0 0 24 24" aria-hidden="true" focusable="false"><g fill="none" stroke="currentColor" stroke-width="2" stroke-linejoin="miter" stroke-linecap="square">${ICONS[m]}</g></svg>`;

// Curve di livello (come su una carta topografica) generate in modo deterministico.
function contours({ cx, cy, rings, step, seed, cls = "topo" }) {
  const out = [];
  for (let k = 1; k <= rings; k++) {
    const r = k * step;
    const pts = [];
    for (let i = 0; i < 72; i++) {
      const t = (i / 72) * Math.PI * 2;
      const w = 1 + 0.13 * Math.sin(3 * t + seed) + 0.07 * Math.sin(5 * t + seed * 2 + k * 0.35) + 0.04 * Math.cos(2 * t + k * 0.6);
      pts.push(`${(cx + Math.cos(t) * r * w * 1.35).toFixed(1)},${(cy + Math.sin(t) * r * w).toFixed(1)}`);
    }
    out.push(`<path d="M${pts.join("L")}Z"/>`);
  }
  return `<g class="${cls}">${out.join("")}</g>`;
}

// Le linee colorate che attraversano lo sfondo dell'hero: le stesse della mappa del percorso.
function heroLines() {
  const line = (m, d, delay) => `<path class="hl hl-${m}" style="--d:${delay}s" d="${d}" pathLength="1"/>`;
  return `<svg class="hero-lines" viewBox="0 0 1440 760" preserveAspectRatio="xMidYMid slice" aria-hidden="true" focusable="false">
      ${contours({ cx: 1180, cy: 150, rings: 9, step: 34, seed: 1.7 })}
      ${contours({ cx: 120, cy: 700, rings: 6, step: 40, seed: 4.2 })}
      ${line("demand", "M-20 520 H520 L640 400 H1460", 0.1)}
      ${line("budget", "M-20 600 H380 L460 520 H700 L820 400", 0.35)}
      ${line("grc", "M200 780 V700 L300 600 H860 L980 480 H1460", 0.55)}
      ${line("devsecops", "M900 780 L1040 640 H1460", 0.75)}
      ${line("arch", "M1460 300 H1180 L1060 420", 0.95)}
      ${line("work", "M-20 440 H300 L380 360 H560", 1.1)}
    </svg>`;
}

// Mappa del percorso come una mappa della metropolitana.
function journeyMap(J) {
  const Y = 190, X = [60, 195, 330, 465, 600, 735, 870, 1100];
  const lbl = (x, y, t, anchor = "middle", cls = "st-l") => `<text class="${cls}" x="${x}" y="${y}" text-anchor="${anchor}">${esc(t)}</text>`;
  const stop = (x, y, cls = "") => `<circle class="st ${cls}" cx="${x}" cy="${y}" r="8"/>`;
  const line = (m, d, delay) => `<path class="ml ml-${m}" style="--d:${delay}s" d="${d}" pathLength="1"/>`;
  return `<svg class="metro" viewBox="0 0 1200 340" role="img" aria-label="${esc(J.aria)}">
      ${line("budget", "M525 190 L625 90 H800", 0.5)}
      ${line("grc", "M395 190 L485 280 H760 L850 190", 0.3)}
      ${line("devsecops", "M790 190 L860 120 H1140", 0.8)}
      ${line("arch", "M985 190 L1075 280 H1140", 1)}
      ${line("demand", `M${X[0]} ${Y} H${X[7]}`, 0)}
      <g class="stops">
        ${X.map((x, i) => (i === 6 ? `<rect class="st st-x" x="${x - 14}" y="${Y - 10}" width="28" height="20" rx="10"/>` : stop(x, Y, i === 0 || i === 7 ? "st-end" : ""))).join("")}
        ${[680, 800].map((x) => stop(x, 90)).join("")}
        ${[540, 625, 710].map((x) => stop(x, 280)).join("")}
        ${[900, 1010, 1140].map((x) => stop(x, 120)).join("")}
        ${stop(1140, 280)}
      </g>
      <g class="labels">
        ${J.main.map((t, i) => lbl(X[i], Y - 24, t, "middle", i === 0 || i === 7 ? "st-l st-big" : "st-l")).join("")}
        ${lbl(880, Y + 30, J.gate, "start", "st-note")}
        ${lbl(680, 68, J.budget[0])}${lbl(800, 68, J.budget[1])}
        ${J.grc.map((t, i) => lbl([540, 625, 710][i], 310, t)).join("")}
        ${lbl(900, 98, J.devsecops[0])}${lbl(1010, 98, J.devsecops[1])}${lbl(1185, 98, J.devsecops[2], "end")}
        ${lbl(1185, 310, J.arch[0], "end")}
      </g>
    </svg>`;
}

// Schema dell'architettura: il binario al centro, gli altri sistemi intorno.
function archDiagram(A) {
  const box = (x, y, w, h, t, cls = "") => `<g class="ad-box ${cls}"><rect x="${x}" y="${y}" width="${w}" height="${h}"/><text x="${x + w / 2}" y="${y + h / 2 + 5}" text-anchor="middle">${esc(t)}</text></g>`;
  const arrow = (x1, y1, x2, y2) => `<path class="ad-arrow" d="M${x1} ${y1} H${x2}" marker-end="url(#ad-tip)"/>`;
  const left = A.left.map((t, i) => box(10, 95 + i * 90, 170, 50, t, "ad-ext")).join("");
  const right = A.right.map((t, i) => box(640, 70 + i * 70, 210, 50, t, "ad-ext")).join("");
  const inside = A.inside.map((t, i) => box(270, 110 + i * 52, 280, 40, t, i === 3 ? "ad-pg" : "ad-in")).join("");
  return `<svg class="archd" viewBox="0 0 860 400" role="img" aria-label="${esc(A.aria)}">
      <defs><marker id="ad-tip" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M0 0L10 5L0 10z" class="ad-tipm"/></marker></defs>
      <rect class="ad-main" x="250" y="40" width="320" height="290"/>
      <text class="ad-title" x="270" y="78">${esc(A.box)}</text><text class="ad-sub" x="550" y="78" text-anchor="end">${esc(A.boxSub)}</text>
      ${inside}
      ${left}${right}
      ${arrow(180, 120, 246)}${arrow(180, 210, 246)}
      ${[95, 165, 235].map((y) => `<path class="ad-arrow" d="M574 ${y} H636" marker-start="url(#ad-tip)" marker-end="url(#ad-tip)"/>`).join("")}
      <path class="ad-arrow" d="M410 330 V352" marker-end="url(#ad-tip)"/>
      <g class="ad-disk"><rect x="150" y="356" width="520" height="40"/><text x="410" y="381" text-anchor="middle">${esc(A.bottom)}</text></g>
    </svg>`;
}

// Una finestra del prodotto, con barra e ombra piena.
function win(img, alt, cls = "", addr = "regia.azienda.it", eager = false) {
  return `<figure class="win ${cls}"><div class="win-bar" aria-hidden="true"><i></i><i></i><i></i><span>${esc(addr)}</span></div><img src="${url(`img/${img}.webp`)}" alt="${esc(alt)}" width="${SHOT_W}" height="${SHOT_H}"${eager ? ' fetchpriority="high"' : ' loading="lazy" decoding="async"'}></figure>`;
}

/* ------------------------------------------------------------------ modello di pagina */

const LOGO = (cls = "") =>
  `<svg class="${cls}" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32" aria-hidden="true" focusable="false"><rect class="logo-bg" width="32" height="32"/><path class="logo-r" fill-rule="evenodd" d="M8 6h14v13h-10v7h-4zM12 10v5h6v-5z"/><path class="logo-leg" d="M14 19h5l5 7h-5z"/></svg>`;

const PAGES = []; // { file, html }
function emit(relPath, html) {
  PAGES.push({ file: relPath, html });
}

// Percorsi (senza base) delle tre pagine tradotte.
const P = {
  it: { home: "", install: "installazione/", docs: `${DOCS_DIR}/` },
  en: { home: "en/", install: "en/installation/", docs: "en/documentation/" },
};

// Piccolo script: animazioni all'ingresso nello schermo e schede dell'installazione.
// Senza JavaScript (o con «riduci movimento») tutto resta visibile e fermo.
const SCRIPT = `<script>
(() => {
  const d = document.documentElement;
  if (!matchMedia("(prefers-reduced-motion: reduce)").matches && "IntersectionObserver" in window) {
    d.classList.add("anim");
    const io = new IntersectionObserver((es) => es.forEach((e) => { if (e.isIntersecting) { const t = e.target; t.classList.add("in"); io.unobserve(t); setTimeout(() => t.removeAttribute("data-reveal"), 1800); } }), { rootMargin: "0px 0px -12% 0px" });
    document.querySelectorAll("[data-reveal]").forEach((el) => io.observe(el));
  }
  document.querySelectorAll("[data-tabs]").forEach((box) => {
    const tabs = [...box.querySelectorAll('[role="tab"]')];
    const show = (t) => tabs.forEach((x) => { const on = x === t; x.setAttribute("aria-selected", on); x.tabIndex = on ? 0 : -1; document.getElementById(x.getAttribute("aria-controls")).hidden = !on; });
    tabs.forEach((t, i) => {
      t.addEventListener("click", () => show(t));
      t.addEventListener("keydown", (e) => { const k = { ArrowRight: 1, ArrowLeft: -1 }[e.key]; if (k) { const n = tabs[(i + k + tabs.length) % tabs.length]; show(n); n.focus(); } });
    });
    box.classList.add("js");
    show(tabs[0]);
  });
})();
</script>`;

function layout({ L, kind, path: pagePath, title, description, body, bodyClass = "", ogImage }) {
  const other = L.lang === "it" ? en : it;
  const here = L.lang;
  const F = landing[here].footer;
  const sw = (lang) => {
    const target = kind === "doc" ? P[lang].docs : P[lang][kind] ?? P[lang].home;
    return lang === here
      ? `<span lang="${lang}" aria-current="true">${lang}</span>`
      : `<a href="${url(target)}" lang="${lang}" hreflang="${lang}" aria-label="${lang === "it" ? "Italiano" : "English"}">${lang}</a>`;
  };
  const pageUrl = ORIGIN + url(pagePath);
  const altLinks = ["it", "en"]
    .map((l) => {
      const target = kind === "doc" ? P[l].docs : P[l][kind] ?? P[l].home;
      return `<link rel="alternate" hreflang="${l}" href="${ORIGIN + url(target)}">`;
    })
    .join("\n  ");
  const cur = (k) => (kind === k ? ' aria-current="page"' : "");
  const fullTitle = title === L.name ? `${L.name}: ${L.tagline}` : `${title} · ${L.name}`;
  const og = ogImage ? `<meta property="og:image" content="${ORIGIN + url(ogImage)}">\n  <meta name="twitter:card" content="summary_large_image">` : `<meta name="twitter:card" content="summary">`;
  void other;
  return `<!doctype html>
<html lang="${L.htmlLang}">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>${esc(fullTitle)}</title>
  <meta name="description" content="${esc(description)}">
  <meta name="color-scheme" content="light dark">
  <meta name="theme-color" content="#0b0c14">
  <link rel="icon" href="${url("logo.svg")}" type="image/svg+xml">
  <link rel="canonical" href="${pageUrl}">
  ${altLinks}
  <meta property="og:type" content="website">
  <meta property="og:site_name" content="Regia">
  <meta property="og:title" content="${esc(fullTitle)}">
  <meta property="og:description" content="${esc(description)}">
  <meta property="og:url" content="${pageUrl}">
  <meta property="og:locale" content="${L.lang === "it" ? "it_IT" : "en_US"}">
  ${og}
  <link rel="preload" href="${url("fonts/atkinson-hyperlegible-next-latin-wght-normal.woff2")}" as="font" type="font/woff2" crossorigin>
  <link rel="stylesheet" href="${url("site.css")}">
</head>
<body class="${bodyClass}">
  <a class="skip-link" href="#contenuto">${esc(L.skip)}</a>
  <header class="site-header">
    <div class="wrap">
      <a class="brand" href="${url(P[here].home)}" aria-label="${L.name}">${LOGO()}<span>${L.name}</span></a>
      <nav class="site-nav" aria-label="${esc(L.menu)}">
        <a href="${url(P[here].home)}#funzionalita">${esc(L.nav.features)}</a>
        <a href="${url(P[here].install)}"${cur("install")}>${esc(L.nav.install)}</a>
        <a href="${url(P[here].docs)}"${cur("doc")}>${esc(L.nav.docs)}</a>
        <a href="${REPO}" rel="noopener">${esc(L.nav.code)}</a>
        <div class="lang" role="group" aria-label="${esc(L.langLabel)}">${sw("it")}${sw("en")}</div>
      </nav>
    </div>
  </header>
  <main id="contenuto" tabindex="-1">
${body}
  </main>
  <footer class="site-footer">
    <div class="wrap">
      <div class="foot-brand">
        <a class="brand" href="${url(P[here].home)}" aria-label="${L.name}">${LOGO()}<span>${L.name}</span></a>
        <p>${esc(L.tagline)}</p>
        <div class="foot-stripes" aria-hidden="true">${MODULE_KEYS.map((m) => `<i class="bg-${m}"></i>`).join("")}</div>
      </div>
      <div class="foot-cols">
        <div><h2>${esc(F.product)}</h2><ul>
          <li><a href="${url(P[here].home)}#funzionalita">${esc(F.features)}</a></li>
          <li><a href="${url(P[here].install)}">${esc(F.install)}</a></li>
        </ul></div>
        <div><h2>${esc(F.docs)}</h2><ul>
          <li><a href="${url(P[here].docs)}">${esc(F.guides)}</a></li>
          ${docs.has("operativo") ? `<li><a href="${docUrl("operativo")}" lang="it">${esc(F.ops)}</a></li>` : ""}
        </ul></div>
        <div><h2>${esc(F.project)}</h2><ul>
          <li><a href="${REPO}" rel="noopener">${esc(F.code)}</a></li>
          <li><a href="${REPO}/blob/main/LICENSE" rel="noopener">${esc(F.license)}</a></li>
          <li><a href="${REPO}/issues" rel="noopener">${esc(F.issues)}</a></li>
        </ul></div>
      </div>
      <p class="foot-line">${esc(F.line)}</p>
    </div>
  </footer>
${SCRIPT}
</body>
</html>
`;
}

// Fascia scura in cima alle pagine interne (installazione, documentazione).
function pageBand({ label, title, lead = "", crumbs = "" }) {
  return `<div class="band"><div class="band-bg" aria-hidden="true"><svg viewBox="0 0 1440 260" preserveAspectRatio="xMidYMid slice">${contours({ cx: 1240, cy: 40, rings: 7, step: 30, seed: 2.4 })}<path class="hl hl-demand" d="M-20 230 H900 L980 150 H1460" pathLength="1"/><path class="hl hl-grc" d="M600 290 L700 190 H1460" pathLength="1" style="--d:.3s"/></svg></div>
    <div class="wrap">${crumbs}${label ? `<p class="label">${esc(label)}</p>` : ""}<h1>${title}</h1>${lead ? `<p class="lead">${lead}</p>` : ""}</div></div>`;
}

/* ------------------------------------------------------------------ home */

const QUICK_DOCKER = `git clone https://github.com/ivseb/regia && cd regia
docker build -f deploy/Dockerfile -t regia .
docker run -d --name regia -p 8080:8080 \\
  -v regia-data:/data --stop-timeout 60 regia`;
const QUICK_BINARY = `git clone https://github.com/ivseb/regia && cd regia
make web build
./bin/regia serve --data ./data`;

function homePage(L) {
  const here = L.lang;
  const T = landing[here];
  const H = T.hero;
  const hasBench = docs.has("benchmark");

  const hero = `
    <section class="hero" aria-labelledby="h-hero">
      ${heroLines()}
      <div class="wrap hero-text">
        <p class="kicker"><span class="dot" aria-hidden="true"></span>${esc(H.kicker)}</p>
        <h1 id="h-hero">${esc(H.titleA)} <mark>${esc(H.titleMark)}</mark> ${esc(H.titleB)}</h1>
        <p class="lead">${esc(H.lead)}</p>
        <div class="actions">
          <a class="btn primary" href="${url(P[here].install)}">${esc(H.primary)}</a>
          <a class="btn ghost" href="#prodotto">${esc(H.secondary)}</a>
          <a class="btn link" href="${REPO}" rel="noopener">${esc(H.code)} →</a>
        </div>
      </div>
      <div class="wrap hero-stage">
        <div class="stage">
          ${win("roadmap-scuro", H.shotBack, "w-back", "regia.azienda.it/roadmap", true)}
          ${win("richieste", H.shotFront, "w-front", "regia.azienda.it/richieste", true)}
          <p class="callout c1" aria-hidden="true"><i class="bg-demand"></i>${esc(H.callouts[0])}</p>
          <p class="callout c2" aria-hidden="true"><i class="bg-grc"></i>${esc(H.callouts[1])}</p>
          <p class="callout c3" aria-hidden="true"><i class="bg-budget"></i>${esc(H.callouts[2])}</p>
        </div>
      </div>
    </section>`;

  const stamps = `
    <section class="stamps" aria-labelledby="h-stamps"><div class="wrap">
      <h2 id="h-stamps" class="label">${esc(T.stamps.label)}</h2>
      <ul>${T.stamps.items.map((s, i) => `<li data-reveal style="--i:${i}">${esc(s)}</li>`).join("")}</ul>
      <p class="note">${esc(T.stamps.note)}</p>
    </div></section>`;

  const J = T.journey;
  const journey = `
    <section class="section journey" id="percorso" aria-labelledby="h-journey"><div class="wrap">
      <div class="head"><p class="label">${esc(J.label)}</p><h2 id="h-journey">${esc(J.title)}</h2><p class="lead">${esc(J.lead)}</p></div>
      <p class="metro-hint" aria-hidden="true">${esc(J.hint)} →</p>
      <div class="metro-wrap" data-reveal tabindex="0" role="group" aria-labelledby="h-journey">${journeyMap(J)}</div>
      <ul class="legend">${J.legend.map(([m, t]) => `<li><i class="bg-${m}"></i>${esc(t)}</li>`).join("")}</ul>
    </div></section>`;

  const tourItems = T.tour.items
    .filter((t) => shotExists(t.img))
    .map((t, i) => {
      const second = t.img2 && shotExists(t.img2) ? win(t.img2, t.alt2, "w-second") : "";
      return `
      <article class="tour-row m-${t.m}${i % 2 ? " flip" : ""}">
        <div class="tour-text" data-reveal>
          <p class="tag">${icon(t.m)}<span>${esc(t.tag)}</span></p>
          <h3>${esc(t.title)}</h3>
          <p>${inline(t.text)}</p>
          <ul class="ticks">${t.points.map((p) => `<li>${inline(p)}</li>`).join("")}</ul>
        </div>
        <div class="tour-media${second ? " has-second" : ""}" data-reveal>
          <div class="tint" aria-hidden="true"></div>
          ${win(t.img, t.alt, "w-main")}
          ${second}
        </div>
      </article>`;
    })
    .join("");
  const tour = `
    <section class="section tour" id="prodotto" aria-labelledby="h-tour"><div class="wrap">
      <div class="head"><p class="label">${esc(T.tour.label)}</p><h2 id="h-tour">${esc(T.tour.title)}</h2></div>
      ${tourItems}
    </div></section>`;

  const N = T.numbers;
  const numbers = `
    <section class="numbers" aria-labelledby="h-num">
      <svg class="numbers-bg" viewBox="0 0 1440 420" preserveAspectRatio="xMidYMid slice" aria-hidden="true" focusable="false">${contours({ cx: 260, cy: 380, rings: 10, step: 36, seed: 0.8, cls: "topo topo-light" })}${contours({ cx: 1300, cy: 40, rings: 6, step: 34, seed: 3.1, cls: "topo topo-light" })}</svg>
      <div class="wrap">
        <p class="label">${esc(N.label)}</p>
        <h2 id="h-num">${esc(N.title)}</h2>
        <dl>${N.items.map(([a, b], i) => `<div data-reveal style="--i:${i}"><dt>${esc(a)}</dt><dd>${esc(b)}</dd></div>`).join("")}</dl>
        ${hasBench ? `<p><a href="${docUrl("benchmark")}" lang="it">${esc(N.link)} →</a></p>` : ""}
      </div>
    </section>`;

  const M = T.modules;
  const modules = `
    <section class="section" id="funzionalita" aria-labelledby="h-mod"><div class="wrap">
      <div class="head"><p class="label">${esc(M.label)}</p><h2 id="h-mod">${esc(M.title)}</h2><p class="lead">${esc(M.lead)}</p></div>
      <div class="modules">${L.modules.items
        .map(
          (m, i) => `
        <article class="module m-${MODULE_KEYS[i]}" data-reveal style="--i:${i % 4}">
          <div class="module-head"><span class="chip">${icon(MODULE_KEYS[i])}</span><div><h3>${esc(m.name)}</h3><p class="sub">${esc(m.sub)}</p></div></div>
          <ul>${m.points.map((p) => `<li>${inline(p)}</li>`).join("")}</ul>
        </article>`
        )
        .join("")}
      </div>
    </div></section>`;

  const A = T.arch;
  const arch = `
    <section class="section arch" id="architettura" aria-labelledby="h-arch"><div class="wrap">
      <div class="head"><p class="label">${esc(A.label)}</p><h2 id="h-arch">${esc(A.title)}</h2><p class="lead">${inline(A.lead)}</p></div>
      <div class="arch-grid">
        <div class="arch-fig" data-reveal>${archDiagram(A)}</div>
        <dl class="arch-points">${A.points.map(([h, p]) => `<div><dt>${esc(h)}</dt><dd>${inline(p)}</dd></div>`).join("")}</dl>
      </div>
    </div></section>`;

  const Q = T.install;
  const install = `
    <section class="section quick" id="installa" aria-labelledby="h-quick"><div class="wrap quick-grid">
      <div class="head"><p class="label">${esc(Q.label)}</p><h2 id="h-quick">${esc(Q.title)}</h2>
        <p class="note">${inline(Q.note)}</p>
        <p><a href="${url(P[here].install)}">${esc(Q.more)} →</a></p></div>
      <div class="term" data-tabs data-reveal>
        <div class="term-bar"><span class="term-dots" aria-hidden="true"><i></i><i></i><i></i></span>
          <div role="tablist" aria-label="${esc(Q.label)}">
            <button role="tab" id="tab-d-${here}" aria-controls="pan-d-${here}" aria-selected="true">${esc(Q.docker)}</button>
            <button role="tab" id="tab-b-${here}" aria-controls="pan-b-${here}" aria-selected="false">${esc(Q.binary)}</button>
          </div></div>
        <div role="tabpanel" id="pan-d-${here}" aria-labelledby="tab-d-${here}"><pre tabindex="0"><code>${esc(QUICK_DOCKER)}</code></pre></div>
        <div role="tabpanel" id="pan-b-${here}" aria-labelledby="tab-b-${here}"><pre tabindex="0"><code>${esc(QUICK_BINARY)}</code></pre></div>
      </div>
    </div></section>`;

  const C = T.cta;
  const cta = `
    <section class="cta" aria-labelledby="h-cta">
      ${heroLines()}
      <div class="wrap">
        <h2 id="h-cta">${esc(C.title)}</h2>
        <p>${esc(C.text)}</p>
        <div class="actions">
          <a class="btn primary" href="${url(P[here].install)}">${esc(C.primary)}</a>
          <a class="btn ghost" href="${url(P[here].docs)}">${esc(C.secondary)}</a>
        </div>
      </div>
    </section>`;

  const body = hero + stamps + journey + tour + numbers + modules + arch + install + cta;
  emit(P[here].home + "index.html", layout({ L, kind: "home", path: P[here].home, title: L.name, description: L.description, body, bodyClass: "home", ogImage: "img/og.png" }));
}

/* ------------------------------------------------------------------ installazione */

function installPage(L) {
  const I = L.install;
  const here = L.lang;
  const isIt = here === "it";
  const code = (s) => `<pre tabindex="0"><code>${esc(s)}</code></pre>`;
  const DOCKER_BUILD = `git clone https://github.com/ivseb/regia && cd regia
docker build -f deploy/Dockerfile -t regia .`;
  const DOCKER_RUN = `docker run -d --name regia -p 8080:8080 \\
  -v regia-data:/data --stop-timeout 60 regia`;
  const BIN_BUILD = `git clone https://github.com/ivseb/regia && cd regia
make web build`;
  const BIN_RUN = `./bin/regia serve --data ./data`;
  const BIN_PG = `REGIA_PG_BIN=/usr/lib/postgresql/18/bin ./bin/regia serve --data ./data`;
  const FIRST_DOCKER = `docker exec -it -e REGIA_ADMIN_PASSWORD regia regia tenant create --data /data \\
  --slug acme --name "ACME S.p.A." --admin-email nome@azienda.it --platform-admin`;
  const FIRST_BIN = `./bin/regia tenant create --data ./data \\
  --slug acme --name "ACME S.p.A." --admin-email nome@azienda.it --platform-admin`;
  const next = I.next.items
    .filter(([, key]) => docs.has(key))
    .map(([t, key, d]) => `<li><a href="${docUrl(key)}" lang="it">${esc(t)}</a><span>${inline(d)}</span></li>`)
    .join("");
  const noteHttp = isIt
    ? "Il cookie di sessione è `Secure`: in produzione metti Regia dietro HTTPS. Per una prova locale su http con un nome diverso da `localhost` si può avviare con `--dev` (o `REGIA_DEV=true`), che toglie anche le protezioni verso le reti interne dei webhook: solo per le prove."
    : "The session cookie is `Secure`: in production put Regia behind HTTPS. For a local trial over http with a name other than `localhost` you can start it with `--dev` (or `REGIA_DEV=true`), which also lifts the webhook protections towards internal networks: for trials only.";
  const body = `
    ${pageBand({ label: I.label, title: esc(I.title), lead: esc(I.lead) })}
    <div class="wrap page"><div class="narrow steps">

      <h2 id="docker">${esc(I.docker.title)}</h2>
      <p>${esc(I.docker.intro)}</p>${code(DOCKER_BUILD)}
      <p>${esc(I.docker.build)}</p>${code(DOCKER_RUN)}
      <p>${esc(I.docker.run)}</p>
      <ul>${I.docker.opts.map((o) => `<li>${inline(o)}</li>`).join("")}</ul>
      <h3>${esc(I.docker.health)}</h3>
      <p>${inline(I.docker.healthText)}</p>${code("curl -fsS http://localhost:8080/healthz")}
      <p>${inline(I.docker.pgvector)}</p>

      <h2 id="binario">${esc(I.binary.title)}</h2>
      <p>${esc(I.binary.intro)}</p>
      <p>${esc(I.binary.build)}</p>${code(BIN_BUILD)}
      <p>${esc(I.binary.run)}</p>${code(BIN_RUN)}
      <h3>${esc(I.binary.pg)}</h3>
      <p>${inline(I.binary.pgText)}</p>${code(BIN_PG)}
      <p>${inline(I.binary.external)}</p>

      <h2 id="primo-accesso">${esc(I.first.title)}</h2>
      <p>${inline(I.first.text)}</p>
      <p>${esc(I.first.docker)}</p>${code(FIRST_DOCKER)}
      <p>${esc(I.first.binary)}</p>${code("export REGIA_ADMIN_PASSWORD='una-password-lunga-e-casuale'\n" + FIRST_BIN)}
      <p>${inline(I.first.after)}</p>
      <p class="note">${inline(noteHttp)}</p>

      <h2 id="produzione">${esc(I.systemd.title)}</h2>
      <p>${inline(I.systemd.text)}</p>

      <h2 id="dopo">${esc(I.next.title)}</h2>
      ${isIt ? "" : `<p class="note">${esc(I.next.docsNote)}</p>`}
      <ul class="rel-list">${next}</ul>
    </div></div>`;
  emit(P[here].install + "index.html", layout({ L, kind: "install", path: P[here].install, title: I.title, description: I.description, body }));
}

/* ------------------------------------------------------------------ documentazione */

function docNav(currentKey) {
  const item = (d) => `<li><a href="${docUrl(d.key)}"${d.key === currentKey ? ' aria-current="page"' : ""}>${esc(d.title)}</a></li>`;
  return `<nav class="docs-nav" aria-label="${esc(it.docsIndex.nav)}">
      <details open id="docs-menu"><summary>${esc(it.docsIndex.nav)}</summary>
        <h2>${esc(it.docsIndex.guides)}</h2><ul>${GUIDES.map(item).join("")}</ul>
        <h2>${esc(it.docsIndex.dev)}</h2><ul>${DEVDOCS.map(item).join("")}</ul>
      </details>
    </nav>
    <script>if (window.matchMedia("(max-width: 1000px)").matches) document.getElementById("docs-menu").removeAttribute("open");</script>`;
}

// Modulo (e quindi colore e icona) a cui appartiene un documento, dal suo nome.
function docModule(key) {
  const rules = [
    [/devsecops|pipeline|sbom/, "devsecops"], [/budget|finanz/, "budget"],
    [/import|lavoro|board|timesheet|coinvolgi|delivery/, "work"], [/knowledge|licen/, "knowledge"],
    [/grc|rischi|conformit|controlli|auditor|dpo|requisiti|versioni/, "grc"],
    [/demand|percorso|processi|scorecard|regole|cabina|governance|catalogo/, "demand"], [/architett/, "arch"],
  ];
  for (const [re, m] of rules) if (re.test(key)) return m;
  return "platform";
}

function docPage(doc) {
  const L = it;
  // Il titolo va nella fascia in alto: si toglie il primo h1 del documento.
  const html = renderDoc(doc).replace(/<h1[^>]*>[\s\S]*?<\/h1>/, "");
  const isDev = doc.key === "sviluppo" || doc.key.startsWith("sviluppo/");
  const crumbs = `<p class="crumbs"><a href="${url(P.it.docs)}">${esc(L.docsIndex.nav)}</a> / ${esc(isDev ? L.docsIndex.dev : L.docsIndex.guides)}</p>`;
  const m = docModule(doc.key);
  const body = `
    ${pageBand({ label: "", title: `<span class="band-ico m-${m}">${icon(m)}</span>${esc(doc.title)}`, crumbs })}
    <div class="wrap docs-layout">
    ${docNav(doc.key)}
    <article class="article">
${html}
      <p class="source-link"><a href="${sourceUrl("docs/" + doc.rel)}" rel="noopener">${esc(L.docsIndex.source)}</a> · <code>docs/${esc(doc.rel)}</code></p>
    </article>
    </div>`;
  emit(`${DOCS_DIR}/${doc.key}/index.html`, layout({ L, kind: "doc", path: `${DOCS_DIR}/${doc.key}/`, title: doc.title, description: doc.description || L.docsIndex.description, body }));
}

function docsIndexPage(L) {
  const D = L.docsIndex;
  const isIt = L.lang === "it";
  const list = (arr) =>
    `<ul class="doc-cards"${isIt ? "" : ' lang="it"'}>${arr
      .map((d) => { const m = docModule(d.key); return `<li class="m-${m}" data-reveal><a href="${docUrl(d.key)}"><span class="chip">${icon(m)}</span><strong>${esc(d.title)}</strong>${d.description ? `<span class="desc">${esc(d.description)}</span>` : ""}</a></li>`; })
      .join("")}</ul>`;
  const notice = isIt
    ? ""
    : `<div class="notice" role="note"><strong>${esc(D.lead)}</strong>${esc(D.leadMore)}</div>`;
  const body = `
    ${pageBand({ label: D.nav, title: esc(D.title), lead: isIt ? esc(D.lead) : "" })}
    <div class="wrap page"><div>
      ${notice}
      <h2 id="guide" class="h-plain">${esc(D.guides)}</h2>
      <p>${esc(D.guidesText)}</p>
      ${list(GUIDES)}
      <h2 id="sviluppatori" class="h-plain">${esc(D.dev)}</h2>
      <p>${esc(D.devText)}</p>
      ${list(DEVDOCS)}
    </div></div>`;
  emit(P[L.lang].docs + "index.html", layout({ L, kind: "doc", path: P[L.lang].docs, title: D.title, description: D.description, body }));
}

/* ------------------------------------------------------------------ 404 */

function notFoundPage() {
  const body = `
    ${pageBand({ label: "404", title: `${esc(it.notFound.title)} · ${esc(en.notFound.title)}` })}
    <div class="wrap page"><div class="narrow">
      <p>${esc(it.notFound.text)}</p>
      <p><a href="${url(P.it.home)}">${esc(it.notFound.home)}</a> · <a href="${url(P.en.home)}">${esc(en.notFound.home)}</a></p>
    </div></div>`;
  emit("404.html", layout({ L: it, kind: "home", path: "404.html", title: it.notFound.title, description: it.description, body }));
}

/* ------------------------------------------------------------------ scrittura */

fs.rmSync(DIST, { recursive: true, force: true });
fs.mkdirSync(DIST, { recursive: true });
fs.cpSync(STATIC, DIST, { recursive: true });
for (const rel of docAssets) {
  const dest = path.join(DIST, DOCS_DIR, rel);
  fs.mkdirSync(path.dirname(dest), { recursive: true });
  fs.copyFileSync(path.join(DOCS, rel), dest);
}

for (const L of [it, en]) {
  homePage(L);
  installPage(L);
  docsIndexPage(L);
}
for (const d of docs.values()) docPage(d);
notFoundPage();

for (const { file, html } of PAGES) {
  const out = path.join(DIST, file);
  fs.mkdirSync(path.dirname(out), { recursive: true });
  fs.writeFileSync(out, html);
}
// Il sito non usa Jekyll: i file che iniziano per «_» devono servirsi tali e quali.
fs.writeFileSync(path.join(DIST, ".nojekyll"), "");

console.log(`sito generato in ${path.relative(process.cwd(), DIST) || "."}: ${PAGES.length} pagine (${docs.size} documenti, ${docAssets.length} immagini), base ${BASE}`);
