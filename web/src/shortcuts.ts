// Scorciatoie da tastiera globali (piano, passo 16; replica/usabilita.md, proposta 10).
// Qui resta solo ciò che serve al guscio: l'ascolto dei tasti. La palette dei comandi, il
// pannello «?» e il dialogo «+ Crea» si caricano alla prima apertura (palette.tsx, tastiera.tsx).
import { useCallback, useEffect, useRef } from "react";
import { useRouterState } from "@tanstack/react-router";
import { tr } from "./i18n/core";
import type { Key } from "./i18n/core";
import { useTypeNames } from "./typesApi";
import type { Me } from "./api";
import { isAuditorProfile, moduleOn, pathVisible } from "./modules";

/** Nome di ogni tipo creabile: quello del tenant (Configurazione → Tipi), con un ripiego. */
export function useKindLabel(): (k: CreateKind) => string {
  const tn = useTypeNames();
  return useCallback(
    (k) => {
      switch (k) {
        case "request": return tn.name("business_request", tr("tastiera.kind.request"));
        case "story": return tn.name("story", tr("tastiera.kind.story"));
        case "task": return tn.name("task", tr("tastiera.kind.task"));
        case "bug": return tn.name("bug", tr("tastiera.kind.bug"));
        case "epic": return tn.name("epic", tr("tastiera.kind.epic"));
        case "document": return tn.name("page", tr("tastiera.kind.document"));
        case "risk": return tn.name("risk", tr("tastiera.kind.risk"));
        case "incident": return tn.name("incident", tr("tastiera.kind.incident"));
        default: return tn.name(k, k); // un tipo del lavoro creato dall'editor
      }
    },
    [tn.name],
  );
}

/** Tipi di elemento che «+ Crea» sa creare, nell'ordine in cui compaiono. */
export type CreateKind = string;
export const WORK_KINDS: CreateKind[] = ["story", "task", "bug", "epic"];
const OTHER_KINDS = ["request", "document", "risk", "incident"];
/** Un tipo creabile del lavoro (di sistema o creato dall'editor): tutto ciò che non è richiesta, documento, rischio o incidente. */
export const isWorkKind = (k: CreateKind | undefined) => !!k && !OTHER_KINDS.includes(k);

/** I tipi che la persona può creare: permessi, moduli accesi e profilo di menu (il revisore non crea). */
export function createKinds(me: Me | undefined | null, workKinds: CreateKind[] = WORK_KINDS): CreateKind[] {
  const perms = me?.active_tenant?.permissions ?? [];
  if (!me?.active_tenant || isAuditorProfile(me)) return [];
  const has = (...ps: string[]) => perms.includes("*") || ps.some((p) => perms.includes(p));
  const out: CreateKind[] = [];
  if (has("element.write") && pathVisible(me, "/richieste/nuova")) out.push("request");
  if (has("element.write") && moduleOn(me, "work")) out.push(...workKinds);
  if (moduleOn(me, "knowledge")) out.push("document");
  if (moduleOn(me, "grc") && has("risk.admin")) out.push("risk");
  if (moduleOn(me, "grc") && has("element.write", "incident.manage")) out.push("incident");
  return out;
}

/** ⌘K / Ctrl+K: apre e chiude la palette da tutta l'app. */
export function usePaletteShortcut(toggle: () => void) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && !e.altKey && e.key.toLowerCase() === "k") {
        e.preventDefault();
        toggle();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [toggle]);
}

export const SHORTCUT = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform) ? "⌘K" : "Ctrl K";

/** «g» seguito da una lettera: vai alla pagina. Il filtro per permessi e moduli è quello del menu. */
export const GO_KEYS: { key: string; to: string; label: Key }[] = [
  { key: "r", to: "/richieste", label: "tastiera.go.requests" },
  { key: "c", to: "/cruscotto", label: "tastiera.go.dashboard" },
  { key: "a", to: "/approvazioni", label: "tastiera.go.approvals" },
  { key: "b", to: "/delivery/board", label: "tastiera.go.board" },
  { key: "l", to: "/delivery/backlog", label: "tastiera.go.backlog" },
  { key: "m", to: "/delivery/mio-lavoro", label: "tastiera.go.mywork" },
  { key: "t", to: "/tempo", label: "tastiera.go.time" },
  { key: "d", to: "/documenti", label: "tastiera.go.documents" },
  { key: "g", to: "/governance", label: "tastiera.go.governance" },
  { key: "k", to: "/rischi", label: "tastiera.go.risks" },
  { key: "s", to: "/devsecops", label: "tastiera.go.devsecops" },
  { key: "e", to: "/architettura", label: "tastiera.go.architecture" },
  { key: "n", to: "/notifiche", label: "tastiera.go.notifications" },
  { key: "p", to: "/profilo", label: "tastiera.go.profile" },
];

/** Azioni sull'elemento aperto (o sulla riga evidenziata della lista). */
export type ItemAction = "assign" | "phase" | "comment" | "follow" | "copy";

/** Chiave dell'elemento aperto nella pagina corrente (dettaglio, anteprima in lista, piano). */
export function useCurrentKey(): string | undefined {
  const loc = useRouterState({ select: (s) => s.location });
  const sel = (loc.search as Record<string, unknown>).sel;
  if (loc.pathname === "/richieste" && typeof sel === "string") return sel;
  // Pagina di dettaglio completa: /e/DEM-12 e /richieste/DEM-12.
  const page = /^\/(?:e|richieste)\/([^/]+)$/.exec(loc.pathname);
  if (page && page[1] !== "nuova") return decodeURIComponent(page[1]);
  const m = /^\/richieste\/([^/]+)\/piano$/.exec(loc.pathname);
  return m ? decodeURIComponent(m[1]) : undefined;
}

