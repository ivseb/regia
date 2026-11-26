// Vista di crisi dell'incidente (replica/usabilita.md, proposta 3), pensata prima per il telefono:
// l'orologio delle notifiche in cima, la prossima azione, e una barra fissa in basso con i comandi
// che si usano con il pollice. Sotto, l'impatto sulle applicazioni (dal grafo), i fornitori,
// decisioni e comunicazioni, la bozza della notifica e la cronologia. Su schermo largo le stesse
// parti stanno in due colonne e la barra torna una riga sotto l'intestazione.
import { useState, type ReactNode } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { crisisApi, type CrisisApp, type CrisisView, type DraftField, type NextAction, type TimelineItem } from "../crisisApi";
import { riskApi, EVENT_KINDS, type Notification } from "../riskApi";
import { ErrorNote, Loading, SectionTitle, StageGate, date } from "../ui";
import { Countdown, dateTime, fromLocalInput } from "./riskUi";
import { Dialog } from "./work-ui";
import { useT } from "../i18n/react";
import { fmtCurrency, fmtDateTime, fmtDuration, fmtNumber } from "../i18n/format";

type T = ReturnType<typeof useT>;

function useRefresh(refKey: string) {
  const qc = useQueryClient();
  return () => {
    void qc.invalidateQueries({ queryKey: ["risk"] });
    void qc.invalidateQueries({ queryKey: ["element", refKey] });
    void qc.invalidateQueries({ queryKey: ["activity"] });
  };
}

function Heading({ id, children }: { id: string; children: ReactNode }) {
  return (
    <div id={id} className="scroll-mt-16">
      <SectionTitle>{children}</SectionTitle>
    </div>
  );
}

const pending = (n: Notification) => n.status === "pending" || n.status === "draft";

export function IncidentCrisisPage({ refKey }: { refKey: string }) {
  const t = useT();
  const refresh = useRefresh(refKey);
  const view = useQuery({ queryKey: ["risk", "crisis", refKey], queryFn: () => crisisApi.crisis(refKey), refetchInterval: 60_000 });
  const detail = useQuery({ queryKey: ["element", refKey], queryFn: () => api.element(refKey) });
  const [recording, setRecording] = useState<null | "choose" | "decision" | "communication" | "update" | "contained">(null);
  const [draftOpen, setDraftOpen] = useState(false);
  const x = view.data;
  const d = detail.data;
  if (view.error) return <div className="px-4 py-6 md:px-7"><ErrorNote error={view.error} /></div>;
  if (!x || !d) return <Loading className="px-4 py-6 text-muted md:px-7" />;
  const inc = x.incident;
  const manage = inc.can.manage;
  const closed = d.element.state_category === "done" || d.element.state_category === "cancelled";
  const open = (id: string) => window.setTimeout(() => document.getElementById(id)?.scrollIntoView({ behavior: "smooth", block: "start" }), 120);
  const goDraft = () => {
    setDraftOpen(true);
    open("bozza");
  };
  const waiting = inc.notifications.filter(pending).sort((a, b) => new Date(a.due_at ?? 8.64e15).getTime() - new Date(b.due_at ?? 8.64e15).getTime());
  return (
    <article className="flex min-w-0 flex-1 flex-col break-words pb-28 md:pb-10" aria-label={t("crisi.page.label", { key: inc.key })}>
      <header className="flex flex-col gap-2 border-b-2 border-rule px-4 pb-3 pt-4 md:px-7">
        <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
          <Link to="/e/$key" params={{ key: inc.key }} search={{ scheda: "dettagli" }} className="font-mono text-[12.5px]">
            {"← "}{inc.key}
          </Link>
          <span className="label text-flag">{t("crisi.page.title")}</span>
          {inc.major && <span className="label border border-flag px-1.5 py-0.5 text-flag">{t("crisi.page.major")}</span>}
        </div>
        <h1 className="m-0 text-[22px] font-bold leading-tight tracking-[-0.02em] md:text-[28px]">{inc.element.title}</h1>
        <StageGate states={d.phases} current={d.element.state} size="lg" names />
        <p className="m-0 text-[12.5px] text-muted">
          {t("crisi.page.meta", { detected: dateTime(inc.detected_at), hours: fmtNumber(Math.round(inc.duration_hours * 10) / 10) })}
          {inc.lead ? ` · ${t("crisi.page.lead", { name: inc.lead })}` : ""}
        </p>
      </header>

      <Clock notifications={waiting} onPrepare={goDraft} manage={manage} />
      <NextCard next={x.next} stale={x.stale_hours} />

      {manage && !closed && <ActionBar x={x} refKey={refKey} forward={d.transitions.filter((tr) => tr.forward && tr.blocked.length === 0 && !tr.comment_required)} onRecord={setRecording} onDraft={goDraft} onMove={(to) => moveTo(d.element.id, to, refresh)} />}

      <div className="grid min-w-0 grid-cols-1 gap-x-8 gap-y-6 px-4 py-5 md:px-7 lg:grid-cols-[minmax(0,1fr)_minmax(0,440px)]">
        <div className="flex min-w-0 flex-col gap-6 lg:col-start-1 lg:row-start-1">
          <Impact apps={x.applications} detected={inc.detected_at} />
          <Providers x={x} />
        </div>
        <div className="flex min-w-0 flex-col gap-6 lg:col-start-2 lg:row-span-2 lg:row-start-1">
          <Decisions x={x} manage={manage} onRecord={setRecording} />
          <Draft x={x} refKey={refKey} manage={manage} open={draftOpen} onOpen={setDraftOpen} />
        </div>
        <div className="flex min-w-0 flex-col gap-6 lg:col-start-1 lg:row-start-2">
          <Timeline items={x.timeline} />
        </div>
      </div>

      {recording === "choose" && <ChooseDialog onPick={setRecording} onClose={() => setRecording(null)} />}
      {recording && recording !== "choose" && <RecordDialog kind={recording} refKey={refKey} onClose={() => setRecording(null)} />}
    </article>
  );
}

