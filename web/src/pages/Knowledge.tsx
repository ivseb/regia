import { Select } from "../select";
import { lazy, Suspense, useEffect, useMemo, useState, type DragEvent, type FormEvent, type ReactNode } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError, type Step } from "../api";
import { kb, type Comparison, type Home, type PageDetail, type Recent, type SearchHit, type Space, type SpaceView, type TreeNode, type VersionInfo } from "../knowledgeApi";
import { headingsOf, PageMarkdown, citedKeys } from "../kbMarkdown";
import { PageHeader } from "../shell";
import { ago, date, DECISIONS, ErrorNote, SectionTitle, StageGate, Loading } from "../ui";
import { Attachments, Comments } from "./Collaboration";
import { EvidencePanel } from "./KnowledgeEvidence";
import { InlineLayer } from "./KnowledgeThreads";
import { ReadingsNotice, ReadingsSection } from "./KnowledgeReadings";
import { ExportDialog, ImportDialog, PagePrint } from "./KnowledgeTransfer";
import { useNarrow } from "./timeline";
import { Dialog } from "./work-ui";
import { useTypeNames } from "../typesApi";
import { useT } from "../i18n/react";
import { fmtDate, fmtDateTime, fmtTime } from "../i18n/format";
import { Trans } from "../i18n/Trans";
import { EmptyState } from "../emptyState";

// Knowledge (piano, passo 19; replica/knowledge.md): «Documenti» con gli spazi e le pagine
// recenti, spazio con l'albero delle pagine e la ricerca, pagina con indice automatico,
// versioni e confronto, documento controllato con revisione e approvazione. L'editor si
// carica solo quando si modifica (KnowledgeEditor.tsx).

const PageEditor = lazy(() => import("./KnowledgeEditor").then((m) => ({ default: m.PageEditor })));

export type PageSearch = { vista?: "storia" | "modifica"; v?: number; da?: number; a?: number; approvata?: boolean };

function AccessBadge({ access }: { access: Space["access"] }) {
  const t = useT();
  return access === "restricted" ? (
    <span className="border border-flag px-1 text-[10.5px] font-semibold text-flag" title={t("knowledge.main.restricted.title")}>
      {t("knowledge.main.restricted.badge")}
    </span>
  ) : null;
}

function DocState({ state, category, controlled }: { state?: string; category?: string; controlled: boolean }) {
  if (!controlled || !state) return null;
  const cls = category === "done" ? "border-ok text-ok" : category === "in_progress" ? "border-accent text-accent" : category === "cancelled" ? "border-hair text-muted" : "border-rule text-fg";
  return <span className={`whitespace-nowrap border px-1 text-[10.5px] font-semibold uppercase ${cls}`}>{state}</span>;
}

// ---------------------------------------------------------------------
// Documenti: spazi e pagine recenti

export function KnowledgeHome() {
  const t = useT();
  const home = useQuery({ queryKey: ["kb", "home"], queryFn: kb.home });
  const [creating, setCreating] = useState(false);
  const h = home.data;
  return (
    <>
      <PageHeader
        path={t("knowledge.main.home.path")}
        title={t("knowledge.main.home.title")}
        actions={h?.can_create && <button className="btn btn-primary" onClick={() => setCreating(true)}>{t("knowledge.main.home.newSpace")}</button>}
      />
      {h && <IndicatorStrip h={h} />}
      <div className="grid min-w-0 grid-cols-1 gap-8 px-4 pb-10 md:px-7 lg:grid-cols-[minmax(0,1fr)_380px]">
        <section className="flex min-w-0 flex-col gap-2" aria-label={t("knowledge.main.home.spaces")}>
          <SectionTitle aside={<span className="font-mono text-[11.5px] text-muted tabular">{h?.spaces.length || ""}</span>}>{t("knowledge.main.home.spaces")}</SectionTitle>
          <ErrorNote error={home.error} />
          {home.isPending && <Loading className="m-0 text-muted" />}
          {h && h.spaces.length === 0 && (
            <EmptyState
              title={t("lavoro.vuoti.documenti.titolo")}
              text={t("lavoro.vuoti.documenti.testo")}
              action={h.can_create ? <button className="btn btn-primary" onClick={() => setCreating(true)}>{t("lavoro.vuoti.documenti.azione")}</button> : undefined}
            />
          )}
          <ul className="m-0 flex list-none flex-col p-0">
            {h?.spaces.map((s) => (
              <li key={s.id} className="border-b border-hair">
                <Link to="/documenti/spazi/$space" params={{ space: s.key }} className="flex min-w-0 items-start gap-3 py-2.5 text-fg no-underline hover:text-accent">
                  <span className="w-14 shrink-0 pt-0.5 font-mono text-[12.5px] text-accent">{s.key}</span>
                  <span className="flex min-w-0 flex-1 flex-col gap-0.5">
                    <span className="flex flex-wrap items-center gap-2 font-semibold">
                      {s.name} <AccessBadge access={s.access} />
                    </span>
                    {s.description && <span className="text-[12.5px] text-muted">{s.description}</span>}
                  </span>
                  <span className="hidden shrink-0 flex-col items-end text-[12px] text-muted sm:flex">
                    <span className="tabular">{s.rights.read ? t("knowledge.main.home.pages", { count: s.pages }) : t("knowledge.main.home.adminOnly")}</span>
                    {s.updated_at && <span>{ago(s.updated_at)}</span>}
                  </span>
                </Link>
              </li>
            ))}
          </ul>
        </section>
        <div className="flex min-w-0 flex-col gap-8">
          {h && h.my_readings.length > 0 && <MyReadings items={h.my_readings} />}
          {h && h.to_review.length > 0 && <RecentList title={t("knowledge.main.home.toReview")} items={h.to_review} />}
          {h && h.review_due.length > 0 && <div id="revisioni" className="scroll-mt-4"><RecentList title={t("knowledge.main.home.reviewDue")} items={h.review_due} /></div>}
          {h && h.open_threads.length > 0 && <OpenThreads items={h.open_threads} />}
          {h && h.campaigns.length > 0 && <Campaigns items={h.campaigns} />}
          <RecentList title={t("knowledge.main.home.recent")} items={h?.recent ?? []} empty={h ? t("knowledge.main.home.noRecent") : undefined} />
        </div>
      </div>
      {creating && <NewSpaceDialog onClose={() => setCreating(false)} />}
    </>
  );
}

function Tile({ label, value, note, tone, href }: { label: string; value: number; note: string; tone?: "danger" | "flag"; href?: string }) {
  const color = value > 0 && tone === "danger" ? "text-danger" : value > 0 && tone === "flag" ? "text-flag" : "text-fg";
  const body = (
    <>
      <span className="label text-muted">{label}</span>
      <span className={`font-mono text-[28px] leading-none font-semibold tabular ${color}`}>{value}</span>
      <span className="text-[12px] text-muted">{note}</span>
    </>
  );
  const cls = "flex min-w-0 flex-col gap-1.5 border-t-2 border-rule py-2.5 pr-3 text-fg no-underline";
  return href ? (
    <a href={href} className={`${cls} hover:text-accent`}>{body}</a>
  ) : (
    <div className={cls}>{body}</div>
  );
}

/** Indicatori della home: revisioni scadute, commenti aperti, campagne di lettura. */
function IndicatorStrip({ h }: { h: Home }) {
  const t = useT();
  const i = h.indicators;
  if (h.spaces.length === 0) return null;
  return (
    <section aria-label={t("knowledge.main.ind.label")} className="grid grid-cols-2 gap-x-4 gap-y-2 px-4 pb-6 md:px-7 lg:grid-cols-4">
      <Tile
        label={t("knowledge.main.ind.overdue")}
        value={i.review_overdue}
        tone="danger"
        note={i.review_soon > 0 ? t("knowledge.main.ind.soon", { count: i.review_soon }) : t("knowledge.main.ind.noneSoon")}
        href={i.review_overdue + i.review_soon > 0 ? "#revisioni" : undefined}
      />
      <Tile
        label={t("knowledge.main.ind.openThreads")}
        value={i.open_threads}
        tone="flag"
        note={i.orphan_threads > 0 ? t("knowledge.main.ind.orphans", { count: i.orphan_threads }) : t("knowledge.main.ind.sentences")}
        href={i.open_threads > 0 ? "#commenti-aperti" : undefined}
      />
      <Tile
        label={t("knowledge.main.ind.campaigns")}
        value={i.campaigns_active}
        tone="danger"
        note={i.campaigns_overdue > 0 ? t("knowledge.main.ind.late", { count: i.campaigns_overdue }) : i.readings_missing > 0 ? t("knowledge.main.ind.missing", { count: i.readings_missing }) : t("knowledge.main.ind.running")}
        href={i.campaigns_active > 0 ? "#campagne" : undefined}
      />
      <Tile label={t("knowledge.main.ind.mine")} value={i.my_readings} tone="flag" note={i.my_readings > 0 ? t("knowledge.main.ind.mineWaiting") : t("knowledge.main.ind.mineNone")} href={i.my_readings > 0 ? "#da-leggere" : undefined} />
    </section>
  );
}

function MyReadings({ items }: { items: Home["my_readings"] }) {
  const t = useT();
  return (
    <section id="da-leggere" className="flex scroll-mt-4 flex-col gap-1" aria-label={t("knowledge.main.mine.title")}>
      <SectionTitle aside={<span className="font-mono text-[11.5px] text-muted tabular">{items.length}</span>}>{t("knowledge.main.mine.title")}</SectionTitle>
      <ul className="m-0 flex list-none flex-col p-0">
        {items.map((r) => (
          <li key={r.key + r.name}>
            <Link to="/documenti/$key" params={{ key: r.key }} hash="letture" className="flex min-w-0 flex-col py-1.5 text-fg no-underline hover:text-accent">
              <span className="min-w-0 truncate">{r.title}</span>
              <span className="text-[11.5px] text-muted">
                <span className="font-mono">{r.key}</span>{t("knowledge.main.mine.version", { version: r.version })}
                {r.due && <span className={r.overdue ? "font-semibold text-danger" : ""}>{t("knowledge.main.mine.due", { date: fmtDate(new Date(r.due + "T00:00:00")) })}</span>}
              </span>
            </Link>
          </li>
        ))}
      </ul>
    </section>
  );
}

function OpenThreads({ items }: { items: Home["open_threads"] }) {
  const t = useT();
  return (
    <section id="commenti-aperti" className="flex scroll-mt-4 flex-col gap-1" aria-label={t("knowledge.main.threads.title")}>
      <SectionTitle>{t("knowledge.main.threads.title")}</SectionTitle>
      <ul className="m-0 flex list-none flex-col p-0">
        {items.map((th) => (
          <li key={th.thread_id}>
            <Link to="/documenti/$key" params={{ key: th.key }} className="flex min-w-0 flex-col gap-0.5 py-1.5 text-fg no-underline hover:text-accent">
              <span className="min-w-0 truncate text-[12.5px] text-muted">«{th.quote}»{th.orphan ? t("knowledge.main.threads.gone") : ""}</span>
              <span className="min-w-0 truncate">{th.excerpt}</span>
              <span className="text-[11.5px] text-muted">
                {th.title} · {th.author} · {ago(th.updated_at)}
                {th.replies > 0 ? t("knowledge.main.threads.replies", { count: th.replies }) : ""}
              </span>
            </Link>
          </li>
        ))}
      </ul>
    </section>
  );
}

