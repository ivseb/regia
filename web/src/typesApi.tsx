// Tipi di elemento configurabili (Configurazione → Tipi): API, nomi per i menu, icone e colori.
// Rispecchia internal/engine/types_admin.go. Il nome di un tipo si legge sempre da qui: non va
// scritto a mano nei componenti, perché ogni organizzazione può rinominarlo.
import { useCallback, useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { call } from "./api";
import { useLang } from "./i18n/react";
import { getLang, tr } from "./i18n/core";

const q = encodeURIComponent;

export type TypeLabel = { name: string; plural: string };

export type TypeConfig = {
  key: string;
  name: string;
  plural_name: string;
  description: string;
  module: string;
  icon: string;
  color: string;
  is_system: boolean;
  portal_enabled: boolean;
  labels: Record<string, TypeLabel>;
  archived: boolean;
  archived_at: string | null;
  elements: number;
  fields: number;
  process_key: string;
  process_name: string;
  updated_at: string;
};

export type TypeUpdate = Partial<{
  name: string;
  plural_name: string;
  description: string;
  icon: string;
  color: string;
  portal_enabled: boolean;
  labels: Record<string, TypeLabel>;
  archived: boolean;
}>;

export type TypeCreate = { key: string; name: string; plural_name?: string; description?: string; module?: string; icon?: string; color?: string; from?: string };

export type TypeName = {
  key: string;
  name: string;
  plural_name: string;
  module: string;
  icon: string;
  color: string;
  portal_enabled: boolean;
  is_system: boolean;
  archived: boolean;
};

export const typesApi = {
  list: () => call<{ items: TypeConfig[]; creatable_modules: string[] }>("GET", "/types"),
  create: (b: TypeCreate) => call<TypeConfig>("POST", "/types", b),
  update: (key: string, b: TypeUpdate) => call<TypeConfig>("PATCH", `/types/${q(key)}`, b),
  /** Nomi per i menu, nella lingua indicata (`labels` del tipo); senza traduzione vale il nome del tipo. */
  names: (lang?: string) => call<{ items: TypeName[] }>("GET", `/type-names${lang ? `?lang=${q(lang)}` : ""}`),
};

/** Moduli come si chiamano per chi configura. */
export const MODULE_LABELS: Record<string, string> = {
  get work() { return tr("configurazione.types.module.work"); },
  get knowledge() { return tr("configurazione.types.module.knowledge"); },
  get strategy() { return tr("configurazione.types.module.strategy"); },
  get core() { return tr("configurazione.types.module.core"); },
  get devsecops() { return tr("configurazione.types.module.devsecops"); },
  get grc() { return tr("configurazione.types.module.grc"); },
  get architecture() { return tr("configurazione.types.module.architecture"); },
};

/** Colori della palette del tema (token, niente esadecimali): chiave, etichetta, classi. */
export const TYPE_COLORS: { key: string; label: string; bg: string; text: string }[] = [
  { key: "", get label() { return tr("configurazione.types.color.none"); }, bg: "bg-hair", text: "text-fg" },
  { key: "accent", get label() { return tr("configurazione.types.color.accent"); }, bg: "bg-accent", text: "text-accent" },
  { key: "ok", get label() { return tr("configurazione.types.color.ok"); }, bg: "bg-ok", text: "text-ok" },
  { key: "flag", get label() { return tr("configurazione.types.color.flag"); }, bg: "bg-flag", text: "text-flag" },
  { key: "danger", get label() { return tr("configurazione.types.color.danger"); }, bg: "bg-danger", text: "text-danger" },
  { key: "muted", get label() { return tr("configurazione.types.color.muted"); }, bg: "bg-muted", text: "text-muted" },
  { key: "fg", get label() { return tr("configurazione.types.color.fg"); }, bg: "bg-fg", text: "text-fg" },
];
const colorOf = (c: string) => TYPE_COLORS.find((x) => x.key === c) ?? TYPE_COLORS[0];

/** Icone disponibili: tracciati su una griglia 24×24, tratto di 1,8 come il resto dell'interfaccia. */
export const TYPE_ICONS: Record<string, { label: string; d: string[] }> = {
  inbox: { get label() { return tr("configurazione.types.icon.inbox"); }, d: ["M3 13h5l1.5 3h5L16 13h5", "M5 5h14l2 8v6H3v-6Z"] },
  doc: { get label() { return tr("configurazione.types.icon.doc"); }, d: ["M6 3h9l4 4v14H6Z", "M14 3v5h5", "M9 13h7M9 17h7"] },
  check: { get label() { return tr("configurazione.types.icon.check"); }, d: ["M4 12.5 9.5 18 20 6.5"] },
  bug: { get label() { return tr("configurazione.types.icon.bug"); }, d: ["M9 8a3 3 0 0 1 6 0v8a3 3 0 0 1-6 0Z", "M5 9l4 2M19 9l-4 2M5 17l4-2M19 17l-4-2M12 5V3"] },
  bolt: { get label() { return tr("configurazione.types.icon.bolt"); }, d: ["M13 3 5 14h6l-1 7 8-11h-6Z"] },
  flag: { get label() { return tr("configurazione.types.icon.flag"); }, d: ["M6 21V4", "M6 5h12l-2 4 2 4H6"] },
  target: { get label() { return tr("configurazione.types.icon.target"); }, d: ["M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18Z", "M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8Z"] },
  wrench: { get label() { return tr("configurazione.types.icon.wrench"); }, d: ["M14 6a4 4 0 0 0 5 5l-9 9a2.5 2.5 0 0 1-4-4l9-9a4 4 0 0 0-1-1Z"] },
  box: { get label() { return tr("configurazione.types.icon.box"); }, d: ["M3 7l9-4 9 4v10l-9 4-9-4Z", "M3 7l9 4 9-4M12 11v10"] },
  shield: { get label() { return tr("configurazione.types.icon.shield"); }, d: ["M12 3l8 3v6c0 5-3.5 8-8 9-4.5-1-8-4-8-9V6Z"] },
  user: { get label() { return tr("configurazione.types.icon.user"); }, d: ["M12 4a4 4 0 1 0 0 8 4 4 0 0 0 0-8Z", "M4 20c0-4 3.5-6 8-6s8 2 8 6"] },
  calendar: { get label() { return tr("configurazione.types.icon.calendar"); }, d: ["M4 6h16v14H4Z", "M4 10h16M8 3v5M16 3v5"] },
  link: { get label() { return tr("configurazione.types.icon.link"); }, d: ["M10 14a4 4 0 0 0 5.5 0l3-3a4 4 0 0 0-5.5-5.5l-1 1", "M14 10a4 4 0 0 0-5.5 0l-3 3A4 4 0 0 0 11 18.5l1-1"] },
  star: { get label() { return tr("configurazione.types.icon.star"); }, d: ["M12 3l2.6 5.6 6.1.7-4.5 4.2 1.2 6L12 16.5 6.6 19.5l1.2-6L3.3 9.3l6.1-.7Z"] },
  layers: { get label() { return tr("configurazione.types.icon.layers"); }, d: ["M12 3l9 5-9 5-9-5Z", "M3 13l9 5 9-5"] },
  chat: { get label() { return tr("configurazione.types.icon.chat"); }, d: ["M4 5h16v11H9l-5 4Z"] },
};

/** Segno di un tipo come appare nelle liste: icona (o iniziale) nel colore scelto. */
export function TypeMark({ icon, color, name, size = 20 }: { icon?: string; color?: string; name?: string; size?: number }) {
  const c = colorOf(color ?? "");
  const g = icon ? TYPE_ICONS[icon] : undefined;
  return (
    <span
      aria-hidden="true"
      className={`inline-flex shrink-0 items-center justify-center border border-current ${c.text}`}
      style={{ width: size, height: size }}
    >
      {g ? (
        <svg viewBox="0 0 24 24" width={size - 6} height={size - 6} fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="square" strokeLinejoin="miter">
          {g.d.map((p) => (
            <path key={p} d={p} />
          ))}
        </svg>
      ) : (
        <span className="font-mono text-[11px] leading-none font-semibold">{(name ?? "?").charAt(0).toUpperCase()}</span>
      )}
    </span>
  );
}

/** «Un tipo» come chip, per esempio nei filtri e nei dettagli. */
export function TypeChip({ icon, color, name }: { icon?: string; color?: string; name: string }) {
  return (
    <span className="inline-flex max-w-full items-center gap-1.5 border border-hair px-1.5 py-px text-[12.5px] leading-[18px]">
      <TypeMark icon={icon} color={color} name={name} size={16} />
      <span className="min-w-0 [overflow-wrap:anywhere]">{name}</span>
    </span>
  );
}

const FEMININE = /(zione|sione|ità|tà|tù|ie|a)$/i;

/** «Nuova richiesta», «Nuovo rischio»: l'articolo segue il genere probabile del nome. In inglese «New request». */
export function newLabel(name: string): string {
  const n = name.trim();
  const lower = `${n.charAt(0).toLowerCase()}${n.slice(1)}`;
  if (getLang() === "en") return `New ${lower}`;
  return `${FEMININE.test(n) ? "Nuova" : "Nuovo"} ${lower}`;
}

/**
 * Nomi dei tipi da leggere ovunque nell'interfaccia. Finché la risposta non arriva (o per un
 * tipo sconosciuto) si usa `fallback`, che è il testo che c'era prima della configurazione.
 */
export function useTypeNames() {
  const lang = useLang();
  const q = useQuery({ queryKey: ["type-names", lang], queryFn: () => typesApi.names(lang), staleTime: 60_000 });
  const byKey = useMemo(() => new Map((q.data?.items ?? []).map((t) => [t.key, t])), [q.data]);
  const name = useCallback((key: string, fallback = key) => byKey.get(key)?.name || fallback, [byKey]);
  const plural = useCallback((key: string, fallback = key) => byKey.get(key)?.plural_name || byKey.get(key)?.name || fallback, [byKey]);
  const lower = useCallback((key: string, fallback = key) => name(key, fallback).toLowerCase(), [name]);
  const create = useCallback((key: string, fallback = key) => newLabel(name(key, fallback)), [name]);
  return { name, plural, lower, create, byKey, items: q.data?.items ?? [], ready: q.isSuccess };
}

const DEFAULT_WORK = ["epic", "story", "task", "bug"];
const WORK_ORDER = ["story", "task", "bug", "epic"];

/**
 * I tipi del lavoro vengono dalla configurazione: quelli del modulo «work», sprint escluso.
 * `keys` sono tutti (per riconoscere un elemento di lavoro anche se il tipo è archiviato), `creatable`
 * solo quelli non archiviati, nell'ordine dei tipi di sistema e poi per nome. Finché la configurazione
 * non è arrivata valgono i quattro di sempre.
 */
export function useWorkTypes() {
  const tn = useTypeNames();
  return useMemo(() => {
    if (!tn.ready) return { ready: false, keys: DEFAULT_WORK, creatable: WORK_ORDER, isWork: (k: string) => DEFAULT_WORK.includes(k) };
    const rank = (k: string) => (WORK_ORDER.includes(k) ? WORK_ORDER.indexOf(k) : 99);
    const work = tn.items.filter((t) => t.module === "work" && t.key !== "sprint").sort((a, b) => rank(a.key) - rank(b.key) || a.name.localeCompare(b.name));
    const keys = work.map((t) => t.key);
    return { ready: true, keys, creatable: work.filter((t) => !t.archived).map((t) => t.key), isWork: (k: string) => keys.includes(k) };
  }, [tn.items, tn.ready]);
}
