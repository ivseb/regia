// «Da fare» (ondata 2, proposta 1): una sola pagina con ciò che aspetta la persona. Sezioni: decisioni,
// valutazioni, verifiche e requisiti, impatti del mio team, timesheet e report, menzioni, e la parte
// «Oggi» con il lavoro assegnato, il timer per elemento e il totale del giorno. Si progetta prima per
// il telefono (375 px): ogni riga è una carta e i pulsanti sono da 44 px. I numeri delle righe vengono
// dalle stesse fonti delle altre pagine (graduatoria, valutazioni, requisiti): nessun calcolo qui.
import { useStateName } from "../stateLabels";
import { useEffect, useMemo, useRef, useState, type FormEvent, type ReactNode } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, collab } from "../api";
import { PageHeader, useMe } from "../shell";
import { ModuleChip, type UiModule } from "../moduleIcon";
import { ElementTitle, ErrorNote, Loading, ago, euro } from "../ui";
import { useT } from "../i18n/react";
import { fmtDate, fmtDateTime, fmtNumber } from "../i18n/format";
import {
  dafareApi,
  type Decision,
  type Todo,
  type TodoDecision,
  type TodoImpact,
  type TodoReport,
  type TodoReview,
  type TodoVerify,
  type TodoWeek,
} from "../dafareApi";
import { Describe } from "./Notifications";
import { tempoApi, isoDay, mondayOf, totalText, weekLabel } from "../tempoApi";
import { fmtMinutes, workApi, type MyItem } from "../workApi";
import { useTypeNames } from "../typesApi";

const SECTION_ID = {
  decisions: "da-fare-decisioni",
  reviews: "da-fare-valutazioni",
  verify: "da-fare-verifiche",
  impacts: "da-fare-impatti",
  time: "da-fare-tempo",
  mentions: "da-fare-menzioni",
  today: "da-fare-oggi",
} as const;

/** Dopo un'azione si ricaricano la pagina, i numeri del menu e le code collegate. */
function useRefreshTodo() {
  const qc = useQueryClient();
  return () => {
    for (const k of [["todo"], ["inbox"], ["reviews"], ["requirements"], ["tempo"], ["notifications"], ["work"]]) qc.invalidateQueries({ queryKey: k });
  };
}

export function DaFare() {
  const t = useT();
  const todo = useQuery({ queryKey: ["todo"], queryFn: dafareApi.todo });
  const data = todo.data;
  const total = data?.counts.total ?? 0;
  const mine = useQuery({ queryKey: ["work", "me"], queryFn: workApi.mine });
  const todayItems = useMemo(() => openAssigned(mine.data?.items ?? []), [mine.data]);
  const [msg, setMsg] = useState("");

  return (
    <>
      <PageHeader
        path={t("dafare.path")}
        title={t("dafare.title")}
        actions={
          <Link to="/notifiche" className="btn no-underline">
            {t("dafare.activity")}
          </Link>
        }
      />
      <div className="flex max-w-5xl flex-col gap-8 px-4 pb-12 md:px-7">
        <p className="m-0 text-muted" aria-live="polite">
          {todo.isPending ? t("dafare.loading") : total > 0 ? t("dafare.summary", { count: total }) : ""}
          {msg && <span className="ml-3 font-semibold text-ok">{msg}</span>}
        </p>
        <ErrorNote error={todo.error} />
        {todo.isPending && <Loading className="text-muted" />}
        {data && (
          <>
            {total > 0 && <JumpBar data={data} hasToday={todayItems.length > 0} />}
            {total === 0 && <EmptyDone data={data} />}
            {data.counts.more && <p className="m-0 text-[12.5px] text-muted">{t("dafare.more")}</p>}
            <Decisions items={data.decisions} onDone={setMsg} />
            <Reviews items={data.reviews} />
            <Verifications items={data.verifications} />
            <Impacts items={data.impacts} />
            <TimeAndReports weeks={data.time} reports={data.reports} />
            <Mentions data={data} />
          </>
        )}
        <Today items={todayItems} loading={mine.isPending} />
      </div>
    </>
  );
}

const WORK_TYPES = ["epic", "story", "task", "bug"];
function openAssigned(items: MyItem[]): MyItem[] {
  return items
    .filter((i) => i.category !== "done" && i.category !== "cancelled")
    .sort((a, b) => (a.due ?? "9999").localeCompare(b.due ?? "9999") || a.key.localeCompare(b.key));
}

// ---------------------------------------------------------------------
// Struttura

