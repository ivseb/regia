// Spazio dell'auditor (ondata 3, C6): i pezzi che servono sia alla home dell'auditor sia alla scheda
// del controllo: richieste di evidenza con la risposta, rilievi con piano e chiusura, campioni con
// l'esito di ogni elemento, commenti in linea. Telefono di primo livello: bersagli da 44 px.
import { useState, type ReactNode } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Select } from "../select";
import { ErrorNote, SectionTitle, date, Loading } from "../ui";
import { auditApi, type AuditComment, type AuditRequest, type Finding, type Outcome, type Sample, type Severity } from "../auditApi";
import { tr } from "../i18n/core";

export const severityName = (s: Severity | string): string => {
  switch (s) {
    case "critical": return tr("governance.auditor.sev_critical");
    case "high": return tr("governance.auditor.sev_high");
    case "medium": return tr("governance.auditor.sev_medium");
    default: return tr("governance.auditor.sev_low");
  }
};

export const outcomeName = (o: Outcome | string): string => {
  switch (o) {
    case "compliant": return tr("governance.auditor.out_compliant");
    case "non_compliant": return tr("governance.auditor.out_non_compliant");
    case "to_clarify": return tr("governance.auditor.out_to_clarify");
    default: return tr("governance.auditor.out_pending");
  }
};

const findingStatus = (s: string): string =>
  s === "closed" ? tr("governance.auditor.fs_closed") : s === "plan_proposed" ? tr("governance.auditor.fs_plan") : tr("governance.auditor.fs_open");

const requestStatus = (s: string): string =>
  s === "closed" ? tr("governance.auditor.rs_closed") : s === "answered" ? tr("governance.auditor.rs_answered") : tr("governance.auditor.rs_open");

/** Rinfresca tutto ciò che dipende dall'audit (home, scheda del controllo, notifiche). */
export function useRefreshAudit() {
  const qc = useQueryClient();
  return () => {
    qc.invalidateQueries({ queryKey: ["grc", "audit"] });
    qc.invalidateQueries({ queryKey: ["notifications"] });
  };
}

function Tag({ tone, children }: { tone?: "danger" | "ok" | "warn"; children: ReactNode }) {
  const cls = tone === "danger" ? "border-danger text-danger" : tone === "ok" ? "border-ok text-ok" : tone === "warn" ? "border-flag text-flag" : "border-hair text-muted";
  return <span className={`inline-flex items-center border px-1.5 py-0.5 text-[11.5px] font-semibold ${cls}`}>{children}</span>;
}

export function ControlLink({ k, title }: { k: string; title?: string }) {
  return (
    <Link to="/e/$key" params={{ key: k }} className="touch-44 inline-flex items-center gap-1.5 text-accent underline">
      <span className="font-mono text-[12px]">{k}</span>
      {title && <span className="no-underline-offset-2">{title}</span>}
    </Link>
  );
}

// ---------------------------------------------------------------------
// Richieste di evidenza