function Campaigns({ items }: { items: Home["campaigns"] }) {
  const t = useT();
  return (
    <section id="campagne" className="flex scroll-mt-4 flex-col gap-1" aria-label={t("knowledge.main.campaigns.title")}>
      <SectionTitle>{t("knowledge.main.campaigns.title")}</SectionTitle>
      <ul className="m-0 flex list-none flex-col p-0">
        {items.map((c) => (
          <li key={c.key + c.name}>
            <Link to="/documenti/$key" params={{ key: c.key }} hash="letture" className="flex min-w-0 flex-col gap-1 py-1.5 text-fg no-underline hover:text-accent">
              <span className="flex min-w-0 items-baseline justify-between gap-2">
                <span className="min-w-0 truncate">{c.title}</span>
                <span className="shrink-0 font-mono text-[12px] tabular">{c.confirmed} / {c.total}</span>
              </span>
              <span className="h-1 w-full bg-hair"><span className={`block h-full ${c.overdue ? "bg-danger" : "bg-accent"}`} style={{ width: `${c.total ? Math.round((c.confirmed * 100) / c.total) : 0}%` }} /></span>
              <span className="text-[11.5px] text-muted">
                v{c.version}
                {c.due && <span className={c.overdue ? "font-semibold text-danger" : ""}>{t("knowledge.main.mine.due", { date: fmtDate(new Date(c.due + "T00:00:00")) })}</span>}
              </span>
            </Link>
          </li>
        ))}
      </ul>
    </section>
  );
}

function RecentList({ title, items, empty }: { title: string; items: Recent[]; empty?: string }) {
  return (
    <section className="flex flex-col gap-1" aria-label={title}>
      <SectionTitle>{title}</SectionTitle>
      {items.length === 0 && empty && <p className="m-0 text-[12.5px] text-muted">{empty}</p>}
      <ul className="m-0 flex list-none flex-col p-0">
        {items.map((r) => (
          <li key={r.key}>
            <Link to="/documenti/$key" params={{ key: r.key }} className="flex min-w-0 flex-col py-1.5 text-fg no-underline hover:text-accent">
              <span className="flex min-w-0 items-baseline gap-2">
                <span className="min-w-0 truncate">{r.title}</span>
                <DocState state={r.state} category={r.state_category} controlled={r.controlled} />
              </span>
              <span className="text-[11.5px] text-muted">
                <span className="font-mono">{r.key}</span> · {r.space_name} · {ago(r.updated_at)}
                {r.updated_by ? ` · ${r.updated_by}` : ""}
              </span>
            </Link>
          </li>
        ))}
      </ul>
    </section>
  );
}

function NewSpaceDialog({ onClose }: { onClose: () => void }) {
  const t = useT();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [key, setKey] = useState("");
  const [touched, setTouched] = useState(false);
  const [description, setDescription] = useState("");
  const [access, setAccess] = useState<"open" | "restricted">("open");
  const proposed = name
    .normalize("NFD")
    .replace(/[̀-ͯ]/g, "")
    .toUpperCase()
    .split(/[^A-Z0-9]+/)
    .filter(Boolean);
  const auto = (proposed.length > 1 ? proposed.map((w) => w[0]).join("") : (proposed[0] ?? "").slice(0, 3)).slice(0, 10);
  const create = useMutation({
    mutationFn: () => kb.createSpace({ key: touched ? key : auto, name, description, access }),
    onSuccess: (s) => {
      qc.invalidateQueries({ queryKey: ["kb"] });
      navigate({ to: "/documenti/spazi/$space", params: { space: s.key } });
    },
  });
  return (
    <Dialog title={t("knowledge.main.newSpace.title")} onClose={onClose}>
      <form
        className="flex flex-col gap-3"
        onSubmit={(e) => {
          e.preventDefault();
          create.mutate();
        }}
      >
        <label className="flex flex-col gap-1">
          <span className="label">{t("knowledge.main.newSpace.name")}</span>
          <input className="field" required value={name} onChange={(e) => setName(e.target.value)} placeholder={t("knowledge.main.newSpace.namePh")} />
        </label>
        <label className="flex flex-col gap-1">
          <span className="label">{t("knowledge.main.newSpace.key")}</span>
          <input
            className="field font-mono uppercase"
            value={touched ? key : auto}
            onChange={(e) => {
              setTouched(true);
              setKey(e.target.value.toUpperCase());
            }}
            maxLength={20}
            pattern="[A-Z][A-Z0-9_]*"
            aria-describedby="chiave-aiuto"
          />
          <span id="chiave-aiuto" className="text-[12px] text-muted">{t("knowledge.main.newSpace.keyHelp", { key: (touched ? key : auto) || "DOC" })}</span>
        </label>
        <label className="flex flex-col gap-1">
          <span className="label">{t("knowledge.main.newSpace.description")}</span>
          <input className="field" value={description} onChange={(e) => setDescription(e.target.value)} />
        </label>
        <fieldset className="m-0 flex flex-col gap-1.5 border-0 p-0">
          <legend className="label mb-1">{t("knowledge.main.newSpace.access")}</legend>
          <label className="flex items-start gap-2">
            <input type="radio" name="access" checked={access === "open"} onChange={() => setAccess("open")} className="mt-1" />
            <span>
              <span className="font-semibold">{t("knowledge.main.newSpace.open")}</span>
              <span className="block text-[12.5px] text-muted">{t("knowledge.main.newSpace.openHelp")}</span>
            </span>
          </label>
          <label className="flex items-start gap-2">
            <input type="radio" name="access" checked={access === "restricted"} onChange={() => setAccess("restricted")} className="mt-1" />
            <span>
              <span className="font-semibold">{t("knowledge.main.newSpace.restricted")}</span>
              <span className="block text-[12.5px] text-muted">{t("knowledge.main.newSpace.restrictedHelp")}</span>
            </span>
          </label>
        </fieldset>
        <ErrorNote error={create.error} />
        <div className="flex justify-end gap-2">
          <button type="button" className="btn" onClick={onClose}>{t("knowledge.main.cancel")}</button>
          <button className="btn btn-primary" disabled={create.isPending || !name.trim()}>{t("knowledge.main.newSpace.create")}</button>
        </div>
      </form>
    </Dialog>
  );
}

// ---------------------------------------------------------------------
// Albero delle pagine

type TreeIndex = { byParent: Map<string, TreeNode[]>; byId: Map<string, TreeNode>; byKey: Map<string, TreeNode> };

function indexTree(pages: TreeNode[]): TreeIndex {
  const byParent = new Map<string, TreeNode[]>();
  const byId = new Map<string, TreeNode>();
  const byKey = new Map<string, TreeNode>();
  for (const p of pages) {
    byId.set(p.id, p);
    byKey.set(p.key, p);
  }
  for (const p of pages) {
    const parent = p.parent_id && byId.has(p.parent_id) ? p.parent_id : "";
    byParent.set(parent, [...(byParent.get(parent) ?? []), p]);
  }
  return { byParent, byId, byKey };
}

function ancestors(idx: TreeIndex, key?: string): Set<string> {
  const out = new Set<string>();
  let n = key ? idx.byKey.get(key) : undefined;
  for (let i = 0; n && n.parent_id && i < 50; i++) {
    out.add(n.parent_id);
    n = idx.byId.get(n.parent_id);
  }
  return out;
}

function descendants(idx: TreeIndex, id: string): Set<string> {
  const out = new Set<string>([id]);
  const walk = (pid: string) => {
    for (const c of idx.byParent.get(pid) ?? []) {
      if (!out.has(c.id)) {
        out.add(c.id);
        walk(c.id);
      }
    }
  };
  walk(id);
  return out;
}

type DropZone = "before" | "inside" | "after";

function SpaceTree({ view, current, onPick }: { view: SpaceView; current?: string; onPick?: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const idx = useMemo(() => indexTree(view.pages), [view.pages]);
  const [open, setOpen] = useState<Set<string>>(() => ancestors(idx, current));
  const [filter, setFilter] = useState("");
  const [drag, setDrag] = useState<{ key: string; over?: string; zone?: DropZone } | null>(null);
  const narrow = useNarrow();
  const canDrag = view.space.rights.write && !narrow;
  useEffect(() => {
    setOpen((o) => new Set([...o, ...ancestors(idx, current)]));
  }, [idx, current]);
  const move = useMutation({
    mutationFn: (m: { key: string; parent?: string; after?: string; before?: string }) => kb.move(m.key, { parent: m.parent, after: m.after, before: m.before }),
    onSettled: () => qc.invalidateQueries({ queryKey: ["kb"] }),
  });
  const toggle = (id: string) =>
    setOpen((o) => {
      const n = new Set(o);
      if (n.has(id)) n.delete(id);
      else n.add(id);
      return n;
    });
  const zoneOf = (e: DragEvent<HTMLElement>): DropZone => {
    const r = e.currentTarget.getBoundingClientRect();
    const y = (e.clientY - r.top) / r.height;
    return y < 0.28 ? "before" : y > 0.72 ? "after" : "inside";
  };
  const drop = (target: TreeNode, zone: DropZone) => {
    if (!drag || drag.key === target.key) return;
    const src = idx.byKey.get(drag.key);
    if (!src || descendants(idx, src.id).has(target.id)) return;
    const parentKey = target.parent_id ? idx.byId.get(target.parent_id)?.key ?? "" : "";
    if (zone === "inside") {
      move.mutate({ key: src.key, parent: target.key });
      setOpen((o) => new Set([...o, target.id]));
    } else move.mutate({ key: src.key, parent: parentKey, [zone]: target.key });
  };
  const f = filter.trim().toLowerCase();
  const matches = f ? view.pages.filter((p) => p.title.toLowerCase().includes(f) || p.key.toLowerCase().includes(f)).slice(0, 200) : [];
  const node = (p: TreeNode, depth: number): ReactNode => {
    const kids = idx.byParent.get(p.id) ?? [];
    const isOpen = open.has(p.id);
    const active = p.key === current;
    const over = drag?.over === p.key ? drag.zone : undefined;
    return (
      <li key={p.id} role="treeitem" aria-expanded={kids.length ? isOpen : undefined} aria-selected={active} className="flex flex-col">
        <div
          className={`flex min-w-0 items-center gap-1 border-y ${over === "before" ? "border-t-accent border-b-transparent" : over === "after" ? "border-b-accent border-t-transparent" : "border-transparent"} ${over === "inside" ? "bg-sel" : active ? "bg-sel" : ""}`}
          style={{ paddingLeft: depth * 14 }}
          draggable={canDrag}
          onDragStart={(e) => {
            e.dataTransfer.effectAllowed = "move";
            e.dataTransfer.setData("text/plain", p.key);
            setDrag({ key: p.key });
          }}
          onDragEnd={() => setDrag(null)}
          onDragOver={(e) => {
            if (!drag) return;
            e.preventDefault();
            const zone = zoneOf(e);
            if (drag.over !== p.key || drag.zone !== zone) setDrag({ ...drag, over: p.key, zone });
          }}
          onDrop={(e) => {
            e.preventDefault();
            drop(p, zoneOf(e));
            setDrag(null);
          }}
        >
          <button
            type="button"
            className={`flex size-6 shrink-0 items-center justify-center text-muted hover:text-fg ${kids.length ? "" : "invisible"}`}
            aria-label={isOpen ? t("knowledge.main.tree.close", { title: p.title }) : t("knowledge.main.tree.open", { title: p.title })}
            onClick={() => toggle(p.id)}
            tabIndex={kids.length ? 0 : -1}
          >
            <span aria-hidden="true" className={`inline-block transition-transform ${isOpen ? "rotate-90" : ""}`}>›</span>
          </button>
          <Link
            to="/documenti/$key"
            params={{ key: p.key }}
            onClick={onPick}
            className={`flex min-w-0 flex-1 items-baseline gap-1.5 py-1 pr-2 no-underline ${active ? "font-semibold text-accent" : "text-fg hover:text-accent"}`}
            aria-current={active ? "page" : undefined}
          >
            <span className="min-w-0 truncate">{p.title}</span>
            {p.controlled && p.state && <span className="shrink-0 text-[10.5px] text-muted">{p.state}</span>}
          </Link>
        </div>
        {kids.length > 0 && isOpen && (
          <ul role="group" className="m-0 flex list-none flex-col p-0">
            {kids.map((c) => node(c, depth + 1))}
          </ul>
        )}
      </li>
    );
  };
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <input className="field py-1.5" type="search" placeholder={t("knowledge.main.tree.filter")} aria-label={t("knowledge.main.tree.filter")} value={filter} onChange={(e) => setFilter(e.target.value)} />
      <ErrorNote error={move.error} />
      {f ? (
        <ul className="m-0 flex list-none flex-col p-0" aria-label={t("knowledge.main.tree.found")}>
          {matches.map((p) => (
            <li key={p.id}>
              <Link to="/documenti/$key" params={{ key: p.key }} onClick={onPick} className="flex min-w-0 flex-col py-1 text-fg no-underline hover:text-accent">
                <span className="truncate">{p.title}</span>
                <span className="font-mono text-[11px] text-muted">{p.key}</span>
              </Link>
            </li>
          ))}
          {matches.length === 0 && <li className="text-[12.5px] text-muted">{t("knowledge.main.tree.noMatch", { filter })}</li>}
        </ul>
      ) : (
        <ul role="tree" aria-label={t("knowledge.main.tree.pagesOf", { name: view.space.name })} className="m-0 flex list-none flex-col p-0 text-[13px]">
          {(idx.byParent.get("") ?? []).map((p) => node(p, 0))}
          {view.pages.length === 0 && <li className="text-[12.5px] text-muted">{t("knowledge.main.tree.empty")}</li>}
        </ul>
      )}
      {canDrag && view.pages.length > 1 && <p className="m-0 text-[11.5px] text-muted">{t("knowledge.main.tree.dragHint")}</p>}
    </div>
  );
}