function moveTo(id: string, to: string, refresh: () => void) {
  void api.transition(id, to).then(refresh);
}

// ---------------------------------------------------------------------
// Orologio e prossima azione

function Clock({ notifications, onPrepare, manage }: { notifications: Notification[]; onPrepare: () => void; manage: boolean }) {
  const t = useT();
  const clockRow = (n: Notification, first: boolean) => (
    <div key={n.id} className={`flex flex-wrap items-center gap-x-5 gap-y-2 border-l-4 px-4 py-3 md:px-7 ${first ? "border-flag bg-sel" : "border-hair"}`}>
      <div className="flex min-w-0 flex-[1_1_14rem] flex-col gap-0.5">
        <span className="label text-flag">{first ? t("crisi.clock.next") : n.scheme_name.split(" · ")[0]}</span>
        <span className="text-[14px] font-semibold">{n.scheme_name.split(" · ")[0]} · {n.stage_name}</span>
        <span className="text-[12.5px] text-muted tabular">
          {n.due_at ? t("crisi.clock.due", { when: dateTime(n.due_at) }) : t("crisi.clock.waiting")}
          {n.status === "draft" ? ` · ${t("crisi.clock.draft")}` : ""}
        </span>
      </div>
      <span className={first ? "text-[20px]" : "text-[15px]"}>
        <Countdown due={n.due_at} />
      </span>
      {first && manage && (
        <button type="button" className="btn btn-primary min-h-[44px] justify-center" onClick={onPrepare}>
          {t("crisi.clock.prepare")}
        </button>
      )}
    </div>
  );
  return (
    <section id="orologio" aria-label={t("crisi.clock.title")} className="flex flex-col border-b border-hair">
      {notifications.length === 0 ? (
        <p className="m-0 border-l-4 border-rule px-4 py-3 text-[13px] text-muted md:px-7">{t("crisi.clock.none")}</p>
      ) : (
        <>
          {notifications.slice(0, 1).map((n) => clockRow(n, true))}
          {notifications.length > 1 && (
            <details className="border-t border-hair">
              <summary className="min-h-[44px] cursor-pointer px-4 py-3 text-[13px] text-muted md:min-h-[24px] md:px-7">{t("crisi.clock.others", { count: notifications.length - 1 })}</summary>
              {notifications.slice(1).map((n) => clockRow(n, false))}
            </details>
          )}
        </>
      )}
    </section>
  );
}

