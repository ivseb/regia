import { useEffect, useMemo, useRef, useState, type ChangeEvent, type DragEvent, type KeyboardEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, collab, type Attachment, type Comment, type Member } from "../api";
import { useMe } from "../shell";
import { Markdown } from "../markdown";
import { ErrorNote, Initials, SectionTitle, ago } from "../ui";
import { useT } from "../i18n/react";
import { tr } from "../i18n/core";
import { fmtDateTime, loc } from "../i18n/format";

// Collaborazione nel dettaglio dell'elemento (piano, passo 3): segui, allegati, commenti.

function usePerms() {
  const perms = useMe().data?.active_tenant?.permissions ?? [];
  const has = (p: string) => perms.includes("*") || perms.includes(p);
  return { canComment: has("comment.write"), canUpload: has("comment.write") || has("element.write") };
}

export function bytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toLocaleString(loc(), { maximumFractionDigits: 0 })} kB`;
  return `${(n / 1024 / 1024).toLocaleString(loc(), { maximumFractionDigits: 1 })} MB`;
}

const full = (iso: string) => fmtDateTime(iso, { dateStyle: "medium", timeStyle: "short" });

/** Arrivando da una notifica (#dettaglio, #commenti) si porta in vista la parte giusta:
 *  sul telefono il dettaglio sta sotto la lista. */
function useScrollToHash<T extends HTMLElement>(hash: string, ready: boolean, target?: (el: T) => Element) {
  const ref = useRef<T>(null);
  const done = useRef(false);
  useEffect(() => {
    if (!ready || done.current || window.location.hash !== `#${hash}` || !ref.current) return;
    done.current = true;
    const el = target ? target(ref.current) : ref.current;
    // Sotto i 768 px la barra in alto è fissa: si lascia il suo spazio.
    const offset = window.innerWidth < 768 ? 56 : 8;
    requestAnimationFrame(() => window.scrollTo({ top: el.getBoundingClientRect().top + window.scrollY - offset }));
  }, [ready, hash, target]);
  return ref;
}
const asideOf = (el: HTMLElement) => el.closest("aside") ?? el;

/** Segui / non seguire, con il numero di osservatori. */
export function FollowButton({ refKey }: { refKey: string }) {
  const t = useT();
  const qc = useQueryClient();
  const st = useQuery({ queryKey: ["watch", refKey], queryFn: () => collab.watch(refKey) });
  const set = useMutation({
    mutationFn: (follow: boolean) => collab.setWatch(refKey, follow),
    onSuccess: (data) => qc.setQueryData(["watch", refKey], data),
  });
  const ref = useScrollToHash<HTMLSpanElement>("dettaglio", !!st.data, asideOf);
  if (!st.data) return null;
  const { following, followers, auto } = st.data;
  return (
    <span ref={ref} className="flex items-center gap-2">
      <span className="hidden text-[12px] text-muted tabular sm:inline" title={auto ? t("configurazione.collab.loSeguiPercheSei") : undefined}>
        {t("configurazione.collab.followerCount", { count: followers })}
      </span>
      <button
        className={`btn py-1 text-[12.5px] ${following ? "" : "btn-primary"}`}
        aria-pressed={following}
        disabled={set.isPending}
        onClick={() => set.mutate(!following)}
      >
        {following ? t("configurazione.collab.nonSeguire") : t("configurazione.collab.segui")}
      </button>
    </span>
  );
}

// ---------------------------------------------------------------------
// Allegati
// ---------------------------------------------------------------------

type Job = { id: number; name: string; progress: number; error?: string };