/** Si sta scrivendo (campo, area di testo, selettore, testo modificabile): le lettere non sono scorciatoie. */
export function isTyping(target: EventTarget | null): boolean {
  const el = target as HTMLElement | null;
  if (!el || !el.tagName) return false;
  if (el.isContentEditable) return true;
  const tag = el.tagName;
  if (tag === "TEXTAREA" || tag === "SELECT") return true;
  if (tag === "INPUT") {
    const type = (el as HTMLInputElement).type;
    return !["checkbox", "radio", "button", "submit", "range", "color"].includes(type);
  }
  return el.getAttribute("role") === "combobox" || el.getAttribute("role") === "textbox";
}

const ROW = "main tbody tr";

/** Righe navigabili della pagina: quelle delle tabelle con almeno un collegamento. */
function rows(): HTMLElement[] {
  return [...document.querySelectorAll<HTMLElement>(ROW)].filter((r) => r.querySelector("a[href]") && r.offsetParent !== null);
}

/** Riga evidenziata con j/k, se c'è ancora nella pagina. */
export function activeRow(): HTMLElement | null {
  return document.querySelector<HTMLElement>("[data-riga-attiva]");
}

/** Chiave dell'elemento di una riga: dal collegamento /e/CHIAVE o dall'anteprima ?sel=CHIAVE. */
export function rowKey(row: HTMLElement | null): string | undefined {
  if (!row) return undefined;
  for (const a of row.querySelectorAll<HTMLAnchorElement>("a[href]")) {
    const h = a.getAttribute("href") ?? "";
    const m = /\/e\/([^/?#]+)/.exec(h) ?? /[?&]sel=([^&#]+)/.exec(h);
    if (m) return decodeURIComponent(m[1]);
  }
  return undefined;
}

function setActive(row: HTMLElement | null) {
  const prev = activeRow();
  if (prev && prev !== row) prev.removeAttribute("data-riga-attiva");
  if (row) {
    row.setAttribute("data-riga-attiva", "true");
    row.scrollIntoView({ block: "nearest" });
  }
}

/** Come si apre la palette: con un testo già scritto, o solo con i passaggi di fase di un elemento. */
export type PaletteStart = { text?: string; only?: "phase"; key?: string };

export type KeyboardHandlers = {
  /** Pagine raggiungibili con «g» + lettera (le stesse del menu). */
  allowed: ReadonlySet<string>;
  help: () => void;
  create: () => void;
  search: (start?: PaletteStart) => void;
  go: (to: string) => void;
  /** Azione sull'elemento (chiave) aperto o evidenziato. */
  item: (action: ItemAction, key: string) => void;
  /** Pagina di un elemento, per Invio sulla riga evidenziata. */
  open: (key: string) => void;
};

/**
 * Scorciatoie senza modificatori: ? (elenco), c (crea), / (cerca), g + lettera (vai a), j e k
 * (riga successiva e precedente), Invio o o (apri la riga), a (assegna a me), s (cambia fase),
 * m (commenta), f (segui), l (copia il link). Non agiscono mentre si scrive o con un dialogo aperto.
 */
export function useKeyboardShortcuts(h: KeyboardHandlers, current: string | undefined) {
  const ref = useRef(h);
  ref.current = h;
  const cur = useRef(current);
  cur.current = current;
  useEffect(() => {
    let g = 0;
    const onKey = (e: KeyboardEvent) => {
      if (e.defaultPrevented || e.metaKey || e.ctrlKey || e.altKey || e.isComposing) return;
      if (isTyping(e.target) || document.querySelector('[aria-modal="true"]')) return;
      const k = e.key;
      const H = ref.current;
      if (g && Date.now() - g < 1500) {
        g = 0;
        const t = GO_KEYS.find((x) => x.key === k.toLowerCase());
        if (t && (H.allowed.has(t.to) || (t.to === "/tempo" && H.allowed.has("/tempo/report")))) {
          e.preventDefault();
          H.go(t.to === "/tempo" && !H.allowed.has("/tempo") ? "/tempo/report" : t.to);
        }
        return;
      }
      const row = activeRow();
      const key = rowKey(row) ?? cur.current;
      const act = (a: ItemAction) => {
        if (!key) return;
        e.preventDefault();
        H.item(a, key);
      };
      switch (k) {
        case "?":
          e.preventDefault();
          H.help();
          break;
        case "c":
          e.preventDefault();
          H.create();
          break;
        case "/":
          e.preventDefault();
          H.search();
          break;
        case "g":
          g = Date.now();
          break;
        case "j":
        case "k": {
          const all = rows();
          if (!all.length) break;
          e.preventDefault();
          const i = row ? all.indexOf(row) : -1;
          const next = k === "j" ? Math.min(all.length - 1, i + 1) : Math.max(0, i < 0 ? 0 : i - 1);
          setActive(all[next]);
          break;
        }
        case "Escape":
          if (row) setActive(null);
          break;
        case "Enter":
        case "o":
          // Invio su un collegamento o un pulsante a fuoco fa quello che è scritto sul pulsante.
          if (row && rowKey(row) && !(k === "Enter" && (e.target as HTMLElement).closest?.("a,button,[role=button],[role=tab],[role=option]"))) {
            e.preventDefault();
            H.open(rowKey(row)!);
          }
          break;
        case "a":
          act("assign");
          break;
        case "s":
          act("phase");
          break;
        case "m":
          act("comment");
          break;
        case "f":
          act("follow");
          break;
        case "l":
          act("copy");
          break;
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);
}