function JumpBar({ data, hasToday }: { data: Todo; hasToday: boolean }) {
  const t = useT();
  const c = data.counts;
  const items: { id: string; label: string; n: number; m: UiModule }[] = [
    { id: SECTION_ID.decisions, label: t("dafare.sec.decisions"), n: c.decisions, m: "demand" as UiModule },
    { id: SECTION_ID.reviews, label: t("dafare.sec.reviews"), n: c.reviews, m: "arch" as UiModule },
    { id: SECTION_ID.verify, label: t("dafare.sec.verify"), n: c.verifications, m: "grc" as UiModule },
    { id: SECTION_ID.impacts, label: t("dafare.sec.impacts"), n: c.impacts, m: "arch" as UiModule },
    { id: SECTION_ID.time, label: t("dafare.sec.time"), n: c.time + c.reports, m: "work" as UiModule },
    { id: SECTION_ID.mentions, label: t("dafare.sec.mentions"), n: c.mentions, m: "demand" as UiModule },
  ].filter((x) => x.n > 0);
  if (hasToday) items.push({ id: SECTION_ID.today, label: t("dafare.sec.today"), n: 0, m: "work" as UiModule });
  return (
    <nav aria-label={t("dafare.jump")} className="flex flex-wrap gap-2">
      {items.map((x) => (
        <a key={x.id} href={`#${x.id}`} className={`m-${x.m} tinta inline-flex min-h-11 items-center gap-1.5 border border-hair border-l-[3px] border-l-mc px-3 text-fg no-underline hover:border-rule hover:border-l-mc md:min-h-0 md:py-1`}>
          {x.label}
          {x.n > 0 && <span className="font-mono text-[11.5px] text-muted tabular">{x.n}</span>}
        </a>
      ))}
    </nav>
  );
}

function Section({ id, title, count, hint, module, children }: { id: string; title: string; count: number; hint?: string; module?: UiModule; children: ReactNode }) {
  if (count === 0) return null;
  return (
    <section id={id} aria-labelledby={`${id}-t`} className="flex scroll-mt-16 flex-col gap-2">
      <h2 id={`${id}-t`} className="m-0 flex items-baseline gap-2 border-b-2 border-rule pb-1.5 text-[17px] font-bold">
        {module && <ModuleChip module={module} size="sm" className="self-center" />}
        {title}
        <span className="font-mono text-[12px] font-normal text-muted tabular">{count}</span>
      </h2>
      {hint && <p className="m-0 text-[12.5px] text-muted">{hint}</p>}
      <ul className="m-0 flex list-none flex-col p-0">{children}</ul>
    </section>
  );
}

function Card({ children, label, className = "" }: { children: ReactNode; label?: string; className?: string }) {
  return (
    <li aria-label={label} className={`flex flex-col gap-2.5 border-b border-hair py-3.5 ${className}`}>
      {children}
    </li>
  );
}

function Chip({ children, tone = "" }: { children: ReactNode; tone?: "" | "flag" | "danger" | "ok" }) {
  const cls = tone === "flag" ? "border-flag text-flag" : tone === "danger" ? "border-danger text-danger" : tone === "ok" ? "border-ok text-ok" : "border-hair text-muted";
  return <span className={`inline-flex items-center border px-1.5 py-px text-[12px] tabular ${cls}`}>{children}</span>;
}