function NextCard({ next, stale }: { next: NextAction; stale: number }) {
  const t = useT();
  if (next.kind === "none") return null;
  return (
    <section aria-label={t("crisi.next.title")} className="flex flex-col gap-0.5 border-b border-hair px-4 py-3 md:px-7">
      <span className="label text-muted">{t("crisi.next.title")}</span>
      <p className="m-0 text-[15px] font-semibold">{t(`crisi.next.${next.kind}.text`)}</p>
      {next.kind === "update" && <p className="m-0 text-[12.5px] text-muted">{t("crisi.next.update.stale", { hours: stale })}</p>}
    </section>
  );
}

// ---------------------------------------------------------------------
// Barra d'azione: fissa in basso sul telefono, in riga sotto l'intestazione su schermo largo.

function ActionBar({ x, refKey, forward, onRecord, onDraft, onMove }: {
  x: CrisisView;
  refKey: string;
  forward: { to: string; label: string }[];
  onRecord: (k: "choose" | "decision" | "communication" | "update" | "contained") => void;
  onDraft: () => void;
  onMove: (to: string) => void;
}) {
  const t = useT();
  const refresh = useRefresh(refKey);
  const classify = useMutation({ mutationFn: () => riskApi.classify(refKey, false), onSuccess: refresh });
  const kind = x.next.kind;
  let primary: ReactNode = null;
  const btn = "btn btn-primary min-h-[44px] flex-[1_1_0] justify-center";
  if (kind === "classify") primary = <button type="button" className={btn} disabled={classify.isPending} onClick={() => classify.mutate()}>{t("crisi.bar.classify")}</button>;
  else if (kind === "notify") primary = <button type="button" className={btn} onClick={onDraft}>{t("crisi.bar.notify")}</button>;
  else if (kind === "contain") primary = <button type="button" className={btn} onClick={() => onRecord("contained")}>{t("crisi.bar.contain")}</button>;
  else if (kind === "update") primary = <button type="button" className={btn} onClick={() => onRecord("update")}>{t("crisi.bar.update")}</button>;
  else if (kind === "resolve")
    primary = (
      <Link to="/e/$key" params={{ key: x.incident.key }} search={{ scheda: "dettagli" }} className={`${btn} no-underline`}>{t("crisi.bar.resolve")}</Link>
    );
  else if (kind === "close" && forward[0]) primary = <button type="button" className={btn} onClick={() => onMove(forward[0].to)}>{forward[0].label}</button>;
  else if (kind === "postmortem")
    primary = (
      <Link to="/e/$key" params={{ key: x.incident.key }} search={{ scheda: "dettagli" }} className={`${btn} no-underline`}>{t("crisi.bar.postmortem")}</Link>
    );
  return (
    <nav
      aria-label={t("crisi.bar.label")}
      className="fixed inset-x-0 bottom-0 z-40 flex gap-2 border-t-2 border-rule bg-bg px-4 py-2 pb-[max(0.5rem,env(safe-area-inset-bottom))] md:static md:z-auto md:border-b md:border-t-0 md:border-hair md:px-7 md:py-3"
    >
      {primary}
      <button type="button" className="btn min-h-[44px] flex-[1_1_0] justify-center" onClick={() => onRecord("choose")}>{t("crisi.bar.record")}</button>
      {x.draft.notification && kind !== "notify" && (
        <button type="button" className="btn min-h-[44px] flex-[1_1_0] justify-center" onClick={onDraft}>{t("crisi.bar.draft")}</button>
      )}
      {classify.error != null && <span className="sr-only"><ErrorNote error={classify.error} /></span>}
    </nav>
  );
}

function ChooseDialog({ onPick, onClose }: { onPick: (k: "decision" | "communication" | "update" | "contained") => void; onClose: () => void }) {
  const t = useT();
  const kinds = ["decision", "communication", "update", "contained"] as const;
  return (
    <Dialog title={t("crisi.record.choose")} onClose={onClose}>
      <div className="flex flex-col gap-2">
        {kinds.map((k) => (
          <button key={k} type="button" className="btn min-h-[44px] justify-start" onClick={() => onPick(k)}>
            <span className="flex flex-col items-start text-left">
              <span>{t(`crisi.record.kind.${k}`)}</span>
              <span className="text-[12px] font-normal text-muted">{t(`crisi.record.hint.${k}`)}</span>
            </span>
          </button>
        ))}
      </div>
    </Dialog>
  );
}