/** Spazio con l'albero a sinistra (sotto i 768 px a scomparsa). */
function SpaceShell({ spaceKey, current, children, aside }: { spaceKey: string; current?: string; children: ReactNode; aside?: ReactNode }) {
  const t = useT();
  const tn = useTypeNames();
  const view = useQuery({ queryKey: ["kb", "tree", spaceKey], queryFn: () => kb.tree(spaceKey) });
  const [drawer, setDrawer] = useState(false);
  const [creating, setCreating] = useState(false);
  const v = view.data;
  useEffect(() => setDrawer(false), [current]);
  if (view.error) return <div className="px-4 py-6 md:px-7"><ErrorNote error={view.error} /></div>;
  const nav = v && (
    <div className="flex min-w-0 flex-col gap-3">
      <div className="flex items-center gap-2">
        <Link to="/documenti/spazi/$space" params={{ space: v.space.key }} className="min-w-0 flex-1 truncate font-bold text-fg no-underline hover:text-accent">
          {v.space.name}
        </Link>
        <AccessBadge access={v.space.access} />
      </div>
      {v.space.rights.write && (
        <button className="btn justify-center" onClick={() => setCreating(true)}>
          {tn.create("page", t("knowledge.main.page.noun"))}
        </button>
      )}
      <SpaceTree view={v} current={current} onPick={() => setDrawer(false)} />
    </div>
  );
  return (
    <div className="flex min-w-0 flex-1 flex-col md:flex-row">
      <aside aria-label={t("knowledge.main.shell.pagesOfSpace")} className="hidden w-[272px] shrink-0 border-r border-hair px-4 py-5 md:sticky md:top-0 md:block md:h-screen md:overflow-y-auto">
        {nav ?? <Loading className="m-0 text-muted" />}
      </aside>
      <div className="flex items-center gap-2 border-b border-hair px-4 py-2 md:hidden">
        <button className="btn py-1" aria-expanded={drawer} aria-controls="albero-mobile" onClick={() => setDrawer(true)}>
          {t("knowledge.main.shell.pages")}
        </button>
        <span className="min-w-0 flex-1 truncate text-[12.5px] text-muted">{v?.space.name}</span>
      </div>
      {drawer && (
        <div className="fixed inset-0 z-[55] flex bg-fg/30 md:hidden" onMouseDown={(e) => e.target === e.currentTarget && setDrawer(false)}>
          <div id="albero-mobile" role="dialog" aria-modal="true" aria-label={t("knowledge.main.shell.pagesOfSpace")} className="flex h-full w-[86%] max-w-[340px] flex-col gap-3 overflow-y-auto border-r-2 border-rule bg-bg p-4">
            <button className="self-end text-[12px] text-muted underline" onClick={() => setDrawer(false)}>{t("knowledge.main.close")}</button>
            {nav}
          </div>
        </div>
      )}
      <div className="flex min-w-0 flex-1">
        <div className="min-w-0 flex-1">{children}</div>
        {aside}
      </div>
      {creating && v && <NewPageDialog space={v.space} parent={current} onClose={() => setCreating(false)} />}
    </div>
  );
}

function NewPageDialog({ space, parent, onClose }: { space: Space; parent?: string; onClose: () => void }) {
  const t = useT();
  const tn = useTypeNames();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const templates = useQuery({ queryKey: ["kb", "templates", space.key], queryFn: () => kb.templates(space.key) });
  const [title, setTitle] = useState("");
  const [template, setTemplate] = useState("");
  const [asChild, setAsChild] = useState(!!parent);
  const create = useMutation({
    mutationFn: () => kb.createPage(space.key, { title, template: template || undefined, parent: asChild ? parent : undefined }),
    onSuccess: (p) => {
      qc.invalidateQueries({ queryKey: ["kb"] });
      navigate({ to: "/documenti/$key", params: { key: p.page.key }, search: { vista: "modifica" } });
    },
  });
  const list = (templates.data?.items ?? []).filter((tp) => !tp.archived);
  return (
    <Dialog title={t("knowledge.main.newPage.title", { create: tn.create("page", t("knowledge.main.page.noun")), space: space.name })} onClose={onClose} wide>
      <form
        className="flex flex-col gap-3"
        onSubmit={(e: FormEvent) => {
          e.preventDefault();
          create.mutate();
        }}
      >
        <label className="flex flex-col gap-1">
          <span className="label">{t("knowledge.main.newPage.titleField")}</span>
          <input className="field" required value={title} onChange={(e) => setTitle(e.target.value)} maxLength={500} />
        </label>
        {parent && (
          <label className="flex items-center gap-2">
            <input type="checkbox" checked={asChild} onChange={(e) => setAsChild(e.target.checked)} />
            {t("knowledge.main.newPage.asChild", { parent })}
          </label>
        )}
        <fieldset className="m-0 flex flex-col gap-1 border-0 p-0">
          <legend className="label mb-1">{t("knowledge.main.newPage.template")}</legend>
          <ErrorNote error={templates.error} />
          <div className="grid grid-cols-1 gap-1 sm:grid-cols-2">
            {[{ id: "", key: "", name: t("knowledge.main.newPage.blank"), description: t("knowledge.main.newPage.blankHelp"), controlled: false }, ...list].map((tp) => (
              <label key={tp.key || "vuota"} className={`flex cursor-pointer items-start gap-2 border p-2 ${template === tp.key ? "border-accent bg-sel" : "border-hair hover:border-rule"}`}>
                <input type="radio" name="template" checked={template === tp.key} onChange={() => setTemplate(tp.key)} className="mt-1" />
                <span className="flex min-w-0 flex-col">
                  <span className="font-semibold">
                    {tp.name}
                    {tp.controlled && <span className="ml-1.5 text-[10.5px] font-semibold text-flag">{t("knowledge.main.controlledBadge")}</span>}
                  </span>
                  <span className="text-[12px] text-muted">{tp.description}</span>
                </span>
              </label>
            ))}
          </div>
        </fieldset>
        <ErrorNote error={create.error} />
        <div className="flex justify-end gap-2">
          <button type="button" className="btn" onClick={onClose}>{t("knowledge.main.cancel")}</button>
          <button className="btn btn-primary" disabled={create.isPending || !title.trim()}>{t("knowledge.main.newPage.create")}</button>
        </div>
      </form>
    </Dialog>
  );
}

// ---------------------------------------------------------------------
// Spazio: presentazione, ricerca, impostazioni

function useDebounced<T>(v: T, ms: number): T {
  const [d, setD] = useState(v);
  useEffect(() => {
    const t = setTimeout(() => setD(v), ms);
    return () => clearTimeout(t);
  }, [v, ms]);
  return d;
}