function useUploader(refKey: string, maxBytes: number | undefined) {
  const qc = useQueryClient();
  const [jobs, setJobs] = useState<Job[]>([]);
  const seq = useRef(0);
  const update = (id: number, patch: Partial<Job>) => setJobs((js) => js.map((j) => (j.id === id ? { ...j, ...patch } : j)));
  async function upload(files: File[], commentId?: string) {
    for (const f of files) {
      const id = ++seq.current;
      setJobs((js) => [...js, { id, name: f.name, progress: 0 }]);
      if (maxBytes && f.size > maxBytes) {
        update(id, { error: tr("configurazione.collab.tooBig", { size: bytes(maxBytes) }) });
        continue;
      }
      try {
        await collab.upload(refKey, f, commentId, (p) => update(id, { progress: p }));
        setJobs((js) => js.filter((j) => j.id !== id));
      } catch (e) {
        update(id, { error: e instanceof Error ? e.message : tr("configurazione.collab.uploadFailed") });
      }
    }
    qc.invalidateQueries({ queryKey: ["attachments", refKey] });
    qc.invalidateQueries({ queryKey: ["comments", refKey] });
    qc.invalidateQueries({ queryKey: ["activity"] });
  }
  const dismiss = (id: number) => setJobs((js) => js.filter((j) => j.id !== id));
  return { jobs, upload, dismiss };
}

function Jobs({ jobs, dismiss }: { jobs: Job[]; dismiss: (id: number) => void }) {
  const t = useT();
  if (jobs.length === 0) return null;
  return (
    <ul className="m-0 flex list-none flex-col gap-1.5 p-0" aria-live="polite">
      {jobs.map((j) => (
        <li key={j.id} className="flex flex-col gap-1 text-[12.5px]">
          <span className="flex items-baseline gap-2">
            <span className="min-w-0 flex-1 break-words">{j.name}</span>
            {j.error ? (
              <>
                <span className="text-danger">{j.error}</span>
                <button className="text-muted underline" onClick={() => dismiss(j.id)}>{t("configurazione.common.chiudi")}</button>
              </>
            ) : (
              <span className="font-mono text-muted tabular">{Math.round(j.progress * 100)}%</span>
            )}
          </span>
          {!j.error && (
            <span className="h-[3px] bg-hair" aria-hidden="true">
              <span className="block h-full bg-accent" style={{ width: `${Math.round(j.progress * 100)}%` }} />
            </span>
          )}
        </li>
      ))}
    </ul>
  );
}

const coarse = typeof window !== "undefined" && window.matchMedia?.("(pointer: coarse)").matches;

/** Pulsanti di caricamento: file (anche più d'uno) e, sul telefono, la fotocamera. */
function UploadButtons({ onFiles, label = tr("configurazione.collab.uploadFile") }: { onFiles: (f: File[]) => void; label?: string }) {
  const t = useT();
  const pick = (e: ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(e.target.files ?? []);
    e.target.value = "";
    if (files.length) onFiles(files);
  };
  return (
    <span className="flex flex-wrap gap-2">
      <label className="btn cursor-pointer py-1 text-[12.5px]">
        {label}
        <input type="file" multiple className="sr-only" onChange={pick} />
      </label>
      {coarse && (
        <label className="btn cursor-pointer py-1 text-[12.5px]">
          {t("configurazione.collab.scattaFoto")}
          <input type="file" accept="image/*" capture="environment" className="sr-only" onChange={pick} />
        </label>
      )}
    </span>
  );
}

function AttachmentRow({ a, onRemoved }: { a: Attachment; onRemoved: () => void }) {
  const t = useT();
  const remove = useMutation({ mutationFn: () => collab.removeAttachment(a.id), onSuccess: onRemoved });
  return (
    <li className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5 border-b border-hair py-1.5">
      <a href={collab.contentUrl(a.id, true)} className="min-w-0 flex-[1_1_160px] font-medium [overflow-wrap:anywhere] text-fg underline decoration-hair underline-offset-2 hover:text-accent" download={a.name}>
        {a.name}
      </a>
      <span className="font-mono text-[12px] text-muted tabular">{bytes(a.size_bytes)}</span>
      <span className="text-[12px] text-muted" title={full(a.created_at)}>
        {a.author_name} · {ago(a.created_at)}
      </span>
      {a.can_delete && (
        <button
          className="text-[12px] text-muted underline hover:text-danger"
          disabled={remove.isPending}
          onClick={() => window.confirm(t("configurazione.collab.confirmDeleteAttachment", { name: a.name })) && remove.mutate()}
        >
          {t("configurazione.common.elimina")}
        </button>
      )}
      {remove.error && <span className="basis-full"><ErrorNote error={remove.error} /></span>}
    </li>
  );
}