function RecordDialog({ kind, refKey, onClose }: { kind: "decision" | "communication" | "update" | "contained"; refKey: string; onClose: () => void }) {
  const t = useT();
  const refresh = useRefresh(refKey);
  const [text, setText] = useState("");
  const [at, setAt] = useState("");
  const add = useMutation({
    mutationFn: () => riskApi.addEvent(refKey, { kind, text, at: fromLocalInput(at) }),
    onSuccess: () => {
      refresh();
      onClose();
    },
  });
  return (
    <Dialog title={t(`crisi.record.kind.${kind}`)} onClose={onClose}>
      <form className="flex flex-col gap-3" onSubmit={(e) => { e.preventDefault(); add.mutate(); }}>
        <label className="flex flex-col gap-1">
          <span className="label">{t("crisi.record.text")}</span>
          <textarea className="field min-h-24" required value={text} onChange={(e) => setText(e.target.value)} placeholder={t(`crisi.record.placeholder.${kind}`)} />
        </label>
        <label className="flex flex-col gap-1">
          <span className="label">{t("crisi.record.when")}</span>
          <input className="field" type="datetime-local" value={at} onChange={(e) => setAt(e.target.value)} />
        </label>
        <ErrorNote error={add.error} />
        <div className="flex gap-2">
          <button className="btn btn-primary min-h-[44px]" disabled={add.isPending || !text.trim()}>{t("crisi.record.save")}</button>
          <button type="button" className="btn min-h-[44px]" onClick={onClose}>{t("crisi.record.cancel")}</button>
        </div>
      </form>
    </Dialog>
  );
}

// ---------------------------------------------------------------------
// Impatto: applicazioni colpite e fornitori

function Impact({ apps, detected }: { apps: CrisisApp[]; detected: string }) {
  const t = useT();
  return (
    <section aria-label={t("crisi.impact.title")} className="flex flex-col gap-3">
      <Heading id="impatto">{t("crisi.impact.title")}</Heading>
      {apps.length === 0 && <p className="m-0 text-[13px] text-muted">{t("crisi.impact.none")}</p>}
      {apps.map((a) => (
        <AppCard key={a.ref.id} a={a} detected={detected} />
      ))}
    </section>
  );
}

function RefLinks({ items }: { items: { key: string; title: string }[] }) {
  return (
    <span className="inline-flex flex-wrap gap-x-3 gap-y-1">
      {items.map((r) => (
        <Link key={r.key} to="/e/$key" params={{ key: r.key }} className="inline-flex min-h-[24px] items-center">
          <span className="font-mono">{r.key}</span>
          <span className="ml-1 min-w-0 break-words">{r.title}</span>
        </Link>
      ))}
    </span>
  );
}

function Row({ k, children }: { k: string; children: ReactNode }) {
  return (
    <div className="grid grid-cols-[110px_minmax(0,1fr)] gap-x-3 text-[13px]">
      <span className="label text-muted">{k}</span>
      <span className="min-w-0 [overflow-wrap:anywhere]">{children}</span>
    </div>
  );
}

