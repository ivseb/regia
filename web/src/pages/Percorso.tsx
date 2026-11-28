// Percorso della richiesta (o dell'impatto), in cima alla scheda: dove siamo nel processo, chi è coinvolto,
// il lavoro che ne nasce, cosa succede dopo e «Cosa faccio adesso» per chi guarda. Una sola richiesta
// (percorsoApi.ts, internal/api/percorso.go): tutto è calcolato dai dati configurati, niente è cablato qui.
// Telefono: la linea delle fasi va a capo dentro il pannello, nessuno scroll orizzontale.
import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { ErrorNote, SectionTitle } from "../ui";
import { Term } from "../help";
import { lookup } from "../i18n/core";
import { useT } from "../i18n/react";
import { useStateName } from "../stateLabels";
import { percorsoApi, type PcNext, type PcTodo, type PcTrigger, type Percorso as PercorsoData } from "../percorsoApi";
import { useNarrow } from "./timeline";
import type { TabKey } from "./ElementPage";
import { PercorsoGuide, VersionNotice } from "./PercorsoVersioni";
import { InProduzione } from "./InProduzione";

type Name = (name: string, labels?: Record<string, string> | null) => string;

/** compact: pannello chiuso in una riga anche su schermo largo (decisione in primo piano, «decisione prima»). */
export function PercorsoPanel({ refKey, onTab, compact = false }: { refKey: string; onTab: (t: TabKey) => void; compact?: boolean }) {
  const t = useT();
  const q = useQuery({ queryKey: ["element", refKey, "percorso"], queryFn: () => percorsoApi.get(refKey) });
  if (q.isPending) return null;
  if (!q.data) return <ErrorNote error={q.error} />;
  return <PercorsoBody p={q.data} onTab={onTab} compact={compact} label={t(q.data.type === "impact" ? "percorso.richiesta.titleImpact" : "percorso.richiesta.title")} />;
}

function PercorsoBody({ p, onTab, label, compact }: { p: PercorsoData; onTab: (t: TabKey) => void; label: string; compact: boolean }) {
  const t = useT();
  const stateName = useStateName();
  const narrow = useNarrow();
  const [openNarrow, setOpenNarrow] = useState(false);
  const labelsOf = (name: string) => p.phases.find((x) => x.name === name)?.labels;
  const nm: Name = (name, labels) => stateName(name, labels ?? labelsOf(name));
  const line = p.phases.filter((x) => !x.off_path || x.status === "current");
  const meta = (
    <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
      {!narrow && !compact && <h2 className="label m-0">{label}</h2>}
      {p.process && (
        <span className="text-[12px] text-muted">
          <Term k="processo">{t("percorso.richiesta.process", { name: p.process })}</Term>
        </span>
      )}
      {p.type === "impact" && (
        <Link to="/e/$key" params={{ key: p.request_key }} className="text-[12px] text-accent no-underline hover:underline">
          {t("percorso.richiesta.ofRequest", { key: p.request_key })}
        </Link>
      )}
    </div>
  );
  const body = (
    <>
      <div className="flex flex-col gap-1.5">
        <span className="text-[12px] text-muted">
          <Term k="fase">{t("percorso.richiesta.phases")}</Term>
        </span>
        <ol className="m-0 flex list-none flex-wrap gap-x-4 gap-y-1.5 p-0" aria-label={t("percorso.richiesta.phases")}>
          {line.map((ph, i) => (
            <li key={ph.name} aria-current={ph.status === "current" ? "step" : undefined} className="flex min-w-0 items-center gap-1.5 text-[13px]">
              <span aria-hidden="true" className={`inline-block h-2.5 w-2.5 shrink-0 ${ph.status === "done" ? "bg-fg" : ph.status === "current" ? "bg-accent" : "border border-muted"}`} />
              <span className="font-mono text-[11px] text-muted tabular" aria-hidden="true">
                {String(i + 1).padStart(2, "0")}
              </span>
              <span className={`min-w-0 break-words ${ph.status === "current" ? "border-b-2 border-accent font-semibold" : ph.status === "done" ? "" : "text-muted"}`}>{nm(ph.name, ph.labels)}</span>
              <span className="sr-only">{t(`percorso.richiesta.phase.${ph.status}`)}</span>
            </li>
          ))}
        </ol>
      </div>

      <VersionNotice p={p} />
      <Now p={p} onTab={onTab} />
      <PercorsoGuide p={p} phaseName={nm(p.current)} />

      <div className="grid min-w-0 gap-x-8 gap-y-5 md:grid-cols-2">
        <Involved p={p} />
        <Work p={p} />
        <Next p={p} nm={nm} />
      </div>
    </>
  );
  // Sul telefono, o quando la scheda aperta è la decisione, il pannello è chiuso: una riga con la fase e cosa
  // tocca a te, così la scheda sotto resta in vista.
  if (narrow || compact) {
    return (
      <section aria-label={label} className="min-w-0 border border-hair">
        <details open={openNarrow} onToggle={(e) => setOpenNarrow(e.currentTarget.open)}>
          <summary className="flex min-h-11 cursor-pointer flex-col justify-center gap-0.5 px-4 py-2">
            <span className="label text-muted">
              {label} · {nm(p.current)}
            </span>
            <span className="text-[13.5px] font-semibold">{nowHeading(t, p)}</span>
          </summary>
          <div className="flex min-w-0 flex-col gap-4 px-4 pt-1 pb-3.5">
            {meta}
            {body}
          </div>
        </details>
      </section>
    );
  }
  return (
    <section aria-label={label} className="flex min-w-0 flex-col gap-4 border border-hair px-4 py-3.5">
      {meta}
      {body}
    </section>
  );
}

