// Sezioni del dettaglio di un elemento, condivise tra la pagina completa (ElementPage.tsx)
// e l'anteprima veloce nella lista delle richieste (Requests.tsx): passaggi di fase, dati
// della domanda e dell'applicazione, persone, approvazioni, storia.
import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, type ElementDetail, type Step, type TransitionView } from "../api";
import { primaryFirst } from "../transitions";
import { useMe } from "../shell";
import { Criticality, ErrorNote, SectionTitle, ago, date, euro, euroRange } from "../ui";
import { useT } from "../i18n/react";
import { lookup, type Key, type Params } from "../i18n/core";
import { loc } from "../i18n/format";

// aria-describedby è un elenco di id separati da spazi: «In esecuzione» non può finire
// nell'id così com'è, o il motivo del blocco non arriva ai lettori di schermo.
const gateId = (to: string) => `gate-${to.replace(/\s+/g, "-")}`;

export function Transitions({ detail, onDone }: { detail: ElementDetail; onDone: () => void }) {
  const tr = useT();
  const [pending, setPending] = useState<TransitionView | null>(null);
  const [comment, setComment] = useState("");
  const run = useMutation({
    mutationFn: (t: TransitionView) => api.transition(detail.element.id, t.to, comment),
    onSuccess: () => {
      setPending(null);
      setComment("");
      onDone();
    },
  });
  if (detail.transitions.length === 0) return null;
  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap gap-2">
        {primaryFirst(detail.transitions).map((t, i) => {
          const blocked = t.blocked.length > 0;
          // Il principale è il passaggio in avanti nel percorso (non «Rimanda» o «Respingi»); se è bloccato
          // non si promuove un'alternativa.
          const primary = i === 0 && !blocked && t.forward !== false;
          // I passaggi indietro o di uscita («Rimanda», «Respingi», «Sospendi») restano raggiungibili ma discreti.
          const quiet = t.forward === false;
          return (
            <button
              key={t.to}
              className={`btn touch-44 ${primary ? "btn-primary" : quiet ? "btn-quiet" : ""}`}
              disabled={run.isPending || blocked}
              aria-describedby={blocked ? gateId(t.to) : undefined}
              title={blocked ? t.blocked.join("; ") : undefined}
              onClick={() => (t.comment_required ? setPending(t) : run.mutate(t))}
            >
              {t.label}
            </button>
          );
        })}
      </div>
      {detail.transitions.some((t) => t.blocked.length > 0) && (
        <ul className="m-0 flex list-none flex-col gap-0.5 p-0 text-[12.5px]">
          {detail.transitions
            .filter((t) => t.blocked.length > 0)
            .map((t) => (
              <li key={t.to} id={gateId(t.to)} className="text-muted">
                <span className="font-semibold text-fg">{t.label}</span>: {t.blocked.join("; ")}
              </li>
            ))}
        </ul>
      )}
      {pending && (
        <form
          className="flex flex-col gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            run.mutate(pending);
          }}
        >
          <label className="flex flex-col gap-1">
            <span className="label">{tr("lavoro.elementsections.reasonFor", { label: pending.label })}</span>
            <textarea className="field min-h-20" required value={comment} onChange={(e) => setComment(e.target.value)} />
          </label>
          <div className="flex gap-2">
            <button className="btn btn-primary" disabled={run.isPending}>{tr("lavoro.elementsections.confirm")}</button>
            <button type="button" className="btn" onClick={() => setPending(null)}>{tr("lavoro.elementsections.cancel")}</button>
          </div>
        </form>
      )}
      <ErrorNote error={run.error} />
    </div>
  );
}

export function ApplicationFacts({ app }: { app: NonNullable<ElementDetail["application"]> }) {
  const t = useT();
  const rows: [string, React.ReactNode][] = [
    [t("lavoro.elementsections.code"), app.code ?? "—"],
    [t("lavoro.elementsections.teamOwner"), app.team ?? "—"],
    [t("lavoro.elementsections.criticality"), <Criticality value={app.criticality} critical={app.supports_critical_function} />],
    [t("lavoro.elementsections.data"), app.data_classification ?? "—"],
    ["Hosting", app.hosting ?? "—"],
    ["RTO / RPO", app.rto_minutes != null || app.rpo_minutes != null ? t("lavoro.elementsections.minutes", { rto: app.rto_minutes ?? "—", rpo: app.rpo_minutes ?? "—" }) : "—"],
    [t("lavoro.elementsections.logicalAssets"), app.assets.join(", ") || "—"],
  ];
  return (
    <section className="flex flex-col gap-2">
      <SectionTitle>{t("lavoro.elementsections.application")}</SectionTitle>
      <dl className="m-0 grid grid-cols-[120px_minmax(0,1fr)]">
        {rows.map(([k, v]) => (
          <div key={k} className="contents">
            <dt className="border-b border-hair py-1.5 text-muted">{k}</dt>
            <dd className="m-0 border-b border-hair py-1.5">{v}</dd>
          </div>
        ))}
      </dl>
    </section>
  );
}