function AppCard({ a, detected }: { a: CrisisApp; detected: string }) {
  const t = useT();
  const r = a.last_release;
  const hoursBefore = r ? Math.max(0, Math.round((new Date(detected).getTime() - new Date(r.at).getTime()) / 3_600_000)) : 0;
  return (
    <article className="flex flex-col gap-1.5 border-l-2 border-rule pl-3">
      <h4 className="m-0 flex flex-wrap items-baseline gap-x-2 text-[15px] font-semibold">
        <Link to="/e/$key" params={{ key: a.ref.key }} search={{ scheda: "produzione" }}>
          <span className="font-mono text-[12.5px]">{a.ref.key}</span> {a.ref.title}
        </Link>
        {a.ref.critical && <span className="label text-flag">{t("crisi.impact.critical")}</span>}
        {a.criticality && <span className="label text-muted">{t(`crisi.crit.${a.criticality as "low"}`)}</span>}
      </h4>
      <Row k={t("crisi.impact.release")}>
        {r ? (
          <span>
            <span className="font-mono">{r.version}</span> · {r.environment} · {fmtDateTime(r.at)}
            {r.suspect && <span className="ml-2 font-semibold text-flag">{t("crisi.impact.suspect", { hours: hoursBefore })}</span>}
          </span>
        ) : (
          <span className="text-muted">{t("crisi.impact.noRelease")}</span>
        )}
      </Row>
      <Row k={t("crisi.impact.vulns")}>
        {a.vulns.open === 0 ? (
          <span className="text-muted">{t("crisi.impact.noVulns")}</span>
        ) : (
          <Link to="/devsecops/vulnerabilita" search={{ q: a.ref.title }}>
            {t("crisi.impact.vulnsText", { open: a.vulns.open, overdue: a.vulns.overdue, critical: a.vulns.critical })}
          </Link>
        )}
      </Row>
      {a.risks.length > 0 && <Row k={t("crisi.impact.risks")}><RefLinks items={a.risks} /></Row>}
      {a.other_incidents.length > 0 && <Row k={t("crisi.impact.otherIncidents")}><RefLinks items={a.other_incidents} /></Row>}
      {a.dependents.length > 0 && <Row k={t("crisi.impact.dependents")}><RefLinks items={a.dependents} /></Row>}
      {a.runbooks.length > 0 && <Row k={t("crisi.impact.runbooks")}><RefLinks items={a.runbooks} /></Row>}
      <p className="m-0 text-[12.5px] text-muted">
        {a.team ? t("crisi.impact.team", { team: a.team }) : t("crisi.impact.noTeam")}
        {a.rto_minutes != null ? ` · ${t("crisi.impact.rto", { time: fmtDuration(a.rto_minutes) })}` : ""}
      </p>
    </article>
  );
}

function Providers({ x }: { x: CrisisView }) {
  const t = useT();
  return (
    <section aria-label={t("crisi.providers.title")} className="flex flex-col gap-2">
      <Heading id="fornitori">{t("crisi.providers.title")}</Heading>
      {x.providers.length === 0 && <p className="m-0 text-[13px] text-muted">{t("crisi.providers.none")}</p>}
      {x.providers.map((p) => (
        <p key={p.ref.id} className="m-0 flex flex-col gap-0.5 border-l-2 border-hair pl-3 text-[13px]">
          <span className="flex flex-wrap items-baseline gap-x-2">
            <Link to="/e/$key" params={{ key: p.ref.key }} className="font-semibold">{p.ref.title}</Link>
            {p.cause && <span className="label text-flag">{t("crisi.providers.cause")}</span>}
            {p.critical && <span className="label text-muted">{t("crisi.providers.critical")}</span>}
          </span>
          <span className="text-muted">
            {p.applications.length > 0 ? t("crisi.providers.serves", { apps: p.applications.join(", ") }) : t("crisi.providers.noApps")}
            {p.next_end ? ` · ${t("crisi.providers.contractEnd", { date: date(p.next_end) })}` : ""}
          </span>
        </p>
      ))}
    </section>
  );
}

// ---------------------------------------------------------------------
// Decisioni e comunicazioni

function Decisions({ x, manage, onRecord }: { x: CrisisView; manage: boolean; onRecord: (k: "decision" | "communication") => void }) {
  const t = useT();
  const block = (kind: "decision" | "communication", items: CrisisView["decisions"]) => (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-baseline justify-between gap-2">
        <h4 className="label m-0 text-muted">{t(`crisi.decisions.${kind}`)}</h4>
        {manage && (
          <button type="button" className="min-h-[24px] text-[12.5px] text-accent underline" onClick={() => onRecord(kind)}>
            {t("crisi.decisions.add")}
          </button>
        )}
      </div>
      {items.length === 0 && <p className="m-0 text-[13px] text-muted">{t(`crisi.decisions.${kind}None`)}</p>}
      <ol className="m-0 flex list-none flex-col gap-1.5 p-0">
        {items.map((e) => (
          <li key={e.id} className="flex flex-col text-[13px]">
            <span className="font-mono text-[11.5px] text-muted tabular">{dateTime(e.at)}{e.created_by ? ` · ${e.created_by}` : ""}</span>
            <span className="whitespace-pre-wrap">{e.text}</span>
          </li>
        ))}
      </ol>
    </div>
  );
  return (
    <section aria-label={t("crisi.decisions.title")} className="flex flex-col gap-3">
      <Heading id="decisioni">{t("crisi.decisions.title")}</Heading>
      {block("decision", x.decisions)}
      {block("communication", x.communications)}
    </section>
  );
}