function Previews({ items }: { items: Attachment[] }) {
  const imgs = items.filter((a) => a.preview);
  if (imgs.length === 0) return null;
  return (
    <div className="flex flex-wrap gap-2">
      {imgs.map((a) => (
        <a key={a.id} href={collab.contentUrl(a.id)} target="_blank" rel="noopener" className="block border border-hair hover:border-accent" title={a.name}>
          <img
            src={collab.contentUrl(a.id)}
            alt={a.name}
            loading="lazy"
            decoding="async"
            width={a.width ?? undefined}
            height={a.height ?? undefined}
            className="block size-24 object-cover"
          />
        </a>
      ))}
    </div>
  );
}

export function Attachments({ refKey }: { refKey: string }) {
  const t = useT();
  const qc = useQueryClient();
  const { canUpload } = usePerms();
  const list = useQuery({ queryKey: ["attachments", refKey], queryFn: () => collab.attachments(refKey) });
  const { jobs, upload, dismiss } = useUploader(refKey, list.data?.max_bytes);
  const [drag, setDrag] = useState(false);
  const items = list.data?.items ?? [];
  const onDrop = (e: DragEvent) => {
    e.preventDefault();
    setDrag(false);
    if (!canUpload) return;
    const files = Array.from(e.dataTransfer.files);
    if (files.length) upload(files);
  };
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["attachments", refKey] });
    qc.invalidateQueries({ queryKey: ["comments", refKey] });
  };
  return (
    <section
      className={`flex flex-col gap-2 ${drag ? "outline-2 outline-offset-4 outline-accent outline-dashed" : ""}`}
      onDragOver={(e) => {
        if (!canUpload) return;
        e.preventDefault();
        setDrag(true);
      }}
      onDragLeave={() => setDrag(false)}
      onDrop={onDrop}
      aria-label={t("configurazione.collab.allegati")}
    >
      <SectionTitle aside={<span className="font-mono text-[11.5px] text-muted tabular">{items.length || ""}</span>}>{t("configurazione.collab.allegati")}</SectionTitle>
      <ErrorNote error={list.error} />
      <Previews items={items} />
      {items.length > 0 && (
        <ul className="m-0 list-none p-0">
          {items.map((a) => (
            <AttachmentRow key={a.id} a={a} onRemoved={refresh} />
          ))}
        </ul>
      )}
      {list.data && items.length === 0 && <p className="m-0 text-[12.5px] text-muted">{t("configurazione.collab.nessunAllegato")}</p>}
      <Jobs jobs={jobs} dismiss={dismiss} />
      {canUpload && (
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
          <UploadButtons onFiles={(f) => upload(f)} />
          <span className="text-[12px] text-muted">
            {coarse ? "" : t("configurazione.collab.oppureTrascinaQuiI")}
            {list.data ? t("configurazione.collab.maxPerFile", { size: bytes(list.data.max_bytes) }) : ""}
          </span>
        </div>
      )}
    </section>
  );
}

// ---------------------------------------------------------------------
// Commenti
// ---------------------------------------------------------------------

// Testo prima del cursore che termina con "@parola": si propone la persona.
const MENTION_AT = /(?:^|[^\p{L}\p{N}_@.])@(\p{L}[\p{L}\p{M}\p{N}'’-]*(?: \p{L}[\p{L}\p{M}\p{N}'’-]*)?)?$/u;