export function DemandFacts({ detail, onDone }: { detail: ElementDetail; onDone: () => void }) {
  const t = useT();
  const d = detail.demand;
  const perms = useMe().data?.active_tenant?.permissions ?? [];
  // Il server verifica comunque: qui si evita di offrire un'azione che verrebbe rifiutata.
  const canEdit = perms.includes("*") || perms.includes("element.write");
  const [editing, setEditing] = useState(false);
  const [low, setLow] = useState("");
  const [high, setHigh] = useState("");
  const [days, setDays] = useState("");
  const save = useMutation({
    mutationFn: () =>
      api.updateDemand(detail.element.id, {
        estimate_low_cents: low ? Math.round(Number(low) * 100_000) : undefined,
        estimate_high_cents: high ? Math.round(Number(high) * 100_000) : undefined,
        estimate_effort_days: days ? Number(days) : undefined,
      }),
    onSuccess: () => {
      setEditing(false);
      onDone();
    },
  });
  if (!d) return null;
  const rows: [string, string][] = [
    [t("lavoro.elementsections.requestingUnit"), d.requesting_unit ?? "—"],
    [t("lavoro.elementsections.driver"), d.driver ? driverName(d.driver) : "—"],
    [t("lavoro.elementsections.desiredDate"), date(d.desired_date)],
    [t("lavoro.elementsections.estimate"), euroRange(d.estimate_low_cents, d.estimate_high_cents)],
    [t("lavoro.elementsections.effort"), d.estimate_effort_days != null ? t("lavoro.elementsections.personDays", { count: d.estimate_effort_days }) : "—"],
  ];
  return (
    <section className="flex flex-col gap-2">
      <SectionTitle aside={canEdit && !editing && <button className="text-[12px] text-accent underline" onClick={() => setEditing(true)}>{t("lavoro.elementsections.updateEstimate")}</button>}>
        {t("lavoro.elementsections.demandData")}
      </SectionTitle>
      <dl className="m-0 grid grid-cols-[140px_minmax(0,1fr)]">
        {rows.map(([k, v]) => (
          <div key={k} className="contents">
            <dt className="border-b border-hair py-1.5 text-muted">{k}</dt>
            <dd className="m-0 border-b border-hair py-1.5 font-medium tabular">{v}</dd>
          </div>
        ))}
      </dl>
      {editing && (
        <form
          className="grid grid-cols-3 gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate();
          }}
        >
          <label className="flex flex-col gap-1"><span className="label">{t("lavoro.elementsections.minK")}</span><input className="field tabular" inputMode="decimal" value={low} onChange={(e) => setLow(e.target.value)} /></label>
          <label className="flex flex-col gap-1"><span className="label">{t("lavoro.elementsections.maxK")}</span><input className="field tabular" inputMode="decimal" value={high} onChange={(e) => setHigh(e.target.value)} /></label>
          <label className="flex flex-col gap-1"><span className="label">{t("lavoro.elementsections.personDaysLabel")}</span><input className="field tabular" inputMode="decimal" value={days} onChange={(e) => setDays(e.target.value)} /></label>
          <div className="col-span-3 flex gap-2">
            <button className="btn btn-primary" disabled={save.isPending}>{t("lavoro.elementsections.save")}</button>
            <button type="button" className="btn" onClick={() => setEditing(false)}>{t("lavoro.elementsections.cancel")}</button>
          </div>
          <div className="col-span-3"><ErrorNote error={save.error} /></div>
        </form>
      )}
    </section>
  );
}

