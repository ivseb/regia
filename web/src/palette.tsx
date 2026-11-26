// Palette dei comandi ⌘K / Ctrl+K (piano, passo 16; replica/usabilita.md, proposta 10): ricerca
// di elementi di tutti i moduli accesi, persone, unità, pagine, pagine della configurazione e
// comandi; azioni sull'elemento aperto; ricerche recenti. Su ogni risultato ci sono azioni
// dirette («Apri», «Assegna a me», «Copia link»: Tab sceglie, Invio esegue). Stile Atlante:
// una lista netta, chiavi in mono, nessun effetto vetro. Sotto i 768 px è a tutto schermo.
import { useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, collab, orgApi, type Element, type TransitionView } from "./api";
import { searchEntities } from "./entities";
import { ErrorNote, titleParts } from "./ui";
import { MODULE_TYPES, moduleOn, pathVisible, type ModuleKey } from "./modules";
import { navPages } from "./nav";
import { createKinds, useCurrentKey, useKindLabel, type CreateKind, type PaletteStart } from "./shortcuts";
import { tr } from "./i18n/core";
import { useTypeNames, useWorkTypes } from "./typesApi";
import { useT } from "./i18n/react";
import { lookup } from "./i18n/core";

const RECENT_KEY = "regia.palette.recenti";
// Tipi cercati dalla palette, nell'ordine in cui compaiono i gruppi dei risultati.
const ALL_TYPES = ["business_request", "impact", "epic", "story", "task", "bug", "application", "logical_asset", "page", "capability", "risk", "incident", "ict_provider", "control", "evidence"];
// Elementi di lavoro: ai quali si può assegnare con «Assegna a me».
const KEY_RE = /^[A-Za-z][A-Za-z0-9_]{0,19}-[1-9][0-9]{0,17}$/;

function readRecent(): string[] {
  try {
    const v = JSON.parse(localStorage.getItem(RECENT_KEY) ?? "[]");
    return Array.isArray(v) ? v.filter((x) => typeof x === "string").slice(0, 8) : [];
  } catch {
    return [];
  }
}

function saveRecent(text: string) {
  const t = text.trim();
  if (t.length < 2) return;
  try {
    localStorage.setItem(RECENT_KEY, JSON.stringify([t, ...readRecent().filter((x) => x !== t)].slice(0, 8)));
  } catch {
    // Archiviazione del browser non disponibile: le ricerche recenti non si salvano.
  }
}