export function RequestItem({ r, showControl = true }: { r: AuditRequest; showControl?: boolean }) {
  const refresh = useRefreshAudit();
  const [mode, setMode] = useState<"" | "answer">("");
  const [response, setResponse] = useState("");
  const [url, setUrl] = useState("");
  const [evidence, setEvidence] = useState("");
  const answer = useMutation({
    mutationFn: () => auditApi.answer(r.id, { response, url, evidence }),
    onSuccess: () => {
      setMode("");
      refresh();
    },
  });
  const close = useMutation({ mutationFn: () => auditApi.closeRequest(r.id), onSuccess: refresh });
  return (
    <li className="flex flex-col gap-1.5 border-b border-hair py-3" data-testid="audit-request">
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
        <span className="font-semibold">{r.title}</span>
        <Tag tone={r.status === "open" ? (r.overdue ? "danger" : "warn") : "ok"}>{requestStatus(r.status)}</Tag>
        {r.overdue && <Tag tone="danger">{tr("governance.auditor.overdue")}</Tag>}
      </div>
      <div className="flex flex-wrap items-center gap-x-3 text-[12.5px] text-muted">
        {showControl && <ControlLink k={r.control.key} />}
        <span>{tr("governance.auditor.req_to", { name: r.assignee || "—" })}</span>
        {r.due && <span>{tr("governance.auditor.req_due", { date: date(r.due) })}</span>}
      </div>
      {r.evidence && <p className="m-0 text-[12.5px] text-muted">{tr("governance.auditor.req_about", { key: r.evidence.key, title: r.evidence.title })}</p>}
      {r.body && <p className="m-0 whitespace-pre-wrap">{r.body}</p>}
      {r.status !== "open" && (
        <div className="flex flex-col gap-1 border-l-2 border-rule pl-3" data-testid="audit-response">
          <span className="label text-muted">{tr("governance.auditor.response_by", { name: r.answered_by || "—", date: date(r.answered_at) })}</span>
          {r.response && <p className="m-0 whitespace-pre-wrap">{r.response}</p>}
          {r.response_url && <a className="touch-44 inline-flex items-center break-all text-accent underline" href={r.response_url} target="_blank" rel="noreferrer noopener">{r.response_url}</a>}
          {r.response_evidence && <ControlLink k={r.response_evidence.key} title={r.response_evidence.title} />}
        </div>
      )}
      {mode === "answer" ? (
        <form
          className="flex flex-col gap-2 border-t-2 border-rule pt-3"
          onSubmit={(e) => {
            e.preventDefault();
            answer.mutate();
          }}
        >
          <label className="flex flex-col gap-1">
            <span className="label text-muted">{tr("governance.auditor.answer_text")}</span>
            <textarea className="field" rows={3} value={response} onChange={(e) => setResponse(e.target.value)} />
          </label>
          <label className="flex flex-col gap-1">
            <span className="label text-muted">{tr("governance.auditor.answer_link")}</span>
            <input className="field" type="url" inputMode="url" value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://" />
          </label>
          <label className="flex flex-col gap-1">
            <span className="label text-muted">{tr("governance.auditor.answer_evidence")}</span>
            <input className="field" value={evidence} onChange={(e) => setEvidence(e.target.value)} placeholder="EVD-12" />
          </label>
          <ErrorNote error={answer.error} />
          <div className="flex flex-wrap gap-2">
            <button className="btn btn-primary" disabled={answer.isPending}>{tr("governance.auditor.answer_send")}</button>
            <button type="button" className="btn" onClick={() => setMode("")}>{tr("governance.auditor.cancel")}</button>
          </div>
        </form>
      ) : (
        <div className="flex flex-wrap gap-2">
          {r.can_answer && <button className="btn btn-primary" onClick={() => setMode("answer")}>{tr("governance.auditor.answer")}</button>}
          {r.can_close && <button className="btn" onClick={() => close.mutate()} disabled={close.isPending}>{tr("governance.auditor.req_close")}</button>}
        </div>
      )}
      <ErrorNote error={close.error} />
    </li>
  );
}

export function RequestList({ items, showControl = true }: { items: AuditRequest[]; showControl?: boolean }) {
  if (items.length === 0) return <p className="m-0 text-muted">{tr("governance.auditor.no_requests")}</p>;
  return <ul className="m-0 flex list-none flex-col p-0">{items.map((r) => <RequestItem key={r.id} r={r} showControl={showControl} />)}</ul>;
}

// ---------------------------------------------------------------------
// Rilievi

