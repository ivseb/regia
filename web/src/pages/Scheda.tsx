// Sintesi di scheda (replica/usabilita.md, proposta 2): ogni scheda si apre sulla sintesi giusta.
// - RequestSummaryTab: la richiesta «decisione prima», con i numeri della graduatoria, del budget gerarchico,
//   delle valutazioni e dei requisiti, e cosa manca e a chi tocca;
// - Application360: la vista unica dell'applicazione, con ciò che richiede azione in cima e un riquadro per
//   area che porta al dettaglio.
// Una richiesta sola per sintesi (schedaApi.ts): i numeri vengono dalle funzioni delle pagine che li mostrano.
import type { ReactNode } from "react";
import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import type { ElementDetail } from "../api";
import { schedaApi, type App360, type App360Node, type RequestSummary, type SummaryAction, type SumRisk } from "../schedaApi";
import { Criticality, ErrorNote, Loading, SectionTitle, ago, date, euro, euroRange } from "../ui";
import { lookup, tr } from "../i18n/core";
import { useT } from "../i18n/react";
import { fmtNumber } from "../i18n/format";

/** Il TabKey di ElementPage che questa sintesi sa aprire (dichiarato qui per non importare la pagina). */
export type SummaryTab = "dettagli" | "analisi" | "valutazioni" | "requisiti" | "approvazioni" | "produzione" | "gate";

const link = "inline-flex min-h-[44px] items-center text-accent underline md:min-h-0";
const keyCls = "font-mono text-[11.5px] font-medium text-accent";

// ---------------------------------------------------------------------
// Pezzi comuni

function Block({ title, aside, attention, children }: { title: string; aside?: ReactNode; attention?: boolean; children: ReactNode }) {
  return (
    <section className="flex min-w-0 flex-col gap-2" aria-label={title}>
      <SectionTitle aside={aside}>
        {attention && <span aria-hidden="true" className="mr-1.5 inline-block size-1.5 bg-flag align-middle" />}
        {title}
      </SectionTitle>
      {children}
    </section>
  );
}

function Stat({ label, value, tone, children }: { label: string; value: ReactNode; tone?: "ok" | "flag" | "danger"; children?: ReactNode }) {
  const cls = tone === "ok" ? "text-ok" : tone === "flag" ? "text-flag" : tone === "danger" ? "text-danger" : "";
  return (
    <div className="flex min-w-0 flex-col gap-0.5">
      <dt className="label">{label}</dt>
      <dd className={`m-0 text-[22px] leading-tight font-bold tabular ${cls}`}>{value}</dd>
      {children && <dd className="m-0 flex flex-col text-[12.5px] text-muted">{children}</dd>}
    </div>
  );
}

function actionText(a: SummaryAction): string {
  return lookup(`scheda.action.${a.kind}`, { count: a.count ?? 0, detail: a.detail ?? "", who: a.who ?? "" }) ?? a.kind;
}

/** Elenco di cosa manca: ogni riga dice che cosa, a chi tocca e porta dove si risolve. */
function Actions({ items, open, empty, label }: { items: SummaryAction[]; open: (a: SummaryAction) => ReactNode; empty: string; label: string }) {
  const t = useT();
  if (items.length === 0)
    return (
      <p className="m-0 flex items-center gap-2" role="status">
        <span aria-hidden="true" className="text-ok">✓</span>
        {empty}
      </p>
    );
  return (
    <ul aria-label={label} className="m-0 flex list-none flex-col p-0">
      {items.map((a, i) => (
        <li key={`${a.kind}-${i}`} className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-x-3 border-b border-hair py-1.5">
          <span aria-hidden="true" className={`size-2 ${a.mine ? "bg-accent" : "border border-flag"}`} />
          <span className="flex min-w-0 flex-col">
            <span className="break-words">{actionText(a)}</span>
            {(a.who && a.kind !== "blocked") || a.mine ? (
              <span className="text-[12.5px] text-muted">{a.mine ? t("scheda.mine") : t("scheda.who", { who: a.who ?? "" })}</span>
            ) : null}
          </span>
          {open(a)}
        </li>
      ))}
    </ul>
  );
}

function ToTab({ tab, onTab, primary }: { tab?: string; onTab: (t: SummaryTab) => void; primary?: boolean }) {
  const t = useT();
  if (!tab) return <span />;
  return (
    <button type="button" className={`btn ${primary ? "btn-primary" : ""}`} onClick={() => onTab(tab as SummaryTab)}>
      {t("scheda.open")}
    </button>
  );
}

