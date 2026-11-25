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

function pngSize(file) {
  const b = fs.readFileSync(file);
  if (b.length < 24 || b.toString("ascii", 1, 4) !== "PNG") return null;
  return { w: b.readUInt32BE(16), h: b.readUInt32BE(20) };
}
function shotsFor(L) {
  const out = [];
  for (const s of L.shots.items) {
    const f = path.join(STATIC, "img", `${s.id}.png`);
    if (!fs.existsSync(f)) continue; // se il file manca la figura si omette
    out.push({ ...s, size: pngSize(f) });
  }
  return out;
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

function layout({ L, kind, path: pagePath, title, description, body, bodyClass = "", ogImage }) {
  const other = L.lang === "it" ? en : it;
  const here = L.lang;
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
  return `<!doctype html>
<html lang="${L.htmlLang}">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>${esc(fullTitle)}</title>
  <meta name="description" content="${esc(description)}">
  <meta name="color-scheme" content="light dark">
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
      <p>${esc(L.footer.line)}</p>
      <ul>
        <li><a href="${REPO}" rel="noopener">${esc(L.footer.code)}</a></li>
        <li><a href="${REPO}/blob/main/LICENSE" rel="noopener">${esc(L.footer.license)}</a></li>
        <li><a href="${url(P[here].docs)}">${esc(L.footer.docs)}</a></li>
      </ul>
    </div>
  </footer>
</body>
</html>
`;
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
  const shots = shotsFor(L);
  const shotsHtml = shots.length
    ? `<section class="section" id="prodotto" aria-labelledby="h-shots"><div class="wrap">
      <p class="label">${esc(L.shots.label)}</p>
      <h2 id="h-shots">${esc(L.shots.title)}</h2>
      <div class="shots">${shots
        .map(
          (s) => `
        <figure><img src="${url(`img/${s.id}.png`)}" alt="${esc(s.alt)}"${s.size ? ` width="${s.size.w}" height="${s.size.h}"` : ""} loading="lazy"><figcaption>${esc(s.cap)}</figcaption></figure>`
        )
        .join("")}
      </div></div></section>`
    : "";
  const modules = L.modules.items
    .map(
      (m, i) => `
        <article class="module"><span class="num" aria-hidden="true">${String(i + 1).padStart(2, "0")}</span>
          <h3>${esc(m.name)}</h3><p class="sub">${esc(m.sub)}</p>
          <ul>${m.points.map((p) => `<li>${inline(p)}</li>`).join("")}</ul></article>`
    )
    .join("");
  const hasBench = docs.has("benchmark");
  const strengths = L.strengths.items
    .map(
      ([h, p], i) => `
        <div class="strength"><h3>${esc(h)}</h3><p>${inline(p)}${i === L.strengths.items.length - 1 && hasBench ? ` <a href="${docUrl("benchmark")}" lang="it">${esc(L.strengths.benchmark)}</a>` : ""}</p></div>`
    )
    .join("");
  const body = `
    <section class="section hero" aria-labelledby="h-hero"><div class="wrap">
      <p class="label">${esc(L.hero.label)}</p>
      <h1 id="h-hero">${esc(L.hero.title)}</h1>
      <p class="lead">${esc(L.hero.lead)}</p>
      <div class="actions">
        <a class="btn primary" href="${url(P[here].install)}">${esc(L.hero.primary)}</a>
        <a class="btn" href="${url(P[here].docs)}">${esc(L.hero.secondary)}</a>
        <a class="btn" href="${REPO}" rel="noopener">${esc(L.hero.code)}</a>
      </div>
    </div></section>
    <div class="wrap"><ul class="facts">${L.facts.map(([a, b]) => `<li><strong>${esc(a)}</strong><span>${esc(b)}</span></li>`).join("")}</ul></div>

    <section class="section" id="cose" aria-labelledby="h-what"><div class="wrap">
      <p class="label">${esc(L.what.label)}</p>
      <h2 id="h-what">${esc(L.what.title)}</h2>
      <div class="two"><div><p>${esc(L.what.p1)}</p><p>${esc(L.what.p2)}</p></div>
      <ul>${L.what.list.map((x) => `<li>${esc(x)}</li>`).join("")}</ul></div>
    </div></section>
${shotsHtml}
    <section class="section" id="funzionalita" aria-labelledby="h-mod"><div class="wrap">
      <p class="label">${esc(L.modules.label)}</p>
      <h2 id="h-mod">${esc(L.modules.title)}</h2>
      <p class="lead">${esc(L.modules.lead)}</p>
      <div class="modules">${modules}
      </div>
    </div></section>

    <section class="section" id="punti-di-forza" aria-labelledby="h-str"><div class="wrap">
      <p class="label">${esc(L.strengths.label)}</p>
      <h2 id="h-str">${esc(L.strengths.title)}</h2>
      <div class="strengths">${strengths}
      </div>
    </div></section>

    <section class="section" id="installa" aria-labelledby="h-quick"><div class="wrap">
      <p class="label">${esc(L.quick.label)}</p>
      <h2 id="h-quick">${esc(L.quick.title)}</h2>
      <div class="quick">
        <div><h3>${esc(L.quick.docker)}</h3><pre tabindex="0"><code>${esc(QUICK_DOCKER)}</code></pre></div>
        <div><h3>${esc(L.quick.binary)}</h3><pre tabindex="0"><code>${esc(QUICK_BINARY)}</code></pre></div>
      </div>
      <p class="note">${inline(L.quick.note)} <a href="${url(P[here].install)}">${esc(L.quick.more)}</a>.</p>
    </div></section>

    <section class="section" id="altro" aria-labelledby="h-more"><div class="wrap">
      <p class="label">${esc(L.more.label)}</p>
      <h2 id="h-more">${esc(L.more.title)}</h2>
      <div class="links">
        <a href="${url(P[here].docs)}"><strong>${esc(L.more.docs[0])}</strong><span>${esc(L.more.docs[1])}</span></a>
        <a href="${REPO}" rel="noopener"><strong>${esc(L.more.code[0])}</strong><span>${esc(L.more.code[1])}</span></a>
        <a href="${REPO}/blob/main/LICENSE" rel="noopener"><strong>${esc(L.more.license[0])}</strong><span>${esc(L.more.license[1])}</span></a>
      </div>
    </div></section>`;
  const og = shots.length ? `img/${shots[0].id}.png` : undefined;
  emit(P[here].home + "index.html", layout({ L, kind: "home", path: P[here].home, title: L.name, description: L.description, body, ogImage: og }));
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
    <div class="wrap page"><div class="narrow">
      <p class="label">${esc(I.label)}</p>
      <h1>${esc(I.title)}</h1>
      <p class="lead">${esc(I.lead)}</p>

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

function docPage(doc) {
  const L = it;
  const html = renderDoc(doc);
  const body = `
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
    `<ul class="rel-list"${isIt ? "" : ' lang="it"'}>${arr.map((d) => `<li><a href="${docUrl(d.key)}">${esc(d.title)}</a>${d.description ? `<span>${esc(d.description)}</span>` : ""}</li>`).join("")}</ul>`;
  const notice = isIt
    ? ""
    : `<div class="notice" role="note"><strong>${esc(D.lead)}</strong>${esc(D.leadMore)}</div>`;
  const body = `
    <div class="wrap page"><div class="narrow">
      <p class="label">${esc(D.nav)}</p>
      <h1>${esc(D.title)}</h1>
      ${isIt ? `<p class="lead">${esc(D.lead)}</p>` : ""}
      ${notice}
      <h2 id="guide">${esc(D.guides)}</h2>
      <p>${esc(D.guidesText)}</p>
      ${list(GUIDES)}
      <h2 id="sviluppatori">${esc(D.dev)}</h2>
      <p>${esc(D.devText)}</p>
      ${list(DEVDOCS)}
    </div></div>`;
  emit(P[L.lang].docs + "index.html", layout({ L, kind: "doc", path: P[L.lang].docs, title: D.title, description: D.description, body }));
}

/* ------------------------------------------------------------------ 404 */

function notFoundPage() {
  const body = `
    <div class="wrap page"><div class="narrow">
      <p class="label">404</p>
      <h1>${esc(it.notFound.title)} · ${esc(en.notFound.title)}</h1>
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