export function FindingItem({ f, showControl = true }: { f: Finding; showControl?: boolean }) {
  const refresh = useRefreshAudit();
  const [mode, setMode] = useState<"" | "plan" | "close">("");
  const [plan, setPlan] = useState(f.action_plan);
  const [due, setDue] = useState(f.action_due);
  const [note, setNote] = useState("");
  const done = () => {
    setMode("");
    refresh();
  };
  const savePlan = useMutation({ mutationFn: () => auditApi.plan(f.id, { action_plan: plan, action_due: due }), onSuccess: done });
  const close = useMutation({ mutationFn: () => auditApi.closeFinding(f.id, note), onSuccess: done });
  return (
    <li className="flex flex-col gap-1.5 border-b border-hair py-3" data-testid="audit-finding">
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
        <span className="font-semibold">{f.title}</span>
        <Tag tone={f.severity === "critical" || f.severity === "high" ? "danger" : f.severity === "medium" ? "warn" : undefined}>{severityName(f.severity)}</Tag>
        <Tag tone={f.status === "closed" ? "ok" : f.status === "plan_proposed" ? "warn" : "danger"}>{findingStatus(f.status)}</Tag>
      </div>
      <div className="flex flex-wrap items-center gap-x-3 text-[12.5px] text-muted">
        {showControl && <ControlLink k={f.control.key} />}
        <span>{tr("governance.auditor.finding_opened", { name: f.opened_by || "—", date: date(f.opened_at) })}</span>
      </div>
      {f.description && <p className="m-0 whitespace-pre-wrap">{f.description}</p>}
      {f.action_plan && (
        <div className="flex flex-col gap-0.5 border-l-2 border-rule pl-3" data-testid="finding-plan">
          <span className="label text-muted">{tr("governance.auditor.plan_by", { name: f.plan_by || "—", date: date(f.plan_at) })}</span>
          <p className="m-0 whitespace-pre-wrap">{f.action_plan}</p>
          {f.action_due && <span className="text-[12.5px] text-muted">{tr("governance.auditor.plan_due", { date: date(f.action_due) })}</span>}
        </div>
      )}
      {f.status === "closed" && (
        <div className="flex flex-col gap-0.5 border-l-2 border-ok pl-3">
          <span className="label text-muted">{tr("governance.auditor.closed_by", { name: f.closed_by || "—", date: date(f.closed_at) })}</span>
          <p className="m-0 whitespace-pre-wrap">{f.closure_note}</p>
        </div>
      )}
      {mode === "plan" && (
        <form className="flex flex-col gap-2 border-t-2 border-rule pt-3" onSubmit={(e) => { e.preventDefault(); savePlan.mutate(); }}>
          <label className="flex flex-col gap-1">
            <span className="label text-muted">{tr("governance.auditor.plan_text")}</span>
            <textarea className="field" rows={3} value={plan} onChange={(e) => setPlan(e.target.value)} required />
          </label>
          <label className="flex flex-col gap-1">
            <span className="label text-muted">{tr("governance.auditor.plan_deadline")}</span>
            <input className="field" type="date" value={due} onChange={(e) => setDue(e.target.value)} />
          </label>
          <ErrorNote error={savePlan.error} />
          <div className="flex flex-wrap gap-2">
            <button className="btn btn-primary" disabled={savePlan.isPending || !plan.trim()}>{tr("governance.auditor.plan_save")}</button>
            <button type="button" className="btn" onClick={() => setMode("")}>{tr("governance.auditor.cancel")}</button>
          </div>
        </form>
      )}
      {mode === "close" && (
        <form className="flex flex-col gap-2 border-t-2 border-rule pt-3" onSubmit={(e) => { e.preventDefault(); close.mutate(); }}>
          <label className="flex flex-col gap-1">
            <span className="label text-muted">{tr("governance.auditor.close_note")}</span>
            <textarea className="field" rows={3} value={note} onChange={(e) => setNote(e.target.value)} required />
          </label>
          <ErrorNote error={close.error} />
          <div className="flex flex-wrap gap-2">
            <button className="btn btn-primary" disabled={close.isPending || !note.trim()}>{tr("governance.auditor.finding_close")}</button>
            <button type="button" className="btn" onClick={() => setMode("")}>{tr("governance.auditor.cancel")}</button>
          </div>
        </form>
      )}
      {mode === "" && (f.can_plan || f.can_close) && (
        <div className="flex flex-wrap gap-2">
          {f.can_plan && <button className="btn btn-primary" onClick={() => setMode("plan")}>{f.action_plan ? tr("governance.auditor.plan_edit") : tr("governance.auditor.plan_add")}</button>}
          {f.can_close && <button className="btn" onClick={() => setMode("close")}>{tr("governance.auditor.finding_close")}</button>}
        </div>
      )}
    </li>
  );
}

export function FindingList({ items, showControl = true }: { items: Finding[]; showControl?: boolean }) {
  if (items.length === 0) return <p className="m-0 text-muted">{tr("governance.auditor.no_findings")}</p>;
  return <ul className="m-0 flex list-none flex-col p-0">{items.map((f) => <FindingItem key={f.id} f={f} showControl={showControl} />)}</ul>;
}

// ---------------------------------------------------------------------
// Campioni

