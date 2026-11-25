// Elenchi di entità per il selettore (web/src/select.tsx): ricerca sul server con limite e lettura
// in blocco dei nomi delle voci già scelte. Un tipo di elemento per volta, stesso comportamento con 60 o 60.000 voci.
import { useEffect, useSyncExternalStore } from "react";
import { api, call } from "./api";
import { tr } from "./i18n/core";
import { SERVER_LIMIT, type Opt } from "./selectLogic";

export type EntityKind = "member" | "group" | "team" | "unit" | "application" | "supplier" | "element";

type Conf = { path: string; parse: (x: Record<string, unknown>) => Opt };
const s = (v: unknown) => (typeof v === "string" ? v : "");

const CONF: Record<Exclude<EntityKind, "element">, Conf> = {
  member: { path: "/members", parse: (x) => ({ value: s(x.id), label: s(x.display_name), detail: x.member_type === "requester" ? tr("common.role.requester") : undefined, requester: x.member_type === "requester" || undefined }) },
  group: { path: "/groups", parse: (x) => ({ value: s(x.id), label: s(x.name) }) },
  team: { path: "/teams", parse: (x) => ({ value: s(x.id), label: s(x.name) }) },
  unit: { path: "/org-units", parse: (x) => ({ value: s(x.id), label: s(x.name), detail: s(x.code) || undefined }) },
  application: { path: "/architecture/applications", parse: (x) => ({ value: s(x.id), label: s(x.name), detail: s(x.code) || s(x.key) || undefined }) },
  supplier: { path: "/ict/providers", parse: (x) => ({ value: s(x.id), label: s(x.name), detail: s(x.key) || undefined }) },
};

const qs = (v: string) => encodeURIComponent(v);

const elementOpt = (e: { id: string; key: string; title: string; type_name?: string }): Opt => ({ value: e.id, label: `${e.key} ${e.title}`, detail: e.type_name });

/** Cerca sul server: massimo `limit` voci che contengono il testo. Per gli elementi, `types` limita ai tipi indicati. */
export async function searchEntities(kind: EntityKind, q: string, opts: { limit?: number; types?: string[] } = {}): Promise<Opt[]> {
  const limit = opts.limit ?? SERVER_LIMIT;
  if (kind === "element") {
    const text = q.trim();
    const typed = opts.types?.length ? `tipo: ${opts.types.join(", ")} ` : "";
    const body = /^[A-Za-z][A-Za-z0-9_]*-\d+$/.test(text) ? `chiave: ${text.toUpperCase()}` : text;
    const r = await api.search(typed + body, undefined, limit);
    return r.items.map(elementOpt);
  }
  const c = CONF[kind];
  const r = await call<{ items: Record<string, unknown>[] }>("GET", `${c.path}?q=${qs(q.trim())}&limit=${limit}`);
  const out = r.items.map(c.parse);
  remember(kind, out);
  return out;
}

// ---------------------------------------------------------------------
// Nomi delle voci scelte: una sola richiesta per tipo, anche con cento selettori nella pagina.

const names = new Map<string, Opt>();
const asked = new Set<string>();
const queue = new Map<EntityKind, Set<string>>();
const listeners = new Set<() => void>();
let version = 0;
let timer: ReturnType<typeof setTimeout> | undefined;

const keyOf = (kind: EntityKind, id: string) => `${kind}:${id}`;

function bump() {
  version++;
  listeners.forEach((l) => l());
}

/** Registra le voci già note (risultati di ricerca) così la scelta non richiede un'altra richiesta. */
export function remember(kind: EntityKind, opts: Opt[]) {
  let changed = false;
  for (const o of opts) {
    const k = keyOf(kind, o.value);
    if (!names.has(k)) {
      names.set(k, o);
      changed = true;
    }
  }
  if (changed) bump();
}

export function knownEntity(kind: EntityKind, id: string): Opt | undefined {
  return names.get(keyOf(kind, id));
}

async function flush() {
  timer = undefined;
  const batch = [...queue];
  queue.clear();
  await Promise.all(
    batch.map(async ([kind, ids]) => {
      const list = [...ids];
      try {
        if (kind === "element") {
          const found = await Promise.all(list.map((id) => api.element(id).then((d) => elementOpt({ ...d.element, type_name: d.element.type_name })).catch(() => undefined)));
          remember(kind, found.filter((o): o is Opt => !!o));
        } else {
          const c = CONF[kind];
          for (let i = 0; i < list.length; i += 100) {
            const part = list.slice(i, i + 100);
            const r = await call<{ items: Record<string, unknown>[] }>("GET", `${c.path}?ids=${qs(part.join(","))}&limit=${part.length}`);
            remember(kind, r.items.map(c.parse));
          }
        }
      } catch {
        /* il nome resta quello di riserva */
      }
      bump();
    }),
  );
}

function want(kind: EntityKind, id: string) {
  const k = keyOf(kind, id);
  if (names.has(k) || asked.has(k)) return;
  asked.add(k);
  if (!queue.has(kind)) queue.set(kind, new Set());
  queue.get(kind)!.add(id);
  timer ??= setTimeout(() => void flush(), 0);
}

function subscribe(l: () => void) {
  listeners.add(l);
  return () => listeners.delete(l);
}

/** Nome di una voce scelta: dalla memoria, dai suggerimenti o con una richiesta in blocco. */
export function useEntityLabels(kind: EntityKind, ids: string[], hints?: Record<string, string>): Record<string, string> {
  useSyncExternalStore(subscribe, () => version);
  const missing = ids.filter((id) => id && !hints?.[id]);
  const key = missing.join(",");
  useEffect(() => {
    for (const id of key ? key.split(",") : []) want(kind, id);
  }, [kind, key]);
  const out: Record<string, string> = {};
  for (const id of ids) out[id] = hints?.[id] ?? names.get(keyOf(kind, id))?.label ?? "";
  return out;
}