export function People({ detail, bare }: { detail: ElementDetail; bare?: boolean }) {
  const t = useT();
  if (detail.stakeholders.length === 0) return null;
  const list = (
    <dl className={`m-0 grid gap-y-1 ${bare ? "grid-cols-[minmax(84px,38%)_minmax(0,1fr)] text-[13px]" : "grid-cols-[140px_minmax(0,1fr)]"}`}>
      {detail.stakeholders.map((s) => (
        <div key={s.role + s.name} className="contents">
          <dt className="text-muted">{s.role_name}</dt>
          <dd className="m-0 min-w-0 break-words">{s.name}{s.group_id ? <span className="text-muted"> {t("lavoro.elementsections.group")}</span> : null}</dd>
        </div>
      ))}
    </dl>
  );
  if (bare) return list;
  return (
    <section className="flex flex-col gap-2">
      <SectionTitle>{t("lavoro.elementsections.people")}</SectionTitle>
      {list}
    </section>
  );
}

/** Vero se la persona che guarda deve decidere un passo di approvazione di questo elemento. */
export function awaitsMyDecision(detail: ElementDetail): boolean {
  return detail.approvals.some((r) => r.steps.some((s) => s.can_decide));
}

export function Approvals({ detail, onDone }: { detail: ElementDetail; onDone: () => void }) {
  const t = useT();
  const ref = useRef<HTMLElement>(null);
  const mine = awaitsMyDecision(detail);
  // Chi arriva dalla coda delle approvazioni o da una notifica trova i pulsanti in vista, non da cercare.
  useEffect(() => {
    if (!mine) return;
    ref.current?.querySelector("[data-decision]")?.scrollIntoView({ block: "center", behavior: "auto" });
  }, [mine]);
  if (detail.approvals.length === 0) return null;
  return (
    <section ref={ref} className="flex flex-col gap-2">
      <SectionTitle>{t("lavoro.elementsections.approvals")}</SectionTitle>
      {detail.approvals.map((r) => (
        <div key={r.id} className="flex flex-col gap-1.5">
          <div className="text-[12px] text-muted">
            {t("lavoro.elementsections.stage", { stage: r.stage })} · {euro(r.amount_cents)} · {decisionName(r.status)}
          </div>
          {r.steps.map((st) => (
            <StepRow key={st.id} step={st} onDone={onDone} />
          ))}
        </div>
      ))}
    </section>
  );
}

function StepRow({ step, onDone }: { step: Step; onDone: () => void }) {
  const t = useT();
  const [mode, setMode] = useState<string | null>(null);
  const [text, setText] = useState("");
  const decide = useMutation({
    mutationFn: (decision: string) =>
      api.decide(step.id, decision, decision === "approved_with_conditions" ? undefined : text, decision === "approved_with_conditions" ? text : undefined),
    onSuccess: () => {
      setMode(null);
      setText("");
      onDone();
    },
  });
  const done = step.decision !== "pending";
  const mark = done
    ? step.decision.startsWith("approved")
      ? "bg-fg text-bg border-fg"
      : "border-danger text-danger"
    : step.can_decide
      ? "border-accent text-accent"
      : "border-muted text-muted";
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-center gap-2.5">
        <span className={`flex size-[18px] items-center justify-center border-[1.5px] text-[11px] font-bold ${mark}`} aria-hidden="true">
          {done ? (step.decision.startsWith("approved") ? "✓" : "×") : step.ordinal}
        </span>
        <span className="flex-1">{step.approver}</span>
        <span className="text-[12.5px] text-muted">
          {decisionName(step.decision)}
          {step.decided_by ? ` · ${step.decided_by}` : ""}
        </span>
      </div>
      {(step.comment || step.conditions) && (
        <p className="m-0 pl-4 md:pl-7 text-[12.5px] text-muted">{step.conditions ? t("lavoro.elementsections.conditionsText", { text: step.conditions }) : step.comment}</p>
      )}
      {step.can_decide && (
        <div data-decision className="flex flex-col gap-2 pl-4 md:pl-7">
          {!mode ? (
            <div className="flex flex-wrap gap-2">
              <button className="btn btn-primary touch-44" disabled={decide.isPending} onClick={() => decide.mutate("approved")}>{t("lavoro.elementsections.approve")}</button>
              <button className="btn touch-44" onClick={() => setMode("approved_with_conditions")}>{t("lavoro.elementsections.withConditions")}</button>
              <button className="btn touch-44" onClick={() => setMode("deferred")}>{t("lavoro.elementsections.defer")}</button>
              <button className="btn btn-danger touch-44" onClick={() => setMode("rejected")}>{t("lavoro.elementsections.reject")}</button>
            </div>
          ) : (
            <form
              className="flex flex-col gap-2"
              onSubmit={(e) => {
                e.preventDefault();
                decide.mutate(mode);
              }}
            >
              <label className="flex flex-col gap-1">
                <span className="label">{mode === "approved_with_conditions" ? t("lavoro.elementsections.conditions") : t("lavoro.elementsections.reason")}</span>
                <textarea className="field min-h-16" required value={text} onChange={(e) => setText(e.target.value)} />
              </label>
              <div className="flex gap-2">
                <button className={`btn touch-44 ${mode === "rejected" ? "btn-danger" : "btn-primary"}`} disabled={decide.isPending}>
                  {t("lavoro.elementsections.confirmDecision", { decision: decisionName(mode) })}
                </button>
                <button type="button" className="btn touch-44" onClick={() => setMode(null)}>{t("lavoro.elementsections.cancel")}</button>
              </div>
            </form>
          )}
          <ErrorNote error={decide.error} />
        </div>
      )}
    </div>
  );
}