function SampleRow({ sample, item, canAudit }: { sample: Sample; item: Sample["items"][number]; canAudit: boolean }) {
  const refresh = useRefreshAudit();
  const [outcome, setOutcome] = useState<Outcome>(item.outcome);
  const [note, setNote] = useState(item.note);
  const save = useMutation({ mutationFn: () => auditApi.assess(sample.id, item.position, outcome, note), onSuccess: refresh });
  const needsNote = outcome === "non_compliant" || outcome === "to_clarify";
  return (
    <li className="flex flex-col gap-1.5 border-b border-hair py-2.5" data-testid="sample-item">
      <div className="flex flex-wrap items-center gap-x-2">
        <span className="font-mono text-[12px] text-muted">{item.position}.</span>
        <ControlLink k={item.evidence.key} title={item.evidence.title} />
        <Tag tone={item.outcome === "compliant" ? "ok" : item.outcome === "pending" ? undefined : "danger"}>{outcomeName(item.outcome)}</Tag>
      </div>
      {item.url && <a className="touch-44 inline-flex items-center break-all text-[12.5px] text-accent underline" href={item.url} target="_blank" rel="noreferrer noopener">{item.url}</a>}
      {item.note && !canAudit && <p className="m-0 text-[12.5px] text-muted">{item.note}</p>}
      {canAudit && (
        <form className="flex flex-col gap-2" onSubmit={(e) => { e.preventDefault(); save.mutate(); }}>
          <Select className="field" aria-label={tr("governance.auditor.outcome_label")} value={outcome} onChange={(e) => setOutcome(e.target.value as Outcome)}>
            <option value="pending">{outcomeName("pending")}</option>
            <option value="compliant">{outcomeName("compliant")}</option>
            <option value="non_compliant">{outcomeName("non_compliant")}</option>
            <option value="to_clarify">{outcomeName("to_clarify")}</option>
          </Select>
          <input className="field" aria-label={tr("governance.auditor.outcome_note")} placeholder={tr("governance.auditor.outcome_note")} value={note} onChange={(e) => setNote(e.target.value)} />
          <ErrorNote error={save.error} />
          <div>
            <button className="btn" disabled={save.isPending || (needsNote && !note.trim()) || (outcome === item.outcome && note === item.note)}>{tr("governance.auditor.outcome_save")}</button>
          </div>
        </form>
      )}
    </li>
  );
}

export function SampleBlock({ s, canAudit }: { s: Sample; canAudit: boolean }) {
  const counts = { compliant: 0, non_compliant: 0, to_clarify: 0, pending: 0 } as Record<string, number>;
  for (const i of s.items) counts[i.outcome]++;
  return (
    <details className="border border-hair px-3 py-1" data-testid="audit-sample">
      <summary className="touch-44 flex cursor-pointer flex-wrap items-center gap-x-2 gap-y-0.5">
        <span className="font-semibold">{tr("governance.auditor.sample_title", { size: s.size, population: s.population })}</span>
        <span className="text-[12.5px] text-muted">{tr("governance.auditor.sample_seed", { seed: s.seed })}</span>
        <span className="text-[12.5px] text-muted">
          {tr("governance.auditor.sample_counts", { ok: counts.compliant, ko: counts.non_compliant, unclear: counts.to_clarify, pending: counts.pending })}
        </span>
      </summary>
      <p className="m-0 pb-1 text-[12.5px] text-muted">{tr("governance.auditor.sample_drawn", { name: s.drawn_by || "—", date: date(s.drawn_at), from: date(s.period_from), to: date(s.period_to) })}</p>
      <ul className="m-0 flex list-none flex-col p-0">
        {s.items.map((i) => <SampleRow key={i.position} sample={s} item={i} canAudit={canAudit} />)}
      </ul>
    </details>
  );
}

// ---------------------------------------------------------------------
// Commenti in linea