function EmptyDone({ data }: { data: Todo }) {
  const t = useT();
  const m = data.next_meeting;
  return (
    <section aria-label={t("dafare.empty.title")} className="flex max-w-2xl flex-col gap-2 border-l-2 border-ok pl-4">
      <h2 className="m-0 text-[22px] font-bold">{t("dafare.empty.title")}</h2>
      <p className="m-0">
        {m ? t("dafare.empty.next", { body: m.body, when: fmtDateTime(m.at, { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" }) }) : t("dafare.empty.noMeeting")}
      </p>
      <p className="m-0 flex flex-wrap gap-x-4 gap-y-1 text-[12.5px]">
        <Link to="/notifiche" className="text-accent underline">{t("dafare.empty.activity")}</Link>
        <Link to="/governance" className="text-accent underline">{t("dafare.empty.governance")}</Link>
      </p>
    </section>
  );
}

// ---------------------------------------------------------------------
// Decisioni

const DECISIONS: { key: Decision; label: "dafare.decision.approved" | "dafare.decision.conditions" | "dafare.decision.deferred" | "dafare.decision.rejected"; quiet?: boolean }[] = [
  { key: "approved", label: "dafare.decision.approved" },
  { key: "approved_with_conditions", label: "dafare.decision.conditions" },
  { key: "deferred", label: "dafare.decision.deferred", quiet: true },
  { key: "rejected", label: "dafare.decision.rejected", quiet: true },
];

function Decisions({ items, onDone }: { items: TodoDecision[]; onDone: (m: string) => void }) {
  const t = useT();
  return (
    <Section module="demand" id={SECTION_ID.decisions} title={t("dafare.sec.decisions")} count={items.length} hint={t("dafare.hint.decisions")}>
      {items.map((d) => (
        <DecisionCard key={d.step_id} d={d} onDone={onDone} />
      ))}
    </Section>
  );
}

function DecisionCard({ d, onDone }: { d: TodoDecision; onDone: (m: string) => void }) {
  const t = useT();
  const refresh = useRefreshTodo();
  const [choice, setChoice] = useState<Decision | null>(null);
  const [text, setText] = useState("");
  const input = useRef<HTMLInputElement>(null);
  const decide = useMutation({
    mutationFn: ({ decision, note }: { decision: Decision; note: string }) => dafareApi.decide(d.step_id, decision, note),
    onSuccess: (_r, v) => {
      onDone(t("dafare.decision.done", { key: d.element_key, decision: t(`dafare.decision.result.${v.decision}` as const) }));
      setChoice(null);
      setText("");
      refresh();
    },
  });
  useEffect(() => {
    if (choice) input.current?.focus();
  }, [choice]);
  const needsText = choice !== null && choice !== "approved";
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (!choice || decide.isPending) return;
    if (needsText && !text.trim()) return;
    decide.mutate({ decision: choice, note: text.trim() });
  };
  const noteLabel = choice === "approved_with_conditions" ? t("dafare.note.conditions") : choice === "approved" ? t("dafare.note.optional") : t("dafare.note.reason");
  const cum = d.cumulative_cents;
  return (
    <Card label={d.element_key} className="m-demand solleva px-3">
      <div className="flex flex-col gap-0.5">
        <span className="flex flex-wrap items-baseline gap-x-2.5 gap-y-0.5">
          <Link to="/e/$key" params={{ key: d.element_key }} search={{ scheda: "approvazioni" }} className="font-mono text-[12.5px] font-medium text-accent no-underline hover:underline">
            {d.element_key}
          </Link>
          <Link to="/e/$key" params={{ key: d.element_key }} search={{ scheda: "approvazioni" }} className="touch-44 inline-flex items-center text-[16px] font-semibold text-fg no-underline hover:underline">
            {d.title}
          </Link>
        </span>
        <span className="text-[12.5px] text-muted">
          {t("dafare.decision.stage", { stage: d.stage })}
          {d.body ? ` · ${t("dafare.decision.body", { body: d.body })}` : ""} · {t("dafare.decision.waiting", { ago: ago(d.requested_at) })}
        </span>
      </div>
      <div className="flex flex-wrap gap-1.5">
        {d.amount_cents != null && <Chip>{t("dafare.decision.amount", { amount: euro(d.amount_cents) })}</Chip>}
        {d.score != null && <Chip>{t("dafare.decision.score", { score: fmtNumber(d.score, { maximumFractionDigits: 1 }) })}</Chip>}
        {d.position != null && <Chip>{t("dafare.decision.position", { pos: d.position, total: d.rank_total })}</Chip>}
        {d.mandatory && <Chip tone="flag">{t("dafare.decision.mandatory")}</Chip>}
        {d.funded === false && cum != null && d.budget_cents != null && (
          <Chip tone="danger">{t("dafare.decision.unfunded", { cum: euro(cum), budget: euro(d.budget_cents) })}</Chip>
        )}
        {d.funded === true && cum != null && d.budget_cents != null && <Chip tone="ok">{t("dafare.decision.funded", { cum: euro(cum), budget: euro(d.budget_cents) })}</Chip>}
        {d.reviews_mandatory_open > 0 && <Chip tone="flag">{t("dafare.decision.reviewsOpen", { count: d.reviews_mandatory_open })}</Chip>}
        {d.requirements_to_accept > 0 && <Chip tone="flag">{t("dafare.decision.requirementsOpen", { count: d.requirements_to_accept })}</Chip>}
      </div>
      {d.inline_decision ? (
        <>
          <div role="group" aria-label={t("dafare.decision.group", { key: d.element_key })} className="flex flex-wrap gap-2">
            {DECISIONS.map((x) => (
              <button
                key={x.key}
                type="button"
                aria-pressed={choice === x.key}
                className={`btn ${x.quiet ? "btn-quiet" : ""} ${choice === x.key ? "btn-primary" : ""}`}
                onClick={() => setChoice(choice === x.key ? null : x.key)}
              >
                {t(x.label)}
              </button>
            ))}
          </div>
          {choice && (
            <form onSubmit={submit} className="flex flex-wrap items-center gap-2">
              <label className="flex min-w-0 flex-[1_1_260px] flex-col gap-1">
                <span className="label text-muted">{noteLabel}</span>
                <input
                  ref={input}
                  className="field"
                  value={text}
                  maxLength={2000}
                  required={needsText}
                  onChange={(e) => setText(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === "Escape") setChoice(null);
                  }}
                />
              </label>
              <span className="flex items-end gap-2 self-end">
                <button className="btn btn-primary" disabled={decide.isPending || (needsText && !text.trim())}>
                  {decide.isPending ? t("dafare.decision.sending") : t("dafare.decision.send")}
                </button>
                <button type="button" className="btn btn-quiet" onClick={() => setChoice(null)}>
                  {t("dafare.cancel")}
                </button>
              </span>
            </form>
          )}
          <ErrorNote error={decide.error} />
        </>
      ) : (
        <div className="flex flex-col items-start gap-1.5">
          <p className="m-0 text-[12.5px] text-muted">{t("dafare.decision.packageFirst")}</p>
          <Link to="/e/$key" params={{ key: d.element_key }} search={{ scheda: "approvazioni" }} className="btn btn-primary no-underline">
            {t("dafare.decision.openPackage")}
          </Link>
        </div>
      )}
    </Card>
  );
}

// ---------------------------------------------------------------------
// Valutazioni e verifiche

function Reviews({ items }: { items: TodoReview[] }) {
  const t = useT();
  return (
    <Section module="arch" id={SECTION_ID.reviews} title={t("dafare.sec.reviews")} count={items.length} hint={t("dafare.hint.reviews")}>
      {items.map((r) => (
        <Card key={`${r.role}-${r.id}`} label={r.key}>
          <div className="flex flex-col gap-0.5">
            <span className="flex flex-wrap items-baseline gap-x-2.5">
              <Link to="/valutazioni/$key" params={{ key: r.key }} className="font-mono text-[12.5px] font-medium text-accent no-underline hover:underline">{r.key}</Link>
              <Link to="/valutazioni/$key" params={{ key: r.key }} className="touch-44 inline-flex items-center text-[16px] font-semibold text-fg no-underline hover:underline">
                {r.type_name}
                {r.application ? ` · ${r.application}` : ""}
              </Link>
            </span>
            <span className="text-[12.5px] text-muted">
              {r.request_key} {r.request_title} · {t("dafare.review.team", { team: r.team })}
            </span>
          </div>
          <div className="flex flex-wrap gap-1.5">
            <Chip>{r.role === "compile" ? t("dafare.review.compile") : t("dafare.review.exam")}</Chip>
            {r.mandatory && <Chip tone="flag">{t("dafare.review.mandatory")}</Chip>}
            {r.required_missing > 0 && <Chip tone="flag">{t("dafare.review.missing", { count: r.required_missing })}</Chip>}
            {r.due_on && <Chip tone={r.late_days > 0 ? "danger" : ""}>{t("dafare.review.due", { date: fmtDate(r.due_on) })}</Chip>}
            {r.late_days > 0 && <Chip tone="danger">{t("dafare.review.late", { count: r.late_days })}</Chip>}
          </div>
          <Link to="/valutazioni/$key" params={{ key: r.key }} className="btn self-start no-underline">
            {r.role === "compile" ? t("dafare.review.openCompile") : t("dafare.review.openExam")}
          </Link>
        </Card>
      ))}
    </Section>
  );
}

function Verifications({ items }: { items: TodoVerify[] }) {
  const t = useT();
  return (
    <Section module="grc" id={SECTION_ID.verify} title={t("dafare.sec.verify")} count={items.length} hint={t("dafare.hint.verify")}>
      {items.map((v) => (
        <Card key={v.requirement_id + v.impact_key} label={v.requirement_key}>
          <div className="flex flex-col gap-0.5">
            <span className="flex flex-wrap items-baseline gap-x-2.5">
              <Link to="/e/$key" params={{ key: v.requirement_key }} search={{ scheda: "dettagli" }} className="font-mono text-[12.5px] font-medium text-accent no-underline hover:underline">{v.requirement_key}</Link>
              <Link to="/e/$key" params={{ key: v.requirement_key }} search={{ scheda: "dettagli" }} className="touch-44 inline-flex items-center text-[16px] font-semibold text-fg no-underline hover:underline">
                {v.title}
              </Link>
            </span>
            <span className="text-[12.5px] text-muted">
              {v.application} <span className="font-mono">{v.impact_key}</span> · {v.request_key} {v.request_title}
            </span>
          </div>
          <div className="flex flex-wrap gap-1.5">
            {v.category && <Chip>{v.category}</Chip>}
            {v.team && <Chip>{t("dafare.review.team", { team: v.team })}</Chip>}
            {v.implemented_by && <Chip>{t("dafare.verify.by", { name: v.implemented_by })}</Chip>}
            <Chip>{t("dafare.verify.since", { ago: ago(v.since) })}</Chip>
          </div>
          <Link to="/e/$key" params={{ key: v.requirement_key }} search={{ scheda: "dettagli" }} className="btn self-start no-underline">
            {t("dafare.verify.open")}
          </Link>
        </Card>
      ))}
    </Section>
  );
}

// ---------------------------------------------------------------------
// Impatti del mio team

function Impacts({ items }: { items: TodoImpact[] }) {
  const t = useT();
  return (
    <Section module="arch" id={SECTION_ID.impacts} title={t("dafare.sec.impacts")} count={items.length} hint={t("dafare.hint.impacts")}>
      {items.map((i) => (
        <ImpactCard key={i.id} i={i} />
      ))}
    </Section>
  );
}

function ImpactCard({ i }: { i: TodoImpact }) {
  const t = useT();
  const stateName = useStateName();
  const refresh = useRefreshTodo();
  const [mode, setMode] = useState<null | "estimate" | "reject">(null);
  const [text, setText] = useState(i.effort_days != null ? String(i.effort_days).replace(".", ",") : "");
  const accept = i.transitions.find((x) => x.forward);
  const reject = i.transitions.find((x) => !x.forward && x.comment_required);
  const blocked = accept?.blocked?.[0];
  const run = useMutation({
    mutationFn: async () => {
      if (mode === "estimate") {
        const days = parseFloat(text.replace(",", "."));
        await api.updateImpact(i.id, { estimate_effort_days: days });
      } else if (mode === "reject" && reject) {
        await api.transition(i.id, reject.to, text.trim());
      }
    },
    onSuccess: () => {
      setMode(null);
      refresh();
    },
  });
  const acceptNow = useMutation({ mutationFn: () => api.transition(i.id, accept!.to), onSuccess: refresh });
  const days = parseFloat(text.replace(",", "."));
  const valid = mode === "estimate" ? Number.isFinite(days) && days >= 0 : text.trim() !== "";
  const free = i.free_days;
  const over = free != null && i.effort_days != null && i.effort_days > free;
  return (
    <Card label={i.key}>
      <div className="flex flex-col gap-0.5">
        <span className="flex flex-wrap items-baseline gap-x-2.5">
          <Link to="/e/$key" params={{ key: i.key }} className="font-mono text-[12.5px] font-medium text-accent no-underline hover:underline">{i.key}</Link>
          <Link to="/e/$key" params={{ key: i.key }} className="touch-44 inline-flex items-center text-[16px] font-semibold text-fg no-underline hover:underline">
            {i.application}
          </Link>
        </span>
        <span className="text-[12.5px] text-muted">
          {i.request_key} {i.request_title}
          {i.team ? ` · ${t("dafare.review.team", { team: i.team })}` : ""}
        </span>
      </div>
      <div className="flex flex-wrap gap-1.5">
        <Chip>{t("dafare.impact.state", { state: stateName(i.state) })}</Chip>
        <Chip tone={i.effort_days == null ? "flag" : ""}>{i.effort_days == null ? t("dafare.impact.noEstimate") : t("dafare.impact.days", { count: i.effort_days, days: fmtNumber(i.effort_days, { maximumFractionDigits: 2 }) })}</Chip>
        {i.from && i.to && <Chip>{t("dafare.impact.period", { from: fmtDate(i.from), to: fmtDate(i.to) })}</Chip>}
        {free != null && i.capacity_days != null ? (
          <Chip tone={over ? "danger" : "ok"}>{t("dafare.impact.capacity", { free: fmtNumber(free, { maximumFractionDigits: 1 }), capacity: fmtNumber(i.capacity_days, { maximumFractionDigits: 1 }), weeks: t("dafare.impact.weeks", { count: i.weeks }) })}</Chip>
        ) : (
          <Chip>{t("dafare.impact.noCapacity")}</Chip>
        )}
      </div>
      {over && <p className="m-0 text-[12.5px] text-danger">{t("dafare.impact.over", { days: fmtNumber((i.effort_days ?? 0) - (free ?? 0), { maximumFractionDigits: 1 }) })}</p>}
      <div className="flex flex-wrap gap-2">
        {accept && (
          <button className="btn btn-primary" disabled={!!blocked || acceptNow.isPending} title={blocked} aria-describedby={blocked ? `blocco-${i.id}` : undefined} onClick={() => acceptNow.mutate()}>
            {t("dafare.impact.accept")}
          </button>
        )}
        {i.can_estimate && (
          <button className="btn" aria-pressed={mode === "estimate"} onClick={() => { setMode(mode === "estimate" ? null : "estimate"); setText(i.effort_days != null ? String(i.effort_days).replace(".", ",") : ""); }}>
            {t("dafare.impact.estimate")}
          </button>
        )}
        {reject && (
          <button className="btn btn-quiet" aria-pressed={mode === "reject"} onClick={() => { setMode(mode === "reject" ? null : "reject"); setText(""); }}>
            {t("dafare.impact.reject")}
          </button>
        )}
        {!accept && !reject && !i.can_estimate && (
          <Link to="/e/$key" params={{ key: i.key }} className="btn no-underline">{t("dafare.impact.open")}</Link>
        )}
      </div>
      {blocked && <p id={`blocco-${i.id}`} className="m-0 text-[12.5px] text-muted">{blocked}</p>}
      {mode && (
        <form
          className="flex flex-wrap items-end gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            if (valid && !run.isPending) run.mutate();
          }}
        >
          <label className="flex min-w-0 flex-[1_1_200px] flex-col gap-1">
            <span className="label text-muted">{mode === "estimate" ? t("dafare.impact.estimateLabel") : t("dafare.impact.reasonLabel")}</span>
            <input autoFocus className="field" inputMode={mode === "estimate" ? "decimal" : "text"} value={text} onChange={(e) => setText(e.target.value)} onKeyDown={(e) => e.key === "Escape" && setMode(null)} />
          </label>
          <button className="btn btn-primary" disabled={!valid || run.isPending}>
            {mode === "estimate" ? t("dafare.impact.saveEstimate") : t("dafare.decision.send")}
          </button>
          <button type="button" className="btn btn-quiet" onClick={() => setMode(null)}>{t("dafare.cancel")}</button>
        </form>
      )}
      <ErrorNote error={run.error ?? acceptNow.error} />
    </Card>
  );
}

// ---------------------------------------------------------------------
// Tempo e status report

function TimeAndReports({ weeks, reports }: { weeks: TodoWeek[]; reports: TodoReport[] }) {
  const t = useT();
  const refresh = useRefreshTodo();
  const approve = useMutation({ mutationFn: (w: TodoWeek) => tempoApi.decide(w.member_id!, w.week, true), onSuccess: refresh });
  const submit = useMutation({ mutationFn: (w: TodoWeek) => tempoApi.submit(w.week), onSuccess: refresh });
  return (
    <Section module="work" id={SECTION_ID.time} title={t("dafare.sec.time")} count={weeks.length + reports.length} hint={t("dafare.hint.time")}>
      {weeks.map((w) => (
        <Card key={`${w.kind}-${w.member_id ?? "me"}-${w.week}`} label={weekLabel(w.week)}>
          <div className="flex flex-col gap-0.5">
            <span className="text-[16px] font-semibold">
              {w.kind === "submit" ? t("dafare.time.submit", { week: weekLabel(w.week), time: totalText(w.minutes) }) : t("dafare.time.approve", { member: w.member ?? "", week: weekLabel(w.week), time: totalText(w.minutes) })}
            </span>
          </div>
          <div className="flex flex-wrap gap-2">
            {w.kind === "submit" ? (
              <>
                <button className="btn btn-primary" disabled={submit.isPending} onClick={() => submit.mutate(w)}>{t("dafare.time.sendButton")}</button>
                <Link to="/tempo" search={{ settimana: w.week }} className="btn no-underline">{t("dafare.time.open")}</Link>
              </>
            ) : (
              <>
                <button className="btn btn-primary" disabled={approve.isPending} onClick={() => approve.mutate(w)}>{t("dafare.time.approveButton")}</button>
                <Link to="/tempo" search={{ settimana: w.week, persona: w.member_id }} className="btn no-underline">{t("dafare.time.review")}</Link>
              </>
            )}
          </div>
        </Card>
      ))}
      {reports.map((r) => (
        <Card key={r.id} label={r.key}>
          <div className="flex flex-col gap-0.5">
            <span className="flex flex-wrap items-baseline gap-x-2.5">
              <Link to="/e/$key" params={{ key: r.key }} search={{ scheda: "stato" }} className="font-mono text-[12.5px] font-medium text-accent no-underline hover:underline">{r.key}</Link>
              <Link to="/e/$key" params={{ key: r.key }} search={{ scheda: "stato" }} className="touch-44 inline-flex items-center text-[16px] font-semibold text-fg no-underline hover:underline">
                {t("dafare.report.title", { title: r.title })}
              </Link>
            </span>
          </div>
          <div className="flex flex-wrap gap-1.5">
            <Chip tone={r.late ? "danger" : ""}>{r.late ? t("dafare.report.late", { date: fmtDate(r.due) }) : t("dafare.report.due", { date: fmtDate(r.due) })}</Chip>
            {r.draft && <Chip>{t("dafare.report.draft")}</Chip>}
          </div>
          <Link to="/e/$key" params={{ key: r.key }} search={{ scheda: "stato" }} className="btn self-start no-underline">{r.draft ? t("dafare.report.continue") : t("dafare.report.write")}</Link>
        </Card>
      ))}
      <li className="list-none"><ErrorNote error={approve.error ?? submit.error} /></li>
    </Section>
  );
}

// ---------------------------------------------------------------------
// Menzioni

function Mentions({ data }: { data: Todo }) {
  const t = useT();
  const refresh = useRefreshTodo();
  const read = useMutation({ mutationFn: (id: string) => collab.markRead(id), onSuccess: refresh });
  return (
    <Section id={SECTION_ID.mentions} title={t("dafare.sec.mentions")} count={data.mentions.length} hint={t("dafare.hint.mentions")}>
      {data.mentions.map((n) => {
        const excerpt = typeof n.payload.excerpt === "string" ? n.payload.excerpt : "";
        return (
          <Card key={n.id} label={n.element_key}>
            <div className="flex flex-col gap-0.5">
              <span><Describe n={n} /></span>
              {n.title && <span className="text-[12.5px] text-muted"><ElementTitle type={n.type} title={n.title} /></span>}
              {excerpt && <span className="min-w-0 text-[12.5px] break-words text-muted">«{excerpt}»</span>}
              <time dateTime={n.created_at} className="text-[12px] text-muted">{ago(n.created_at)}</time>
            </div>
            <div className="flex flex-wrap gap-2">
              {n.element_key && (
                <Link to="/e/$key" params={{ key: n.element_key }} hash="commenti" onClick={() => read.mutate(n.id)} className="btn btn-primary no-underline">
                  {t("dafare.mention.reply")}
                </Link>
              )}
              <button className="btn" disabled={read.isPending} onClick={() => read.mutate(n.id)}>{t("dafare.mention.done")}</button>
            </div>
          </Card>
        );
      })}
    </Section>
  );
}

// ---------------------------------------------------------------------
// Oggi: lavoro assegnato, timer per elemento, totale del giorno

type Timer = { key: string; title: string; startedAt: number };
const timerKey = (member: string) => `regia.timer.${member}`;

function readTimer(member: string): Timer | null {
  try {
    const v = JSON.parse(localStorage.getItem(timerKey(member)) ?? "null");
    return v && typeof v.key === "string" && typeof v.startedAt === "number" ? v : null;
  } catch {
    return null;
  }
}
function writeTimer(member: string, v: Timer | null) {
  try {
    if (v) localStorage.setItem(timerKey(member), JSON.stringify(v));
    else localStorage.removeItem(timerKey(member));
  } catch {
    // Archiviazione non disponibile: il timer vale finché la pagina resta aperta.
  }
}

function Today({ items, loading }: { items: MyItem[]; loading: boolean }) {
  const t = useT();
  const stateName = useStateName();
  const tn = useTypeNames();
  const qc = useQueryClient();
  const me = useMe();
  const member = me.data?.active_tenant?.member_id ?? "";
  const week = mondayOf();
  const today = isoDay(new Date());
  const ts = useQuery({ queryKey: ["tempo", "timesheet", "oggi", week], queryFn: () => tempoApi.timesheet(week), staleTime: 15_000 });
  const [timer, setTimer] = useState<Timer | null>(() => (member ? readTimer(member) : null));
  const [, tick] = useState(0);
  useEffect(() => {
    if (member) setTimer(readTimer(member));
  }, [member]);
  useEffect(() => {
    if (!timer) return;
    const id = window.setInterval(() => tick((n) => n + 1), 15_000);
    return () => window.clearInterval(id);
  }, [timer]);
  const [note, setNote] = useState("");
  const log = useMutation({
    mutationFn: async (tm: Timer) => {
      const minutes = Math.max(1, Math.round((Date.now() - tm.startedAt) / 60_000));
      await workApi.logTime(tm.key, { duration: `${minutes}m`, date: today });
      return { key: tm.key, minutes };
    },
    onSuccess: (r) => {
      setNote(t("dafare.today.logged", { time: fmtMinutes(r.minutes), key: r.key }));
      qc.invalidateQueries({ queryKey: ["tempo"] });
      qc.invalidateQueries({ queryKey: ["todo"] });
      qc.invalidateQueries({ queryKey: ["work"] });
    },
  });
  const stop = () => {
    if (!timer) return;
    const tm = timer;
    writeTimer(member, null);
    setTimer(null);
    log.mutate(tm);
  };
  const start = (it: MyItem) => {
    if (timer) stop();
    const tm = { key: it.key, title: it.title, startedAt: Date.now() };
    writeTimer(member, tm);
    setTimer(tm);
    setNote("");
  };
  const submit = useMutation({
    mutationFn: () => tempoApi.submit(week),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["tempo"] });
      qc.invalidateQueries({ queryKey: ["todo"] });
    },
  });
  const sheet = ts.data;
  const day = sheet?.days.find((d) => d.date === today);
  const elapsed = timer ? Math.max(0, Math.round((Date.now() - timer.startedAt) / 60_000)) : 0;
  const canSubmit = !!sheet && sheet.settings.require_approval && (sheet.status === "draft" || sheet.status === "rejected") && sheet.total > 0 && sheet.editable;
  const shown = items.slice(0, 30);
  if (!loading && items.length === 0 && !timer && !sheet?.total) return null;
  return (
    <section id={SECTION_ID.today} aria-labelledby={`${SECTION_ID.today}-t`} className="flex scroll-mt-16 flex-col gap-3">
      <h2 id={`${SECTION_ID.today}-t`} className="m-0 flex items-center gap-2 border-b-2 border-rule pb-1.5 text-[17px] font-bold"><ModuleChip module="work" size="sm" />{t("dafare.sec.today")}</h2>
      <div className="flex flex-wrap items-center gap-x-5 gap-y-2" aria-live="polite">
        <p className="m-0 text-[16px] font-semibold tabular" data-testid="today-total">
          {day ? t("dafare.today.total", { time: totalText(day.total), capacity: totalText(day.capacity) }) : t("dafare.today.totalNone")}
        </p>
        {canSubmit && (
          <button className="btn" disabled={submit.isPending} onClick={() => submit.mutate()}>{t("dafare.today.submit")}</button>
        )}
        {sheet && sheet.status === "submitted" && <span className="text-[12.5px] text-muted">{t("dafare.today.submitted")}</span>}
        {note && <span className="text-[12.5px] text-ok">{note}</span>}
      </div>
      <ErrorNote error={log.error ?? submit.error} />
      {timer && (
        <div className="flex flex-wrap items-center gap-3 border border-accent px-3 py-2" role="status">
          <span className="min-w-0 flex-1 break-words">
            <span className="font-mono text-[12.5px] text-accent">{timer.key}</span> {timer.title}
          </span>
          <span className="font-mono tabular">{t("dafare.today.running", { time: fmtMinutes(elapsed) })}</span>
          <button className="btn btn-primary" onClick={stop}>{t("dafare.today.stop")}</button>
        </div>
      )}
      {loading && <Loading className="text-muted" rows={2} />}
      {!loading && items.length === 0 && <p className="m-0 text-muted">{t("dafare.today.none")}</p>}
      <ul className="m-0 flex list-none flex-col p-0">
        {shown.map((it) => {
          const running = timer?.key === it.key;
          const loggable = WORK_TYPES.includes(it.type);
          const overdue = it.due != null && it.due < today;
          return (
            <li key={it.id} className="flex min-h-11 flex-wrap items-center gap-x-3 gap-y-1 border-b border-hair py-2">
              <Link to="/e/$key" params={{ key: it.key }} className="shrink-0 font-mono text-[12px] text-accent no-underline hover:underline">{it.key}</Link>
              <span className="min-w-0 flex-[1_1_220px] break-words"><ElementTitle type={it.type} title={it.title} /></span>
              <span className="flex shrink-0 flex-wrap items-center gap-2.5 text-[12px] text-muted">
                <span>{tn.name(it.type, it.type_name)}</span>
                <span>{stateName(it.state)}</span>
                {it.due && <span className={`tabular ${overdue ? "font-semibold text-danger" : ""}`}>{overdue ? t("dafare.today.overdue", { date: fmtDate(it.due) }) : t("dafare.today.due", { date: fmtDate(it.due) })}</span>}
                {loggable && (
                  <button
                    className={`btn py-1 ${running ? "btn-primary" : ""}`}
                    aria-label={running ? t("dafare.today.stopAria", { key: it.key }) : t("dafare.today.startAria", { key: it.key })}
                    onClick={() => (running ? stop() : start(it))}
                  >
                    {running ? t("dafare.today.stop") : t("dafare.today.start")}
                  </button>
                )}
              </span>
            </li>
          );
        })}
      </ul>
      {items.length > shown.length && (
        <Link to="/delivery/mio-lavoro" className="self-start text-[12.5px] text-accent underline">{t("dafare.today.all", { count: items.length })}</Link>
      )}
    </section>
  );
}