export function SpacePage({ spaceKey, q, onSearch }: { spaceKey: string; q?: string; onSearch: (q: string) => void }) {
  const t = useT();
  const tn = useTypeNames();
  const view = useQuery({ queryKey: ["kb", "tree", spaceKey], queryFn: () => kb.tree(spaceKey) });
  const [text, setText] = useState(q ?? "");
  const debounced = useDebounced(text.trim(), 250);
  useEffect(() => {
    if ((q ?? "") !== debounced) onSearch(debounced);
    // onSearch cambia a ogni render del router: basta il testo.
  }, [debounced]); // eslint-disable-line react-hooks/exhaustive-deps
  const found = useQuery({ queryKey: ["kb", "search", spaceKey, debounced], queryFn: () => kb.search(spaceKey, debounced), enabled: debounced.length >= 2 });
  const v = view.data;
  const [transfer, setTransfer] = useState<"export" | "import" | null>(null);
  const recent = useMemo(() => [...(v?.pages ?? [])].sort((a, b) => b.updated_at.localeCompare(a.updated_at)).slice(0, 12), [v]);
  const roots = (v?.pages ?? []).filter((p) => !p.parent_id);
  useEffect(() => {
    if (v) document.title = t("knowledge.main.space.docTitle", { name: v.space.name });
    return () => {
      document.title = "Regia";
    };
  }, [v]); // eslint-disable-line react-hooks/exhaustive-deps
  return (
    <SpaceShell spaceKey={spaceKey}>
      <div className="flex max-w-[920px] min-w-0 flex-col gap-6 px-4 py-6 md:px-8">
        <header className="flex flex-wrap items-end gap-3">
          <div className="flex min-w-0 flex-col gap-1">
            <span className="font-mono text-[11.5px] text-muted">
              <Link to="/documenti" className="text-muted no-underline hover:text-accent">{t("knowledge.main.space.crumb")}</Link> / {spaceKey}
            </span>
            <h1 className="m-0 text-[30px] leading-[1.05] font-bold tracking-[-0.025em]">{v?.space.name ?? spaceKey}</h1>
            {v?.space.description && <p className="m-0 text-muted">{v.space.description}</p>}
          </div>
          <div className="ml-auto flex flex-wrap gap-2">
            {v && <button className="btn" onClick={() => setTransfer("export")}>{t("knowledge.main.space.export")}</button>}
            {v?.space.rights.write && <button className="btn" onClick={() => setTransfer("import")}>{t("knowledge.main.space.import")}</button>}
            {v?.space.rights.manage && (
              <Link to="/documenti/spazi/$space/impostazioni" params={{ space: spaceKey }} className="btn no-underline">
                {t("knowledge.main.space.settings")}
              </Link>
            )}
          </div>
        </header>
        {transfer === "export" && <ExportDialog space={spaceKey} onClose={() => setTransfer(null)} />}
        {transfer === "import" && <ImportDialog space={spaceKey} onClose={() => setTransfer(null)} />}
        <div role="search" className="flex flex-col gap-2">
          <label className="flex flex-col gap-1">
            <span className="label">{t("knowledge.main.space.search")}</span>
            <input className="field" type="search" value={text} onChange={(e) => setText(e.target.value)} placeholder={t("knowledge.main.space.searchPh")} />
          </label>
          <ErrorNote error={found.error} />
          {debounced.length >= 2 && found.data && <SearchResults hits={found.data.items} text={debounced} />}
        </div>
        {debounced.length < 2 && v && (
          <div className="grid grid-cols-1 gap-8 md:grid-cols-2">
            <section className="flex flex-col gap-1">
              <SectionTitle>{t("knowledge.main.space.sections")}</SectionTitle>
              {roots.length === 0 && <p className="m-0 text-[12.5px] text-muted">{t("knowledge.main.space.noItems")} {v.space.rights.write ? t("knowledge.main.space.start", { create: tn.create("page", t("knowledge.main.page.noun")) }) : ""}</p>}
              <ul className="m-0 flex list-none flex-col p-0">
                {roots.map((p) => (
                  <li key={p.id}>
                    <Link to="/documenti/$key" params={{ key: p.key }} className="flex items-baseline gap-2 py-1 text-fg no-underline hover:text-accent">
                      <span className="min-w-0 truncate">{p.title}</span>
                      <DocState state={p.state} category={p.state_category} controlled={p.controlled} />
                    </Link>
                  </li>
                ))}
              </ul>
            </section>
            <section className="flex flex-col gap-1">
              <SectionTitle>{t("knowledge.main.space.recent")}</SectionTitle>
              <ul className="m-0 flex list-none flex-col p-0">
                {recent.map((p) => (
                  <li key={p.id}>
                    <Link to="/documenti/$key" params={{ key: p.key }} className="flex items-baseline justify-between gap-2 py-1 text-fg no-underline hover:text-accent">
                      <span className="min-w-0 truncate">{p.title}</span>
                      <span className="shrink-0 text-[11.5px] text-muted">{ago(p.updated_at)}</span>
                    </Link>
                  </li>
                ))}
              </ul>
            </section>
          </div>
        )}
      </div>
    </SpaceShell>
  );
}

/** Evidenzia nel titolo le parole cercate (prefissi). */
function highlight(title: string, text: string): ReactNode {
  const words = text
    .toLowerCase()
    .split(/[^\p{L}\p{N}]+/u)
    .filter((w) => w.length > 1)
    .map((w) => w.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"));
  if (!words.length) return title;
  const re = new RegExp(`(${words.join("|")})`, "giu");
  return title.split(re).map((part, i) => (i % 2 ? <mark key={i} className="bg-sel font-semibold text-fg">{part}</mark> : part));
}

function SearchResults({ hits, text }: { hits: SearchHit[]; text: string }) {
  const t = useT();
  if (hits.length === 0) return <p className="m-0 text-muted">{t("knowledge.main.search.none", { text })}</p>;
  return (
    <ol className="m-0 flex list-none flex-col p-0" aria-label={t("knowledge.main.search.results")}>
      {hits.map((h) => (
        <li key={h.key} className="border-b border-hair">
          <Link to="/documenti/$key" params={{ key: h.key }} className="flex flex-col gap-0.5 py-2 text-fg no-underline hover:text-accent">
            <span className="flex items-baseline gap-2">
              <span className="font-semibold">{highlight(h.title, text)}</span>
              <span className="font-mono text-[11px] text-muted">{h.key}</span>
            </span>
            {h.excerpt.length > 0 && (
              <span className="text-[12.5px] text-muted">
                {h.excerpt.map((s, i) => (s.hit ? <mark key={i} className="bg-sel font-semibold text-fg">{s.t}</mark> : <span key={i}>{s.t}</span>))}
              </span>
            )}
          </Link>
        </li>
      ))}
    </ol>
  );
}

export function SpaceSettings({ spaceKey }: { spaceKey: string }) {
  const t = useT();
  const qc = useQueryClient();
  const view = useQuery({ queryKey: ["kb", "tree", spaceKey], queryFn: () => kb.tree(spaceKey) });
  const access = useQuery({ queryKey: ["kb", "access", spaceKey], queryFn: () => kb.access(spaceKey) });
  const templates = useQuery({ queryKey: ["kb", "templates", spaceKey], queryFn: () => kb.templates(spaceKey) });
  const members = useQuery({ queryKey: ["members"], queryFn: api.members, staleTime: 300_000 });
  const groups = useQuery({ queryKey: ["groups"], queryFn: () => import("../fieldsApi").then((m) => m.fieldsApi.groups()), staleTime: 300_000 });
  const sp = view.data?.space;
  const [name, setName] = useState<string | null>(null);
  const [description, setDescription] = useState<string | null>(null);
  const update = useMutation({
    mutationFn: (s: { name?: string; description?: string; access?: string }) => kb.updateSpace(spaceKey, s),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["kb"] }),
  });
  const [who, setWho] = useState("");
  const [role, setRole] = useState("viewer");
  const grant = useMutation({
    mutationFn: () => kb.grant(spaceKey, who.startsWith("g:") ? { role, group_id: who.slice(2) } : { role, member_id: who.slice(2) }),
    onSuccess: (r) => {
      qc.setQueryData(["kb", "access", spaceKey], r);
      setWho("");
    },
  });
  const revoke = useMutation({ mutationFn: (id: string) => kb.revoke(spaceKey, id), onSuccess: (r) => qc.setQueryData(["kb", "access", spaceKey], r) });
  if (view.error) return <div className="px-4 py-6 md:px-7"><ErrorNote error={view.error} /></div>;
  if (!sp) return <Loading className="px-4 py-6 text-muted md:px-7" />;
  return (
    <>
      <PageHeader path={t("knowledge.main.settings.path", { space: spaceKey })} title={t("knowledge.main.settings.title", { name: sp.name })} actions={<Link to="/documenti/spazi/$space" params={{ space: spaceKey }} className="btn no-underline">{t("knowledge.main.settings.back")}</Link>} />
      <div className="flex max-w-[860px] flex-col gap-8 px-4 pb-10 md:px-7">
        {!sp.rights.manage && <p className="m-0 text-muted">{t("knowledge.main.settings.managerOnly")}</p>}
        <section className="flex flex-col gap-3">
          <SectionTitle>{t("knowledge.main.settings.space")}</SectionTitle>
          <form
            className="grid grid-cols-1 gap-3 md:grid-cols-2"
            onSubmit={(e) => {
              e.preventDefault();
              update.mutate({ name: name ?? undefined, description: description ?? undefined });
            }}
          >
            <label className="flex flex-col gap-1">
              <span className="label">{t("knowledge.main.newSpace.name")}</span>
              <input className="field" value={name ?? sp.name} onChange={(e) => setName(e.target.value)} disabled={!sp.rights.manage} />
            </label>
            <label className="flex flex-col gap-1">
              <span className="label">{t("knowledge.main.newSpace.description")}</span>
              <input className="field" value={description ?? sp.description} onChange={(e) => setDescription(e.target.value)} disabled={!sp.rights.manage} />
            </label>
            <div className="flex flex-wrap items-center gap-3 md:col-span-2">
              <span className="label">{t("knowledge.main.newSpace.access")}</span>
              <div role="radiogroup" aria-label={t("knowledge.main.newSpace.access")} className="flex border border-hair text-[12.5px]">
                {(["open", "restricted"] as const).map((a) => (
                  <button
                    key={a}
                    type="button"
                    role="radio"
                    aria-checked={sp.access === a}
                    disabled={!sp.rights.manage || update.isPending}
                    className={`px-3 py-1 ${sp.access === a ? "bg-fg text-bg" : "text-muted hover:text-fg"}`}
                    onClick={() => sp.access !== a && update.mutate({ access: a })}
                  >
                    {a === "open" ? t("knowledge.main.newSpace.open") : t("knowledge.main.newSpace.restricted")}
                  </button>
                ))}
              </div>
              <span className="text-[12.5px] text-muted">
                {sp.access === "open" ? t("knowledge.main.settings.openHelp") : t("knowledge.main.settings.restrictedHelp")}
              </span>
              {sp.rights.manage && <button className="btn ml-auto" disabled={update.isPending || (name === null && description === null)}>{t("knowledge.main.save")}</button>}
            </div>
            <div className="md:col-span-2"><ErrorNote error={update.error} /></div>
          </form>
        </section>
        <section className="flex flex-col gap-2">
          <SectionTitle>{t("knowledge.main.settings.roles")}</SectionTitle>
          <ErrorNote error={access.error ?? revoke.error} />
          <table className="w-full border-collapse text-[13px]">
            <thead>
              <tr>
                <th scope="col" className="label border-b-2 border-rule py-1.5 text-left">{t("knowledge.main.settings.who")}</th>
                <th scope="col" className="label border-b-2 border-rule py-1.5 text-left">{t("knowledge.main.settings.role")}</th>
                <th scope="col" className="label border-b-2 border-rule py-1.5 text-left">{t("knowledge.main.settings.can")}</th>
                <th scope="col" className="border-b-2 border-rule"><span className="sr-only">{t("knowledge.main.settings.actions")}</span></th>
              </tr>
            </thead>
            <tbody>
              {(access.data?.items ?? []).map((a) => (
                <tr key={a.id}>
                  <td className="border-b border-hair py-1.5">{a.name}{a.group_id && <span className="text-muted">{t("knowledge.main.settings.group")}</span>}</td>
                  <td className="border-b border-hair py-1.5">{a.role_name}</td>
                  <td className="border-b border-hair py-1.5">{a.level === "write" ? t("knowledge.main.settings.readWrite") : a.level === "read" ? t("knowledge.main.settings.read") : "—"}</td>
                  <td className="border-b border-hair py-1.5 text-right">
                    {sp.rights.manage && <button className="text-[12px] text-muted underline hover:text-danger" onClick={() => revoke.mutate(a.id)}>{t("knowledge.main.settings.remove")}</button>}
                  </td>
                </tr>
              ))}
              {access.data && access.data.items.length === 0 && (
                <tr><td colSpan={4} className="py-2 text-muted">{t("knowledge.main.settings.noRoles")}</td></tr>
              )}
            </tbody>
          </table>
          {sp.rights.manage && (
            <form
              className="flex flex-wrap items-end gap-2"
              onSubmit={(e) => {
                e.preventDefault();
                if (who) grant.mutate();
              }}
            >
              <label className="flex min-w-[220px] flex-1 flex-col gap-1">
                <span className="label">{t("knowledge.main.settings.who")}</span>
                <Select className="field" value={who} onChange={(e) => setWho(e.target.value)}>
                  <option value="">{t("knowledge.main.settings.choose")}</option>
                  <optgroup label={t("knowledge.main.settings.groups")}>
                    {(groups.data?.items ?? []).map((g) => <option key={g.id} value={`g:${g.id}`}>{g.name}</option>)}
                  </optgroup>
                  <optgroup label={t("knowledge.main.settings.people")}>
                    {(members.data?.items ?? []).filter((m) => m.member_type !== "requester").map((m) => <option key={m.id} value={`m:${m.id}`}>{m.display_name}</option>)}
                  </optgroup>
                </Select>
              </label>
              <label className="flex flex-col gap-1">
                <span className="label">{t("knowledge.main.settings.role")}</span>
                <Select className="field" value={role} onChange={(e) => setRole(e.target.value)}>
                  <option value="viewer">{t("knowledge.main.settings.viewer")}</option>
                  <option value="editor">{t("knowledge.main.settings.editor")}</option>
                </Select>
              </label>
              <button className="btn btn-primary" disabled={!who || grant.isPending}>{t("knowledge.main.settings.add")}</button>
              <div className="w-full"><ErrorNote error={grant.error} /></div>
            </form>
          )}
        </section>
        <TemplatesSection spaceKey={spaceKey} items={templates.data?.items ?? []} canCreate={sp.rights.manage} error={templates.error} />
      </div>
    </>
  );
}