function RiskList({ items, empty }: { items: SumRisk[]; empty: string }) {
  const t = useT();
  if (items.length === 0) return <p className="m-0 text-muted">{empty}</p>;
  return (
    <ul className="m-0 flex list-none flex-col p-0">
      {items.map((r) => (
        <li key={r.key} className="flex flex-wrap items-baseline gap-x-2 border-b border-hair py-1.5">
          <Link to="/e/$key" params={{ key: r.key }} className={`${keyCls} no-underline`}>{r.key}</Link>
          <span className="min-w-0 flex-[1_1_10rem] break-words">{r.title}</span>
          <span className="text-[12.5px] text-muted">
            {r.phase}
            {r.band && <> · <span className={r.over_appetite ? "font-semibold text-danger" : ""}>{r.band}</span></>}
            {r.over_appetite && <> · {t("scheda.req.riskOver")}</>}
            {r.via && <> · {t("scheda.req.riskVia", { key: r.via })}</>}
          </span>
        </li>
      ))}
    </ul>
  );
}

// ---------------------------------------------------------------------
// Richiesta «decisione prima»

export function RequestSummaryTab({ detail, onTab }: { detail: ElementDetail; onTab: (t: SummaryTab) => void }) {
  const key = detail.element.key || detail.element.id;
  const q = useQuery({ queryKey: ["element", key, "summary"], queryFn: () => schedaApi.request(key) });
  if (q.isPending) return <Loading className="text-muted" />;
  if (!q.data) return <ErrorNote error={q.error} />;
  return <RequestSummaryBody detail={detail} s={q.data} onTab={onTab} />;
}

