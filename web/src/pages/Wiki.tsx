// Wiki personale (replica/ai.md §2 punto 1, ondata 4): pagine private della persona in Markdown,
// con i link agli elementi, la storia di ogni modifica (autore, differenze, annullamento) e il
// registro. Con l'AI accesa l'assistente le tiene aggiornate; senza, si scrivono a mano.
import { useMemo, useState, type FormEvent } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { wikiApi, linkCodes, type WikiChange, type WikiHome as Home, type WikiRevision, type WikiState, type WikiSummary } from "../wikiApi";
import { kb } from "../knowledgeApi";
import { PageMarkdown } from "../kbMarkdown";
import { Select } from "../select";
import { PageHeader } from "../shell";
import { ErrorNote, Loading, SectionTitle, ago } from "../ui";
import { useT } from "../i18n/react";
import { lookup } from "../i18n/core";

const homeKey = ["wiki"];
const logKey = ["wiki", "log"];

function useInvalidate() {
  const qc = useQueryClient();
  return () => qc.invalidateQueries({ queryKey: homeKey });
}

function who(by: string) {
  return lookup(`ai.wiki.by.${by}`) ?? by;
}

// ---------------------------------------------------------------------
// Indice: pagine, aggiornamento con l'assistente, registro
// ---------------------------------------------------------------------

export function WikiHome() {
  const t = useT();
  const home = useQuery({
    queryKey: homeKey,
    queryFn: wikiApi.home,
    // Mentre l'assistente aggiorna, lo stato e le pagine si rileggono.
    refetchInterval: (q) => (q.state.data?.state.running || q.state.data?.state.requested_at ? 1500 : false),
  });
  const h = home.data;
  const [creating, setCreating] = useState(false);
  return (
    <>
      <PageHeader
        path={t("ai.wiki.path")}
        title={t("ai.wiki.title")}
        actions={
          <button type="button" className="btn btn-primary touch-44" aria-expanded={creating} onClick={() => setCreating((c) => !c)}>
            {t("ai.wiki.new")}
          </button>
        }
      />
      <div className="flex max-w-[880px] min-w-0 flex-col gap-8 px-4 pb-10 md:px-7" data-testid="wiki">
        <p className="m-0 max-w-[640px] text-muted">
          {t("ai.wiki.intro")} {h?.ai.ready ? t("ai.wiki.introAI", { tokens: h.context_tokens }) : t("ai.wiki.manualOnly")}
        </p>
        <ErrorNote error={home.error} />
        {creating && <NewPage onDone={() => setCreating(false)} />}
        {h?.ai.ready && <AssistantBox h={h} />}
        {home.isPending ? <Loading className="text-muted" /> : h && <PageList pages={h.pages} />}
        {h && h.shared.length > 0 && <SharedList pages={h.shared} />}
        <WikiLog stamp={h?.state.last_run_at ?? ""} />
        {h && h.pages.length > 0 && (
          <p className="m-0">
            <a className="touch-44 inline-flex items-center text-accent underline" href={wikiApi.exportUrl} download>
              {t("ai.wiki.export")}
            </a>
          </p>
        )}
      </div>
    </>
  );
}

function lastRun(t: ReturnType<typeof useT>, st: WikiState): string {
  if (!st.last_run_at) return t("ai.wiki.ai.never");
  const when = ago(st.last_run_at);
  if (st.last_outcome === "ok") return t("ai.wiki.ai.lastOk", { when, count: st.last_pages });
  if (st.last_outcome === "nulla") return t("ai.wiki.ai.lastNothing", { when });
  const code = st.last_error ?? "";
  const reason = code.startsWith("ai.budget")
    ? t("ai.wiki.ai.error.budget")
    : code === "ai.not_configured" || code === "ai.disabled"
      ? t("ai.wiki.ai.error.config")
      : code === "ai.wiki_no_tools"
        ? t("ai.wiki.ai.error.tools")
        : t("ai.wiki.ai.error.other");
  return t("ai.wiki.ai.lastError", { when, reason });
}