function TemplatesSection({ spaceKey, items, canCreate, error }: { spaceKey: string; items: import("../knowledgeApi").Template[]; canCreate: boolean; error: unknown }) {
  const t = useT();
  const qc = useQueryClient();
  const [edit, setEdit] = useState<{ id?: string; key: string; name: string; description: string; body: string; controlled: boolean } | null>(null);
  const save = useMutation({
    mutationFn: () =>
      edit!.id
        ? kb.updateTemplate(edit!.id, { name: edit!.name, description: edit!.description, body: edit!.body, controlled: edit!.controlled })
        : kb.createTemplate(spaceKey, { key: edit!.key, name: edit!.name, description: edit!.description, body: edit!.body, controlled: edit!.controlled }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["kb", "templates", spaceKey] });
      setEdit(null);
    },
  });
  return (
    <section className="flex flex-col gap-2">
      <SectionTitle aside={canCreate && <button className="text-[12px] text-accent underline" onClick={() => setEdit({ key: "", name: "", description: "", body: t("knowledge.main.tpl.defaultBody"), controlled: false })}>{t("knowledge.main.tpl.new")}</button>}>
        {t("knowledge.main.tpl.title")}
      </SectionTitle>
      <ErrorNote error={error} />
      <ul className="m-0 flex list-none flex-col p-0">
        {items.map((tp) => (
          <li key={tp.id} className="flex items-start gap-3 border-b border-hair py-2">
            <span className="flex min-w-0 flex-1 flex-col">
              <span className="font-semibold">
                {tp.name} {tp.controlled && <span className="text-[10.5px] text-flag">{t("knowledge.main.controlledBadge")}</span>} {tp.archived && <span className="text-[10.5px] text-muted">{t("knowledge.main.tpl.archived")}</span>}
              </span>
              <span className="text-[12.5px] text-muted">{tp.description}</span>
              <span className="text-[11.5px] text-muted">{tp.space_key ? t("knowledge.main.tpl.onlyIn", { space: tp.space_key }) : t("knowledge.main.tpl.allSpaces")}</span>
            </span>
            {tp.can_edit && (
              <button className="text-[12px] text-muted underline hover:text-fg" onClick={() => setEdit({ id: tp.id, key: tp.key, name: tp.name, description: tp.description, body: tp.body, controlled: tp.controlled })}>
                {t("knowledge.main.tpl.edit")}
              </button>
            )}
          </li>
        ))}
      </ul>
      {edit && (
        <Dialog title={edit.id ? t("knowledge.main.tpl.dialog", { name: edit.name }) : t("knowledge.main.tpl.new")} onClose={() => setEdit(null)} wide>
          <form
            className="flex flex-col gap-3"
            onSubmit={(e) => {
              e.preventDefault();
              save.mutate();
            }}
          >
            {!edit.id && (
              <label className="flex flex-col gap-1">
                <span className="label">{t("knowledge.main.newSpace.key")}</span>
                <input className="field font-mono" required pattern="[a-z][a-z0-9_]*" value={edit.key} onChange={(e) => setEdit({ ...edit, key: e.target.value.toLowerCase() })} />
              </label>
            )}
            <label className="flex flex-col gap-1">
              <span className="label">{t("knowledge.main.newSpace.name")}</span>
              <input className="field" required value={edit.name} onChange={(e) => setEdit({ ...edit, name: e.target.value })} />
            </label>
            <label className="flex flex-col gap-1">
              <span className="label">{t("knowledge.main.newSpace.description")}</span>
              <input className="field" value={edit.description} onChange={(e) => setEdit({ ...edit, description: e.target.value })} />
            </label>
            <label className="flex flex-col gap-1">
              <span className="label">{t("knowledge.main.tpl.text")}</span>
              <textarea className="field min-h-[220px] font-mono text-[12.5px]" value={edit.body} onChange={(e) => setEdit({ ...edit, body: e.target.value })} />
              <span className="text-[12px] text-muted">{t("knowledge.main.tpl.vars", { vars: "{{titolo}}, {{data}}, {{autore}}" })}</span>
            </label>
            <label className="flex items-center gap-2">
              <input type="checkbox" checked={edit.controlled} onChange={(e) => setEdit({ ...edit, controlled: e.target.checked })} />
              {t("knowledge.main.tpl.controlled")}
            </label>
            <ErrorNote error={save.error} />
            <div className="flex justify-end gap-2">
              <button type="button" className="btn" onClick={() => setEdit(null)}>{t("knowledge.main.cancel")}</button>
              <button className="btn btn-primary" disabled={save.isPending}>{t("knowledge.main.save")}</button>
            </div>
          </form>
        </Dialog>
      )}
    </section>
  );
}

// ---------------------------------------------------------------------
// Pagina

export function KnowledgePage({ pageKey, search, onSearch }: { pageKey: string; search: PageSearch; onSearch: (s: PageSearch) => void }) {
  const t = useT();
  const detail = useQuery({ queryKey: ["kb", "page", pageKey], queryFn: () => kb.page(pageKey), refetchInterval: 60_000 });
  const d = detail.data;
  useEffect(() => {
    if (d) document.title = `${d.page.title} · ${d.page.space_name} · Regia`;
    return () => {
      document.title = "Regia";
    };
  }, [d]);
  if (detail.isPending) return <Loading className="px-4 py-6 text-muted md:px-7" />;
  if (!d) return <div className="px-4 py-6 md:px-7"><ErrorNote error={detail.error} /></div>;
  if (search.vista === "modifica" && d.rights.write) {
    return (
      <Suspense fallback={<p className="px-4 py-6 text-muted md:px-7">{t("knowledge.main.page.loadingEditor")}</p>}>
        <PageEditor detail={d} onDone={() => onSearch({})} />
      </Suspense>
    );
  }
  return <PageReader d={d} search={search} onSearch={onSearch} />;
}

function PageReader({ d, search, onSearch }: { d: PageDetail; search: PageSearch; onSearch: (s: PageSearch) => void }) {
  const t = useT();
  const p = d.page;
  // Chi legge soltanto vede la versione approvata in vigore; chi scrive la versione di lavoro.
  // Revisori e approvatori di turno leggono la versione in revisione.
  const decider = d.approvals.some((a) => a.status === "pending" && a.steps.some((s) => s.can_decide));
  const showApproved = !!d.approved && (search.approvata ?? (!d.rights.write && !decider));
  const body = showApproved ? d.approved!.body : p.body;
  const title = showApproved ? d.approved!.title : p.title;
  const history = search.vista === "storia";
  const toc = useMemo(() => headingsOf(body).filter((h) => h.level <= 3), [body]);
  const keys = useMemo(() => citedKeys(body), [body]);
  const refs = useQuery({ queryKey: ["kb", "refs", keys.join(",")], queryFn: () => kb.refs(keys), enabled: keys.length > 0, staleTime: 60_000 });
  const [dialog, setDialog] = useState<"move" | "control" | "delete" | "link" | "export" | "import" | null>(null);
  const [printing, setPrinting] = useState(false);
  const wide = !useNarrow();
  const showingVersion = showApproved ? d.approved!.version : p.version;
  const showVersion = (v: number) => {
    if (v === d.approved?.version) onSearch({ approvata: true });
    else if (v === p.version) onSearch({ approvata: false });
    else onSearch({ vista: "storia", v });
  };
  return (
    <SpaceShell
      spaceKey={p.space_key}
      current={p.key}
      aside={
        !history && toc.length > 1 && wide ? (
          <nav aria-label={t("knowledge.main.page.onThisPage")} className="hidden w-[220px] shrink-0 py-6 pr-4 xl:block">
            <div className="sticky top-6 flex flex-col gap-1">
              <span className="label text-muted">{t("knowledge.main.page.onThisPage")}</span>
              <Toc items={toc} />
            </div>
          </nav>
        ) : undefined
      }
    >
      <article className="flex max-w-[880px] min-w-0 flex-col gap-5 px-4 py-6 md:px-8 xl:max-w-[1240px]" aria-label={title}>
        <header className="flex flex-col gap-2">
          <nav aria-label={t("knowledge.main.page.path")} className="flex flex-wrap items-center gap-x-1 font-mono text-[11.5px] text-muted">
            <Link to="/documenti/spazi/$space" params={{ space: p.space_key }} className="text-muted no-underline hover:text-accent">{p.space_key}</Link>
            {d.path.map((c) => (
              <span key={c.key} className="flex items-center gap-1">
                <span aria-hidden="true">/</span>
                <Link to="/documenti/$key" params={{ key: c.key }} className="max-w-[220px] truncate text-muted no-underline hover:text-accent">{c.title}</Link>
              </span>
            ))}
            <span aria-hidden="true">/</span>
            <span>{p.key}</span>
          </nav>
          <div className="flex flex-col gap-3">
            {/* Il titolo ha tutta la riga (a capo solo tra le parole, righe bilanciate); i pulsanti stanno sotto. */}
            <h1 className="m-0 w-full min-w-0 text-[25px] md:text-[30px] leading-[1.12] font-bold tracking-[-0.025em] [text-wrap:balance] [overflow-wrap:anywhere] [hyphens:manual]">{title}</h1>
            <PageActions d={d} history={history} onSearch={onSearch} onDialog={setDialog} onPrint={() => setPrinting(true)} />
          </div>
          <p className="m-0 text-[12.5px] text-muted">
            {showApproved ? (
              <>{t("knowledge.main.page.approvedLine", { version: d.approved!.version, date: d.approved!.approved_at ? date(d.approved!.approved_at) : "", by: d.approved!.approved_by })}</>
            ) : (
              <>
                {t("knowledge.main.page.updated", { version: p.version, when: ago(p.updated_at) })}
                {p.updated_by ? t("knowledge.main.page.updatedBy", { name: p.updated_by }) : ""}{t("knowledge.main.page.createdBy", { name: p.created_by || "—" })}
              </>
            )}
          </p>
        </header>
        {d.lock && !d.lock.mine && (
          <p role="status" className="m-0 border-l-2 border-flag pl-3 text-[13px]">
            <Trans
              k="knowledge.main.page.lock"
              params={{ from: fmtTime(d.lock.acquired_at), to: fmtTime(d.lock.expires_at) }}
              slots={{ editing: <span className="font-semibold">{t("knowledge.main.page.lockEditing", { name: d.lock.name })}</span> }}
            />
          </p>
        )}
        {p.controlled && <ControlledPanel d={d} showApproved={showApproved} onToggle={() => onSearch({ ...search, approvata: !showApproved })} />}
        {p.controlled && !history && <ReadingsNotice pageKey={p.key} />}
        {history ? (
          <History d={d} search={search} onSearch={onSearch} />
        ) : (
          <>
            {toc.length > 1 && (
              <details className="border-y border-hair py-2 xl:hidden">
                <summary className="label cursor-pointer text-muted">{t("knowledge.main.page.onThisPageCount", { count: toc.length })}</summary>
                <div className="pt-2"><Toc items={toc} /></div>
              </details>
            )}
            {body.trim() ? (
              <div>
                <InlineLayer pageKey={p.key} body={body}>
                  <PageMarkdown text={body} mentions={p.mentions} refs={refs.data?.items} />
                </InlineLayer>
              </div>
            ) : (
              <p className="m-0 text-muted">{t("knowledge.main.page.empty")}{d.rights.write ? t("knowledge.main.page.emptyHint") : ""}</p>
            )}
            {p.controlled && <ReadingsSection d={d} showingVersion={showingVersion} onShowVersion={showVersion} />}
            {d.children.length > 0 && (
              <section className="flex flex-col gap-1" aria-label={t("knowledge.main.page.children")}>
                <SectionTitle aside={<span className="font-mono text-[11.5px] text-muted tabular">{d.children.length}</span>}>{t("knowledge.main.page.children")}</SectionTitle>
                <ul className="m-0 flex list-none flex-col p-0">
                  {d.children.map((c) => (
                    <li key={c.key}>
                      <Link to="/documenti/$key" params={{ key: c.key }} className="block py-1 text-fg no-underline hover:text-accent">{c.title}</Link>
                    </li>
                  ))}
                </ul>
              </section>
            )}
            <EvidencePanel d={d} />
            <Attachments refKey={p.key} />
            <Comments refKey={p.key} />
          </>
        )}
      </article>
      {dialog === "move" && <MoveDialog d={d} onClose={() => setDialog(null)} />}
      {dialog === "control" && <ControlDialog d={d} onClose={() => setDialog(null)} />}
      {dialog === "delete" && <DeleteDialog d={d} onClose={() => setDialog(null)} />}
      {dialog === "link" && <LinkDialog d={d} onClose={() => setDialog(null)} />}
      {dialog === "export" && <ExportDialog space={p.space_key} page={{ key: p.key, title: p.title }} onClose={() => setDialog(null)} />}
      {dialog === "import" && <ImportDialog space={p.space_key} parent={{ key: p.key, title: p.title }} onClose={() => setDialog(null)} />}
      {printing && <PagePrint d={d} title={title} body={body} showApproved={showApproved} refs={refs.data?.items} onDone={() => setPrinting(false)} />}
    </SpaceShell>
  );
}