// ---------------------------------------------------------------------
// Bozza della notifica

function fieldValue(f: DraftField): string {
  if (f.missing) return "";
  switch (f.kind) {
    case "date":
      return fmtDateTime(f.value);
    case "minutes":
      return fmtDuration(Number(f.value));
    case "money":
      return fmtCurrency(Number(f.value) / 100);
    case "number":
      return fmtNumber(Number(f.value));
    default:
      return f.value;
  }
}

/** Il testo della bozza dai campi: «etichetta: valore», una riga per campo compilato. */
function composeDraft(t: T, fields: DraftField[]): string {
  return fields
    .filter((f) => !f.missing)
    .map((f) => {
      const v = fieldValue(f);
      const label = t(`crisi.draft.field.${f.key as "incident"}`);
      return v.includes("\n") ? `${label}:\n${v.split("\n").map((l) => `- ${l}`).join("\n")}` : `${label}: ${v}`;
    })
    .join("\n");
}

function Draft({ x, refKey, manage, open, onOpen }: { x: CrisisView; refKey: string; manage: boolean; open: boolean; onOpen: (v: boolean) => void }) {
  const t = useT();
  const refresh = useRefresh(refKey);
  const n = x.draft.notification;
  const [content, setContent] = useState<string | null>(null);
  const [ref, setRef] = useState("");
  const [channel, setChannel] = useState("");
  const [at, setAt] = useState("");
  const text = content ?? (n?.content || composeDraft(t, x.draft.fields));
  const save = useMutation({ mutationFn: () => riskApi.saveDraft(n!.id, text), onSuccess: refresh });
  const submit = useMutation({
    mutationFn: () => riskApi.submit(n!.id, { content: text, reference: ref, channel, submitted_at: fromLocalInput(at) }),
    onSuccess: () => {
      refresh();
      onOpen(false);
      setContent(null);
    },
  });
  const required = x.draft.fields.filter((f) => f.required);
  const others = x.draft.fields.filter((f) => !f.required);
  const fieldRow = (f: DraftField) => (
    <div key={f.key} className={`grid grid-cols-[120px_minmax(0,1fr)] gap-x-3 border-l-2 py-1 pl-2 text-[13px] ${f.missing && f.required ? "border-flag bg-sel" : "border-transparent"}`} data-missing={f.missing ? "true" : undefined}>
      <span className="label text-muted">{t(`crisi.draft.field.${f.key as "incident"}`)}</span>
      <span className="min-w-0 whitespace-pre-wrap">
        {f.missing ? <span className={f.required ? "font-semibold text-flag" : "text-muted"}>{f.required ? t("crisi.draft.missing") : t("crisi.draft.optional")}</span> : fieldValue(f)}
      </span>
    </div>
  );
  return (
    <section aria-label={t("crisi.draft.title")} className="flex flex-col gap-2">
      <Heading id="bozza">{t("crisi.draft.title")}</Heading>
      {n ? (
        <p className="m-0 text-[13px]">
          <span className="font-semibold">{n.scheme_name.split(" · ")[0]} · {n.stage_name}</span>
          {n.due_at && <span className="text-muted"> · {t("crisi.clock.due", { when: dateTime(n.due_at) })}</span>}
        </p>
      ) : (
        <p className="m-0 text-[13px] text-muted">{t("crisi.draft.noNotification")}</p>
      )}
      <p className={`m-0 text-[12.5px] ${x.draft.missing > 0 ? "font-semibold text-flag" : "text-ok"}`}>
        {x.draft.missing > 0 ? t("crisi.draft.missingCount", { count: x.draft.missing }) : t("crisi.draft.complete")}
      </p>
      <div className="flex flex-col">{required.map(fieldRow)}</div>
      <details className="text-[13px]">
        <summary className="min-h-[24px] cursor-pointer text-muted">{t("crisi.draft.more", { count: others.length })}</summary>
        <div className="flex flex-col">{others.map(fieldRow)}</div>
      </details>
      {n && x.draft.missing > 0 && manage && (
        <Link to="/e/$key" params={{ key: x.incident.key }} search={{ scheda: "dettagli" }} className="self-start text-[12.5px]">{t("crisi.draft.fill")}</Link>
      )}
      {n && manage && !open && (
        <button type="button" className="btn btn-primary min-h-[44px] justify-center" onClick={() => onOpen(true)}>
          {n.status === "draft" ? t("crisi.draft.resume") : t("crisi.draft.prepare")}
        </button>
      )}
      {n && manage && open && (
        <div className="flex flex-col gap-2">
          <label className="flex flex-col gap-1">
            <span className="label">{t("crisi.draft.text")}</span>
            <textarea className="field min-h-40 font-mono text-[12.5px]" value={text} onChange={(e) => setContent(e.target.value)} />
          </label>
          <div className="flex flex-wrap gap-2">
            <button type="button" className="btn min-h-[44px]" disabled={save.isPending} onClick={() => save.mutate()}>{t("crisi.draft.save")}</button>
            <button type="button" className="btn min-h-[44px]" onClick={() => setContent(composeDraft(t, x.draft.fields))}>{t("crisi.draft.regenerate")}</button>
            <button type="button" className="btn min-h-[44px]" onClick={() => onOpen(false)}>{t("crisi.draft.close")}</button>
          </div>
          <fieldset className="m-0 flex flex-col gap-2 border-0 p-0">
            <legend className="label mb-1">{t("crisi.draft.submitTitle")}</legend>
            <input className="field" type="datetime-local" aria-label={t("crisi.draft.sentAt")} value={at} onChange={(e) => setAt(e.target.value)} />
            <input className="field" placeholder={t("crisi.draft.reference")} aria-label={t("crisi.draft.reference")} value={ref} onChange={(e) => setRef(e.target.value)} />
            <input className="field" placeholder={t("crisi.draft.channel")} aria-label={t("crisi.draft.channel")} value={channel} onChange={(e) => setChannel(e.target.value)} />
            <button type="button" className="btn btn-primary min-h-[44px] justify-center" disabled={submit.isPending} onClick={() => submit.mutate()}>{t("crisi.draft.submit")}</button>
          </fieldset>
          <ErrorNote error={save.error ?? submit.error} />
        </div>
      )}
      {n?.status === "submitted" && <p className="m-0 text-[13px] text-ok">{t("crisi.draft.sent")}</p>}
    </section>
  );
}