function AssistantBox({ h }: { h: Home }) {
  const t = useT();
  const qc = useQueryClient();
  const st = h.state;
  // La casella cambia subito; il valore del server arriva con la risposta.
  const [optimistic, setOptimistic] = useState<boolean | null>(null);
  const auto = useMutation({
    mutationFn: wikiApi.setAuto,
    onSuccess: (v) => qc.setQueryData(homeKey, v),
  });
  const refresh = useMutation({
    mutationFn: wikiApi.refresh,
    onSuccess: (v) => qc.setQueryData(homeKey, v),
  });
  const running = st.running || !!st.requested_at || refresh.isPending;
  return (
    <section className="flex flex-col gap-3 border-l-2 border-accent bg-side px-4 py-3" aria-labelledby="wiki-ai-title" data-testid="wiki-assistente">
      <h2 id="wiki-ai-title" className="m-0 text-[15px] font-semibold">
        {t("ai.wiki.ai.title")}
      </h2>
      <label className="flex items-start gap-2">
        <input
          type="checkbox"
          className="mt-1"
          checked={optimistic ?? st.auto}
          onChange={(e) => {
            const on = e.target.checked;
            setOptimistic(on);
            auto.mutate(on, { onSettled: () => setOptimistic(null) });
          }}
        />
        <span className="flex flex-col gap-0.5">
          <span className="font-semibold">{t("ai.wiki.ai.auto")}</span>
          <span className="text-[12.5px] text-muted">{t("ai.wiki.ai.autoHelp")}</span>
        </span>
      </label>
      {h.ai.masked && <p className="m-0 text-[12.5px] text-muted">{t("ai.wiki.ai.masked")}</p>}
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <button type="button" className="btn touch-44" disabled={running} onClick={() => refresh.mutate()}>
          {t("ai.wiki.ai.refresh")}
        </button>
        <p className="m-0 min-w-0 flex-1 basis-60 text-[13px]" role="status" data-testid="wiki-stato">
          {running ? t("ai.wiki.ai.running") : lastRun(t, st)}
        </p>
      </div>
      <ErrorNote error={auto.error ?? refresh.error} />
    </section>
  );
}

function NewPage({ onDone }: { onDone: () => void }) {
  const t = useT();
  const invalidate = useInvalidate();
  const navigate = useNavigate();
  const [title, setTitle] = useState("");
  const [body, setBody] = useState("");
  const create = useMutation({
    mutationFn: () => wikiApi.create(title, body),
    onSuccess: (v) => {
      invalidate();
      onDone();
      navigate({ to: "/profilo/wiki/$id", params: { id: v.page.id } });
    },
  });
  function submit(e: FormEvent) {
    e.preventDefault();
    create.mutate();
  }
  return (
    <form onSubmit={submit} className="flex flex-col gap-3 border-b border-hair pb-5" aria-label={t("ai.wiki.new")}>
      <label className="flex flex-col gap-1.5">
        <span className="label">{t("ai.wiki.newTitle")}</span>
        <input className="field" required maxLength={120} placeholder={t("ai.wiki.newTitlePh")} value={title} onChange={(e) => setTitle(e.target.value)} />
      </label>
      <label className="flex flex-col gap-1.5">
        <span className="label">{t("ai.wiki.body")}</span>
        <textarea
          className="field min-h-[160px] font-mono text-[13px]"
          value={body}
          onChange={(e) => setBody(e.target.value)}
          aria-describedby="wiki-new-help"
        />
        <span id="wiki-new-help" className="text-[12px] text-muted">
          {t("ai.wiki.bodyHelp")}
        </span>
      </label>
      <ErrorNote error={create.error} />
      <div>
        <button className="btn btn-primary touch-44" disabled={create.isPending || !title.trim()}>
          {t("ai.wiki.create")}
        </button>
      </div>
    </form>
  );
}