function Toc({ items }: { items: { level: number; text: string; id: string }[] }) {
  const min = Math.min(...items.map((h) => h.level));
  return (
    <ol className="m-0 flex list-none flex-col gap-0.5 p-0 text-[12.5px]">
      {items.map((h) => (
        <li key={h.id} style={{ paddingLeft: (h.level - min) * 12 }}>
          <a href={`#${h.id}`} className="block truncate py-0.5 text-muted no-underline hover:text-accent">{h.text}</a>
        </li>
      ))}
    </ol>
  );
}

function PageActions({
  d,
  history,
  onSearch,
  onDialog,
  onPrint,
}: {
  d: PageDetail;
  history: boolean;
  onSearch: (s: PageSearch) => void;
  onDialog: (x: "move" | "control" | "delete" | "link" | "export" | "import") => void;
  onPrint: () => void;
}) {
  const t = useT();
  const [menu, setMenu] = useState<"altro" | "esporta" | null>(null);
  const editable = d.rights.write && !(d.page.controlled && (d.page.state_category === "in_progress" || d.page.state_category === "cancelled"));
  const items = (list: ReadonlyArray<readonly [string, string]>, run: (k: string) => void) => (
    <div role="menu" className="absolute top-full right-0 z-30 mt-1 flex w-60 flex-col border border-rule bg-bg py-1 text-[13px]" onMouseLeave={() => setMenu(null)}>
      {list.map(([k, label]) => (
        <button
          key={k}
          role="menuitem"
          className={`px-3 py-1.5 text-left hover:bg-sel ${k === "delete" ? "text-danger" : ""}`}
          onClick={() => {
            setMenu(null);
            run(k);
          }}
        >
          {label}
        </button>
      ))}
    </div>
  );
  return (
    <div className="relative flex shrink-0 flex-wrap gap-2">
      {editable && !history && (
        <button className="btn btn-primary" onClick={() => onSearch({ vista: "modifica" })}>
          {t("knowledge.main.actions.edit")}
        </button>
      )}
      <button className="btn" onClick={() => onSearch(history ? {} : { vista: "storia" })} aria-pressed={history}>
        {history ? t("knowledge.main.actions.backToPage") : t("knowledge.main.actions.history")}
      </button>
      <button className="btn" aria-haspopup="menu" aria-expanded={menu === "esporta"} onClick={() => setMenu((m) => (m === "esporta" ? null : "esporta"))}>
        {t("knowledge.main.actions.export")}
      </button>
      {d.rights.write && (
        <button className="btn" aria-haspopup="menu" aria-expanded={menu === "altro"} onClick={() => setMenu((m) => (m === "altro" ? null : "altro"))}>
          {t("knowledge.main.actions.more")}
        </button>
      )}
      {menu === "esporta" &&
        items(
          [
            ["print", t("knowledge.main.actions.print")],
            ["export", t("knowledge.main.actions.markdownZip")],
          ],
          (k) => (k === "print" ? onPrint() : onDialog("export")),
        )}
      {menu === "altro" &&
        items(
          [
            ["move", t("knowledge.main.actions.move")],
            ["link", t("knowledge.main.actions.link")],
            ["import", t("knowledge.main.actions.importUnder")],
            ["control", d.page.controlled ? t("knowledge.main.actions.controlled") : t("knowledge.main.actions.makeControlled")],
            ["delete", t("knowledge.main.actions.delete")],
          ],
          (k) => onDialog(k as "move" | "link" | "import" | "control" | "delete"),
        )}
    </div>
  );
}

function ControlledPanel({ d, showApproved, onToggle }: { d: PageDetail; showApproved: boolean; onToggle: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const p = d.page;
  const [pending, setPending] = useState<{ to: string; label: string } | null>(null);
  const [comment, setComment] = useState("");
  const refresh = () => qc.invalidateQueries({ queryKey: ["kb"] });
  const move = useMutation({
    mutationFn: (x: { to: string; comment?: string }) => api.transition(p.id, x.to, x.comment),
    onSuccess: () => {
      setPending(null);
      setComment("");
      refresh();
      qc.invalidateQueries({ queryKey: ["inbox"] });
    },
  });
  const reviewed = useMutation({ mutationFn: () => kb.reviewed(p.key), onSuccess: refresh });
  const due = p.next_review ? new Date(p.next_review + "T00:00:00") : null;
  const late = due && due.getTime() < Date.now();
  const soon = due && !late && due.getTime() - Date.now() < 30 * 86_400_000;
  const active = d.approvals.find((a) => a.status === "pending");
  return (
    <section aria-label={t("knowledge.main.panel.label")} className="flex flex-col gap-2 border-y border-hair py-2.5">
      {/* Striscia compatta: stato, versione in vigore, revisione e quale versione si sta leggendo. */}
      <div className="flex flex-wrap items-center gap-x-5 gap-y-1 text-[12.5px]">
        <span className="label text-flag">{t("knowledge.main.panel.controlled")}</span>
        {(d.phases ?? []).length > 0 && <StageGate states={d.phases} current={p.state} />}
        <span>
          <span className="text-muted">{t("knowledge.main.panel.inForce")}</span>
          {p.approved_version ? <span className="font-semibold">v{p.approved_version}</span> : <span className="text-muted">{t("knowledge.main.panel.noApproved")}</span>}
        </span>
        <span>
          <span className="text-muted">{t("knowledge.main.panel.nextReview")}</span>
          {due ? <span className={late ? "font-semibold text-danger" : soon ? "font-semibold text-flag" : ""}>{fmtDate(due)}</span> : <span className="text-muted">—</span>}
          {p.review_months && <span className="text-muted">{t("knowledge.main.panel.every", { count: p.review_months })}</span>}
        </span>
        {d.approved && (
          <span>
            <span className="text-muted">{showApproved ? t("knowledge.main.panel.readApproved", { version: d.approved.version }) : t("knowledge.main.panel.readWorking", { version: p.version })} · </span>
            <button className="touch-44 text-accent underline" onClick={onToggle}>
              {showApproved ? t("knowledge.main.panel.toWorking", { version: p.version }) : t("knowledge.main.panel.toApproved", { version: d.approved.version })}
            </button>
          </span>
        )}
      </div>
      <details className="text-[13px]">
        <summary className="cursor-pointer py-1 text-[12.5px] text-muted">{t("knowledge.main.panel.people")}</summary>
        <dl className="m-0 grid grid-cols-[110px_minmax(0,1fr)] gap-y-1 pt-1">
          <dt className="text-muted">{t("knowledge.main.panel.reviewers")}</dt>
          <dd className="m-0">{d.reviewers.map((r) => r.name).join(", ") || <span className="text-muted">{t("knowledge.main.panel.tbd")}</span>}</dd>
          <dt className="text-muted">{t("knowledge.main.panel.approvers")}</dt>
          <dd className="m-0">{d.approvers.map((r) => r.name).join(", ") || <span className="text-muted">{t("knowledge.main.panel.tbd")}</span>}</dd>
        </dl>
      </details>
      {active && <ApprovalSteps steps={active.steps} onDone={refresh} />}
      {!active && p.state_category === "todo" && d.approvals[0] && (d.approvals[0].status === "rejected" || d.approvals[0].status === "deferred") && (
        <p className="m-0 border-l-2 border-flag pl-3 text-[13px]">
          <span className="font-semibold">{t("knowledge.main.panel.sentBack")}</span>
          {d.approvals[0].steps
            .filter((s) => s.decision === "rejected" || s.decision === "deferred")
            .map((s) => (
              <span key={s.id}>
                {t("knowledge.main.panel.sentBackBy", { name: s.decided_by ?? s.approver })}{s.comment ? t("knowledge.main.panel.quote", { comment: s.comment }) : ""}
              </span>
            ))}
        </p>
      )}
      <div className="flex flex-wrap gap-2">
        {d.transitions.map((tx) => (
          <button
            key={tx.to}
            className={`btn ${tx.to === "In revisione" ? "btn-primary" : ""}`}
            disabled={tx.blocked.length > 0 || move.isPending}
            title={tx.blocked.join("; ") || undefined}
            onClick={() => (tx.comment_required ? setPending({ to: tx.to, label: tx.label }) : move.mutate({ to: tx.to }))}
          >
            {tx.label}
          </button>
        ))}
        {p.state_category === "done" && p.review_months && (
          <button className="btn" onClick={() => reviewed.mutate()} disabled={reviewed.isPending} title={t("knowledge.main.panel.reviewedTitle")}>
            {t("knowledge.main.panel.reviewed")}
          </button>
        )}
      </div>
      {d.transitions.some((tx) => tx.blocked.length) && (
        <ul className="m-0 pl-5 text-[12.5px] text-muted">
          {d.transitions.flatMap((tx) => tx.blocked.map((b) => <li key={tx.to + b}>{tx.label}: {b}</li>))}
        </ul>
      )}
      {pending && (
        <form
          className="flex flex-col gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            move.mutate({ to: pending.to, comment });
          }}
        >
          <label className="flex flex-col gap-1">
            <span className="label">{t("knowledge.main.panel.reason", { label: pending.label })}</span>
            <textarea className="field min-h-[64px]" required value={comment} onChange={(e) => setComment(e.target.value)} />
          </label>
          <div className="flex gap-2">
            <button className="btn btn-primary" disabled={move.isPending}>{t("knowledge.main.panel.confirm")}</button>
            <button type="button" className="btn" onClick={() => setPending(null)}>{t("knowledge.main.cancel")}</button>
          </div>
        </form>
      )}
      <ErrorNote error={move.error ?? reviewed.error} />
    </section>
  );
}