export function MentionBox({
  value,
  onChange,
  onSubmit,
  members,
  placeholder,
  autoFocus,
  id,
}: {
  value: string;
  onChange: (v: string) => void;
  onSubmit: () => void;
  members: Member[];
  placeholder?: string;
  autoFocus?: boolean;
  id?: string;
}) {
  const t = useT();
  const ref = useRef<HTMLTextAreaElement>(null);
  const [query, setQuery] = useState<string | null>(null);
  const [active, setActive] = useState(0);
  const suggestions = useMemo(() => {
    if (query === null) return [];
    const q = query.toLowerCase();
    return members.filter((m) => m.display_name.toLowerCase().includes(q)).slice(0, 6);
  }, [query, members]);

  function sync(text: string, caret: number) {
    const m = MENTION_AT.exec(text.slice(0, caret));
    setQuery(m ? (m[1] ?? "") : null);
    setActive(0);
  }
  function pick(m: Member) {
    const el = ref.current;
    if (!el || query === null) return;
    const caret = el.selectionStart;
    const start = caret - query.length - 1;
    const next = value.slice(0, start) + "@" + m.display_name + " " + value.slice(caret);
    onChange(next);
    setQuery(null);
    const pos = start + m.display_name.length + 2;
    requestAnimationFrame(() => {
      el.focus();
      el.setSelectionRange(pos, pos);
    });
  }
  function onKey(e: KeyboardEvent<HTMLTextAreaElement>) {
    if (suggestions.length > 0) {
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        setActive((a) => (a + (e.key === "ArrowDown" ? 1 : suggestions.length - 1)) % suggestions.length);
        return;
      }
      if (e.key === "Enter" || e.key === "Tab") {
        e.preventDefault();
        pick(suggestions[active]);
        return;
      }
      if (e.key === "Escape") {
        e.preventDefault();
        setQuery(null);
        return;
      }
    }
    if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
      e.preventDefault();
      onSubmit();
    }
  }
  const listId = useMemo(() => `menzioni-${Math.random().toString(36).slice(2)}`, []);
  return (
    <div className="relative">
      <textarea
        ref={ref}
        id={id}
        className="field min-h-24 resize-y text-[16px] md:text-[13.5px]"
        value={value}
        placeholder={placeholder}
        autoFocus={autoFocus}
        aria-autocomplete="list"
        aria-controls={suggestions.length ? listId : undefined}
        onChange={(e) => {
          onChange(e.target.value);
          sync(e.target.value, e.target.selectionStart);
        }}
        onKeyDown={onKey}
        onClick={(e) => sync(e.currentTarget.value, e.currentTarget.selectionStart)}
        onBlur={() => window.setTimeout(() => setQuery(null), 150)}
      />
      {suggestions.length > 0 && (
        <ul id={listId} role="listbox" aria-label={t("configurazione.collab.personeDaMenzionare")} className="absolute inset-x-0 top-full z-30 m-0 list-none border border-rule bg-bg p-0">
          {suggestions.map((m, i) => (
            <li
              key={m.id}
              role="option"
              aria-selected={i === active}
              className={`flex cursor-pointer items-center gap-2 px-2.5 py-2 ${i === active ? "bg-sel" : ""}`}
              onMouseDown={(e) => {
                e.preventDefault();
                pick(m);
              }}
              onMouseEnter={() => setActive(i)}
            >
              <Initials name={m.display_name} />
              <span className="flex-1">{m.display_name}</span>
              {m.member_type === "requester" && <span className="text-[11.5px] text-muted">{t("configurazione.collab.portale")}</span>}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

export function CommentItem({ c, members, refKey }: { c: Comment; members: Member[]; refKey: string }) {
  const t = useT();
  const qc = useQueryClient();
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(c.body);
  const done = () => {
    qc.invalidateQueries({ queryKey: ["comments", refKey] });
    qc.invalidateQueries({ queryKey: ["attachments", refKey] });
    qc.invalidateQueries({ queryKey: ["activity"] });
  };
  const save = useMutation({
    mutationFn: () => collab.editComment(c.id, draft),
    onSuccess: () => {
      setEditing(false);
      done();
    },
  });
  const remove = useMutation({ mutationFn: () => collab.deleteComment(c.id), onSuccess: done });
  return (
    <li className="flex gap-2.5 border-b border-hair py-3 first:pt-1">
      <Initials name={c.author_name} />
      <div className="flex min-w-0 flex-1 flex-col gap-1.5">
        <div className="flex flex-wrap items-baseline gap-x-2 text-[12.5px]">
          <span className="font-semibold text-fg">{c.author_name}</span>
          <time dateTime={c.created_at} title={full(c.created_at)} className="text-muted">{ago(c.created_at)}</time>
          {c.edited_at && (
            <span className="text-muted" title={t("configurazione.collab.editedOn", { date: full(c.edited_at) })}>{t("configurazione.collab.modificato")}</span>
          )}
          {!editing && (c.can_edit || c.can_delete) && (
            <span className="ml-auto flex gap-3">
              {c.can_edit && (
                <button className="text-muted underline hover:text-fg" onClick={() => { setDraft(c.body); setEditing(true); }}>
                  {t("configurazione.common.modifica")}
                </button>
              )}
              {c.can_delete && (
                <button
                  className="text-muted underline hover:text-danger"
                  disabled={remove.isPending}
                  onClick={() => window.confirm(t("configurazione.collab.confirmDeleteComment")) && remove.mutate()}
                >
                  {t("configurazione.common.elimina")}
                </button>
              )}
            </span>
          )}
        </div>
        {editing ? (
          <form
            className="flex flex-col gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              save.mutate();
            }}
          >
            <MentionBox value={draft} onChange={setDraft} onSubmit={() => save.mutate()} members={members} autoFocus />
            <div className="flex gap-2">
              <button className="btn btn-primary py-1" disabled={save.isPending || !draft.trim()}>{t("configurazione.common.salva")}</button>
              <button type="button" className="btn py-1" onClick={() => setEditing(false)}>{t("configurazione.common.annulla")}</button>
            </div>
            <ErrorNote error={save.error} />
          </form>
        ) : (
          <Markdown text={c.body} mentions={c.mentions} />
        )}
        {c.attachments.length > 0 && (
          <ul className="m-0 flex list-none flex-wrap gap-1.5 p-0">
            {c.attachments.map((a) => (
              <li key={a.id}>
                <a
                  href={collab.contentUrl(a.id, !a.preview)}
                  target={a.preview ? "_blank" : undefined}
                  rel="noopener"
                  className="inline-flex max-w-full items-center gap-1.5 border border-hair px-2 py-0.5 text-[12px] text-fg no-underline hover:border-accent"
                >
                  <span className="min-w-0 [overflow-wrap:anywhere]">{a.name}</span>
                  <span className="font-mono text-muted tabular">{bytes(a.size_bytes)}</span>
                </a>
              </li>
            ))}
          </ul>
        )}
        <ErrorNote error={remove.error} />
      </div>
    </li>
  );
}

export function Comments({ refKey }: { refKey: string }) {
  const t = useT();
  const { canComment } = usePerms();
  const list = useQuery({ queryKey: ["comments", refKey], queryFn: () => collab.comments(refKey) });
  const members = useQuery({ queryKey: ["members"], queryFn: api.members, staleTime: 300_000, enabled: canComment });
  const items = list.data?.items ?? [];
  const ref = useScrollToHash<HTMLElement>("commenti", !!list.data);
  return (
    <section ref={ref} id="commenti" className="flex flex-col gap-2" aria-label={t("configurazione.collab.commenti")}>
      <SectionTitle aside={<span className="font-mono text-[11.5px] text-muted tabular">{items.length || ""}</span>}>{t("configurazione.collab.commenti")}</SectionTitle>
      <ErrorNote error={list.error} />
      {items.length > 0 && (
        <ol className="m-0 flex list-none flex-col p-0">
          {items.map((c) => (
            <CommentItem key={c.id} c={c} members={members.data?.items ?? []} refKey={refKey} />
          ))}
        </ol>
      )}
      {list.data && items.length === 0 && <p className="m-0 text-[12.5px] text-muted">{t("configurazione.collab.nessunCommentoScriviIl")}</p>}
      <CommentComposer refKey={refKey} />
    </section>
  );
}

/** Campo per un nuovo commento, con menzioni e allegati (anche nella scheda Attività). */
export function CommentComposer({ refKey, autoFocus }: { refKey: string; autoFocus?: boolean }) {
  const t = useT();
  const qc = useQueryClient();
  const { canComment } = usePerms();
  const members = useQuery({ queryKey: ["members"], queryFn: api.members, staleTime: 300_000, enabled: canComment });
  const atts = useQuery({ queryKey: ["attachments", refKey], queryFn: () => collab.attachments(refKey), enabled: canComment });
  const { jobs, upload, dismiss } = useUploader(refKey, atts.data?.max_bytes);
  const [body, setBody] = useState("");
  const [files, setFiles] = useState<File[]>([]);
  const send = useMutation({
    mutationFn: () => collab.addComment(refKey, body),
    onSuccess: async (c) => {
      const pending = files;
      setBody("");
      setFiles([]);
      qc.invalidateQueries({ queryKey: ["comments", refKey] });
      qc.invalidateQueries({ queryKey: ["activity"] });
      if (pending.length) await upload(pending, c.id);
    },
  });
  const submit = () => {
    if (body.trim() && !send.isPending) send.mutate();
  };
  if (!canComment) return <p className="m-0 text-[12.5px] text-muted">{t("configurazione.collab.puoiLeggereICommenti")}</p>;
  return (
    <form
      className="flex flex-col gap-2 pt-1"
      onSubmit={(e) => {
        e.preventDefault();
        submit();
      }}
    >
      <label className="sr-only" htmlFor={`commento-${refKey}`}>{t("configurazione.collab.nuovoCommento")}</label>
      <MentionBox
        id={`commento-${refKey}`}
        value={body}
        onChange={setBody}
        onSubmit={submit}
        members={members.data?.items ?? []}
        autoFocus={autoFocus}
        placeholder={t("configurazione.collab.scriviUnCommentoPer")}
      />
      {files.length > 0 && (
        <ul className="m-0 flex list-none flex-wrap gap-1.5 p-0">
          {files.map((f, i) => (
            <li key={i} className="inline-flex items-center gap-1.5 border border-hair px-2 py-0.5 text-[12px]">
              <span className="min-w-0 [overflow-wrap:anywhere]">{f.name}</span>
              <span className="font-mono text-muted tabular">{bytes(f.size)}</span>
              <button type="button" aria-label={t("configurazione.collab.removeFile", { name: f.name })} className="text-muted hover:text-danger" onClick={() => setFiles((fs) => fs.filter((_, j) => j !== i))}>
                ×
              </button>
            </li>
          ))}
        </ul>
      )}
      <div className="flex flex-wrap items-center gap-2">
        <button className="btn btn-primary" disabled={send.isPending || !body.trim()}>
          {send.isPending ? t("configurazione.collab.invio") : t("configurazione.collab.commenta")}
        </button>
        <UploadButtons label={t("configurazione.collab.allega")} onFiles={(f) => setFiles((fs) => [...fs, ...f])} />
        <span className="hidden text-[12px] text-muted md:inline">
          {t("configurazione.collab.grassettoCorsivoCodiceLink")}
        </span>
      </div>
      <ErrorNote error={send.error} />
      <Jobs jobs={jobs} dismiss={dismiss} />
    </form>
  );
}

/** Porta in vista un contenitore arrivando con #commenti (anche fuori dalla sezione Commenti). */
export { useScrollToHash };