/** Aggiorna le viste che dipendono dall'elemento (dopo un passaggio, una decisione, una modifica). */
export function useRefreshElement(refKey: string) {
  const qc = useQueryClient();
  return () => {
    qc.invalidateQueries({ queryKey: ["element", refKey] });
    qc.invalidateQueries({ queryKey: ["search"] });
    qc.invalidateQueries({ queryKey: ["inbox"] });
    qc.invalidateQueries({ queryKey: ["activity"] });
    qc.invalidateQueries({ queryKey: ["links", refKey] });
  };
}

/** Nomi dei campi personalizzati per la storia ("ha modificato Canale"). */
export function fieldNamesOf(detail: ElementDetail | undefined): Record<string, string> {
  return Object.fromEntries((detail?.custom_fields ?? []).map((f) => [f.key, f.name]));
}

export function History({ id, fieldNames }: { id: string; fieldNames?: Record<string, string> }) {
  const t = useT();
  const hist = useQuery({ queryKey: ["activity", id], queryFn: () => api.activity(id) });
  if (!hist.data) return null;
  const items = [...hist.data.items].reverse();
  return (
    <section className="flex flex-col gap-2">
      <SectionTitle>{t("lavoro.elementsections.history")}</SectionTitle>
      <ol className="m-0 flex list-none flex-col gap-1.5 p-0">
        {items.map((a) => (
          <li key={a.id} className="grid grid-cols-[88px_minmax(0,1fr)] gap-2 text-[12.5px]">
            <span className="text-muted tabular">{ago(a.created_at)}</span>
            <span>
              <span className="font-semibold">{a.actor_name}</span> {describe(t, a.kind, a.field_key, a.added, a.removed, fieldNames, a)}
            </span>
          </li>
        ))}
      </ol>
    </section>
  );
}



/** Mai una chiave tecnica a vista: se non c'è un nome, la chiave diventa una parola. */
function words(key: string): string {
  return key.replace(/\.inverse$|\.attrs$/, "").replace(/[_.]+/g, " ").trim();
}

type T = ReturnType<typeof useT>;
const named = (key: string) => lookup(`lavoro.elementsections.${key}`);

/** Nomi delle decisioni, dei driver e dei ruoli (le tabelle di ui.tsx, qui con il catalogo di questa area). */
export const decisionName = (d: string) => named(`decision.${d}`) ?? d;
export const driverName = (d: string) => named(`driver.${d}`) ?? d;
export const roleName = (r: string) => named(`role.${r}`) ?? r;

