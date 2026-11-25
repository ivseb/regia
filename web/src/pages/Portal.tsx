// Portale del richiedente (demand.md §14 D01, §16 decisione 7; piano, passo 14; ondata 3: catalogo).
// Semplice e accogliente ma sobrio: un catalogo di voci (carte con ricerca), ognuna con il suo modulo a passi e la
// bozza salvata sul server; le mie richieste con la fase spiegata in parole semplici, il prossimo passo, chi la
// segue, i commenti e gli allegati per i chiarimenti. Il modulo di ogni voce si disegna in Configurazione → Portale.
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { defaultsOf, payloadOf } from "../fields";
import { Markdown } from "../markdown";
import { catalogApi, type EntryView } from "../portalCatalogApi";
import { reportApi, type MyRequest, type Portal, type PortalDraft } from "../reportApi";
import { useMe } from "../shell";
import { ErrorNote, SectionTitle, ago, date, Loading } from "../ui";
import { Attachments, Comments } from "./Collaboration";
import { CatalogIcon, EMPTY, FirstStep, ReviewStep, StepBody, entryFacts, formOf, stepIncomplete, useStepTitle, type Form } from "./PortalForm";
import { useT } from "../i18n/react";

function PortalHeader({ path, title, children }: { path: string; title: string; children?: ReactNode }) {
  return (
    <header className="flex flex-col gap-2 px-4 pt-6 pb-4 md:px-7">
      <span className="font-mono text-[11.5px] text-muted">{path}</span>
      <h1 className="m-0 text-[30px] leading-[1.08] font-bold tracking-[-0.02em] md:text-[34px]">{title}</h1>
      {children}
    </header>
  );
}

/** Avanzamento a segmenti, come lo stage-gate: la fase in parole, senza numeri. */
function Steps({ step, steps, phase }: { step: number; steps: number; phase: string }) {
  return (
    <span className="flex flex-col gap-1" aria-label={phase}>
      <span className="flex gap-0.5" aria-hidden="true">
        {Array.from({ length: steps }, (_, i) => (
          <span key={i} className={`h-[5px] w-3 ${step === 0 ? "bg-hair" : i < step - 1 ? "bg-fg" : i === step - 1 ? "bg-accent" : "bg-hair"}`} />
        ))}
      </span>
      <span className="text-[12px]">
        <span className={step === 0 ? "font-semibold text-flag" : "font-semibold"}>{phase}</span>
      </span>
    </span>
  );
}

// ---------------------------------------------------------------------
// Home del portale

export function PortalHome() {
  const t = useT();
  const me = useMe().data;
  const portal = useQuery({ queryKey: ["portal"], queryFn: reportApi.portal });
  const p = portal.data;
  const first = me?.display_name.split(/\s+/)[0] ?? "";
  return (
    <>
      <PortalHeader path={t("lavoro.portal.path")} title={first ? t("lavoro.portal.hello", { name: first }) : t("lavoro.portal.title")}>
        {p?.welcome ? (
          <div className="max-w-2xl text-[15px] leading-relaxed">
            <Markdown text={p.welcome} />
          </div>
        ) : (
          <p className="m-0 max-w-2xl text-[15px] text-muted">{t("lavoro.portal.intro")}</p>
        )}
      </PortalHeader>
      <div className="flex max-w-4xl flex-col gap-9 px-4 pb-12 md:px-7">
        <ErrorNote error={portal.error} />
        {p && <Catalog portal={p} />}
        {p && p.drafts.length > 0 && <Drafts drafts={p.drafts} entries={p.entries} />}
        {p && <MyRequests items={p.requests} />}
      </div>
    </>
  );
}