export function CommentsBox({ refKey, comments, canWrite, engagement }: { refKey: string; comments: AuditComment[]; canWrite: boolean; engagement?: string }) {
  const refresh = useRefreshAudit();
  const [body, setBody] = useState("");
  const [evidence, setEvidence] = useState("");
  const add = useMutation({
    mutationFn: () => auditApi.comment(refKey, { body, evidence: evidence.trim() || undefined, engagement }),
    onSuccess: () => {
      setBody("");
      setEvidence("");
      refresh();
    },
  });
  return (
    <div className="flex flex-col gap-2">
      {comments.length === 0 ? (
        <p className="m-0 text-muted">{tr("governance.auditor.no_comments")}</p>
      ) : (
        <ul className="m-0 flex list-none flex-col p-0">
          {comments.map((c) => (
            <li key={c.id} className="flex flex-col gap-0.5 border-b border-hair py-2" data-testid="audit-comment">
              <span className="text-[12.5px] text-muted">
                {tr("governance.auditor.comment_by", { name: c.author || "—", date: date(c.created_at) })}
                {c.evidence && <> · {tr("governance.auditor.comment_on", { key: c.evidence.key })}</>}
              </span>
              <p className="m-0 whitespace-pre-wrap">{c.body}</p>
            </li>
          ))}
        </ul>
      )}
      {canWrite && (
        <form className="flex flex-col gap-2" onSubmit={(e) => { e.preventDefault(); add.mutate(); }}>
          <label className="flex flex-col gap-1">
            <span className="label text-muted">{tr("governance.auditor.comment_label")}</span>
            <textarea className="field" rows={2} value={body} onChange={(e) => setBody(e.target.value)} />
          </label>
          <label className="flex flex-col gap-1">
            <span className="label text-muted">{tr("governance.auditor.comment_evidence")}</span>
            <input className="field" value={evidence} onChange={(e) => setEvidence(e.target.value)} placeholder="EVD-12" />
          </label>
          <ErrorNote error={add.error} />
          <div><button className="btn" disabled={add.isPending || !body.trim()}>{tr("governance.auditor.comment_send")}</button></div>
        </form>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------
// Scheda del controllo

/** Ciò che l'audit ha scritto sul controllo: rilievi, richieste, commenti e campioni. */
export function AuditBox({ refKey }: { refKey: string }) {
  const q = useQuery({ queryKey: ["grc", "audit", "control", refKey], queryFn: () => auditApi.control(refKey), retry: false });
  const a = q.data;
  if (q.isPending) return <Loading className="m-0 text-muted" />;
  if (!a) return null; // senza accesso (403) o modulo spento: niente riquadro
  const empty = a.findings.length + a.requests.length + a.comments.length + a.samples.length === 0;
  if (empty && !a.can.audit) return null;
  const open = a.findings.filter((f) => f.status !== "closed").length;
  return (
    <section className="flex flex-col gap-3" aria-label={tr("governance.auditor.box_title")} data-testid="audit-box">
      <SectionTitle aside={open > 0 ? <Tag tone="danger">{tr("governance.auditor.box_open_findings", { count: open })}</Tag> : undefined}>{tr("governance.auditor.box_title")}</SectionTitle>
      {a.can.audit && a.engagements.length > 0 && (
        <p className="m-0 text-[12.5px] text-muted">
          {tr("governance.auditor.box_in_engagement", { title: a.engagements[0].title })}{" "}
          <Link to="/audit-spazio" search={{ incarico: a.engagements[0].id }} className="touch-44 inline-flex items-center text-accent underline">{tr("governance.auditor.box_work")}</Link>
        </p>
      )}
      <div className="flex flex-col gap-1">
        <span className="label text-muted">{tr("governance.auditor.findings")}</span>
        <FindingList items={a.findings} showControl={false} />
      </div>
      <div className="flex flex-col gap-1">
        <span className="label text-muted">{tr("governance.auditor.requests")}</span>
        <RequestList items={a.requests} showControl={false} />
      </div>
      {a.samples.length > 0 && (
        <div className="flex flex-col gap-2">
          <span className="label text-muted">{tr("governance.auditor.samples")}</span>
          {a.samples.map((s) => <SampleBlock key={s.id} s={s} canAudit={a.can.audit} />)}
        </div>
      )}
      <div className="flex flex-col gap-1">
        <span className="label text-muted">{tr("governance.auditor.comments")}</span>
        <CommentsBox refKey={refKey} comments={a.comments} canWrite={a.can.audit || a.can.respond} engagement={a.engagements[0]?.id} />
      </div>
    </section>
  );
}