function ApprovalSteps({ steps, onDone }: { steps: Step[]; onDone: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const [reject, setReject] = useState<string | null>(null);
  const [comment, setComment] = useState("");
  const decide = useMutation({
    mutationFn: (x: { id: string; decision: string; comment?: string }) => api.decide(x.id, x.decision, x.comment),
    onSuccess: () => {
      setReject(null);
      setComment("");
      qc.invalidateQueries({ queryKey: ["inbox"] });
      onDone();
    },
  });
  return (
    <div className="flex flex-col gap-1.5">
      <span className="label text-muted">{t("knowledge.main.steps.title")}</span>
      <ol className="m-0 flex list-none flex-col p-0 text-[13px]">
        {steps.map((s, i) => (
          <li key={s.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-hair py-1.5">
            <span className="font-mono text-[11.5px] text-muted tabular">{String(i + 1).padStart(2, "0")}</span>
            <span className="min-w-0 flex-1">
              {s.ordinal === 1 ? t("knowledge.main.steps.review") : t("knowledge.main.steps.approval")}: {s.approver}
            </span>
            <span className={s.decision === "pending" ? "text-muted" : s.decision === "rejected" ? "text-danger" : "text-ok"}>
              {DECISIONS[s.decision] ?? s.decision}
              {s.decided_by ? ` · ${s.decided_by}` : ""}
            </span>
            {s.can_decide && s.decision === "pending" && (
              <span className="flex gap-2">
                <button className="btn btn-primary py-1" disabled={decide.isPending} onClick={() => decide.mutate({ id: s.id, decision: "approved" })}>
                  {t("knowledge.main.steps.approve")}
                </button>
                <button className="btn py-1" onClick={() => setReject(s.id)}>
                  {t("knowledge.main.steps.sendBack")}
                </button>
              </span>
            )}
          </li>
        ))}
      </ol>
      {reject && (
        <form
          className="flex flex-col gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            decide.mutate({ id: reject, decision: "rejected", comment });
          }}
        >
          <label className="flex flex-col gap-1">
            <span className="label">{t("knowledge.main.steps.whatToChange")}</span>
            <textarea className="field min-h-[64px]" required value={comment} onChange={(e) => setComment(e.target.value)} />
          </label>
          <div className="flex gap-2">
            <button className="btn btn-danger" disabled={decide.isPending}>{t("knowledge.main.steps.sendBack")}</button>
            <button type="button" className="btn" onClick={() => setReject(null)}>{t("knowledge.main.cancel")}</button>
          </div>
        </form>
      )}
      <ErrorNote error={decide.error} />
    </div>
  );
}

function LinkDialog({ d, onClose }: { d: PageDetail; onClose: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const [text, setText] = useState("");
  const debounced = useDebounced(text.trim(), 250);
  const found = useQuery({ queryKey: ["kb", "link-search", debounced], queryFn: () => api.search(debounced, undefined, 10), enabled: debounced.length >= 2 });
  const link = useMutation({
    mutationFn: (key: string) => kb.link(d.page.key, key),
    onSuccess: (r) => {
      qc.setQueryData(["kb", "page", d.page.key], r);
      onClose();
    },
  });
  return (
    <Dialog title={t("knowledge.main.link.title")} onClose={onClose}>
      <label className="flex flex-col gap-1">
        <span className="label">{t("knowledge.main.link.field")}</span>
        <input className="field" autoFocus value={text} onChange={(e) => setText(e.target.value)} placeholder={t("knowledge.main.link.placeholder")} />
      </label>
      <ErrorNote error={found.error ?? link.error} />
      <ul className="m-0 flex list-none flex-col p-0">
        {(found.data?.items ?? []).filter((e) => e.id !== d.page.id).map((e) => (
          <li key={e.id}>
            <button className="flex w-full min-w-0 items-baseline gap-2 py-1.5 text-left hover:text-accent" onClick={() => link.mutate(e.key)}>
              <span className="shrink-0 font-mono text-[12px] text-accent">{e.key}</span>
              <span className="min-w-0 flex-1 truncate">{e.title}</span>
              <span className="shrink-0 text-[11.5px] text-muted">{e.type_name}</span>
            </button>
          </li>
        ))}
      </ul>
    </Dialog>
  );
}

function MoveDialog({ d, onClose }: { d: PageDetail; onClose: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const view = useQuery({ queryKey: ["kb", "tree", d.page.space_key], queryFn: () => kb.tree(d.page.space_key) });
  const idx = useMemo(() => indexTree(view.data?.pages ?? []), [view.data]);
  const self = idx.byKey.get(d.page.key);
  const blocked = self ? descendants(idx, self.id) : new Set<string>();
  const [parent, setParent] = useState(() => (d.page.parent_id ? idx.byId.get(d.page.parent_id)?.key ?? "" : ""));
  const [position, setPosition] = useState("");
  useEffect(() => {
    if (d.page.parent_id) setParent(idx.byId.get(d.page.parent_id)?.key ?? "");
  }, [idx, d.page.parent_id]);
  const options: { key: string; title: string; depth: number }[] = [];
  const walk = (pid: string, depth: number) => {
    for (const p of idx.byParent.get(pid) ?? []) {
      if (blocked.has(p.id)) continue;
      options.push({ key: p.key, title: p.title, depth });
      walk(p.id, depth + 1);
    }
  };
  walk("", 0);
  const parentNode = parent ? idx.byKey.get(parent) : undefined;
  const siblings = (idx.byParent.get(parentNode?.id ?? "") ?? []).filter((p) => p.key !== d.page.key);
  const move = useMutation({
    mutationFn: () => kb.move(d.page.key, { parent, before: position || undefined }),
    onSuccess: (r) => {
      qc.setQueryData(["kb", "page", d.page.key], r);
      qc.invalidateQueries({ queryKey: ["kb", "tree"] });
      onClose();
    },
  });
  return (
    <Dialog title={t("knowledge.main.move.title", { title: d.page.title })} onClose={onClose}>
      <form
        className="flex flex-col gap-3"
        onSubmit={(e) => {
          e.preventDefault();
          move.mutate();
        }}
      >
        <label className="flex flex-col gap-1">
          <span className="label">{t("knowledge.main.move.inside")}</span>
          <Select className="field" value={parent} onChange={(e) => {
            setParent(e.target.value);
            setPosition("");
          }}>
            <option value="">{t("knowledge.main.move.root")}</option>
            {options.map((o) => (
              <option key={o.key} value={o.key}>{" ".repeat(o.depth)}{o.title}</option>
            ))}
          </Select>
        </label>
        <label className="flex flex-col gap-1">
          <span className="label">{t("knowledge.main.move.position")}</span>
          <Select className="field" value={position} onChange={(e) => setPosition(e.target.value)}>
            <option value="">{t("knowledge.main.move.end")}</option>
            {siblings.map((s) => <option key={s.key} value={s.key}>{t("knowledge.main.move.before", { title: s.title })}</option>)}
          </Select>
        </label>
        <ErrorNote error={move.error} />
        <div className="flex justify-end gap-2">
          <button type="button" className="btn" onClick={onClose}>{t("knowledge.main.cancel")}</button>
          <button className="btn btn-primary" disabled={move.isPending}>{t("knowledge.main.move.submit")}</button>
        </div>
      </form>
    </Dialog>
  );
}

function ControlDialog({ d, onClose }: { d: PageDetail; onClose: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const members = useQuery({ queryKey: ["members"], queryFn: api.members, staleTime: 300_000 });
  const [controlled, setControlled] = useState(true);
  const [months, setMonths] = useState<string>(d.page.review_months ? String(d.page.review_months) : "12");
  const [reviewers, setReviewers] = useState<string[]>(d.reviewers.map((r) => r.id));
  const [approvers, setApprovers] = useState<string[]>(d.approvers.map((r) => r.id));
  const save = useMutation({
    mutationFn: () => kb.control(d.page.key, { controlled, review_months: controlled && months ? Number(months) : null, reviewers, approvers }),
    onSuccess: (r) => {
      qc.setQueryData(["kb", "page", d.page.key], r);
      qc.invalidateQueries({ queryKey: ["kb", "tree"] });
      onClose();
    },
  });
  const people = (members.data?.items ?? []).filter((m) => m.member_type !== "requester");
  const picker = (label: string, value: string[], set: (v: string[]) => void) => (
    <fieldset className="m-0 flex flex-col gap-1 border-0 p-0">
      <legend className="label mb-1">{label}</legend>
      <div className="flex max-h-[150px] flex-col overflow-y-auto border border-hair px-2 py-1">
        {people.map((m) => (
          <label key={m.id} className="flex items-center gap-2 py-0.5">
            <input type="checkbox" checked={value.includes(m.id)} onChange={(e) => set(e.target.checked ? [...value, m.id] : value.filter((x) => x !== m.id))} />
            {m.display_name}
          </label>
        ))}
      </div>
    </fieldset>
  );
  const locked = d.page.controlled && (d.page.approved_version != null || (d.page.state_category !== "" && d.page.state_category !== "todo"));
  return (
    <Dialog title={t("knowledge.main.panel.label")} onClose={onClose} wide>
      <form
        className="flex flex-col gap-3"
        onSubmit={(e) => {
          e.preventDefault();
          save.mutate();
        }}
      >
        <p className="m-0 text-[12.5px] text-muted">
          {t("knowledge.main.control.help")}
        </p>
        <label className="flex items-center gap-2">
          <input type="checkbox" checked={controlled} disabled={locked} onChange={(e) => setControlled(e.target.checked)} />
          {t("knowledge.main.control.page")}
        </label>
        {controlled && (
          <>
            <label className="flex flex-col gap-1">
              <span className="label">{t("knowledge.main.control.periodic")}</span>
              <Select className="field" value={months} onChange={(e) => setMonths(e.target.value)}>
                <option value="">{t("knowledge.main.control.none")}</option>
                {[3, 6, 12, 24, 36].map((n) => <option key={n} value={n}>{t("knowledge.main.control.everyMonths", { count: n })}</option>)}
              </Select>
            </label>
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
              {picker(t("knowledge.main.panel.reviewers"), reviewers, setReviewers)}
              {picker(t("knowledge.main.panel.approvers"), approvers, setApprovers)}
            </div>
          </>
        )}
        <ErrorNote error={save.error} />
        <div className="flex justify-end gap-2">
          <button type="button" className="btn" onClick={onClose}>{t("knowledge.main.cancel")}</button>
          <button className="btn btn-primary" disabled={save.isPending}>{t("knowledge.main.save")}</button>
        </div>
      </form>
    </Dialog>
  );
}

function DeleteDialog({ d, onClose }: { d: PageDetail; onClose: () => void }) {
  const t = useT();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const remove = useMutation({
    mutationFn: () => kb.remove(d.page.key),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["kb"] });
      navigate({ to: "/documenti/spazi/$space", params: { space: d.page.space_key } });
    },
  });
  return (
    <Dialog title={t("knowledge.main.delete.title")} onClose={onClose}>
      <p className="m-0">
        {t("knowledge.main.delete.body", { title: d.page.title, children: d.children.length > 0 ? t("knowledge.main.delete.children", { count: d.children.length }) : "" })}
      </p>
      <ErrorNote error={remove.error} />
      <div className="flex justify-end gap-2">
        <button className="btn" onClick={onClose}>{t("knowledge.main.cancel")}</button>
        <button className="btn btn-danger" disabled={remove.isPending} onClick={() => remove.mutate()}>{t("knowledge.main.delete.submit")}</button>
      </div>
    </Dialog>
  );
}