function PageList({ pages }: { pages: WikiSummary[] }) {
  const t = useT();
  return (
    <section className="flex flex-col gap-2">
      <SectionTitle aside={<span className="font-mono text-[11.5px] text-muted tabular">{pages.length}</span>}>{t("ai.wiki.pages")}</SectionTitle>
      {pages.length === 0 ? (
        <p className="m-0 text-muted">{t("ai.wiki.empty")}</p>
      ) : (
        <ul className="m-0 flex list-none flex-col p-0" aria-label={t("ai.wiki.pages")}>
          {pages.map((p) => (
            <li key={p.id} className="flex min-w-0 flex-col gap-0.5 border-b border-hair py-2.5">
              <Link
                to="/profilo/wiki/$id"
                params={{ id: p.id }}
                className="touch-44 inline-flex min-w-0 items-center self-start font-semibold [overflow-wrap:anywhere]"
              >
                {p.title}
              </Link>
              {p.excerpt && <span className="text-[13px] text-muted [overflow-wrap:anywhere]">{p.excerpt}</span>}
              <span className="flex flex-wrap gap-x-3 text-[12px] text-muted">
                <span>
                  {t("ai.wiki.meta", {
                    version: p.version,
                    who: who(p.updated_by),
                    when: ago(p.updated_at),
                  })}
                </span>
                {p.shared && <span className="font-semibold text-accent">{t("ai.wiki.sharedBadge")}</span>}
                {p.promoted_key && <span>{t("ai.wiki.promotedBadge", { key: p.promoted_key })}</span>}
              </span>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function SharedList({ pages }: { pages: WikiSummary[] }) {
  const t = useT();
  return (
    <section className="flex flex-col gap-2">
      <SectionTitle>{t("ai.wiki.sharedWithMe")}</SectionTitle>
      <ul className="m-0 flex list-none flex-col p-0" aria-label={t("ai.wiki.sharedWithMe")}>
        {pages.map((p) => (
          <li key={p.id} className="flex flex-wrap items-baseline gap-x-3 border-b border-hair py-1.5">
            <Link to="/profilo/wiki/$id" params={{ id: p.id }} className="touch-44 inline-flex items-center [overflow-wrap:anywhere]">
              {p.title}
            </Link>
            <span className="text-[12px] text-muted">
              {t("ai.wiki.sharedBy", { name: p.owner ?? "" })} · {ago(p.updated_at)}
            </span>
          </li>
        ))}
      </ul>
    </section>
  );
}

function opLabel(op: string) {
  return lookup(`ai.wiki.op.${op}`) ?? op;
}

/** stamp: l'ora dell'ultimo aggiornamento dell'assistente, così il registro si rilegge quando finisce. */
function WikiLog({ stamp }: { stamp: string }) {
  const t = useT();
  const qc = useQueryClient();
  const log = useQuery({ queryKey: [...logKey, stamp], queryFn: wikiApi.log });
  const [done, setDone] = useState(false);
  const undo = useMutation({
    mutationFn: (r: WikiRevision) => wikiApi.undo(r.id),
    onSuccess: () => {
      setDone(true);
      qc.invalidateQueries({ queryKey: homeKey });
    },
  });
  const items = log.data?.items ?? [];
  return (
    <section className="flex flex-col gap-2" data-testid="wiki-registro">
      <SectionTitle>{t("ai.wiki.log.title")}</SectionTitle>
      <ErrorNote error={log.error ?? undo.error} />
      {done && (
        <p className="m-0 text-[13px]" role="status">
          {t("ai.wiki.undone")}
        </p>
      )}
      {!log.isPending && items.length === 0 && <p className="m-0 text-muted">{t("ai.wiki.log.empty")}</p>}
      {items.length > 0 && (
        <ul className="m-0 flex list-none flex-col p-0" aria-label={t("ai.wiki.log.list")}>
          {items.map((r) => (
            <li key={r.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-hair py-2">
              <span className="flex min-w-0 flex-1 basis-60 flex-col gap-0.5">
                <span className="flex flex-wrap items-baseline gap-x-2">
                  <span className="label m-0">{opLabel(r.op)}</span>
                  {r.deleted ? (
                    <span className="[overflow-wrap:anywhere]">{r.page_title}</span>
                  ) : (
                    <Link to="/profilo/wiki/$id" params={{ id: r.page_id }} className="touch-44 inline-flex items-center [overflow-wrap:anywhere]">
                      {r.page_title}
                    </Link>
                  )}
                </span>
                <span className="text-[12px] text-muted [overflow-wrap:anywhere]">
                  {who(r.author)} · {ago(r.created_at)} ·{" "}
                  {t("ai.wiki.log.lines", {
                    added: r.added,
                    removed: r.removed,
                  })}
                  {r.note && <> · {r.note}</>}
                </span>
              </span>
              {r.latest && (
                <button
                  type="button"
                  className="btn touch-44 py-1"
                  aria-label={t("ai.wiki.undoLabel", {
                    version: r.version,
                    title: r.page_title,
                  })}
                  disabled={undo.isPending}
                  onClick={() => {
                    setDone(false);
                    undo.mutate(r);
                  }}
                >
                  {t("ai.wiki.undo")}
                </button>
              )}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

// ---------------------------------------------------------------------
// Pagina
// ---------------------------------------------------------------------

export function WikiPageView({ id }: { id: string }) {
  const t = useT();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const view = useQuery({
    queryKey: ["wiki", "page", id],
    queryFn: () => wikiApi.get(id),
  });
  const [editing, setEditing] = useState(false);
  const [history, setHistory] = useState(false);
  const [promote, setPromote] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const refresh = () => qc.invalidateQueries({ queryKey: homeKey });
  const share = useMutation({
    mutationFn: (s: boolean) => wikiApi.share(id, s),
    onSuccess: refresh,
  });
  const remove = useMutation({
    mutationFn: () => wikiApi.remove(id),
    onSuccess: () => {
      refresh();
      navigate({ to: "/profilo/wiki" });
    },
  });
  const v = view.data;
  const p = v?.page;
  const keyRefs = useMemo(() => (v?.refs ?? []).filter((r) => r.token === r.key), [v]);
  const body = useMemo(() => (p ? linkCodes(p.body, v?.refs ?? []) : ""), [p, v]);
  return (
    <>
      <PageHeader path={t("ai.wiki.path")} title={p?.title ?? t("ai.wiki.title")} />
      <div className="flex max-w-[880px] min-w-0 flex-col gap-5 px-4 pb-10 md:px-7" data-testid="wiki-pagina">
        <Link to="/profilo/wiki" className="touch-44 inline-flex items-center self-start text-[13px]">
          ← {t("ai.wiki.back")}
        </Link>
        <ErrorNote error={view.error} />
        {!p ? (
          view.isPending && <Loading className="text-muted" />
        ) : (
          <>
            <p className="m-0 flex flex-wrap gap-x-3 text-[12.5px] text-muted">
              <span>
                {t("ai.wiki.meta", {
                  version: p.version,
                  who: p.mine ? who(p.updated_by) : (p.owner ?? ""),
                  when: ago(p.updated_at),
                })}
              </span>
              {p.shared && <span className="font-semibold text-accent">{t("ai.wiki.sharedBadge")}</span>}
              {p.promoted_key && (
                <Link to="/documenti/$key" params={{ key: p.promoted_key }}>
                  {t("ai.wiki.promotedBadge", { key: p.promoted_key })}
                </Link>
              )}
            </p>
            {!p.mine && <p className="m-0 border-l-2 border-hair pl-3 text-[13px]">{t("ai.wiki.readOnly", { name: p.owner ?? "" })}</p>}
            {p.mine && !editing && (
              <div className="flex flex-wrap gap-2" role="group" aria-label={t("ai.wiki.title")} data-testid="wiki-azioni">
                <button type="button" className="btn btn-primary touch-44" onClick={() => setEditing(true)}>
                  {t("ai.wiki.edit")}
                </button>
                <button type="button" className="btn touch-44" aria-expanded={history} onClick={() => setHistory((h) => !h)}>
                  {history ? t("ai.wiki.hideHistory") : t("ai.wiki.history")}
                </button>
                <button type="button" className="btn touch-44" disabled={share.isPending} onClick={() => share.mutate(!p.shared)}>
                  {p.shared ? t("ai.wiki.unshare") : t("ai.wiki.share")}
                </button>
                <button type="button" className="btn touch-44 whitespace-normal" aria-expanded={promote} onClick={() => setPromote((x) => !x)}>
                  {t("ai.wiki.promote")}
                </button>
                {confirmDelete ? (
                  <>
                    <button type="button" className="btn btn-danger touch-44" disabled={remove.isPending} onClick={() => remove.mutate()}>
                      {t("ai.wiki.confirmDelete")}
                    </button>
                    <button type="button" className="btn touch-44" onClick={() => setConfirmDelete(false)}>
                      {t("common.cancel")}
                    </button>
                  </>
                ) : (
                  <button type="button" className="btn btn-quiet touch-44" onClick={() => setConfirmDelete(true)}>
                    {t("ai.wiki.delete")}
                  </button>
                )}
              </div>
            )}
            {confirmDelete && <p className="m-0 text-[12.5px] text-muted">{t("ai.wiki.deleteNote")}</p>}
            {p.shared && p.mine && <p className="m-0 text-[12.5px] text-muted">{t("ai.wiki.sharedNote")}</p>}
            <ErrorNote error={share.error ?? remove.error} />
            {promote && p.mine && <Promote id={id} />}
            {editing ? (
              <Editor key={p.version} id={id} title={p.title} body={p.body} version={p.version} onDone={() => setEditing(false)} />
            ) : p.body.trim() ? (
              <PageMarkdown text={body} refs={keyRefs} />
            ) : (
              <p className="m-0 text-muted">{t("ai.wiki.emptyBody")}</p>
            )}
            {history && p.mine && <History id={id} current={p.version} />}
          </>
        )}
      </div>
    </>
  );
}

function Editor({ id, title: t0, body: b0, version, onDone }: { id: string; title: string; body: string; version: number; onDone: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const [title, setTitle] = useState(t0);
  const [body, setBody] = useState(b0);
  const save = useMutation({
    mutationFn: () => wikiApi.save(id, title, body, version),
    onSuccess: (v) => {
      qc.setQueryData(["wiki", "page", id], v);
      qc.invalidateQueries({ queryKey: homeKey });
      onDone();
    },
  });
  function submit(e: FormEvent) {
    e.preventDefault();
    save.mutate();
  }
  return (
    <form onSubmit={submit} className="flex flex-col gap-3" aria-label={t("ai.wiki.edit")}>
      <label className="flex flex-col gap-1.5">
        <span className="label">{t("ai.wiki.newTitle")}</span>
        <input className="field" required maxLength={120} value={title} onChange={(e) => setTitle(e.target.value)} />
      </label>
      <label className="flex flex-col gap-1.5">
        <span className="label">{t("ai.wiki.body")}</span>
        <textarea
          className="field min-h-[320px] font-mono text-[13px]"
          value={body}
          onChange={(e) => setBody(e.target.value)}
          aria-describedby="wiki-edit-help"
        />
        <span id="wiki-edit-help" className="text-[12px] text-muted">
          {t("ai.wiki.bodyHelp")}
        </span>
      </label>
      <ErrorNote error={save.error} />
      <div className="flex flex-wrap gap-2">
        <button className="btn btn-primary touch-44" disabled={save.isPending || !title.trim()}>
          {save.isPending ? t("ai.wiki.saving") : t("ai.wiki.save")}
        </button>
        <button type="button" className="btn touch-44" onClick={onDone}>
          {t("ai.wiki.cancel")}
        </button>
      </div>
    </form>
  );
}

function Promote({ id }: { id: string }) {
  const t = useT();
  const spaces = useQuery({ queryKey: ["kb-spaces"], queryFn: kb.spaces });
  const writable = (spaces.data?.items ?? []).filter((s) => s.rights.write);
  const [space, setSpace] = useState("");
  const qc = useQueryClient();
  const go = useMutation({
    mutationFn: () => wikiApi.promote(id, space || writable[0]?.key || ""),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["wiki", "page", id] });
      qc.invalidateQueries({ queryKey: homeKey });
    },
  });
  if (spaces.isPending) return <Loading className="text-muted" rows={1} />;
  return (
    <section className="flex flex-col gap-2 border-l-2 border-hair pl-3" aria-label={t("ai.wiki.promote")}>
      <ErrorNote error={spaces.error} />
      {writable.length === 0 ? (
        <p className="m-0 text-[13px] text-muted">{t("ai.wiki.promoteNoSpace")}</p>
      ) : (
        <div className="flex flex-wrap items-end gap-2">
          <label className="flex min-w-0 flex-col gap-1.5">
            <span className="label">{t("ai.wiki.promoteSpace")}</span>
            <Select className="field" value={space || writable[0].key} onChange={(e) => setSpace(e.target.value)}>
              {writable.map((s) => (
                <option key={s.key} value={s.key}>
                  {s.name}
                </option>
              ))}
            </Select>
          </label>
          <button type="button" className="btn btn-primary touch-44 whitespace-normal" disabled={go.isPending} onClick={() => go.mutate()}>
            {t("ai.wiki.promoteGo")}
          </button>
        </div>
      )}
      <ErrorNote error={go.error} />
      {go.data && (
        <p className="m-0 text-[13px]" role="status">
          {t("ai.wiki.promoted")}{" "}
          <Link to="/documenti/$key" params={{ key: go.data.key }} className="font-mono">
            {go.data.key}
          </Link>
        </p>
      )}
    </section>
  );
}

function History({ id, current }: { id: string; current: number }) {
  const t = useT();
  const qc = useQueryClient();
  const hist = useQuery({
    queryKey: ["wiki", "history", id, current],
    queryFn: () => wikiApi.history(id),
  });
  const [open, setOpen] = useState<number | null>(null);
  const [confirm, setConfirm] = useState<number | null>(null);
  const after = () => {
    setConfirm(null);
    qc.invalidateQueries({ queryKey: homeKey });
  };
  const restore = useMutation({
    mutationFn: (n: number) => wikiApi.restore(id, n),
    onSuccess: after,
  });
  const undo = useMutation({
    mutationFn: (r: WikiRevision) => wikiApi.undo(r.id),
    onSuccess: after,
  });
  return (
    <section className="flex flex-col gap-2" data-testid="wiki-storia">
      <SectionTitle>{t("ai.wiki.history")}</SectionTitle>
      <ErrorNote error={hist.error ?? restore.error ?? undo.error} />
      <ul className="m-0 flex list-none flex-col p-0" aria-label={t("ai.wiki.historyList")}>
        {(hist.data?.items ?? []).map((r) => (
          <li key={r.id} className="flex flex-col gap-1 border-b border-hair py-2">
            <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
              <span className="flex min-w-0 flex-1 basis-56 flex-col">
                <span>
                  <span className="font-mono tabular">v{r.version}</span> <span className="label m-0">{opLabel(r.op)}</span>
                </span>
                <span className="text-[12px] text-muted [overflow-wrap:anywhere]">
                  {who(r.author)} · {ago(r.created_at)} ·{" "}
                  {t("ai.wiki.log.lines", {
                    added: r.added,
                    removed: r.removed,
                  })}
                  {r.note && <> · {r.note}</>}
                </span>
              </span>
              <span className="flex flex-wrap gap-2">
                <button
                  type="button"
                  className="btn touch-44 py-1"
                  aria-expanded={open === r.version}
                  onClick={() => setOpen(open === r.version ? null : r.version)}
                >
                  {open === r.version ? t("ai.wiki.hideDiff") : t("ai.wiki.diff")}
                </button>
                {r.latest ? (
                  <button type="button" className="btn touch-44 py-1" disabled={undo.isPending} onClick={() => undo.mutate(r)}>
                    {t("ai.wiki.undo")}
                  </button>
                ) : confirm === r.version ? (
                  <button type="button" className="btn btn-danger touch-44 py-1" disabled={restore.isPending} onClick={() => restore.mutate(r.version)}>
                    {t("ai.wiki.confirmRestore")}
                  </button>
                ) : (
                  <button type="button" className="btn touch-44 whitespace-normal py-1" onClick={() => setConfirm(r.version)}>
                    {t("ai.wiki.restore")}
                  </button>
                )}
              </span>
            </div>
            {open === r.version && <Change id={id} version={r.version} />}
          </li>
        ))}
      </ul>
    </section>
  );
}

function Change({ id, version }: { id: string; version: number }) {
  const c = useQuery({
    queryKey: ["wiki", "version", id, version],
    queryFn: () => wikiApi.version(id, version),
  });
  if (c.isPending) return <Loading className="text-muted" rows={1} />;
  if (!c.data) return <ErrorNote error={c.error} />;
  return <DiffLines c={c.data} />;
}

function DiffLines({ c }: { c: WikiChange }) {
  const t = useT();
  const changed = c.lines.filter((l) => l.op !== "eq");
  return (
    <div className="flex flex-col gap-1" aria-label={t("ai.wiki.diffLabel", { version: c.revision.version })} role="region">
      {c.title_from && <p className="m-0 text-[12.5px]">{t("ai.wiki.diffTitle", { from: c.title_from, to: c.revision.title })}</p>}
      {changed.length === 0 ? (
        <p className="m-0 text-[12.5px] text-muted">{t("ai.wiki.diffSame")}</p>
      ) : (
        <div className="border-y border-hair font-mono text-[12.5px] leading-relaxed">
          {changed.map((l, i) => (
            <div key={i} className="flex flex-col">
              {(l.op === "del" || l.op === "mod") && (
                <del className="block bg-[color-mix(in_srgb,var(--danger)_10%,transparent)] px-2 whitespace-pre-wrap no-underline [overflow-wrap:anywhere]">
                  − {l.op === "mod" ? l.old : l.text}
                </del>
              )}
              {(l.op === "add" || l.op === "mod") && (
                <ins className="block bg-[color-mix(in_srgb,var(--ok)_12%,transparent)] px-2 whitespace-pre-wrap no-underline [overflow-wrap:anywhere]">
                  + {l.text}
                </ins>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