/** Il catalogo: una carta per voce (nome, descrizione, icona, categoria), con la ricerca. */
function Catalog({ portal }: { portal: Portal }) {
  const t = useT();
  const [search, setSearch] = useState("");
  const needle = search.trim().toLowerCase();
  const shown = portal.entries.filter((e) => !needle || `${e.label} ${e.description} ${e.category}`.toLowerCase().includes(needle));
  const groups = [...new Set(shown.map((e) => e.category))];
  if (portal.entries.length === 0) return <p className="m-0 text-muted">{t("lavoro.portal.noTypes")}</p>;
  return (
    <section className="flex flex-col gap-4" aria-label={t("lavoro.portal.newRequest")}>
      <SectionTitle>{t("lavoro.portal.newRequest")}</SectionTitle>
      <input
        type="search"
        className="field max-w-md"
        aria-label={t("lavoro.catalogo.search")}
        placeholder={t("lavoro.catalogo.searchPlaceholder")}
        value={search}
        onChange={(e) => setSearch(e.target.value)}
      />
      {shown.length === 0 && <p role="status" className="m-0 text-muted">{t("lavoro.catalogo.noMatch", { text: search.trim() })}</p>}
      {groups.map((cat) => (
        <div key={cat} className="flex flex-col gap-2">
          {groups.length > 1 && <h3 className="label m-0 text-muted">{cat || t("lavoro.catalogo.other")}</h3>}
          <ul className="m-0 grid list-none grid-cols-1 gap-3 p-0 sm:grid-cols-2">
            {shown.filter((e) => e.category === cat).map((e) => (
              <EntryCard key={e.key} entry={e} draft={portal.drafts.find((d) => d.entry === e.key)} />
            ))}
          </ul>
        </div>
      ))}
    </section>
  );
}

function EntryCard({ entry, draft }: { entry: EntryView; draft?: PortalDraft }) {
  const t = useT();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const restart = useMutation({
    mutationFn: () => reportApi.deleteDraft(draft!.id),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ["portal"] });
      navigate({ to: "/portale/nuova", search: { tipo: entry.key } });
    },
  });
  return (
    <li className="flex flex-col gap-3 border border-hair p-4">
      <div className="flex items-start gap-3">
        <span className="mt-0.5 text-accent"><CatalogIcon name={entry.icon} /></span>
        <div className="flex min-w-0 flex-col gap-0.5">
          <h4 className="m-0 text-[16px] font-semibold">{entry.label}</h4>
          {entry.description && <span className="text-[13.5px] text-muted">{entry.description}</span>}
        </div>
      </div>
      {draft ? (
        <div className="flex flex-col gap-2">
          <span className="text-[12.5px] text-muted">
            {t("lavoro.catalogo.hasDraft", { title: draft.title || t("lavoro.portal.untitled"), when: ago(draft.updated_at) })}
          </span>
          <div className="flex flex-wrap gap-2">
            <Link to="/portale/nuova" search={{ tipo: entry.key, bozza: draft.id }} aria-label={t("lavoro.catalogo.resumeNamed", { name: entry.label })} className="btn btn-primary no-underline">
              {t("lavoro.catalogo.resume")}
            </Link>
            <button type="button" className="btn" disabled={restart.isPending} onClick={() => window.confirm(t("lavoro.catalogo.confirmRestart")) && restart.mutate()}>
              {t("lavoro.catalogo.restart")}
            </button>
          </div>
          <ErrorNote error={restart.error} />
        </div>
      ) : (
        <Link to="/portale/nuova" search={{ tipo: entry.key }} aria-label={t("lavoro.catalogo.startNamed", { name: entry.label })} className="btn btn-primary self-start no-underline">
          {t("lavoro.portal.start")}
        </Link>
      )}
    </li>
  );
}