// ---------------------------------------------------------------------
// Storia: versioni, confronto, ripristino

function History({ d, search, onSearch }: { d: PageDetail; search: PageSearch; onSearch: (s: PageSearch) => void }) {
  const t = useT();
  const qc = useQueryClient();
  const key = d.page.key;
  const list = useQuery({ queryKey: ["kb", "versions", key, d.page.version], queryFn: () => kb.versions(key) });
  const items = list.data?.items ?? [];
  const to = search.a ?? d.page.version;
  const from = search.da ?? Math.max(1, to - 1);
  const comparing = search.da !== undefined || search.a !== undefined;
  const view = search.v;
  const diff = useQuery({ queryKey: ["kb", "diff", key, from, to], queryFn: () => kb.diff(key, from, to), enabled: comparing && from !== to });
  const version = useQuery({ queryKey: ["kb", "version", key, view], queryFn: () => kb.version(key, view!), enabled: !!view });
  const restore = useMutation({
    mutationFn: (n: number) => kb.restore(key, n),
    onSuccess: (r) => {
      qc.setQueryData(["kb", "page", key], r);
      qc.invalidateQueries({ queryKey: ["kb", "versions", key] });
      onSearch({ vista: "storia" });
    },
  });
  return (
    <div className="flex flex-col gap-5">
      <section className="flex flex-col gap-2">
        <SectionTitle aside={<span className="font-mono text-[11.5px] text-muted tabular">{items.length || ""}</span>}>{t("knowledge.main.history.versions")}</SectionTitle>
        <ErrorNote error={list.error ?? restore.error} />
        <div className="max-w-full overflow-x-auto">
          <table className="w-full border-collapse text-[13px]">
            <thead>
              <tr>
                {[t("knowledge.main.history.colVersion"), t("knowledge.main.history.colWhen"), t("knowledge.main.history.colWho"), t("knowledge.main.history.colNote"), ""].map((h, i) => (
                  <th key={i} scope="col" className={`label border-b-2 border-rule py-1.5 pr-3 text-left ${i === 1 ? "hidden sm:table-cell" : ""}`}>
                    {h || <span className="sr-only">{t("knowledge.main.settings.actions")}</span>}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {items.map((v) => (
                <VersionRow key={v.version} v={v} canRestore={d.rights.write && !v.current} active={view === v.version}
                  onView={() => onSearch({ vista: "storia", v: v.version })}
                  onCompare={() => onSearch({ vista: "storia", da: Math.max(1, v.version - 1), a: v.version })}
                  onRestore={() => restore.mutate(v.version)} />
              ))}
            </tbody>
          </table>
        </div>
      </section>
      {items.length > 1 && (
        <section className="flex flex-col gap-2" aria-label={t("knowledge.main.history.compareLabel")}>
          <SectionTitle>{t("knowledge.main.history.compare")}</SectionTitle>
          <div className="flex flex-wrap items-end gap-2">
            <label className="flex flex-col gap-1">
              <span className="label">{t("knowledge.main.history.from")}</span>
              <Select className="field py-1" value={from} onChange={(e) => onSearch({ vista: "storia", da: Number(e.target.value), a: to })}>
                {items.map((v) => <option key={v.version} value={v.version}>v{v.version}</option>)}
              </Select>
            </label>
            <label className="flex flex-col gap-1">
              <span className="label">{t("knowledge.main.history.to")}</span>
              <Select className="field py-1" value={to} onChange={(e) => onSearch({ vista: "storia", da: from, a: Number(e.target.value) })}>
                {items.map((v) => <option key={v.version} value={v.version}>v{v.version}</option>)}
              </Select>
            </label>
            {!comparing && <button className="btn" onClick={() => onSearch({ vista: "storia", da: from, a: to })}>{t("knowledge.main.history.compare")}</button>}
          </div>
          <ErrorNote error={diff.error} />
          {comparing && from === to && <p className="m-0 text-muted">{t("knowledge.main.history.pickTwo")}</p>}
          {comparing && diff.data && <DiffView c={diff.data} />}
        </section>
      )}
      {view && (
        <section className="flex flex-col gap-2" aria-label={t("knowledge.main.history.version", { version: view })}>
          <SectionTitle aside={<button className="text-[12px] text-muted underline" onClick={() => onSearch({ vista: "storia" })}>{t("knowledge.main.close")}</button>}>
            {t("knowledge.main.history.version", { version: view })}
          </SectionTitle>
          <ErrorNote error={version.error} />
          {version.data && (
            <>
              <h2 className="m-0 text-[22px] font-bold">{version.data.title}</h2>
              <PageMarkdown text={version.data.body} />
            </>
          )}
        </section>
      )}
    </div>
  );
}

function VersionRow({ v, canRestore, active, onView, onCompare, onRestore }: { v: VersionInfo; canRestore: boolean; active: boolean; onView: () => void; onCompare: () => void; onRestore: () => void }) {
  const t = useT();
  const [confirm, setConfirm] = useState(false);
  return (
    <tr className={active ? "bg-sel" : undefined}>
      <td className="border-b border-hair py-1.5 pr-3 align-top whitespace-nowrap">
        <span className="font-mono tabular">v{v.version}</span>
        {v.current && <span className="ml-1.5 text-[10.5px] font-semibold text-accent">{t("knowledge.main.row.current")}</span>}
        {v.in_force && <span className="ml-1.5 text-[10.5px] font-semibold text-ok">{t("knowledge.main.row.inForce")}</span>}
        {v.approved_at && !v.in_force && <span className="ml-1.5 text-[10.5px] text-muted">{t("knowledge.main.row.approved")}</span>}
      </td>
      <td className="hidden border-b border-hair py-1.5 pr-3 align-top whitespace-nowrap sm:table-cell" title={fmtDateTime(v.created_at, {})}>
        {fmtDateTime(v.created_at)}
      </td>
      <td className="border-b border-hair py-1.5 pr-3 align-top">{v.author || "—"}</td>
      <td className="border-b border-hair py-1.5 pr-3 align-top">
        {v.note || <span className="text-muted">—</span>}
        {v.approved_by && <span className="block text-[11.5px] text-muted">{t("knowledge.main.row.approvedBy", { name: v.approved_by })}</span>}
      </td>
      <td className="border-b border-hair py-1.5 text-right align-top whitespace-nowrap">
        <span className="flex flex-wrap justify-end gap-x-2 text-[12px]">
          <button className="text-accent underline" onClick={onView}>{t("knowledge.main.row.view")}</button>
          {v.version > 1 && <button className="text-accent underline" onClick={onCompare}>{t("knowledge.main.row.diff")}</button>}
          {canRestore && !confirm && <button className="text-muted underline hover:text-fg" onClick={() => setConfirm(true)}>{t("knowledge.main.row.restore")}</button>}
          {confirm && (
            <>
              <button className="font-semibold text-danger underline" onClick={onRestore}>{t("knowledge.main.row.confirmRestore")}</button>
              <button className="text-muted underline" onClick={() => setConfirm(false)}>{t("knowledge.main.row.no")}</button>
            </>
          )}
        </span>
      </td>
    </tr>
  );
}

function DiffView({ c }: { c: Comparison }) {
  const t = useT();
  const [onlyChanges, setOnlyChanges] = useState(true);
  // Con «solo le modifiche» restano 2 righe di contesto attorno a ogni cambiamento.
  const keep = useMemo(() => {
    const k = new Set<number>();
    c.lines.forEach((l, i) => {
      if (l.op !== "eq") for (let j = i - 2; j <= i + 2; j++) k.add(j);
    });
    return k;
  }, [c]);
  const st = c.stats;
  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-[12.5px]">
        <span>
          v{c.from.version} ({c.from.author}) → v{c.to.version} ({c.to.author})
        </span>
        <span className="text-ok">{t("knowledge.main.diff.added", { count: st.added })}</span>
        <span className="text-danger">{t("knowledge.main.diff.removed", { count: st.removed })}</span>
        <span className="text-accent">{t("knowledge.main.diff.changed", { count: st.changed })}</span>
        {c.title_changed && <span>{t("knowledge.main.diff.title", { from: c.from.title, to: c.to.title })}</span>}
        <label className="ml-auto flex items-center gap-1.5">
          <input type="checkbox" checked={onlyChanges} onChange={(e) => setOnlyChanges(e.target.checked)} />
          {t("knowledge.main.diff.onlyChanges")}
        </label>
      </div>
      {st.added + st.removed + st.changed === 0 ? (
        <p className="m-0 text-muted">{t("knowledge.main.diff.same")}</p>
      ) : (
        <div className="max-w-full overflow-x-auto border-y border-hair font-mono text-[12.5px] leading-relaxed">
          {c.lines.map((l, i) => {
            if (onlyChanges && !keep.has(i)) {
              return keep.has(i - 1) ? <div key={i} className="px-2 text-muted">⋯</div> : null;
            }
            const mark = l.op === "add" ? "+" : l.op === "del" ? "−" : l.op === "mod" ? "~" : " ";
            // i18n-ok: classi CSS
            const cls = l.op === "add" ? "bg-ok/10 border-ok" : l.op === "del" ? "bg-danger/10 border-danger" : l.op === "mod" ? "bg-sel border-accent" : "border-transparent";
            return (
              <div key={i} className={`flex min-w-0 gap-2 border-l-2 px-2 ${cls}`}>
                <span className="w-16 shrink-0 text-right text-[11px] text-muted tabular select-none" aria-hidden="true">
                  {l.a ?? ""}
                  {l.a && l.b ? ":" : ""}
                  {l.b ?? ""}
                </span>
                <span className="w-3 shrink-0 select-none" aria-hidden="true">{mark}</span>
                <span className="sr-only">{l.op === "add" ? t("knowledge.main.diff.opAdd") : l.op === "del" ? t("knowledge.main.diff.opDel") : l.op === "mod" ? t("knowledge.main.diff.opMod") : ""}</span>
                <span className="min-w-0 flex-1 break-words whitespace-pre-wrap">
                  {l.op === "mod" && l.parts
                    ? l.parts.map((p, j) =>
                        p.op === "eq" ? (
                          <span key={j}>{p.text}</span>
                        ) : p.op === "add" ? (
                          <ins key={j} className="bg-ok/20 text-fg no-underline">{p.text}</ins>
                        ) : (
                          <del key={j} className="bg-danger/15 text-muted">{p.text}</del>
                        ),
                      )
                    : l.text || " "}
                </span>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

export function isApiError(e: unknown, code: string): boolean {
  return e instanceof ApiError && e.code === code;
}