function RequestSummaryBody({ detail, s, onTab }: { detail: ElementDetail; s: RequestSummary; onTab: (t: SummaryTab) => void }) {
  const t = useT();
  const d = detail.demand;
  const rank = s.rank;
  const area = rank?.area;
  const decision = s.decision;
  const stageName = (st: string) => lookup(`lavoro.reviews.stage${st.charAt(0).toUpperCase()}${st.slice(1)}`) ?? st;
  return (
    <div className="flex min-w-0 flex-col gap-6" aria-label={t("scheda.req.label")}>
      {decision && (
        <section aria-label={t("scheda.req.decision")} className="flex max-w-3xl flex-wrap items-center gap-x-4 gap-y-2 border-l-[3px] border-accent bg-sel/40 px-3 py-2">
          <span className="flex min-w-0 flex-1 flex-col">
            <span className="font-semibold">{t("scheda.req.decision")}</span>
            <span className="text-[12.5px] text-muted">
              {decision.amount_cents != null ? t("scheda.req.decisionLine", { stage: decision.stage, amount: euro(decision.amount_cents) }) : t("scheda.req.decisionStage", { stage: decision.stage })}
              {" · "}
              {t("scheda.req.decisionProgress", { done: decision.steps_done + 1, all: decision.steps_all })}
            </span>
            {decision.waiting.length > 0 && <span className="text-[12.5px] text-muted">{t("scheda.req.decisionWaiting", { who: decision.waiting.join(", ") })}</span>}
          </span>
          <button type="button" className={`btn touch-44 ${decision.can_decide ? "btn-primary" : ""}`} onClick={() => onTab("approvazioni")}>
            {decision.can_decide ? t("scheda.req.decide") : t("scheda.req.openDecision")}
          </button>
        </section>
      )}

      <Block title={t("scheda.req.missing")}>
        <Actions
          items={s.missing.filter((a) => a.kind !== "decision")}
          empty={t("scheda.req.missingNone")}
          label={t("scheda.req.missing")}
          open={(a) => <ToTab tab={a.tab} onTab={onTab} />}
        />
      </Block>

      <Block title={t("scheda.req.numbers")}>
        <dl className="m-0 grid grid-cols-2 gap-x-4 gap-y-4 lg:grid-cols-4">
          <Stat label={t("scheda.req.score")} value={s.score?.value != null ? fmtNumber(s.score.value, { maximumFractionDigits: 1 }) : t("scheda.req.scoreNone")} tone={s.score?.value == null ? "flag" : undefined}>
            {s.score?.version != null && <span>{t("scheda.req.scoreVersion", { version: s.score.version })}</span>}
            {s.score?.stale && <span className="text-flag">{t("scheda.req.scoreStale")}</span>}
          </Stat>
          <Stat
            label={t("scheda.req.rank")}
            value={rank?.in_ranking ? t("scheda.req.rankOf", { position: rank.position, total: rank.total }) : t("scheda.req.rankOut")}
            tone={rank?.in_ranking && rank.funded === false ? "flag" : undefined}
          >
            {rank?.in_ranking && rank.funded != null && <span className={rank.funded ? "text-ok" : "text-flag"}>{rank.funded ? t("scheda.req.rankFunded") : t("scheda.req.rankUnfunded")}</span>}
            {rank?.in_ranking && rank.funded == null && <span>{t("scheda.req.rankNoBudget")}</span>}
            {rank?.in_ranking && rank.budget_cents != null && <span>{t("scheda.req.rankBudget", { budget: euro(rank.budget_cents), cumulative: euro(rank.cumulative_cents) })}</span>}
            {rank?.pinned && <span>{t("scheda.req.rankPinned")}</span>}
          </Stat>
          <Stat label={t("scheda.req.estimate")} value={d && (d.estimate_low_cents != null || d.estimate_high_cents != null) ? euroRange(d.estimate_low_cents, d.estimate_high_cents) : t("scheda.req.estimateNone")} tone={d && d.estimate_low_cents == null && d.estimate_high_cents == null ? "flag" : undefined} />
          <Stat
            label={t("scheda.req.deadline")}
            value={d?.regulatory_deadline ? date(d.regulatory_deadline) : d?.desired_date ? date(d.desired_date) : t("scheda.req.deadlineNone")}
            tone={d?.regulatory_deadline ? "flag" : undefined}
          >
            {d?.regulatory_deadline && <span className="text-flag">{t("scheda.req.deadlineMandatory", { date: date(d.regulatory_deadline) })}</span>}
            {d?.desired_date && d.regulatory_deadline && <span>{t("scheda.req.deadlineDesired", { date: date(d.desired_date) })}</span>}
          </Stat>
        </dl>
        <div className="flex flex-wrap items-center gap-x-5 gap-y-0 text-[12.5px]">
          {rank?.in_ranking && <Link to="/graduatoria" className={link}>{t("scheda.req.openRanking")}</Link>}
          {s.days_in_stage != null && <span className="text-muted">{t("scheda.req.stageDays", { count: s.days_in_stage })}</span>}
        </div>
        {rank?.in_ranking && (
          <div className="flex max-w-3xl flex-col gap-0.5 border-t border-hair pt-2" aria-label={t("scheda.req.area")}>
            <span className="label">{t("scheda.req.area")}</span>
            {area ? (
              <>
                <span className="font-semibold">{t("scheda.req.areaPos", { unit: area.unit, position: area.position, total: area.total })}</span>
                <span className="text-[12.5px] text-muted">
                  {area.period && <>{t("scheda.req.areaPeriod", { period: area.period })} · </>}
                  {area.has_allocation ? t("scheda.req.areaAlloc", { allocated: euro(area.allocated_cents), residual: euro(area.residual_cents) }) : t("scheda.req.areaNoAlloc", { unit: area.unit })}
                </span>
                {area.fits_residual != null && <span className={`text-[12.5px] ${area.fits_residual ? "text-ok" : "text-flag"}`}>{area.fits_residual ? t("scheda.req.areaFits") : t("scheda.req.areaNoFit")}</span>}
                <span className="flex flex-wrap gap-x-5">
                  <Link to="/graduatoria" search={{ unita: area.unit_id, periodo: area.period_id }} className={link}>{t("scheda.req.openRanking")}</Link>
                  <Link to="/budget" search={{ periodo: area.period_id }} className={link}>{t("scheda.req.openBudget")}</Link>
                </span>
              </>
            ) : (
              <span className="text-[12.5px] text-muted">{t("scheda.req.areaNone")}</span>
            )}
          </div>
        )}
      </Block>

      <div className="grid min-w-0 grid-cols-1 gap-6 lg:grid-cols-2">
        {s.reviews && (
          <Block
            title={t("scheda.req.reviews")}
            attention={s.reviews.mandatory_open > 0}
            aside={<button type="button" className="text-[12.5px] text-accent underline" onClick={() => onTab("valutazioni")}>{t("scheda.req.reviewsAll")}</button>}
          >
            {s.reviews.items.length === 0 ? (
              <p className="m-0 text-muted">{t("scheda.req.reviewsNone")}</p>
            ) : (
              <ul className="m-0 flex list-none flex-col p-0">
                {s.reviews.items.map((r) => (
                  <li key={r.key} className="flex flex-col border-b border-hair py-1.5">
                    <span className="flex flex-wrap items-baseline gap-x-2">
                      <Link to="/valutazioni/$key" params={{ key: r.key }} className={`${keyCls} no-underline`}>{r.key}</Link>
                      <span className="min-w-0 flex-[1_1_10rem] break-words">{r.type}{r.impact ? ` · ${r.impact}` : ""}</span>
                    </span>
                    <span className="text-[12.5px] text-muted">
                      {t("scheda.req.reviewLine", { team: r.team, stage: stageName(r.stage) })}
                      {r.required_missing > 0 && <> · <span className="text-flag">{t("scheda.req.reviewMissing", { count: r.required_missing })}</span></>}
                      {r.late_days > 0 && <> · <span className="text-danger">{t("scheda.req.reviewLate", { count: r.late_days })}</span></>}
                    </span>
                  </li>
                ))}
                {s.reviews.open > s.reviews.items.length && <li className="py-1.5 text-[12.5px] text-muted">{t("scheda.more", { count: s.reviews.open - s.reviews.items.length })}</li>}
              </ul>
            )}
          </Block>
        )}

        {s.requirements && (
          <Block
            title={t("scheda.req.requirements")}
            attention={s.requirements.to_accept + s.requirements.open + s.requirements.to_verify > 0}
            aside={<button type="button" className="text-[12.5px] text-accent underline" onClick={() => onTab("requisiti")}>{t("scheda.req.reqsAll")}</button>}
          >
            {s.requirements.total === 0 ? (
              <p className="m-0 text-muted">{t("scheda.req.reqNone")}</p>
            ) : (
              <>
                <dl className="m-0 grid grid-cols-2 gap-x-4 gap-y-3 sm:grid-cols-4">
                  <Stat label={t("scheda.req.reqAccept")} value={s.requirements.to_accept} tone={s.requirements.to_accept > 0 ? "flag" : undefined} />
                  <Stat label={t("scheda.req.reqOpen")} value={s.requirements.open} tone={s.requirements.open > 0 ? "flag" : undefined} />
                  <Stat label={t("scheda.req.reqVerify")} value={s.requirements.to_verify} />
                  <Stat label={t("scheda.req.reqDone")} value={s.requirements.verified} tone="ok" />
                </dl>
                {s.requirements.ready && <p className="m-0 text-ok">{t("scheda.req.reqReady")}</p>}
                <ul className="m-0 flex list-none flex-col p-0">
                  {s.requirements.top.map((r) => (
                    <li key={r.key} className="flex flex-wrap items-baseline gap-x-2 border-b border-hair py-1.5">
                      <Link to="/e/$key" params={{ key: r.key }} className={`${keyCls} no-underline`}>{r.key}</Link>
                      <span className="min-w-0 flex-[1_1_10rem] break-words">{r.title}</span>
                      <span className="text-[12.5px] text-muted">{lookup(`scheda.req.reqStatus.${r.status}`) ?? r.status}</span>
                    </li>
                  ))}
                </ul>
              </>
            )}
          </Block>
        )}

        <Block title={t("scheda.req.risks")} attention={s.risks.some((r) => r.over_appetite)}>
          <RiskList items={s.risks} empty={t("scheda.req.risksNone")} />
        </Block>

        {s.impacts > 0 && (
          <Block title={t("scheda.req.impactsOpen")}>
            <p className="m-0 flex flex-wrap items-center gap-x-4">
              <span>{t("scheda.req.impacts", { count: s.impacts })}</span>
              <button type="button" className="text-accent underline" onClick={() => onTab("analisi")}>{t("scheda.open")}</button>
            </p>
          </Block>
        )}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------
// Applicazione 360

export function Application360({ detail, onTab }: { detail: ElementDetail; onTab: (t: SummaryTab) => void }) {
  const key = detail.element.key || detail.element.id;
  const q = useQuery({ queryKey: ["element", key, "360"], queryFn: () => schedaApi.application(key) });
  if (q.isPending) return <Loading className="text-muted" />;
  if (!q.data) return <ErrorNote error={q.error} />;
  return <Application360Body detail={detail} s={q.data} onTab={onTab} />;
}

function Application360Body({ detail, s, onTab }: { detail: ElementDetail; s: App360; onTab: (t: SummaryTab) => void }) {
  const t = useT();
  const app = detail.application;
  const name = detail.element.title;
  // Dove porta ogni azione: una scheda della stessa pagina o la pagina che la risolve.
  const open = (a: SummaryAction): ReactNode => {
    switch (a.kind) {
      case "vuln_overdue":
      case "vuln_critical":
        return <Link to="/devsecops/vulnerabilita" search={{ q: name }} className="btn no-underline">{t("scheda.open")}</Link>;
      case "pipeline_gap":
        return <Link to="/devsecops/aderenza" className="btn no-underline">{t("scheda.open")}</Link>;
      case "incident_open":
        return s.incidents[0] ? <Link to="/e/$key" params={{ key: s.incidents[0].key }} className="btn no-underline">{t("scheda.open")}</Link> : <span />;
      case "risk_over": {
        const r = s.risks.find((x) => x.over_appetite);
        return r ? <Link to="/e/$key" params={{ key: r.key }} className="btn no-underline">{t("scheda.open")}</Link> : <span />;
      }
      case "controls_invalid":
      case "compliance_gap":
        return <Link to="/conformita" search={{ tab: "applicazioni", perimetro: `application:${s.key}` }} className="btn no-underline">{t("scheda.open")}</Link>;
      default:
        return <ToTab tab={a.tab} onTab={onTab} />;
    }
  };
  const v = s.vulns;
  const vulnAttn = !!v && (v.overdue > 0 || v.critical_open > 0);
  const gateAttn = !!s.gate && (s.gate.failing > 0 || s.gate.overrides_pending > 0);
  const pipeAttn = !!s.pipeline && s.pipeline.configured && (s.pipeline.status === "parziale" || s.pipeline.status === "fuori_politica");
  const compAttn = !!s.compliance && (s.compliance.controls_invalid > 0 || s.compliance.gaps > 0);
  const relAttn = !!s.releases && (s.releases.services === 0 || s.releases.stale > 0);
  const blocks: { attn: boolean; node: ReactNode }[] = [];
  const add = (attn: boolean, node: ReactNode) => blocks.push({ attn, node });

  if (v) {
    add(
      vulnAttn,
      <Block
        key="vulns"
        title={t("scheda.app.vulns")}
        attention={vulnAttn}
        aside={<Link to="/devsecops/vulnerabilita" search={{ q: name }} className="text-[12.5px]">{t("scheda.app.vulnAll")}</Link>}
      >
        <dl className="m-0 grid grid-cols-2 gap-x-4 gap-y-3 sm:grid-cols-4">
          <Stat label={t("scheda.app.vulnOpen")} value={v.open} />
          <Stat label={t("scheda.app.vulnOverdue")} value={v.overdue} tone={v.overdue > 0 ? "danger" : undefined} />
          <Stat label={t("scheda.app.vulnSoon")} value={v.soon} tone={v.soon > 0 ? "flag" : undefined} />
          <Stat label={t("scheda.app.vulnCritical")} value={v.critical_open} tone={v.critical_open > 0 ? "danger" : undefined} />
        </dl>
        {v.critical_open > 0 && <p className="m-0 text-[12.5px] text-muted">{t("scheda.app.vulnProd", { count: v.critical_prod })}</p>}
        {v.items.length === 0 ? (
          <p className="m-0 text-muted">{t("scheda.app.vulnNone")}</p>
        ) : (
          <ul className="m-0 flex list-none flex-col p-0">
            {v.items.map((i) => (
              <li key={i.id} className="flex flex-wrap items-baseline gap-x-2 border-b border-hair py-1.5">
                <Link to="/devsecops/vulnerabilita" search={{ q: i.cve }} className={`${keyCls} no-underline`}>{i.cve}</Link>
                <span className="min-w-0 flex-[1_1_10rem] break-words">{i.package ?? ""}</span>
                <span className="text-[12.5px] text-muted">
                  {lookup(`scheda.app.sev.${i.severity}`) ?? i.severity}
                  {i.overdue ? <> · <span className="font-semibold text-danger">{t("scheda.app.vulnLate")}</span></> : i.days_left != null ? <> · {t("scheda.app.vulnDays", { count: i.days_left })}</> : null}
                </span>
              </li>
            ))}
          </ul>
        )}
      </Block>,
    );
  }

  if (s.releases) {
    const r = s.releases;
    add(
      relAttn,
      <Block key="releases" title={t("scheda.app.releases")} attention={relAttn} aside={<button type="button" className="text-[12.5px] text-accent underline" onClick={() => onTab("produzione")}>{t("scheda.app.releasesAll")}</button>}>
        {r.latest.length === 0 ? (
          <p className="m-0 text-muted">{t("scheda.app.releasesNone")}</p>
        ) : (
          <ul className="m-0 flex list-none flex-col p-0">
            {r.latest.map((x, i) => (
              <li key={`${x.service}-${x.version}-${i}`} className="flex flex-col border-b border-hair py-1.5">
                <span className="break-words">{t("scheda.app.releaseLine", { service: x.service, version: x.version, env: x.environment })}</span>
                <span className="text-[12.5px] text-muted">
                  {ago(x.at)}
                  {x.request_key && <> · {t("scheda.app.releaseRequest", { key: x.request_key })}</>}
                </span>
              </li>
            ))}
          </ul>
        )}
        <p className="m-0 text-[12.5px] text-muted">
          {t("scheda.app.releaseServices", { count: r.services })}
          {r.stale > 0 && <> · <span className="text-flag">{t("scheda.app.releaseStale", { count: r.stale })}</span></>}
          {r.unscanned > 0 && <> · {t("scheda.app.releaseUnscanned", { count: r.unscanned })}</>}
        </p>
      </Block>,
    );
  }

  if (s.gate) {
    const g = s.gate;
    add(
      gateAttn,
      <Block key="gate" title={t("scheda.app.gate")} attention={gateAttn} aside={<button type="button" className="text-[12.5px] text-accent underline" onClick={() => onTab("gate")}>{t("scheda.app.gateAll")}</button>}>
        {g.cells.length === 0 ? (
          <p className="m-0 text-muted">{t("scheda.app.gateNone")}</p>
        ) : (
          <ul className="m-0 flex list-none flex-col p-0">
            {g.cells.map((c) => (
              <li key={c.environment} className="flex flex-wrap items-baseline gap-x-2 border-b border-hair py-1.5">
                <span className={`min-w-0 flex-1 ${c.result === "fail" ? "font-semibold text-danger" : ""}`}>
                  {t("scheda.app.gateCell", { env: c.environment, result: c.result ? (lookup(`scheda.app.gateResult.${c.result}`) ?? c.result) : t("scheda.app.gateNoDecision") })}
                </span>
                {c.version && <span className="font-mono text-[11.5px] text-muted">{c.version}</span>}
              </li>
            ))}
          </ul>
        )}
        {g.overrides_pending > 0 && <p className="m-0 text-[12.5px] text-flag">{t("scheda.app.gateOverrides", { count: g.overrides_pending })}</p>}
      </Block>,
    );
  }

  if (s.pipeline) {
    const p = s.pipeline;
    add(
      pipeAttn,
      <Block key="pipeline" title={t("scheda.app.pipeline")} attention={pipeAttn} aside={<Link to="/devsecops/aderenza" className="text-[12.5px]">{t("scheda.app.pipelineAll")}</Link>}>
        {!p.configured ? (
          <p className="m-0 text-muted">{t("scheda.app.pipelineNone")}</p>
        ) : (
          <>
            <p className="m-0">
              <span className={`font-semibold ${p.status === "aderente" ? "text-ok" : "text-flag"}`}>{lookup(`scheda.app.pipelineStatus.${p.status}`) ?? p.status}</span>
              <span className="text-muted"> · {t("scheda.app.pipelineClass", { class: p.class })}</span>
            </p>
            {p.missing.length > 0 ? (
              <p className="m-0 text-[12.5px] text-muted">{t("scheda.app.pipelineMissing", { list: p.missing.join(", ") })}</p>
            ) : p.status === "aderente" ? (
              <p className="m-0 text-[12.5px] text-muted">{t("scheda.app.pipelineOk")}</p>
            ) : (
              <p className="m-0 text-[12.5px] text-muted">{t("scheda.app.pipelineNoRun")}</p>
            )}
          </>
        )}
      </Block>,
    );
  }

  if (s.has_compliance) {
    const c = s.compliance;
    add(
      compAttn,
      <Block
        key="compliance"
        title={t("scheda.app.compliance")}
        attention={compAttn}
        aside={<Link to="/conformita" search={{ tab: "applicazioni", perimetro: `application:${s.key}` }} className="text-[12.5px]">{t("scheda.app.complianceAll")}</Link>}
      >
        {!c || c.applicable === 0 ? (
          <p className="m-0 text-muted">{t("scheda.app.complianceNone")}</p>
        ) : (
          <>
            <p className="m-0 font-semibold">{t("scheda.app.complianceLine", { percent: c.percent, covered: c.covered, applicable: c.applicable })}</p>
            <ul className="m-0 flex list-none flex-col p-0">
              {c.controls.map((x) => (
                <li key={x.key} className="flex flex-wrap items-baseline gap-x-2 border-b border-hair py-1.5">
                  <Link to="/e/$key" params={{ key: x.key }} className={`${keyCls} no-underline`}>{x.key}</Link>
                  <span className="min-w-0 flex-[1_1_10rem] break-words">{x.title}</span>
                  <span className={`text-[12.5px] ${x.valid ? "text-muted" : "font-semibold text-flag"}`}>{x.valid ? t("scheda.app.controlValid") : t("scheda.app.controlInvalid")}</span>
                </li>
              ))}
            </ul>
          </>
        )}
      </Block>,
    );
  }

  if (s.has_risks) {
    const over = s.risks.some((r) => r.over_appetite);
    add(over, <Block key="risks" title={t("scheda.app.risks")} attention={over}><RiskList items={s.risks} empty={t("scheda.app.risksNone")} /></Block>);
  }

  if (s.has_incidents) {
    add(
      s.incidents_open > 0,
      <Block key="incidents" title={t("scheda.app.incidents")} attention={s.incidents_open > 0}>
        {s.incidents.length === 0 ? (
          <p className="m-0 text-muted">{t("scheda.app.incidentsNone")}</p>
        ) : (
          <ul className="m-0 flex list-none flex-col p-0">
            {s.incidents.map((i) => (
              <li key={i.key} className="flex flex-wrap items-baseline gap-x-2 border-b border-hair py-1.5">
                <Link to="/e/$key" params={{ key: i.key }} className={`${keyCls} no-underline`}>{i.key}</Link>
                <span className="min-w-0 flex-[1_1_10rem] break-words">{i.title}</span>
                <span className="text-[12.5px] text-muted">
                  {i.state}
                  {i.major && <> · <span className="font-semibold text-danger">{t("scheda.app.incidentMajor")}</span></>} · {ago(i.detected_at)}
                </span>
              </li>
            ))}
          </ul>
        )}
      </Block>,
    );
  }

  add(
    false,
    <Block key="requests" title={t("scheda.app.requests")} aside={s.requests_open > 5 ? <span className="font-mono text-[11.5px] text-muted tabular">{s.requests_open}</span> : undefined}>
      {s.requests.length === 0 ? (
        <p className="m-0 text-muted">{t("scheda.app.requestsNone")}</p>
      ) : (
        <ul className="m-0 flex list-none flex-col p-0">
          {s.requests.map((r) => (
            <li key={r.key} className="flex flex-col border-b border-hair py-1.5">
              <span className="flex flex-wrap items-baseline gap-x-2">
                <Link to="/e/$key" params={{ key: r.request_key }} className={`${keyCls} no-underline`}>{r.request_key}</Link>
                <span className="min-w-0 flex-[1_1_10rem] break-words">{r.request_title}</span>
              </span>
              <span className="text-[12.5px] text-muted">
                <Link to="/e/$key" params={{ key: r.key }} className="text-muted">{r.key}</Link> · {t("scheda.app.requestLine", { request: r.state, stage: r.request_state })}
              </span>
            </li>
          ))}
        </ul>
      )}
    </Block>,
  );

  if (s.deps) add(false, <DepsBlock key="deps" name={name} deps={s.deps} />);

  if (s.has_documents) {
    add(
      false,
      <Block key="docs" title={t("scheda.app.documents")}>
        {s.documents.length === 0 ? (
          <p className="m-0 text-muted">{t("scheda.app.documentsNone")}</p>
        ) : (
          <ul className="m-0 flex list-none flex-col p-0">
            {s.documents.map((d) => (
              <li key={d.key} className="flex flex-wrap items-baseline gap-x-2 border-b border-hair py-1.5">
                <Link to="/documenti/$key" params={{ key: d.key }} className={`${keyCls} no-underline`}>{d.key}</Link>
                <span className="min-w-0 flex-[1_1_10rem] break-words">{d.title}</span>
                <span className="text-[12.5px] text-muted">{d.space}{d.controlled && <> · {t("scheda.app.documentControlled")}</>}</span>
              </li>
            ))}
          </ul>
        )}
      </Block>,
    );
  }

  // Ciò che richiede azione sta in cima, anche tra i riquadri.
  const ordered = [...blocks.filter((b) => b.attn), ...blocks.filter((b) => !b.attn)];
  return (
    <div className="flex min-w-0 flex-col gap-6" aria-label={t("scheda.app.label")}>
      <Block title={t("scheda.app.attention")} attention={s.actions.length > 0}>
        <Actions items={s.actions} empty={t("scheda.app.attentionNone")} label={t("scheda.app.attention")} open={open} />
      </Block>
      <Block title={t("scheda.app.owners")}>
        <dl className="m-0 grid grid-cols-[minmax(96px,34%)_minmax(0,1fr)]">
          {app && (
            <>
              <dt className="border-b border-hair py-1.5 text-muted">{t("scheda.app.criticality")}</dt>
              <dd className="m-0 border-b border-hair py-1.5"><Criticality value={app.criticality} critical={app.supports_critical_function} /></dd>
              {app.rto_minutes != null || app.rpo_minutes != null ? (
                <>
                  <dt className="border-b border-hair py-1.5 text-muted">RTO / RPO</dt>
                  <dd className="m-0 border-b border-hair py-1.5">{t("scheda.app.rtoRpo", { rto: app.rto_minutes ?? "—", rpo: app.rpo_minutes ?? "—" })}</dd>
                </>
              ) : null}
              {app.data_classification && (
                <>
                  <dt className="border-b border-hair py-1.5 text-muted">{t("scheda.app.data")}</dt>
                  <dd className="m-0 border-b border-hair py-1.5">{app.data_classification}</dd>
                </>
              )}
            </>
          )}
          {s.team && (
            <>
              <dt className="border-b border-hair py-1.5 text-muted">{t("scheda.app.teamLabel")}</dt>
              <dd className="m-0 border-b border-hair py-1.5">
                {s.team}
                {s.team_lead && <span className="text-muted"> · {t("scheda.app.teamLead", { name: s.team_lead })}</span>}
              </dd>
            </>
          )}
          {s.owners.map((o, i) => (
            <div key={`${o.role}-${i}`} className="contents">
              <dt className="border-b border-hair py-1.5 text-muted">{o.role}</dt>
              <dd className="m-0 border-b border-hair py-1.5">{o.name}</dd>
            </div>
          ))}
        </dl>
        {!s.team && s.owners.length === 0 && <p className="m-0 text-muted">{t("scheda.app.ownersNone")}</p>}
      </Block>
      <div className="grid min-w-0 grid-cols-1 gap-6 lg:grid-cols-2">{ordered.map((b) => b.node)}</div>
    </div>
  );
}

/** Mappa piccola: chi questa applicazione usa a sinistra, chi la usa a destra; i nomi sono sempre visibili. */
function DepsBlock({ name, deps }: { name: string; deps: NonNullable<App360["deps"]> }) {
  const t = useT();
  const list = (items: App360Node[]) => {
    const shown = items.slice(0, 5);
    return (
      <ul className="m-0 flex list-none flex-col gap-1 p-0">
        {shown.map((n) => (
          <li key={n.key} className="flex min-w-0 flex-col border border-hair px-2 py-1">
            <Link to="/e/$key" params={{ key: n.key }} className="min-w-0 break-words no-underline">
              <span className={keyCls}>{n.code ?? n.key}</span> <span className="text-fg">{n.name}</span>
            </Link>
            <span className="text-[12px] text-muted">
              {n.via}
              {n.criticality && <> · <Criticality value={n.criticality} /></>}
            </span>
          </li>
        ))}
        {items.length > shown.length && <li className="text-[12.5px] text-muted">{t("scheda.more", { count: items.length - shown.length })}</li>}
      </ul>
    );
  };
  const empty = deps.dependencies.length === 0 && deps.dependents.length === 0;
  return (
    <Block title={t("scheda.app.deps")} aside={<Link to="/architettura/mappa" className="text-[12.5px]">{t("scheda.app.depsOpen")}</Link>}>
      {empty ? (
        <p className="m-0 text-muted">{t("scheda.app.depsNone")}</p>
      ) : (
        <div role="group" aria-label={t("scheda.app.depsMap", { name })} className="grid grid-cols-1 gap-3 md:grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)] md:items-center">
          <div className="flex min-w-0 flex-col gap-1">
            <span className="label">{t("scheda.app.depsUses")}</span>
            {deps.dependencies.length > 0 ? list(deps.dependencies) : <span className="text-muted">{tr("scheda.none")}</span>}
          </div>
          <div className="border-2 border-fg px-3 py-2 text-center font-semibold break-words">{name}</div>
          <div className="flex min-w-0 flex-col gap-1">
            <span className="label">{t("scheda.app.depsUsedBy")}</span>
            {deps.dependents.length > 0 ? list(deps.dependents) : <span className="text-muted">{tr("scheda.none")}</span>}
          </div>
        </div>
      )}
    </Block>
  );
}