export function describe(
  t: T,
  kind: string,
  field: string | null,
  added: Record<string, unknown> | null,
  removed: Record<string, unknown> | null,
  fieldNames: Record<string, string> = {},
  extra?: { relation_label?: string; other?: string },
): string {
  const h = (k: string, p?: Params) => t(`lavoro.elementsections.h.${k}` as Key, p);
  const hn = (k: string) => named(`h.${k}`);
  const s = (v: unknown) => String(v);
  const when = (v: unknown) => (v ? new Date(s(v)).toLocaleDateString(loc()) : "");
  const addedS: unknown = added;
  const removedS: unknown = removed;
  switch (kind) {
    case "created":
      return hn(`created.${s(added?.type)}`) ?? h("created.element");
    case "state":
      return added?.comment
        ? h("stateComment", { from: s(removed?.name ?? "?"), to: s(added?.name ?? "?"), comment: s(added.comment) })
        : h("state", { from: s(removed?.name ?? "?"), to: s(added?.name ?? "?") });
    case "stakeholder":
      return h("stakeholder", { role: field ? roleName(field) : "" });
    case "approval":
      return field === "requested"
        ? h("approvalRequested")
        : added?.comment
          ? h("approvalDecidedComment", { decision: decisionName(s(added?.decision)), comment: s(added.comment) })
          : h("approvalDecided", { decision: decisionName(s(added?.decision)) });
    case "field":
      return field === "demand"
        ? h("field.demand")
        : field === "impact"
          ? h("field.impact")
          : field === "application"
            ? h("field.application")
            : field === "title"
              ? typeof addedS === "string"
                ? h("field.titleTo", { title: addedS })
                : h("field.title")
              : field === "body"
                ? h("field.body")
                : field?.startsWith("fields.")
                  ? h("field.custom", { name: fieldNames[field.slice(7)] ?? words(field.slice(7)) })
                  : field === "time_rating"
                    ? (() => {
                        const to = typeof addedS === "string" ? (named(`time.${addedS}`) ?? addedS) : null;
                        const was = typeof removedS === "string" ? (named(`time.${removedS}`) ?? removedS) : null;
                        if (to && was) return h("field.ratingToWas", { to, was });
                        if (to) return h("field.ratingTo", { to });
                        if (was) return h("field.ratingWas", { was });
                        return h("field.rating");
                      })()
                    : field
                      ? hn(`fieldlabel.${field}`) !== undefined
                        ? h("field.other", { what: hn(`fieldlabel.${field}`) ?? "" })
                        : h("field.custom", { name: fieldNames[field] ?? words(field) })
                      : h("field.unknown");
    case "process":
      return field === "migration"
        ? h("processMigration", { version: s(added?.version), state: s(added?.state) })
        : h("processStart", { process: s(added?.process), version: s(added?.version) });
    case "impact":
      return h("impact", { impact: s(added?.impact), application: s(added?.application) });
    case "relation": {
      const other = extra?.other;
      if (field === "removed") return other ? h("relRemovedWith", { other }) : h("relRemoved");
      const rel = extra?.relation_label ?? hn(`rel.${field ?? ""}`) ?? words(field ?? "");
      return field?.endsWith(".attrs")
        ? other
          ? h("relUpdatedWith", { rel, other })
          : h("relUpdated", { rel })
        : h("relLinked", { rel, other: other ?? h("anItem") });
    }
    case "rule":
      return added?.note ? h("ruleNote", { rule: s(added?.rule), note: s(added.note) }) : h("rule", { rule: s(added?.rule) });
    case "comment":
      return field === "deleted" ? h("commentDeleted") : field === "edited" ? h("commentEdited") : h("commented");
    case "attachment":
      return field === "removed" ? h("attachmentRemoved", { name: s(added?.name ?? "") }) : h("attachmentAdded", { name: s(added?.name ?? "") });
    // Valutazione, graduatoria e governance (piano, passi 8 e 9).
    case "score":
      return (
        h("score", { total: Number(added?.total).toLocaleString(loc()), version: s(added?.version) }) +
        (removed?.total != null ? h("scorePrev", { prev: Number(removed.total).toLocaleString(loc()) }) : "") +
        (added?.comment ? h("scoreComment", { comment: s(added.comment) }) : "")
      );
    case "rank":
      return field === "automatic"
        ? h("rankAuto", { reason: s(added?.reason) })
        : h("rankMoved", { from: s(removed?.position), to: s(added?.position), reason: s(added?.reason) });
    case "meeting": {
      const day = when(added?.scheduled_at);
      if (field === "agenda") return h("meetingAgenda", { body: s(added?.body), when: day });
      if (field === "minutes") return h("meetingMinutes", { body: s(added?.body), when: day });
      const outcome = hn(`outcome.${s(added?.decision)}`) ?? s(added?.decision);
      return h("meetingDecision", { body: s(added?.body), when: day, outcome }) + (added?.conditions ? h("meetingConditions", { conditions: s(added.conditions) }) : "");
    }
    // Valutazioni con questionari (piano, passo 4).
    case "review": {
      const what = `${s(added?.type ?? h("reviewWord"))} ${s(added?.review ?? "")}`.trim();
      if (field === "opened") return h("reviewOpened", { what, team: s(added?.team) }) + (added?.rule ? h("withRule") : "");
      if (field === "decided") return h("reviewDecided", { what, outcome: s(added?.outcome) });
      if (field === "cancelled") return h("reviewCancelled", { what });
      return h("review", { what });
    }
    case "answers":
      return h("answers", { count: (added?.questions as string[] | undefined)?.length ?? 0 });
    case "assignment":
      return added?.member_id ? h("assignmentSet", { section: s(field) }) : h("assignmentRemoved", { section: s(field) });
    case "prescription":
      return field === "added" ? h("prescriptionAdded", { text: s(added?.text) }) : h("prescriptionUpdated");
    case "sla":
      return field === "escalation" ? h("slaEscalation", { count: Number(added?.late_days) || 0 }) : h("slaLate", { due: s(added?.due) });
    // Work management (piano, passi 10 e 18): assegnatario, sprint, epic, ore.
    case "work": {
      const ref = (v: Record<string, unknown> | null) => (v?.key ? `${s(v.key)} ${s(v.title ?? "")}`.trim() : "");
      const mins = (m: unknown) => {
        const n = Number(m) || 0;
        return [Math.floor(n / 60) ? `${Math.floor(n / 60)}h` : "", n % 60 ? `${n % 60}m` : ""].filter(Boolean).join(" ") || "0h";
      };
      switch (field) {
        case "assignee":
          return added?.name ? h("workAssigned", { name: s(added.name) }) : h("workUnassigned");
        case "sprint":
          return added ? h("workSprint", { ref: ref(added) }) : removed ? h("workBacklogWas", { ref: ref(removed) }) : h("workBacklog");
        case "sprint.update":
          return h("workSprintUpdate");
        case "parent":
          return added ? h("workParent", { ref: ref(added) }) : h("workNoParent");
        case "epic":
          return h("workEpic", { epic: s(added?.epic ?? "") });
        case "time":
          return h("workTime", { time: mins(added?.minutes), date: new Date(s(added?.date) + "T00:00:00").toLocaleDateString(loc()) });
        case "time.removed":
          return h("workTimeRemoved");
        default:
          return h("workDefault");
      }
    }
    // Status report (piano, passo 13).
    case "report":
      return h("report", { end: s(added?.period_end), status: hn(`overall.${s(added?.overall)}`) ?? "" });
    // Moduli più recenti: conoscenza, rischi e incidenti, conformità, requisiti, DevSecOps, import.
    case "page":
      if (field === "moved") return h("pageMoved");
      if (field === "reviewed") return h("pageReviewed");
      return (h(field === "restored" ? "pageRestored" : "pageSaved", { version: s(added?.version ?? "") }) + (added?.note ? h("pageNote", { note: s(added.note) }) : "")).replace("  ", " ");
    case "incident":
      return field === "postmortem" ? h("incidentPostmortem") : added?.summary ? h("incidentClassifiedSummary", { summary: s(added.summary) }) : h("incidentClassified");
    case "notification":
      return (h("notification", { scheme: s(added?.scheme ?? "") }) + (added?.stage ? ` · ${s(added.stage)}` : "")).trim();
    case "risk":
      return added?.residual ? h("riskResidual", { residual: s(added.residual) }) : h("risk");
    case "contract":
      return field === "deleted" ? h("contractDeleted") : added?.reference ? h("contractSavedRef", { reference: s(added.reference) }) : h("contractSaved");
    case "grc":
      return hn(`grc.${field ?? ""}`) ?? h("grcDefault");
    case "requirement":
      return field === "ready" ? h("requirementReady") : field === "criterion" ? h("requirementCriterion") : h("requirementCreated");
    case "verification":
      return field === "rejected" ? h("verificationFailed") : h("verificationPassed");
    case "exception":
      return field === "updated" ? h("exceptionUpdated") : h("exceptionRequested", { exception: s(added?.exception ?? "") }).trim();
    case "gate": {
      const env = s(added?.environment ?? "");
      const envver = env + (added?.version ? " " + s(added.version) : "");
      const known = ["pass", "warn", "fail", "override_requested", "override_approved", "override_rejected", "override_revoked"];
      return h(field && known.includes(field) ? `gate.${field}` : "gateDefault", { env, envver });
    }
    case "release":
      return h("release", { env: s(added?.environment ?? h("production")), service: s(added?.service ?? ""), version: s(added?.version ?? "") }).trim();
    case "schedule":
      return h("schedule");
    case "import":
      return h("import", { sprint: s(added?.sprint ?? ""), source: s(added?.source ?? h("otherTool")) }).trim();
    default:
      return words(kind);
  }
}