/** Traduce il testo libero nella query: chiave esatta, oppure parole con l'ultima come prefisso. */
export function paletteQuery(text: string, types: string[] = ALL_TYPES): string | null {
  if (types.length === 0) return null;
  const TYPES = `tipo: ${types.join(", ")}`;
  const words = text.replace(/[:",*]/g, " ").split(/\s+/).filter(Boolean);
  if (words.length === 0) return null;
  if (words.length === 1 && KEY_RE.test(words[0])) return `${TYPES} ${words[0].toUpperCase()}`;
  const last = words[words.length - 1];
  const rest = words.slice(0, -1);
  const tail = last.length >= 2 ? `${last}*` : last;
  return `${TYPES} ${[...rest, tail].join(" ")}`;
}

const sevName = (sev: string) => lookup(`palette.sev.${sev}`) ?? sev;

// I nomi dei tipi (singolare e plurale) arrivano da Configurazione → Tipi: niente testi scritti a mano.
// Ordine dei gruppi nei risultati: prima i tipi di uso più comune.
const TYPE_ORDER = ALL_TYPES;

const cap = (t: string) => t.charAt(0).toUpperCase() + t.slice(1);

/** Azione diretta su un risultato, oltre a «Apri»: Tab la sceglie, Invio la esegue. */
type Act = { id: string; label: string; run: () => void };

type Item = {
  id: string;
  group: string;
  label: string;
  keyText?: string;
  detail?: string;
  disabled?: boolean;
  run: () => void;
  /** Azioni dirette oltre a quella principale (la principale è `run`, «Apri»). */
  more?: Act[];
};

function useDebounced<T>(v: T, ms: number): T {
  const [d, setD] = useState(v);
  useEffect(() => {
    const t = setTimeout(() => setD(v), ms);
    return () => clearTimeout(t);
  }, [v, ms]);
  return d;
}

export type PaletteProps = {
  open: boolean;
  onClose: () => void;
  start?: PaletteStart;
  /** Messaggio breve dopo un'azione (assegnato, link copiato…). */
  notify?: (text: string) => void;
  /** Apre «+ Crea» (con un tipo, se indicato) e il pannello «?». */
  onCreate?: (kind?: CreateKind) => void;
  onHelp?: () => void;
};

export function CommandPalette({ open, ...rest }: PaletteProps) {
  if (!open) return null;
  return <Palette {...rest} />;
}

function Palette({ onClose, start, notify, onCreate, onHelp }: Omit<PaletteProps, "open">) {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const me = useQuery({ queryKey: ["me"], queryFn: api.me }).data;
  const perms = me?.active_tenant?.permissions ?? [];
  const tn = useTypeNames();
  const workTypes = useWorkTypes();
  const t = useT();
  const kindLabel = useKindLabel();
  const say = notify ?? (() => {});
  // La ricerca non guarda nei moduli spenti del tenant.
  // I tipi creati dall'organizzazione (Configurazione → Tipi) si cercano come gli altri.
  const custom = tn.items.filter((t) => !t.is_system && !ALL_TYPES.includes(t.key)).map((t) => t.key);
  const customKey = custom.join(",");
  const types = useMemo(
    () =>
      [...ALL_TYPES, ...customKey.split(",").filter(Boolean)].filter((t) => {
        const owner = (Object.keys(MODULE_TYPES) as ModuleKey[]).find((k) => MODULE_TYPES[k].includes(t));
        return !owner || moduleOn(me, owner);
      }),
    [me, customKey],
  );
  const input = useRef<HTMLInputElement>(null);
  const list = useRef<HTMLUListElement>(null);
  const [text, setText] = useState(start?.text ?? "");
  const [active, setActive] = useState(0);
  // Azione scelta con Tab sul risultato evidenziato (0 = «Apri»).
  const [actIdx, setActIdx] = useState(0);
  const [recent] = useState(readRecent);
  const [pending, setPending] = useState<{ el: Element; t: TransitionView } | null>(null);
  const [comment, setComment] = useState("");
  const only = start?.only;

  // Il fuoco torna dove era alla chiusura.
  useEffect(() => {
    const prev = document.activeElement as HTMLElement | null;
    input.current?.focus();
    return () => prev?.focus?.();
  }, []);

  const pageKey = useCurrentKey();
  const current = start?.key ?? pageKey;
  const detail = useQuery({ queryKey: ["element", current], queryFn: () => api.element(current!), enabled: !!current });
  const watch = useQuery({ queryKey: ["watch", current], queryFn: () => collab.watch(current!), enabled: !!current && !only });

  const dq = useDebounced(text, 110);
  const pq = paletteQuery(dq, types);
  const found = useQuery({
    queryKey: ["palette", pq],
    queryFn: () => api.search(pq!, undefined, 8),
    enabled: !!pq && dq.trim().length >= 2 && !only,
    placeholderData: (prev) => prev,
    staleTime: 15_000,
  });

  // Persone e unità non sono elementi: si cercano a parte. Aprono la lista delle richieste filtrata,
  // quindi non compaiono se quella pagina non è visibile (modulo spento, profilo del revisore).
  const vq0 = dq.trim();
  const listTarget = pathVisible(me, "/richieste") ? "/richieste" : null;
  const people = useQuery({
    queryKey: ["palette-people", vq0],
    queryFn: () => searchEntities("member", vq0, { limit: 5 }),
    enabled: !!listTarget && vq0.length >= 2 && !only,
    placeholderData: (prev) => prev,
    staleTime: 15_000,
    retry: false,
  });
  const units = useQuery({
    queryKey: ["palette-units", vq0],
    queryFn: () => orgApi.units({ q: vq0, limit: 5 }),
    enabled: !!listTarget && vq0.length >= 2 && !only,
    placeholderData: (prev) => prev,
    staleTime: 15_000,
    retry: false,
  });

  // Le vulnerabilità non sono elementi: si cercano a parte, per identificativo (CVE-2026-…, GHSA-…),
  // pacchetto o servizio. Il modulo si carica solo se si scrive qualcosa che somiglia a un identificativo.
  const canVuln = (perms.includes("*") || perms.includes("element.read")) && moduleOn(me, "devsecops");
  const vq = dq.trim();
  const vulns = useQuery({
    queryKey: ["palette-vuln", vq],
    queryFn: () => import("./vulnApi").then((m) => m.vulnApi.items({ q: vq, stage: "all", limit: 6 })),
    enabled: canVuln && vq.length >= 3 && /^(cve|ghsa|osv|pysec|rustsec)|^\d{4}-\d/i.test(vq) && !only,
    placeholderData: (prev) => prev,
    staleTime: 15_000,
    retry: false,
  });

  const refresh = (key: string) => {
    qc.invalidateQueries({ queryKey: ["element", key] });
    qc.invalidateQueries({ queryKey: ["search"] });
    qc.invalidateQueries({ queryKey: ["inbox"] });
    qc.invalidateQueries({ queryKey: ["activity"] });
  };
  const move = useMutation({
    mutationFn: ({ el, t, note }: { el: Element; t: TransitionView; note?: string }) => api.transition(el.id, t.to, note),
    onSuccess: (_, v) => {
      refresh(v.el.key);
      onClose();
    },
  });
  const follow = useMutation({
    mutationFn: ({ key, on }: { key: string; on: boolean }) => collab.setWatch(key, on),
    onSuccess: (data, v) => {
      qc.setQueryData(["watch", v.key], data);
      onClose();
    },
  });
  // Azioni dirette su un risultato (le due librerie dell'elemento si caricano al primo uso).
  const assign = (key: string) =>
    import("./tastiera")
      .then((m) => m.assignToMe(key, { me, qc, notify: say }))
      .catch((e) => say(e instanceof Error && e.message ? e.message : tr("errors.network")))
      .finally(onClose);
  const copyKeyLink = (key: string) =>
    import("./tastiera")
      .then(async (m) => {
        if (await m.copyLink(key)) say(t("tastiera.linkCopied", { key }));
      })
      .finally(onClose);

  const go = (to: string, search?: Record<string, string>) => {
    navigate({ to, search: search as never });
    onClose();
  };
  const openElement = (el: Element) => {
    saveRecent(text);
    navigate({ to: "/e/$key", params: { key: el.key } });
    onClose();
  };

  // Tutte le pagine del menu, anche quelle che nel menu sono una voce sola (navPages): la configurazione
  // e l'indice degli elenchi (c'è se almeno un elenco è raggiungibile).
  const allPages = useMemo(() => (me?.active_tenant ? navPages(me, {}, tn) : []), [me, tn.byKey]);
  const hasLists = allPages.some((it) => it.to === "/elenco");
  const navConfig = useMemo(
    () => allPages.filter((it) => it.to && (it.to.startsWith("/configurazione") || it.to === "/processi")),
    [allPages],
  );

  const items = useMemo<Item[]>(() => {
    const q = text.trim().toLowerCase();
    const match = (...xs: (string | undefined)[]) => !q || xs.some((x) => x && x.toLowerCase().includes(q));
    const out: Item[] = [];
    const canWrite = perms.includes("*") || perms.includes("element.write");
    // Azioni: prima quelle sull'elemento aperto.
    const d = detail.data;
    if (d && current) {
      const key = d.element.key || current;
      for (const tr of d.transitions) {
        const blocked = tr.blocked.length > 0;
        if (match(tr.label, tr.to, t("palette.kw.stage"))) {
          out.push({
            id: `tr-${tr.to}`,
            group: t("palette.group.actions"),
            label: tr.label,
            keyText: key,
            detail: blocked ? tr.blocked.join("; ") : t(tr.comment_required ? "palette.detail.moveToComment" : "palette.detail.moveTo", { to: tr.to }),
            disabled: blocked,
            run: () => (tr.comment_required ? setPending({ el: d.element, t: tr }) : move.mutate({ el: d.element, t: tr })),
          });
        }
      }
      if (only === "phase") return out;
      if (workTypes.isWork(d.element.type) && canWrite && match(t("tastiera.cmd.assignMe"), t("tastiera.kw.assign"))) {
        out.push({ id: "assign-me", group: t("palette.group.actions"), label: t("tastiera.cmd.assignMe"), keyText: key, run: () => void assign(key) });
      }
      if (match(t("tastiera.cmd.copyLink"), t("tastiera.kw.copy"))) {
        out.push({ id: "copy-link", group: t("palette.group.actions"), label: t("tastiera.cmd.copyLink"), keyText: key, run: () => void copyKeyLink(key) });
      }
      if (watch.data && match(t("palette.kw.follow"))) {
        const on = !watch.data.following;
        out.push({ id: "watch", group: t("palette.group.actions"), label: on ? t("palette.act.follow") : t("palette.act.unfollow"), keyText: key, detail: t("palette.detail.watchers", { count: watch.data.followers }), run: () => follow.mutate({ key, on }) });
      }
      if (d.element.type === "business_request" && match(t("palette.kw.plan"))) {
        out.push({ id: "plan", group: t("palette.group.actions"), label: t("palette.act.openPlan"), keyText: key, run: () => go(`/richieste/${encodeURIComponent(key)}/piano`) });
      }
    }
    if (only === "phase") return out;
    const canCreate = (perms.includes("*") || perms.includes("element.write")) && pathVisible(me, "/richieste/nuova");
    const newReq = tn.create("business_request", "Richiesta");
    if (canCreate && match(newReq.toLowerCase(), t("palette.kw.new"))) {
      out.push({ id: "new", group: t("palette.group.actions"), label: newReq, detail: t("palette.act.opensForm"), run: () => go("/richieste/nuova") });
    }
    // «Nuovo progetto»: fuori dal menu, resta qui e nella testata dei progetti.
    const canNewProject = canWrite && perms.some((p) => p === "*" || p === "element.read") && pathVisible(me, "/progetti/nuovo");
    const newProject = tn.create("project", "Progetto");
    if (canNewProject && match(newProject.toLowerCase(), t("palette.kw.new"))) {
      out.push({ id: "new-project", group: t("palette.group.actions"), label: newProject, detail: t("shell.menu.newProject"), run: () => go("/progetti/nuovo") });
    }
    // Comandi: «+ Crea» per ogni tipo permesso e l'elenco delle scorciatoie.
    const cmd = t("tastiera.group.commands");
    const kinds = createKinds(me, workTypes.creatable);
    if (onCreate && kinds.length > 0) {
      if (match(t("tastiera.cmd.create"), t("tastiera.kw.create"))) {
        out.push({ id: "cmd-create", group: cmd, label: t("tastiera.cmd.create"), detail: t("tastiera.cmd.createDetail"), run: () => { onCreate(); onClose(); } });
      }
      if (q) {
        for (const k of kinds) {
          const name = kindLabel(k);
          if (match(t("tastiera.cmd.createKind", { name }), t("tastiera.kw.create"))) {
            out.push({ id: `cmd-create-${k}`, group: cmd, label: t("tastiera.cmd.createKind", { name }), detail: t("tastiera.cmd.createDetail"), run: () => { onCreate(k); onClose(); } });
          }
        }
      }
    }
    if (onHelp && match(t("tastiera.cmd.help"), t("tastiera.kw.help"))) {
      out.push({ id: "cmd-help", group: cmd, label: t("tastiera.cmd.help"), keyText: "?", run: () => { onHelp(); onClose(); } });
    }
    // Elementi trovati, con le azioni dirette.
    if (q.length >= 2) {
      for (const el of found.data?.items ?? []) {
        const more: Act[] = [];
        if (workTypes.isWork(el.type) && canWrite) more.push({ id: "assign", label: t("tastiera.act.assign"), run: () => void assign(el.key) });
        more.push({ id: "copy", label: t("tastiera.act.copy"), run: () => void copyKeyLink(el.key) });
        out.push({
          id: `el-${el.id}`,
          group: tn.byKey.has(el.type) ? tn.plural(el.type) : t("palette.group.otherItems"),
          label: titleParts(el.type, el.title).main,
          keyText: `${cap(tn.name(el.type, el.type_name))} · ${el.key}`,
          detail: [el.state, titleParts(el.type, el.title).context && t("palette.detail.context", { type: tn.lower("business_request", t("palette.requestFallback")), context: titleParts(el.type, el.title).context ?? "" })].filter(Boolean).join(" · "),
          run: () => openElement(el),
          more,
        });
      }
    }
    if (q.length >= 3) {
      for (const v of vulns.data?.items ?? []) {
        out.push({
          id: `vu-${v.id}`,
          group: t("palette.group.vulns"),
          label: [v.package, v.service].filter(Boolean).join(" · ") || v.cve,
          keyText: v.cve,
          detail: `${sevName(v.severity)} · ${v.stage_name}`,
          run: () => go("/devsecops/vulnerabilita", { q: v.cve }),
        });
      }
    }
    // Persone (con la lista del loro lavoro) e unità (con le loro richieste).
    if (q.length >= 2 && listTarget) {
      for (const p of people.data ?? []) {
        const name = p.label.replace(/"/g, "");
        out.push({
          id: `pe-${p.value}`,
          group: t("tastiera.group.people"),
          label: p.label,
          keyText: t("tastiera.person"),
          detail: p.detail,
          run: () => go(listTarget, { q: `assegnatario: "${name}"` }),
          more: [{ id: "reported", label: t("tastiera.act.openedBy"), run: () => go(listTarget, { q: `autore: "${name}"` }) }],
        });
      }
      for (const u of units.data?.items ?? []) {
        out.push({
          id: `un-${u.id}`,
          group: t("tastiera.group.units"),
          label: u.name,
          keyText: t("tastiera.unit"),
          detail: u.path,
          run: () => go(listTarget, { q: `tipo: business_request unita: "${u.name.replace(/"/g, "")}"` }),
        });
      }
    }
    // Pagine.
    // Le stesse pagine del menu (shell.tsx), con gli stessi permessi.
    const pages: [string, string, Record<string, string>?][] = [
      [tr("dafare.nav.todo"), "/da-fare"],
      [tn.plural("business_request", "Richieste"), "/richieste"],
      [t("palette.page.requestsTable", { type: tn.plural("business_request", "Richieste").toLowerCase() }), "/richieste", { vista: "tabella" }],
      [t("palette.page.dashboard"), "/cruscotto"],
      [t("palette.page.roadmap"), "/roadmap"],
      [t("palette.page.ranking"), "/graduatoria"],
      [t("palette.page.cabina"), "/cabina"],
      [t("palette.page.governance"), "/governance"],
      [t("palette.page.approvals"), "/approvazioni"],
      [t("palette.page.reviews"), "/valutazioni"],
      [t("palette.page.verify"), "/requisiti"],
      [t("palette.page.board"), "/delivery/board"],
      [t("palette.page.backlog"), "/delivery/backlog"],
      [t("palette.page.mywork"), "/delivery/mio-lavoro"],
      [t("palette.page.archAssets"), "/architettura", { tab: "asset" }],
      [t("palette.page.archApps"), "/architettura", { tab: "applicazioni" }],
      [t("palette.page.archTeams"), "/architettura", { tab: "team" }],
      [t("palette.page.notifications"), "/notifiche"],
      [t("palette.page.documents"), "/documenti"],
      [t("palette.page.archMap"), "/architettura/mappa"],
      [t("palette.page.capability"), "/architettura/capability"],
      [t("palette.page.deps"), "/architettura/dipendenze"],
      [t("palette.page.rationalization"), "/architettura/razionalizzazione"],
      [t("palette.page.diagrams"), "/architettura/diagrammi"],
      [t("palette.page.risks"), "/rischi"],
      [t("palette.page.providers"), "/rischi", { tab: "fornitori" }],
      [t("palette.page.incidents"), "/rischi", { tab: "incidenti" }],
      [t("palette.page.devsecops"), "/devsecops"],
      [t("palette.page.dora"), "/devsecops/metriche"],
      [t("palette.page.vulns"), "/devsecops/vulnerabilita"],
      [t("palette.page.adherence"), "/devsecops/aderenza"],
      [t("palette.page.profile"), "/profilo"],
      [t("palette.page.timesheet"), "/tempo"],
      [t("palette.page.timeReport"), "/tempo/report"],
      [t("palette.page.timeTeam"), "/tempo/team"],
    ];
    const has = (...ps: string[]) => perms.includes("*") || ps.some((p) => perms.includes(p));
    if (has("grc.read", "grc.admin")) pages.push([t("palette.page.compliance"), "/conformita"]);
    if (has("finance.read")) pages.push([t("palette.page.finance"), "/finanza"]);
    if (has("element.read")) pages.push([tr("budget.nav.treePalette"), "/budget"]);
    if (has("portal.submit", "element.write")) pages.push([t("palette.page.portal"), "/portale"]);
    if (hasLists) pages.push([t("shell.menu.lists.title"), "/elenco"]);
    for (const [label, to, search] of pages) {
      if (match(label) && pathVisible(me, to)) out.push({ id: `p-${label}`, group: t("palette.group.pages"), label, detail: to, run: () => go(to, search) });
    }
    // Pagine della configurazione: le stesse del menu, con gli stessi permessi.
    for (const it of navConfig) {
      if (match(it.label, it.to) && pathVisible(me, it.to!)) out.push({ id: `cfg-${it.to}`, group: t("tastiera.group.config"), label: it.label, detail: it.to, run: () => go(it.to!) });
    }
    if (!q) {
      for (const r of recent) out.push({ id: `r-${r}`, group: t("palette.group.recent"), label: r, run: () => setText(r) });
    }
    return out;
    // go, move, follow, assign e openElement dipendono solo da valori già elencati.
  }, [text, detail.data, watch.data, found.data, vulns.data, people.data, units.data, current, recent, perms.join(","), me, tn.byKey, workTypes, navConfig, hasLists, only]);

  const enabled = items.filter((i) => !i.disabled);
  const cur = enabled.length ? enabled[Math.min(active, enabled.length - 1)] : undefined;
  useEffect(() => {
    setActive(0);
    setActIdx(0);
  }, [text]);
  useEffect(() => setActIdx(0), [cur?.id]);
  useEffect(() => {
    if (cur) list.current?.querySelector(`[data-id="${CSS.escape(cur.id)}"]`)?.scrollIntoView({ block: "nearest" });
  }, [cur]);

  const acts = (it: Item): Act[] => [{ id: "open", label: t("tastiera.act.open"), run: it.run }, ...(it.more ?? [])];

  const groups = [
    t("palette.group.actions"),
    t("tastiera.group.commands"),
    ...[...new Set([...TYPE_ORDER, ...tn.items.map((t) => t.key)].filter((k) => tn.byKey.has(k)).map((k) => tn.plural(k)))],
    t("palette.group.otherItems"),
    t("palette.group.vulns"),
    t("tastiera.group.people"),
    t("tastiera.group.units"),
    t("palette.group.recent"),
    t("palette.group.pages"),
    t("tastiera.group.config"),
  ];
  const loading = (found.isFetching || people.isFetching || units.isFetching) && text.trim().length >= 2;

  return (
    <div
      className="fixed inset-0 z-[70] flex items-start justify-center bg-black/40 md:px-4 md:pt-[12vh]"
      onMouseDown={(e) => e.target === e.currentTarget && onClose()}
    >
      <div role="dialog" aria-modal="true" aria-label={t("palette.dialog")} className="flex w-full flex-col border-rule bg-bg max-md:h-dvh md:max-h-[70vh] md:max-w-[640px] md:border-[1.5px]">
        {pending ? (
          <form
            className="flex flex-col gap-3 p-4"
            onSubmit={(e) => {
              e.preventDefault();
              move.mutate({ el: pending.el, t: pending.t, note: comment });
            }}
            onKeyDown={(e) => {
              if (e.key === "Escape") {
                e.preventDefault();
                setPending(null);
                setTimeout(() => input.current?.focus());
              }
            }}
          >
            <span className="font-mono text-[12px] text-accent">{pending.el.key}</span>
            <label className="flex flex-col gap-1">
              <span className="label">{t("palette.reasonFor", { name: pending.t.label })}</span>
              <textarea
                autoFocus
                required
                className="field min-h-24"
                value={comment}
                onChange={(e) => setComment(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
                    e.preventDefault();
                    e.currentTarget.form?.requestSubmit();
                  }
                }}
              />
            </label>
            <ErrorNote error={move.error} />
            <div className="flex gap-2">
              <button className="btn btn-primary" disabled={move.isPending || !comment.trim()}>{t("palette.confirm")}</button>
              <button type="button" className="btn" onClick={() => setPending(null)}>{t("palette.back")}</button>
            </div>
          </form>
        ) : (
          <>
            <div className="flex items-center border-b-2 border-rule">
              <span className="self-stretch bg-rule px-3 font-mono text-[11.5px] leading-[44px] text-bg">{t("palette.search.label")}</span>
              <input
                ref={input}
                value={text}
                onChange={(e) => setText(e.target.value)}
                role="combobox"
                aria-expanded="true"
                aria-controls="palette-risultati"
                aria-activedescendant={cur ? `palette-${cur.id}` : undefined}
                aria-label={t("palette.input.label")}
                placeholder={t("palette.input.placeholder")}
                autoComplete="off"
                spellCheck={false}
                enterKeyHint="search"
                className="h-11 min-w-0 flex-1 border-0 bg-bg px-3 text-[15px] text-fg outline-none"
                onKeyDown={(e) => {
                  switch (e.key) {
                    case "ArrowDown":
                      e.preventDefault();
                      if (enabled.length) setActive((a) => (Math.min(a, enabled.length - 1) + 1) % enabled.length);
                      break;
                    case "ArrowUp":
                      e.preventDefault();
                      if (enabled.length) setActive((a) => (Math.min(a, enabled.length - 1) - 1 + enabled.length) % enabled.length);
                      break;
                    case "Enter": {
                      e.preventDefault();
                      const list = cur ? acts(cur) : [];
                      (list[Math.min(actIdx, list.length - 1)] ?? cur)?.run();
                      break;
                    }
                    case "Escape":
                      e.preventDefault();
                      onClose();
                      break;
                    case "Tab": {
                      // Il fuoco resta nella palette; Tab passa all'azione diretta successiva del risultato.
                      e.preventDefault();
                      const n = cur ? acts(cur).length : 0;
                      if (n > 1) setActIdx((a) => (a + (e.shiftKey ? -1 : 1) + n) % n);
                      break;
                    }
                  }
                }}
              />
              <span className="px-3 font-mono text-[11px] text-muted tabular" aria-live="polite">
                {loading ? "…" : ""}
              </span>
              <button type="button" className="btn mr-1 border-transparent md:hidden" onClick={onClose}>{t("tastiera.close")}</button>
            </div>
            <ul id="palette-risultati" ref={list} role="listbox" aria-label={t("palette.results")} className="m-0 min-h-0 flex-1 list-none overflow-y-auto p-0">
              {groups.map((g) => {
                const rows = items.filter((i) => i.group === g);
                if (!rows.length) return null;
                return (
                  <li key={g} role="presentation">
                    <div className="label border-b border-hair px-3 pt-3 pb-1 text-muted">{g}</div>
                    <ul role="group" aria-label={g} className="m-0 list-none p-0">
                      {rows.map((it) => {
                        const sel = cur?.id === it.id;
                        const list = acts(it);
                        const chips = (touch: boolean) => (
                          <span className={touch ? "flex flex-wrap gap-2" : "inline-flex gap-1"}>
                            {list.map((a, i) => (
                              <button
                                key={a.id}
                                type="button"
                                tabIndex={-1}
                                data-action={a.id}
                                onMouseDown={(e) => {
                                  e.preventDefault();
                                  e.stopPropagation();
                                  a.run();
                                }}
                                className={`border px-1.5 py-px text-[11.5px] ${touch ? "touch-44" : ""} ${sel && i === actIdx ? "border-fg bg-fg text-bg" : "border-hair text-muted hover:text-fg"}`}
                              >
                                {a.label}
                              </button>
                            ))}
                          </span>
                        );
                        return (
                          <li
                            key={it.id}
                            id={`palette-${it.id}`}
                            data-id={it.id}
                            role="option"
                            aria-selected={sel}
                            aria-disabled={it.disabled || undefined}
                            onMouseDown={(e) => {
                              e.preventDefault();
                              if (!it.disabled) it.run();
                            }}
                            onMouseMove={() => {
                              const k = enabled.indexOf(it);
                              if (k >= 0 && k !== active) setActive(k);
                            }}
                            className={`flex flex-col border-b border-hair px-3 py-2 ${it.disabled ? "cursor-not-allowed text-muted" : "cursor-pointer"} ${sel ? "bg-sel" : ""}`}
                          >
                            <span className="flex items-baseline gap-3">
                              {it.keyText && <span className="shrink-0 font-mono text-[12px] font-medium text-accent">{it.keyText}</span>}
                              <span className={`min-w-0 flex-1 [overflow-wrap:anywhere] ${sel ? "font-semibold" : ""}`}>{it.label}</span>
                              {it.detail && <span className="hidden max-w-[40%] text-right text-[12px] [overflow-wrap:anywhere] text-muted sm:inline">{it.detail}</span>}
                              {!!it.more?.length && sel && <span className="hidden shrink-0 md:inline-flex">{chips(false)}</span>}
                            </span>
                            {!!it.more?.length && <span className="mt-1 flex md:hidden">{chips(true)}</span>}
                          </li>
                        );
                      })}
                    </ul>
                  </li>
                );
              })}
              {text.trim().length >= 2 && !loading && found.data && found.data.items.length === 0 && !items.some((i) => i.group !== t("palette.group.pages")) && (
                <li className="px-3 py-4 text-muted">{t("palette.noResults", { text: text.trim() })}</li>
              )}
              {only === "phase" && items.length === 0 && <li className="px-3 py-4 text-muted">{t("tastiera.noPhase")}</li>}
            </ul>
            {(move.error || follow.error || found.error) && (
              <div className="border-t border-hair px-3 py-2">
                <ErrorNote error={move.error ?? follow.error ?? found.error} />
              </div>
            )}
            <div className="hidden border-t border-hair px-3 py-1.5 font-mono text-[11px] text-muted sm:block">
              {t("palette.hints")}
              {!!cur?.more?.length && ` · ${t("tastiera.hintTab")}`}
            </div>
          </>
        )}
      </div>
    </div>
  );
}