// ---------------------------------------------------------------------
// Cronologia unica

function Timeline({ items }: { items: TimelineItem[] }) {
  const t = useT();
  return (
    <section aria-label={t("crisi.timeline.title")} className="flex flex-col gap-2">
      <Heading id="cronologia">{t("crisi.timeline.title")}</Heading>
      <ol className="m-0 flex list-none flex-col p-0">
        {items.map((it, i) => (
          <li
            key={`${it.kind}-${it.at}-${i}`}
            className={`grid grid-cols-[96px_minmax(0,1fr)] gap-x-3 border-b border-hair py-1.5 text-[13px] sm:grid-cols-[130px_120px_minmax(0,1fr)] ${it.future ? "border-dashed text-muted" : ""}`}
            data-future={it.future ? "true" : undefined}
          >
            <span className="font-mono text-[12px] text-muted tabular">{dateTime(it.at)}</span>
            <span className={`text-[12px] ${it.kind === "deadline" || it.kind === "notification" ? "font-semibold text-flag" : "text-muted"}`}>{kindLabel(t, it)}</span>
            <span className="col-span-2 min-w-0 sm:col-span-1">{lineText(it)}</span>
          </li>
        ))}
      </ol>
    </section>
  );
}

function kindLabel(t: T, it: TimelineItem): string {
  if (it.kind === "release") return t("crisi.timeline.release");
  if (it.kind === "cve") return t("crisi.timeline.cve");
  if (it.kind === "deadline") return it.future ? t("crisi.timeline.deadlineFuture") : t("crisi.timeline.deadlinePast");
  return EVENT_KINDS[it.kind] ?? it.kind;
}

function lineText(it: TimelineItem): string {
  if (it.kind === "release") return `${it.title} · ${it.detail}`;
  if (it.kind === "cve") return `${it.title} · ${it.detail}`;
  if (it.kind === "deadline") return `${it.detail.split(" · ")[0]} · ${it.title}`;
  return it.detail;
}