function Drafts({ drafts, entries }: { drafts: PortalDraft[]; entries: EntryView[] }) {
  const t = useT();
  const qc = useQueryClient();
  const remove = useMutation({ mutationFn: reportApi.deleteDraft, onSuccess: () => qc.invalidateQueries({ queryKey: ["portal"] }) });
  return (
    <section className="flex flex-col gap-2" aria-label={t("lavoro.portal.drafts")}>
      <SectionTitle aside={<span className="font-mono text-[11.5px] text-muted tabular">{drafts.length}</span>}>{t("lavoro.portal.draftsToFinish")}</SectionTitle>
      <ul className="m-0 flex list-none flex-col p-0">
        {drafts.map((d) => (
          <li key={d.id} className="flex flex-wrap items-center gap-3 border-b border-hair py-2.5">
            <span className="flex min-w-0 flex-1 flex-col">
              <span className="truncate font-semibold">{d.title || t("lavoro.portal.untitled")}</span>
              <span className="text-[12px] text-muted">
                {entries.find((x) => x.key === d.entry)?.label ?? d.type} · {t("lavoro.portal.saved", { when: ago(d.updated_at) })}
              </span>
            </span>
            <Link to="/portale/nuova" search={{ tipo: d.entry, bozza: d.id }} className="btn no-underline">{t("lavoro.portal.resume")}</Link>
            <button className="touch-44 text-[12px] text-muted underline hover:text-danger" onClick={() => window.confirm(t("lavoro.portal.confirmDelete")) && remove.mutate(d.id)}>
              {t("lavoro.portal.delete")}
            </button>
          </li>
        ))}
      </ul>
      <ErrorNote error={remove.error} />
    </section>
  );
}