function nowHeading(t: ReturnType<typeof useT>, p: PercorsoData): string {
  const mine = p.todo.some((d) => d.mine);
  const rest = p.todo.some((d) => !d.mine && d.kind !== "nothing");
  return mine ? t("percorso.richiesta.now.mine") : rest ? t("percorso.richiesta.now.wait") : t("percorso.richiesta.now.nothing");
}

// ---------------------------------------------------------------------
// Cosa faccio adesso

function todoText(t: ReturnType<typeof useT>, d: PcTodo): string {
  const anon = !d.who && (d.kind === "wait_review" || d.kind === "wait_team") ? "Anon" : "";
  return lookup(`percorso.richiesta.todo.${d.kind}${anon}`, { detail: d.detail ?? "", who: d.who ?? "", key: d.key ?? "", count: d.count ?? 0 }) ?? t("percorso.richiesta.todo.nothing");
}

function Now({ p, onTab }: { p: PercorsoData; onTab: (t: TabKey) => void }) {
  const t = useT();
  const mine = p.todo.filter((d) => d.mine);
  const rest = p.todo.filter((d) => !d.mine && d.kind !== "nothing");
  const heading = nowHeading(t, p);
  const items = mine.length > 0 ? [...mine, ...rest] : rest;
  return (
    <div role="region" aria-label={t("percorso.richiesta.now.title")} className="flex flex-col gap-2 border-l-[3px] border-accent bg-sel/40 px-3 py-2.5">
      <span className="label text-muted">{t("percorso.richiesta.now.title")}</span>
      <span className="font-semibold">{heading}</span>
      {items.length > 0 && (
        <ul className="m-0 flex list-none flex-col gap-2 p-0">
          {items.map((d, i) => (
            <li key={`${d.kind}-${d.key}-${i}`} className="flex flex-wrap items-center gap-x-4 gap-y-1.5">
              <span className={`min-w-0 flex-1 basis-60 text-[13.5px] ${d.mine ? "" : "text-muted"}`}>{todoText(t, d)}</span>
              <TodoAction d={d} onTab={onTab} />
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function TodoAction({ d, onTab }: { d: PcTodo; onTab: (t: TabKey) => void }) {
  const text = lookup(`percorso.richiesta.act.${d.kind}`);
  if (!text) return null;
  const cls = `btn touch-44 ${d.mine ? "btn-primary" : ""}`;
  if (d.link?.startsWith("/valutazioni/") && d.key) {
    return (
      <Link to="/valutazioni/$key" params={{ key: d.key }} className={`${cls} no-underline`}>
        {text}
      </Link>
    );
  }
  if (d.kind === "advance" || d.kind === "unblock") {
    return (
      <button
        type="button"
        className={cls}
        onClick={() => {
          window.scrollTo({ top: 0, behavior: "smooth" });
          document.querySelector<HTMLElement>("header button, header a")?.focus({ preventScroll: true });
        }}
      >
        {text}
      </button>
    );
  }
  if (!d.tab) return null;
  return (
    <button type="button" className={cls} onClick={() => onTab(d.tab as TabKey)}>
      {text}
    </button>
  );
}

// ---------------------------------------------------------------------
// Chi è coinvolto

function Chip({ children, tone }: { children: string; tone?: "flag" | "muted" }) {
  return <span className={`border px-1 py-px text-[11px] ${tone === "flag" ? "border-flag text-flag" : "border-hair text-muted"}`}>{children}</span>;
}

function Involved({ p }: { p: PercorsoData }) {
  const t = useT();
  const open = p.reviews.filter((r) => r.open).length;
  return (
    <div className="flex min-w-0 flex-col gap-3">
      <SectionTitle>{t("percorso.richiesta.involved.title")}</SectionTitle>
      <div className="flex flex-col gap-1">
        <span className="flex flex-wrap items-baseline gap-x-2 text-[12.5px] font-semibold">
          <Term k="valutazione">{t("percorso.richiesta.involved.reviews")}</Term>
          {p.reviews.length > 0 && <span className="font-normal text-muted tabular">{t("percorso.richiesta.involved.reviewsCount", { open, closed: p.reviews.length - open })}</span>}
        </span>
        {p.reviews.length === 0 ? (
          <span className="text-[12.5px] text-muted">{t("percorso.richiesta.involved.reviewsNone")}</span>
        ) : (
          <ul className="m-0 flex list-none flex-col gap-1 p-0">
            {p.reviews.map((r) => (
              <li key={r.key} className="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[13px]">
                <Link to="/valutazioni/$key" params={{ key: r.key }} className="font-mono text-[12px] text-accent no-underline hover:underline">
                  {r.key}
                </Link>
                <span className="min-w-0 break-words">
                  {r.type}
                  {r.team && <span className="text-muted"> · {r.team}</span>}
                  {r.impact && <span className="text-muted"> · {r.impact}</span>}
                </span>
                <Chip tone={r.open && r.mandatory ? "flag" : "muted"}>{r.open ? (r.mandatory ? t("percorso.richiesta.involved.mandatory") : t("percorso.richiesta.involved.open")) : t("percorso.richiesta.involved.closed")}</Chip>
              </li>
            ))}
          </ul>
        )}
      </div>
      <div className="flex flex-col gap-1">
        <span className="text-[12.5px] font-semibold">
          <Term k="approvazione">{t("percorso.richiesta.involved.approvals")}</Term>
        </span>
        {p.approvals.length === 0 ? (
          <span className="text-[12.5px] text-muted">{t("percorso.richiesta.involved.approvalsNone")}</span>
        ) : (
          <ul className="m-0 flex list-none flex-col gap-1 p-0 text-[13px]">
            {p.approvals.map((a, i) => (
              <li key={i}>{t("percorso.richiesta.involved.approval", { stage: a.stage, done: a.steps_done, all: a.steps_all, who: a.waiting.join(", ") })}</li>
            ))}
          </ul>
        )}
      </div>
      <div className="flex flex-col gap-1">
        <span className="text-[12.5px] font-semibold">
          <Term k="impatto">{t("percorso.richiesta.involved.impacts")}</Term>
        </span>
        {p.impacts.length === 0 ? (
          <span className="text-[12.5px] text-muted">{t("percorso.richiesta.involved.impactsNone")}</span>
        ) : (
          <ul className="m-0 flex list-none flex-col gap-1 p-0">
            {p.impacts.map((im) => (
              <li key={im.key} className="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[13px]">
                <Link to="/e/$key" params={{ key: im.key }} className="font-mono text-[12px] text-accent no-underline hover:underline">
                  {im.key}
                </Link>
                <span className="min-w-0 break-words">
                  {im.application}
                  <span className="text-muted"> · {im.team || t("percorso.richiesta.involved.noTeam")}</span>
                </span>
                <Chip>{im.state}</Chip>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------
// Il lavoro: richiesta → impatti → epic → story

function Work({ p }: { p: PercorsoData }) {
  const t = useT();
  const d = p.delivery;
  const total = d?.progress?.total ?? 0;
  return (
    <div className="flex min-w-0 flex-col gap-3">
      <SectionTitle>{t("percorso.richiesta.work.title")}</SectionTitle>
      <ol className="m-0 flex list-none flex-wrap items-center gap-x-2 gap-y-1 p-0 text-[12.5px]" aria-label={t("percorso.richiesta.work.title")}>
        {p.type === "business_request" && (
          <li className="flex items-center gap-2">
            <Term k="richiesta">{t("percorso.richiesta.work.request")}</Term>
            <span aria-hidden="true" className="text-muted">→</span>
          </li>
        )}
        <li className="flex items-center gap-2">
          <Term k="impatto">{t("percorso.richiesta.work.impacts")}</Term>
          <span className="font-mono tabular text-muted">{p.impacts.length}</span>
          <span aria-hidden="true" className="text-muted">→</span>
        </li>
        <li className="flex items-center gap-2">
          <Term k="epic">{t("percorso.richiesta.work.epics")}</Term>
          <span className="font-mono tabular text-muted">{d?.epics.length ?? 0}</span>
          <span aria-hidden="true" className="text-muted">→</span>
        </li>
        <li>
          <Term k="story">{t("percorso.richiesta.work.stories")}</Term> <span className="font-mono tabular text-muted">{d?.progress ? `${d.progress.done}/${total}` : "0/0"}</span>
        </li>
      </ol>
      {!d || d.epics.length === 0 ? (
        <span className="text-[12.5px] text-muted">{t("percorso.richiesta.work.none")}</span>
      ) : (
        <>
          {d.progress && (
            <div className="flex flex-col gap-1">
              <span className="text-[13px] tabular">{total > 0 ? t("percorso.richiesta.work.progress", { done: d.progress.done, total, percent: d.progress.percent }) : t("percorso.richiesta.work.noStories")}</span>
              <span className="block h-1.5 w-full bg-hair" role="img" aria-label={t("percorso.richiesta.work.progress", { done: d.progress.done, total, percent: d.progress.percent })}>
                <span className="block h-full bg-accent" style={{ width: `${Math.min(100, d.progress.percent)}%` }} />
              </span>
            </div>
          )}
          {/* Il passo dopo la chiusura delle story: da quando il lavoro è in produzione. */}
          <InProduzione refKey={p.key} />
          <ul className="m-0 flex list-none flex-col gap-1 p-0">
            {d.epics.map((e) => (
              <li key={e.key} className="flex flex-wrap items-baseline gap-x-2 text-[13px]">
                <Link to="/e/$key" params={{ key: e.key }} className="font-mono text-[12px] text-accent no-underline hover:underline">
                  {e.key}
                </Link>
                <span className="min-w-0 break-words text-muted">{e.team ?? ""}</span>
                <span className="font-mono text-[12px] tabular text-muted">
                  {e.progress.done}/{e.progress.total}
                </span>
              </li>
            ))}
          </ul>
        </>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------
// Cosa succede dopo

function ruleText(t: ReturnType<typeof useT>, r: PcTrigger): string {
  const kind = r.kind === "open_review" || r.kind === "assign_role" || r.kind === "create_epic" ? r.kind : "other";
  return t(`percorso.richiesta.rule.${kind}`, { name: r.name || r.ref, role: r.role, rule: r.rule });
}

function Next({ p, nm }: { p: PercorsoData; nm: Name }) {
  const t = useT();
  const fwd = p.next.filter((n) => n.forward);
  const others = p.next.filter((n) => !n.forward);
  return (
    <div className="flex min-w-0 flex-col gap-3 md:col-span-2">
      <SectionTitle>{t("percorso.richiesta.next.title")}</SectionTitle>
      {p.closed ? (
        <span className="text-[12.5px] text-muted">{t("percorso.richiesta.next.closed")}</span>
      ) : fwd.length === 0 ? (
        <span className="text-[12.5px] text-muted">{t("percorso.richiesta.next.none")}</span>
      ) : (
        <ul className="m-0 flex list-none flex-col gap-4 p-0">
          {fwd.map((n) => (
            <NextStep key={n.to} n={n} nm={nm} rules={p.triggers.filter((r) => r.phase.toLowerCase() === n.to.toLowerCase())} />
          ))}
        </ul>
      )}
      {others.length > 0 && !p.closed && <span className="text-[12.5px] text-muted">{t("percorso.richiesta.next.others", { names: others.map((o) => nm(o.to)).join(", ") })}</span>}
    </div>
  );
}

function NextStep({ n, nm, rules }: { n: PcNext; nm: Name; rules: PcTrigger[] }) {
  const t = useT();
  const waits = n.waiting.map((w) =>
    w.kind === "reviews"
      ? t(w.who ? "percorso.richiesta.next.waitReviews" : "percorso.richiesta.next.waitReviewsAnon", { count: w.count ?? 0, who: w.who ?? "" })
      : t("percorso.richiesta.next.waitApproval", { who: w.who ?? "" }),
  );
  const needs = [...n.blocked, ...waits];
  return (
    <li className="flex min-w-0 flex-col gap-1.5">
      <span className="flex flex-wrap items-baseline gap-x-3">
        <span className="font-semibold">{t("percorso.richiesta.next.to", { name: nm(n.to) })}</span>
        <span className="text-[12px] text-muted">{n.mine ? t("percorso.richiesta.next.mine") : t("percorso.richiesta.next.notMine")}</span>
      </span>
      {needs.length === 0 ? (
        <span className="text-[13px] text-muted">{t("percorso.richiesta.next.ready")}</span>
      ) : (
        <div className="flex flex-col gap-0.5 text-[13px]">
          <span className="text-muted">{t("percorso.richiesta.next.blockedBy")}</span>
          <ul className="m-0 list-disc pl-5">
            {needs.map((x, i) => (
              <li key={i}>{x}</li>
            ))}
          </ul>
        </div>
      )}
      <div className="flex flex-col gap-0.5 text-[13px]">
        <span className="text-muted">
          <Term k="regola">{t("percorso.richiesta.next.rulesLabel", { phase: nm(n.to) })}</Term>
        </span>
        {rules.length === 0 ? (
          <span>{t("percorso.richiesta.next.noRules")}</span>
        ) : (
          <ul className="m-0 list-disc pl-5">
            {rules.map((r, i) => (
              <li key={i}>
                {ruleText(t, r)}
                {r.when && (
                  <span className="text-muted">
                    {" "}
                    ({t("percorso.richiesta.rule.when", { when: r.when })}
                    {r.when_met != null && <>, {t(r.when_met ? "percorso.richiesta.rule.whenYes" : "percorso.richiesta.rule.whenNo")}</>})
                  </span>
                )}
                {r.action_when && <span className="text-muted"> ({t("percorso.richiesta.rule.perImpact", { when: r.action_when })})</span>}
              </li>
            ))}
          </ul>
        )}
      </div>
    </li>
  );
}