function MyRequests({ items }: { items: MyRequest[] }) {
  const t = useT();
  return (
    <section className="flex flex-col gap-2" aria-label={t("lavoro.portal.myRequests")}>
      <SectionTitle aside={<span className="font-mono text-[11.5px] text-muted tabular">{items.length || ""}</span>}>{t("lavoro.portal.myRequests")}</SectionTitle>
      {items.length === 0 ? (
        <p className="m-0 text-muted">{t("lavoro.portal.noneSent")}</p>
      ) : (
        <ul className="m-0 flex list-none flex-col p-0">
          {items.map((r) => (
            <li key={r.id}>
              <Link
                to="/portale/richieste/$key"
                params={{ key: r.key }}
                className="grid grid-cols-1 gap-2 border-b border-hair py-3 text-fg no-underline hover:bg-sel/60 sm:grid-cols-[minmax(0,1fr)_200px] sm:gap-4"
              >
                <span className="flex min-w-0 flex-col gap-0.5">
                  <span className="flex items-baseline gap-2">
                    <span className="shrink-0 font-mono text-[12px] text-accent">{r.key}</span>
                    <span className="truncate text-[15px] font-semibold">{r.title}</span>
                  </span>
                  <span className="text-[13px] text-muted">{r.text}</span>
                  <span className="text-[12px] text-muted">
                    {t("lavoro.portal.updated", { when: ago(r.updated_at) })}
                    {r.comments > 0 && ` · ${t("lavoro.portal.comments", { count: r.comments })}`}
                  </span>
                </span>
                <Steps step={r.step} steps={r.steps} phase={r.phase} />
              </Link>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

// ---------------------------------------------------------------------
// Nuova richiesta a passi, con la bozza salvata sul server

/** «Richieste simili già aperte»: si cerca mentre si scrive il titolo (con una pausa); «È questa» aggiunge un voto e un commento. */
function SimilarBox({ text }: { text: string }) {
  const t = useT();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [typed, setTyped] = useState(text);
  const [no, setNo] = useState<string[]>([]);
  useEffect(() => {
    const id = window.setTimeout(() => setTyped(text), 400);
    return () => window.clearTimeout(id);
  }, [text]);
  const q = typed.trim();
  const found = useQuery({ queryKey: ["portal", "similar", q], queryFn: () => reportApi.similar(q), enabled: q.length >= 8, staleTime: 30_000 });
  const support = useMutation({
    mutationFn: (key: string) => catalogApi.support(key, t("lavoro.catalogo.supportText")),
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: ["portal"] });
      navigate({ to: "/portale/richieste/$key", params: { key: r.key } });
    },
  });
  const items = (q.length >= 8 ? (found.data?.items ?? []) : []).filter((r) => !no.includes(r.key));
  if (items.length === 0) return null;
  return (
    <section aria-label={t("dafare.portal.similarTitle")} className="flex flex-col gap-2 border border-hair p-3">
      <h3 className="m-0 text-[14px] font-bold">{t("dafare.portal.similarTitle")}</h3>
      <p className="m-0 text-[12.5px] text-muted">{t("lavoro.catalogo.similarHint")}</p>
      <ul className="m-0 flex list-none flex-col p-0">
        {items.map((r) => (
          <li key={r.key} className="flex flex-col gap-1 border-t border-hair py-2">
            <span className="flex flex-wrap items-baseline gap-x-2.5">
              <span className="font-mono text-[12px] text-accent">{r.key}</span>
              <span className="min-w-0 flex-1 font-semibold">{r.title}</span>
              <span className="text-[12px] text-muted">{r.phase}</span>
            </span>
            <span className="flex flex-wrap gap-2">
              {r.mine ? (
                <Link to="/portale/richieste/$key" params={{ key: r.key }} className="btn no-underline">
                  {t("dafare.portal.similarOpen", { key: r.key })}
                </Link>
              ) : (
                <button type="button" className="btn btn-primary" disabled={support.isPending} onClick={() => support.mutate(r.key)}>
                  {t("lavoro.catalogo.itIsThis")}
                  <span className="sr-only"> {r.key}</span>
                </button>
              )}
              <button type="button" className="btn" onClick={() => setNo([...no, r.key])}>
                {t("lavoro.catalogo.notThis")}
                <span className="sr-only"> {r.key}</span>
              </button>
            </span>
          </li>
        ))}
      </ul>
      <ErrorNote error={support.error} />
    </section>
  );
}

export function PortalNew({ type, draftId }: { type?: string; draftId?: string }) {
  const t = useT();
  const portal = useQuery({ queryKey: ["portal"], queryFn: reportApi.portal });
  if (portal.error) return <div className="p-6"><ErrorNote error={portal.error} /></div>;
  if (!portal.data) return <Loading className="px-4 py-6 text-muted md:px-7" />;
  const asked = portal.data.drafts.find((d) => d.id === draftId);
  const entry = portal.data.entries.find((x) => x.key === (asked?.entry ?? type)) ?? portal.data.entries[0];
  if (!entry) return <p className="px-4 py-6 text-muted md:px-7">{t("lavoro.portal.noTypesAvailable")}</p>;
  // Una sola bozza per voce: «Inizia» riprende quella già aperta invece di aprirne un'altra.
  const draft = asked ?? (draftId ? undefined : portal.data.drafts.find((d) => d.entry === entry.key));
  return <Wizard key={draft?.id ?? entry.key} entry={entry} portal={portal.data} draft={draft} resumed={!asked && !!draft} />;
}

function Wizard({ entry, portal, draft, resumed }: { entry: EntryView; portal: Portal; draft?: PortalDraft; resumed?: boolean }) {
  const t = useT();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const stepTitle = useStepTitle();
  const init = useMemo(() => formOf(draft), [draft]);
  // L'unità della persona (appartenenza alle unità organizzative) è già scelta; la bozza vince se l'ha cambiata.
  const fromProfile = !init.f.requesting_unit_id && !!portal.my_unit && portal.units.some((u) => u.key === portal.my_unit);
  const [f, setF] = useState<Form>(fromProfile ? { ...init.f, requesting_unit_id: portal.my_unit } : init.f ?? EMPTY);
  const [custom, setCustom] = useState<Record<string, unknown>>(() => ({ ...defaultsOf(entry.fields), ...init.custom }));
  const [id, setId] = useState<string | null>(draft?.id ?? null);
  const std = new Set(entry.steps.flatMap((s) => s.fields.filter((x) => x.std).map((x) => x.key)));
  const steps = [
    { key: "cosa", label: t("lavoro.portal.stepWhat") },
    ...entry.steps.map((s, i) => ({ key: `passo-${i}`, label: stepTitle(s, i) })),
    { key: "invio", label: t("lavoro.portal.stepReview") },
  ];
  const [step, setStep] = useState(Math.min(draft?.step ?? 0, steps.length - 1));
  const [saved, setSaved] = useState<string | null>(draft ? draft.updated_at : null);
  const headingRef = useRef<HTMLHeadingElement>(null);
  useEffect(() => headingRef.current?.focus(), [step]);

  const facts = entryFacts(entry, f, custom);
  const body = (s: number) => {
    const data: Record<string, unknown> = { description: f.description, fields: payloadOf(entry.fields, custom, facts) };
    if (std.has("requesting_unit_id") && f.requesting_unit_id) data.requesting_unit_id = f.requesting_unit_id;
    if (std.has("driver") && f.driver) data.driver = f.driver;
    if (std.has("is_mandatory")) data.is_mandatory = f.is_mandatory;
    if (std.has("regulatory_deadline") && f.is_mandatory && f.regulatory_deadline) data.regulatory_deadline = f.regulatory_deadline;
    if (std.has("desired_date") && f.desired_date) data.desired_date = f.desired_date;
    return { entry: entry.key, type: entry.type, title: f.title, data, step: s };
  };
  const save = useMutation({
    mutationFn: (s: number) => reportApi.saveDraft(id, body(s)),
    onSuccess: (d) => {
      setId(d.id);
      setSaved(d.updated_at);
      qc.invalidateQueries({ queryKey: ["portal"] });
    },
  });
  const send = useMutation({
    mutationFn: async () => {
      const d = await reportApi.saveDraft(id, body(step));
      setId(d.id);
      return reportApi.submitDraft(d.id);
    },
    onSuccess: (el) => {
      qc.invalidateQueries({ queryKey: ["portal"] });
      navigate({ to: "/portale/richieste/$key", params: { key: el.key }, search: { inviata: true } });
    },
  });
  const go = (s: number) => {
    save.mutate(s);
    setStep(s);
  };
  const cur = steps[step].key;
  const stepView = cur.startsWith("passo-") ? entry.steps[Number(cur.slice(6))] : undefined;
  const anyIncomplete = entry.steps.some((s) => stepIncomplete(entry, s, f, custom, facts));
  const canNext = cur === "cosa" ? f.title.trim().length > 0 : stepView ? !stepIncomplete(entry, stepView, f, custom, facts) : true;
  const set = (k: keyof Form) => (e: { target: { value: string } }) => setF({ ...f, [k]: e.target.value });

  return (
    <>
      <PortalHeader path={`${t("lavoro.portal.path")} / ${entry.label.toLowerCase()}`} title={entry.label}>
        {entry.description && <p className="m-0 max-w-2xl text-muted">{entry.description}</p>}
      </PortalHeader>
      <div className="flex max-w-3xl flex-col gap-6 px-4 pb-12 md:px-7">
        <ol className="m-0 flex list-none gap-1 p-0" aria-label={t("lavoro.portal.steps")}>
          {steps.map((s, i) => (
            <li key={s.key} className="flex min-w-0 flex-1 flex-col gap-1.5" aria-current={i === step ? "step" : undefined}>
              <span className={`h-1 ${i < step ? "bg-fg" : i === step ? "bg-accent" : "bg-hair"}`} />
              <span className={`truncate text-[12px] ${i === step ? "font-semibold" : "text-muted"}`}>
                <span className="font-mono tabular">{i + 1}</span> <span className="hidden sm:inline">{s.label}</span>
              </span>
            </li>
          ))}
        </ol>
        <form
          className="flex flex-col gap-5"
          onSubmit={(e) => {
            e.preventDefault();
            if (cur === "invio") {
              if (!anyIncomplete) send.mutate();
            } else if (canNext) go(step + 1);
          }}
        >
          <h2 ref={headingRef} tabIndex={-1} className="m-0 text-[20px] font-bold outline-none">
            {steps[step].label}
          </h2>
          {resumed && <p role="status" className="m-0 border-l-2 border-accent pl-3 text-[13px] text-muted">{t("dafare.portal.resume")}</p>}
          {cur === "cosa" && <FirstStep entry={entry} f={f} set={set} extra={<SimilarBox text={f.title} />} />}
          {stepView && <StepBody entry={entry} step={stepView} f={f} setF={setF} custom={custom} setCustom={(k, v) => setCustom((c) => ({ ...c, [k]: v }))} units={portal.units} myUnit={portal.my_unit} />}
          {cur === "invio" && <ReviewStep entry={entry} f={f} custom={custom} units={portal.units} />}
          {cur === "invio" && anyIncomplete && <p role="status" className="m-0 border-l-2 border-flag pl-3 text-[13px]">{t("lavoro.catalogo.missing")}</p>}
          <ErrorNote error={send.error ?? save.error} />
          <div className="flex flex-wrap items-center gap-2 border-t border-hair pt-4">
            {step > 0 && (
              <button type="button" className="btn" onClick={() => go(step - 1)}>
                {t("lavoro.portal.back")}
              </button>
            )}
            {cur === "invio" ? (
              <button className="btn btn-primary" disabled={send.isPending || !f.title.trim() || anyIncomplete}>
                {send.isPending ? t("lavoro.portal.sending") : t("lavoro.portal.send")}
              </button>
            ) : (
              <button className="btn btn-primary" disabled={!canNext}>
                {t("lavoro.portal.next")}
              </button>
            )}
            <button type="button" className="btn" disabled={save.isPending} onClick={() => save.mutate(step)}>
              {t("lavoro.portal.saveLater")}
            </button>
            <span className="text-[12px] text-muted" aria-live="polite">
              {save.isPending ? t("lavoro.portal.saving") : saved ? t("lavoro.portal.draftSaved", { when: ago(saved) }) : t("lavoro.portal.autoSave")}
            </span>
          </div>
          <Link to="/portale" className="touch-44 self-start text-[12px] text-muted underline">{t("lavoro.portal.backToPortal")}</Link>
        </form>
      </div>
    </>
  );
}


// ---------------------------------------------------------------------
// La richiesta vista dal richiedente

export function PortalRequestPage({ refKey, justSent }: { refKey: string; justSent?: boolean }) {
  const t = useT();
  const qc = useQueryClient();
  const req = useQuery({ queryKey: ["portal-request", refKey], queryFn: () => reportApi.portalRequest(refKey) });
  const [comment, setComment] = useState("");
  const act = useMutation({
    mutationFn: ({ to, comment }: { to: string; comment?: string }) => api.transition(req.data!.id, to, comment),
    onSuccess: () => {
      setComment("");
      qc.invalidateQueries({ queryKey: ["portal-request", refKey] });
      qc.invalidateQueries({ queryKey: ["portal"] });
    },
  });
  if (req.error) return <div className="px-4 py-6 md:px-7"><ErrorNote error={req.error} /><Link to="/portale" className="text-accent underline">{t("lavoro.portal.backToPortal")}</Link></div>;
  if (!req.data) return <Loading className="px-4 py-6 text-muted md:px-7" />;
  const r = req.data;
  const needsComment = r.actions.some((a) => a.comment_required);
  return (
    <article className="flex flex-col" aria-label={`${r.key} ${r.title}`}>
      <PortalHeader path={`${t("lavoro.portal.path")} / ${r.key}`} title={r.title}>
        {justSent && (
          <p role="status" className="m-0 border-l-2 border-ok pl-3 text-[14px]">
            {t("lavoro.portal.justSent")}
          </p>
        )}
      </PortalHeader>
      <div className="grid max-w-5xl grid-cols-1 gap-8 px-4 pb-12 md:px-7 lg:grid-cols-[minmax(0,1fr)_300px]">
        <div className="flex min-w-0 flex-col gap-7">
          <section className="flex flex-col gap-3" aria-label={t("lavoro.portal.status")}>
            <SectionTitle>{t("lavoro.portal.status")}</SectionTitle>
            <p className="m-0 text-[18px] leading-snug">
              <span className="font-semibold">{r.phase}.</span> {r.text}
            </p>
            <ol className="m-0 flex list-none flex-col gap-0 p-0">
              {r.path.map((s, i) => (
                <li key={s.name} className="grid grid-cols-[22px_minmax(0,1fr)] items-center gap-2 py-1">
                  <span
                    aria-hidden="true"
                    className={`flex size-[18px] items-center justify-center border font-mono text-[10px] tabular ${
                      s.state === "done" ? "border-fg bg-fg text-bg" : s.state === "current" ? "border-accent bg-accent text-accent-fg" : "border-hair text-muted"
                    }`}
                  >
                    {i + 1}
                  </span>
                  <span className={s.state === "current" ? "font-semibold" : s.state === "done" ? "" : "text-muted"}>
                    {s.name}
                    <span className="sr-only">{s.state === "done" ? ` ${t("lavoro.portal.done")}` : s.state === "current" ? ` ${t("lavoro.portal.now")}` : ""}</span>
                  </span>
                </li>
              ))}
            </ol>
            {r.next_step && (
              <p className="m-0 border-l-2 border-accent pl-3">
                <span className="label text-muted">{t("lavoro.portal.nextStep")}</span>
                <br />
                {r.next_step}
              </p>
            )}
            {r.actions.length > 0 && (
              <div className="flex flex-col gap-2">
                {needsComment && (
                  <label className="flex flex-col gap-1">
                    <span className="label text-muted">{t("lavoro.portal.reason")}</span>
                    <textarea className="field min-h-16" value={comment} onChange={(e) => setComment(e.target.value)} />
                  </label>
                )}
                <div className="flex flex-wrap gap-2">
                  {r.actions.map((a) => (
                    <button
                      key={a.to}
                      className={`btn ${a.to === r.path.find((p) => p.state === "todo")?.name ? "btn-primary" : ""}`}
                      disabled={act.isPending || !!a.blocked || (a.comment_required && !comment.trim())}
                      title={a.blocked}
                      onClick={() => act.mutate({ to: a.to, comment: a.comment_required ? comment : undefined })}
                    >
                      {a.label}
                    </button>
                  ))}
                </div>
                <ErrorNote error={act.error} />
              </div>
            )}
          </section>
          {r.description && (
            <section className="flex flex-col gap-2" aria-label={t("lavoro.portal.theRequest")}>
              <SectionTitle>{t("lavoro.portal.theNeed")}</SectionTitle>
              <div className="leading-relaxed">
                <Markdown text={r.description} />
              </div>
            </section>
          )}
          <Comments refKey={r.key} />
          <Attachments refKey={r.key} />
        </div>
        <aside className="flex flex-col gap-6" aria-label={t("lavoro.portal.summary")}>
          <section className="flex flex-col gap-2">
            <SectionTitle>{t("lavoro.portal.followers")}</SectionTitle>
            {r.followed_by.length === 0 ? (
              <p className="m-0 text-[12.5px] text-muted">{t("lavoro.portal.noFollowers")}</p>
            ) : (
              <dl className="m-0 flex flex-col">
                {r.followed_by.map((p, i) => (
                  <div key={i} className="flex flex-col border-b border-hair py-1.5">
                    <dt className="text-[12px] text-muted">{p.role}</dt>
                    <dd className="m-0 font-semibold">{p.name}</dd>
                  </div>
                ))}
              </dl>
            )}
          </section>
          <section className="flex flex-col gap-2">
            <SectionTitle>{t("lavoro.portal.data")}</SectionTitle>
            <dl className="m-0 flex flex-col text-[13px]">
              {[[t("lavoro.portal.code"), r.key], [t("lavoro.portal.type"), r.type_name], [t("lavoro.portal.opened"), date(r.created_at)], [t("lavoro.portal.updatedLabel"), ago(r.updated_at)], ...r.data.map((x) => [x.label, x.value])].map(([k, v]) => (
                <div key={k} className="grid grid-cols-[110px_minmax(0,1fr)] gap-2 border-b border-hair py-1.5">
                  <dt className="text-muted">{k}</dt>
                  <dd className="m-0 break-words">{v}</dd>
                </div>
              ))}
            </dl>
          </section>
          <Link to="/portale" className="text-[12.5px] text-accent underline">{t("lavoro.portal.allMine")}</Link>
        </aside>
      </div>
    </article>
  );
}
