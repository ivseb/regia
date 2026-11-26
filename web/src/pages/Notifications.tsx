import { Fragment, useState, type ReactNode } from "react";
import { Link } from "@tanstack/react-router";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { collab, type Notification, type NotificationKind, type NotificationPrefs } from "../api";
import { dafareApi } from "../dafareApi";
import { PageHeader } from "../shell";
import { ElementTitle, ErrorNote, SectionTitle } from "../ui";
import { useT } from "../i18n/react";
import { tr, type Key, type Params } from "../i18n/core";
import { fmtCurrency, fmtDate, fmtTime } from "../i18n/format";
import { ModuleChip, type UiModule } from "../moduleIcon";
import { weekLabel } from "../tempoApi";

// Notifiche in-app (piano, passo 3): elenco per giorno, lettura, preferenze.

export const KIND_LABELS: Record<NotificationKind, { label: Key; hint: Key }> = {
  mention: { label: "lavoro.notifications.kind.mention.label", hint: "lavoro.notifications.kind.mention.hint" },
  comment: { label: "lavoro.notifications.kind.comment.label", hint: "lavoro.notifications.kind.comment.hint" },
  role: { label: "lavoro.notifications.kind.role.label", hint: "lavoro.notifications.kind.role.hint" },
  phase: { label: "lavoro.notifications.kind.phase.label", hint: "lavoro.notifications.kind.phase.hint" },
  approval: { label: "lavoro.notifications.kind.approval.label", hint: "lavoro.notifications.kind.approval.hint" },
  referent: { label: "lavoro.notifications.kind.referent.label", hint: "lavoro.notifications.kind.referent.hint" },
  review: { label: "lavoro.notifications.kind.review.label", hint: "lavoro.notifications.kind.review.hint" },
  sla: { label: "lavoro.notifications.kind.sla.label", hint: "lavoro.notifications.kind.sla.hint" },
  report: { label: "lavoro.notifications.kind.report.label", hint: "lavoro.notifications.kind.report.hint" },
  requirement: { label: "lavoro.notifications.kind.requirement.label", hint: "lavoro.notifications.kind.requirement.hint" },
  // Knowledge (piano, passo 19).
  document: { label: "lavoro.notifications.kind.document.label", hint: "lavoro.notifications.kind.document.hint" },
  vulnerability: { label: "lavoro.notifications.kind.vulnerability.label", hint: "lavoro.notifications.kind.vulnerability.hint" },
  // Budget (replica/budget.md §3): l'etichetta si legge al momento dell'uso, così segue la lingua.
  budget: { label: "budget.notif.label", hint: "budget.notif.hint" },
  risk: { label: "lavoro.notifications.kind.risk.label", hint: "lavoro.notifications.kind.risk.hint" },
  timesheet: { label: "lavoro.notifications.kind.timesheet.label", hint: "lavoro.notifications.kind.timesheet.hint" },
};

const str = (v: unknown) => (typeof v === "string" ? v : "");
const moneyText = (cents: unknown) => fmtCurrency((Number(cents) || 0) / 100, "EUR", { maximumFractionDigits: 0 });

const TONE: Record<string, string> = { b: "font-semibold", d: "font-semibold text-danger", f: "font-semibold text-flag" };

/**
 * Frase del catalogo con pezzi di JSX (`slots`) e con tre segni di enfasi: <b>…</b> grassetto,
 * <d>…</d> grassetto rosso, <f>…</f> grassetto arancio. Come <Trans>, ma con questi segni.
 */
function Rich({ k, params, slots }: { k: Key; params?: Params; slots?: Record<string, ReactNode> }) {
  const t = useT();
  const keep: Params = { ...params };
  for (const name of Object.keys(slots ?? {})) keep[name] = `{${name}}`;
  const fill = (text: string) =>
    text.split(/(\{\w+\})/g).map((p, i) => {
      const m = /^\{(\w+)\}$/.exec(p);
      return m && slots && m[1] in slots ? <Fragment key={i}>{slots[m[1]]}</Fragment> : <Fragment key={i}>{p}</Fragment>;
    });
  return (
    <>
      {t(k, keep)
        .split(/(<[bdf]>.*?<\/[bdf]>)/g)
        .map((part, i) => {
          const m = /^<([bdf])>(.*)<\/\1>$/.exec(part);
          return m ? <span key={i} className={TONE[m[1]]}>{fill(m[2])}</span> : <Fragment key={i}>{fill(part)}</Fragment>;
        })}
    </>
  );
}

/** Testo della notifica (lo stesso delle email, notify.Describe). */
export function Describe({ n }: { n: Notification }) {
  const t = useT();
  const key = n.element_key ? <span className="font-mono text-[12.5px] font-medium text-accent">{n.element_key}</span> : t("lavoro.notifications.anItem");
  const actor = <span className="font-semibold">{n.actor_name}</span>;
  const by = <span className="text-muted"> · {n.actor_name}</span>;
  const p = n.payload;
  const quote = p.quote ? <span className="text-muted">{t("lavoro.notifications.quote", { quote: str(p.quote) })}</span> : null;
  const when = p.due ? t("lavoro.notifications.byDate", { due: str(p.due) }) : "";
  const R = (k: string, params?: Params, slots?: Record<string, ReactNode>) => (
    <Rich k={`lavoro.notifications.${k}` as Key} params={params} slots={{ actor, key, by, quote, ...slots }} />
  );
  const ev = str(p.event);
  switch (n.kind) {
    case "mention":
      return R(p.inline ? "mention.inline" : "mention.plain");
    case "comment":
      switch (ev) {
        case "inline_comment":
        case "inline_reply":
        case "inline_resolved":
        case "inline_reopened":
          return R(`comment.${ev}`);
      }
      return R("comment.plain");
    case "role":
      return R(p.group ? "role.group" : "role.user", { role: str(p.role_name).toLowerCase() });
    case "phase":
      return R("phase", { from: str(p.from), to: str(p.to) });
    case "approval":
      return R("approval", { stage: str(p.stage) });
    case "referent":
      return R("referent");
    case "review":
      switch (ev) {
        case "opened":
        case "submitted":
        case "clarification":
          return R(`review.${ev}`, { team: str(p.team) });
        case "section":
          return R("review.section", { section: str(p.section) });
        case "decided":
          return R("review.decided", { outcome: str(p.outcome) });
      }
      return R("review.plain");
    case "sla":
      switch (ev) {
        case "reminder":
          return R("sla.reminder", { due: str(p.due), team: str(p.team) });
        case "overdue":
          return R("sla.overdue", { due: str(p.due) });
        case "escalation":
          return R("sla.escalation", { count: Number(p.late_days) || 0, team: str(p.team) });
      }
      return R("sla.plain");
    case "report":
      switch (ev) {
        case "due":
        case "overdue":
          return R(`report.${ev}`, { due: str(p.due) });
        case "submitted": {
          const rag = str(p.overall);
          const overall = rag === "green" || rag === "amber" || rag === "red" ? t(`lavoro.notifications.rag.${rag}` as Key) : "";
          return R("report.submitted", { overall });
        }
      }
      return R("report.plain");
    case "requirement":
      switch (ev) {
        case "assigned":
        case "exception_revoked":
          return R(`requirement.${ev}`, { requirement: str(p.requirement) });
        case "to_verify":
          return R("requirement.to_verify", { application: str(p.application) });
        case "verified":
        case "rejected":
          return R(`requirement.${ev}`, { impact: str(p.impact) });
        case "exception_approved":
        case "exception_rejected":
        case "exception_expired":
          return R(`requirement.${ev}`, { requirement: str(p.requirement) });
        case "exception_expiring":
          return R("requirement.exception_expiring", { date: str(p.expires_on) });
      }
      return R("requirement.plain");
    case "document":
      switch (ev) {
        case "review_due":
        case "review_overdue":
        case "reading_overdue":
          return R(`document.${ev}`, { due: str(p.due), version: String(p.version) });
        case "reading_requested":
        case "reading_reminder":
        case "reading_reopened":
          return R(`document.${ev}`, { version: String(p.version), when });
        case "reading_overdue_summary":
          return R("document.reading_overdue_summary", { count: Number(p.missing) || 0 });
      }
      return R("document.plain");
    case "risk":
      switch (ev) {
        case "risk_accepted":
          return R("risk.risk_accepted", { until: str(p.until) });
        case "risk_rejected":
        case "risk_acceptance_expired":
          return R(`risk.${ev}`);
        case "risk_review_due":
        case "risk_treatment_due":
          return R(`risk.${ev}`, { due: str(p.due) });
        case "contract_expiring":
          return R("risk.contract_expiring", { contract: str(p.contract), provider: str(p.provider), date: str(p.end_date) });
        case "incident_classified":
          return R("risk.incident_classified", { outcome: str(p.outcome), scheme: str(p.scheme) });
        case "notification_due":
        case "notification_overdue":
          return R(`risk.${ev}`, { stage: str(p.stage), scheme: str(p.scheme), due: str(p.due) });
      }
      return R("risk.plain");
    case "timesheet": {
      const week = str(p.week) ? weekLabel(str(p.week)) : "";
      switch (ev) {
        case "submitted":
          return R("timesheet.submitted", { member: str(p.member), week, count: Number(p.count) || 1 });
        case "approved":
        case "reopened":
          return R(`timesheet.${ev}`, { week });
        case "rejected":
          return R("timesheet.rejected", { week, comment: str(p.comment) });
      }
      return R("timesheet.plain", { week });
    }
    case "budget":
      return (
        <>
          <span className="font-semibold text-flag">{tr("budget.notif.closing")}</span>{" "}
          {tr("budget.notif.text", { amount: moneyText(p.residual_cents), node: str(p.node), period: str(p.period), count: Number(p.days) || 0 })}
        </>
      );
    case "vulnerability": {
      const count = Number(p.count) || 0;
      const many = t("lavoro.notifications.vuln.count", { count });
      switch (ev) {
        case "vuln_due_soon":
          return R("vuln.vuln_due_soon", { many, due: str(p.due) });
        case "vuln_overdue":
          return R("vuln.vuln_overdue", { many, step: str(p.step), days: Number(p.days) || 0 });
        case "vuln_exception_approved":
        case "vuln_exception_expiring":
          return R(`vuln.${ev}`, { exception: str(p.exception), until: str(p.until) });
        case "vuln_exception_rejected":
        case "vuln_exception_expired":
          return R(`vuln.${ev}`, { exception: str(p.exception) });
      }
      return R("vuln.plain");
    }
  }
  return <>{n.kind}</>;
}

function dayLabel(iso: string): string {
  const d = new Date(iso);
  const today = new Date();
  const start = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
  const diff = Math.round((start(today) - start(d)) / 86_400_000);
  if (diff === 0) return tr("lavoro.notifications.today");
  if (diff === 1) return tr("lavoro.notifications.yesterday");
  return fmtDate(d, { weekday: "long", day: "numeric", month: "long", year: d.getFullYear() === today.getFullYear() ? undefined : "numeric" });
}

/** Contatore delle notifiche non lette (campanella del guscio). */
export function useUnread(enabled: boolean) {
  return useQuery({ queryKey: ["notifications", "count"], queryFn: collab.unread, enabled, refetchInterval: 120_000 });
}

/** Modulo di provenienza di una notifica, ricavato dal tipo di avviso (nessuno per commenti e menzioni). */
function moduleOfKind(kind: NotificationKind): UiModule | undefined {
  switch (kind) {
    case "phase":
    case "approval":
    case "referent":
      return "demand";
    case "review":
      return "arch";
    case "requirement":
    case "risk":
    case "sla":
      return "grc";
    case "document":
      return "knowledge";
    case "vulnerability":
      return "devsecops";
    case "budget":
    case "report":
      return "budget";
    case "timesheet":
      return "work";
    default:
      return undefined;
  }
}

function Row({ n, onOpen }: { n: Notification; onOpen: (n: Notification) => void }) {
  const t = useT();
  const unread = !n.read_at;
  const excerpt = str(n.payload.excerpt);
  const mod = moduleOfKind(n.kind);
  const body = (
    <>
      <span className={`mt-1.5 size-2 shrink-0 ${unread ? "bg-accent" : ""}`} aria-hidden="true" />
      {/* Il testo per i lettori di schermo si aggiunge al nome del link, non lo sostituisce
          (prima si sentiva solo l'etichetta, senza il testo della notifica). */}
      {unread && <span className="sr-only">{t("lavoro.notifications.unreadSr")}</span>}
      {mod && <ModuleChip module={mod} size="sm" className="mt-0.5 shrink-0" />}
      <span className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className={unread ? "" : "text-muted"}><Describe n={n} /></span>
        {n.title && <span className="min-w-0 text-[12.5px] break-words text-muted"><ElementTitle type={n.type} title={n.title} /></span>}
        {excerpt && <span className="min-w-0 text-[12.5px] break-words text-muted">«{excerpt}»</span>}
      </span>
      <time dateTime={n.created_at} className="shrink-0 font-mono text-[11.5px] text-muted tabular">
        {fmtTime(n.created_at)}
      </time>
    </>
  );
  const cls = `flex items-start gap-3 px-4 py-2.5 md:px-7 no-underline text-fg hover:bg-sel/60`;
  return (
    <li className="border-b border-hair">
      {n.kind === "timesheet" ? (
        <Link to="/tempo" search={{ settimana: str(n.payload.week) || undefined, persona: n.payload.event === "submitted" ? str(n.payload.member_id) || undefined : undefined }} className={cls} onClick={() => onOpen(n)}>
          {body}
        </Link>
      ) : n.element_key && n.type === "review" ? (
        <Link to="/valutazioni/$key" params={{ key: n.element_key }} className={cls} onClick={() => onOpen(n)}>
          {body}
        </Link>
      ) : n.element_key && n.kind === "approval" ? (
        <Link to="/e/$key" params={{ key: n.element_key }} search={{ scheda: "approvazioni" }} className={cls} onClick={() => onOpen(n)}>
          {body}
        </Link>
      ) : n.element_key && n.kind === "report" ? (
        <Link to="/e/$key" params={{ key: n.element_key }} search={{ scheda: "stato" }} className={cls} onClick={() => onOpen(n)}>
          {body}
        </Link>
      ) : n.element_key ? (
        <Link
          to="/e/$key"
          params={{ key: n.element_key }}
          hash={n.kind === "mention" || n.kind === "comment" ? "commenti" : "dettaglio"}
          className={cls} onClick={() => onOpen(n)}>
          {body}
        </Link>
      ) : (
        <button className={`${cls} w-full text-left`} onClick={() => onOpen(n)}>
          {body}
        </button>
      )}
    </li>
  );
}

/** Avvisi dello stesso giorno sullo stesso elemento: una riga di riepilogo con Silenzia, i dettagli ripiegati. */
type Group = { id: string; elementId: string | null; key: string; title: string; type: string; items: Notification[] };

function groupByElement(items: Notification[]): Group[] {
  const out: Group[] = [];
  const byKey = new Map<string, Group>();
  for (const n of items) {
    const k = n.element_key || `_${n.id}`;
    let g = byKey.get(k);
    if (!g) {
      g = { id: k, elementId: n.element_id, key: n.element_key, title: n.title, type: n.type, items: [] };
      byKey.set(k, g);
      out.push(g);
    }
    g.items.push(n);
  }
  return out;
}

/** «3 passaggi, ultimo: Accettato» per le fasi; altrimenti il numero di avvisi. */
function groupSummary(g: Group, t: ReturnType<typeof useT>): string {
  const phases = g.items.filter((n) => n.kind === "phase");
  if (phases.length > 1 && phases.length === g.items.length) {
    return t("dafare.act.phases", { count: phases.length, last: str(phases[0].payload.to) });
  }
  return t("dafare.act.group", { count: g.items.length });
}

function GroupRow({ g, silenced, onOpen, onSilence }: { g: Group; silenced: boolean; onOpen: (n: Notification) => void; onSilence: (g: Group, on: boolean) => void }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  if (g.items.length === 1 && !g.key) return <Row n={g.items[0]} onOpen={onOpen} />;
  const single = g.items.length === 1;
  const unread = g.items.some((n) => !n.read_at);
  // Silenzia vale per l'elemento: tace solo gli avvisi informativi (commenti, ruoli, fasi), mai ciò che chiede un'azione.
  const canSilence = !!g.key && !!g.elementId;
  return (
    <li className="border-b border-hair">
      {!single && (
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 px-4 py-2.5 md:px-7">
          <span className={`size-2 shrink-0 ${unread ? "bg-accent" : ""}`} aria-hidden="true" />
          <Link to="/e/$key" params={{ key: g.key }} className="shrink-0 font-mono text-[12.5px] font-medium text-accent no-underline hover:underline">{g.key}</Link>
          <span className="min-w-0 flex-[1_1_200px] break-words font-medium"><ElementTitle type={g.type} title={g.title} /></span>
          <span className="shrink-0 text-[12.5px] text-muted">{groupSummary(g, t)}</span>
          <span className="flex shrink-0 gap-2">
            <button className="btn py-1 text-[12.5px]" aria-expanded={open} onClick={() => setOpen(!open)}>
              {open ? t("dafare.act.hide") : t("dafare.act.show")}
            </button>
            {canSilence && (
              <button
                className="btn btn-quiet py-1 text-[12.5px]"
                aria-label={silenced ? t("dafare.act.unsilenceAria", { key: g.key }) : t("dafare.act.silenceAria", { key: g.key })}
                onClick={() => onSilence(g, !silenced)}
              >
                {silenced ? t("dafare.act.unsilence") : t("dafare.act.silence")}
              </button>
            )}
          </span>
        </div>
      )}
      {(single || open) && (
        <ol className="m-0 list-none p-0">
          {g.items.map((n) => (
            <Row key={n.id} n={n} onOpen={onOpen} />
          ))}
        </ol>
      )}
      {single && canSilence && (
        <div className="flex flex-wrap items-center gap-2 px-4 pb-2 md:px-7">
          <button
            className="btn btn-quiet py-1 text-[12.5px]"
            aria-label={silenced ? t("dafare.act.unsilenceAria", { key: g.key }) : t("dafare.act.silenceAria", { key: g.key })}
            onClick={() => onSilence(g, !silenced)}
          >
            {silenced ? t("dafare.act.unsilence") : t("dafare.act.silence")}
          </button>
          {silenced && <span className="text-[12px] text-muted">{t("dafare.act.silencedHint")}</span>}
        </div>
      )}
      {!single && silenced && <p className="m-0 px-4 pb-2 text-[12px] text-muted md:px-7">{t("dafare.act.silencedHint")}</p>}
    </li>
  );
}

export function Notifications() {
  const t = useT();
  const qc = useQueryClient();
  const [unreadOnly, setUnreadOnly] = useState(false);
  const list = useInfiniteQuery({
    queryKey: ["notifications", "list", unreadOnly],
    queryFn: ({ pageParam }) => collab.notifications(unreadOnly, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next,
  });
  const count = useUnread(true);
  const refresh = () => qc.invalidateQueries({ queryKey: ["notifications"] });
  const read = useMutation({ mutationFn: (id: string) => collab.markRead(id), onSuccess: refresh });
  const readAll = useMutation({ mutationFn: collab.markAllRead, onSuccess: refresh });
  const silenced = useQuery({ queryKey: ["notifications", "silenced"], queryFn: dafareApi.silenced });
  const silence = useMutation({
    mutationFn: ({ g, on }: { g: Group; on: boolean }) => dafareApi.silence(g.key, on),
    onSuccess: () => {
      refresh();
      qc.invalidateQueries({ queryKey: ["todo"] });
    },
  });
  const items = list.data?.pages.flatMap((p) => p.items) ?? [];
  const silencedIds = new Set(silenced.data?.items ?? []);

  const groups: { day: string; groups: Group[] }[] = [];
  const perDay: { day: string; items: Notification[] }[] = [];
  for (const n of items) {
    const day = dayLabel(n.created_at);
    if (perDay.at(-1)?.day !== day) perDay.push({ day, items: [] });
    perDay.at(-1)!.items.push(n);
  }
  for (const d of perDay) groups.push({ day: d.day, groups: groupByElement(d.items) });

  return (
    <>
      <PageHeader
        path={t("dafare.act.path")}
        title={t("dafare.nav.activity")}
        actions={
          <>
            <Link to="/da-fare" className="btn no-underline">{t("dafare.act.todo")}</Link>
            <button className="btn" disabled={readAll.isPending || !count.data?.unread} onClick={() => readAll.mutate()}>
              {t("lavoro.notifications.markAll")}
            </button>
          </>
        }
      />
      <p className="m-0 max-w-3xl px-4 pb-3 text-[13px] text-muted md:px-7">{t("dafare.act.intro")}</p>
      <div role="tablist" aria-label={t("lavoro.notifications.filter")} className="flex gap-6 border-b border-hair px-4 pt-1 md:px-7">
        {[false, true].map((u) => (
          <button
            key={String(u)}
            role="tab"
            aria-selected={unreadOnly === u}
            className={`pb-2.5 ${unreadOnly === u ? "border-b-2 border-fg font-semibold" : "text-muted hover:text-fg"}`}
            onClick={() => setUnreadOnly(u)}
          >
            {u ? t("lavoro.notifications.tabUnread") : t("lavoro.notifications.tabAll")}
            {u && !!count.data?.unread && <span className="ml-1.5 font-mono text-[11.5px] tabular">{count.data.unread >= 100 ? "99+" : count.data.unread}</span>}
          </button>
        ))}
      </div>
      <ErrorNote error={list.error ?? readAll.error ?? silence.error} />
      {groups.map((g) => (
        <section key={g.day} aria-label={g.day}>
          <h2 className="label m-0 border-b-2 border-rule px-4 pt-5 pb-1.5 first-letter:uppercase md:px-7">{g.day}</h2>
          <ol className="m-0 list-none p-0">
            {g.groups.map((x) => (
              <GroupRow
                key={x.id}
                g={x}
                silenced={!!x.elementId && silencedIds.has(x.elementId)}
                onOpen={(n) => !n.read_at && read.mutate(n.id)}
                onSilence={(grp, on) => silence.mutate({ g: grp, on })}
              />
            ))}
          </ol>
        </section>
      ))}
      {list.data && items.length === 0 && (
        <p className="px-4 py-8 text-muted md:px-7">{unreadOnly ? t("lavoro.notifications.emptyUnread") : t("lavoro.notifications.empty")}</p>
      )}
      {list.hasNextPage && (
        <div className="px-4 md:px-7">
          <button className="btn my-4" onClick={() => list.fetchNextPage()} disabled={list.isFetchingNextPage}>
            {list.isFetchingNextPage ? t("lavoro.notifications.loading") : t("lavoro.notifications.more")}
          </button>
        </div>
      )}
      <Preferences />
    </>
  );
}

function Preferences() {
  const t = useT();
  const qc = useQueryClient();
  const prefs = useQuery({ queryKey: ["notification-prefs"], queryFn: collab.prefs });
  const save = useMutation({
    mutationFn: (kinds: NotificationPrefs["kinds"]) => collab.setPrefs(kinds),
    onSuccess: (p) => qc.setQueryData(["notification-prefs"], p),
  });
  if (!prefs.data) return null;
  const data = save.isPending && save.variables ? { ...prefs.data, kinds: save.variables } : prefs.data;
  const toggle = (kind: NotificationKind, field: "in_app" | "email") =>
    save.mutate(data.kinds.map((k) => (k.kind === kind ? { ...k, [field]: !k[field] } : k)));
  return (
    <section className="mt-6 flex max-w-3xl flex-col gap-2 px-4 pb-10 md:px-7" aria-label={t("lavoro.notifications.prefsAria")}>
      <SectionTitle>{t("lavoro.notifications.prefs")}</SectionTitle>
      <p className="m-0 text-[12.5px] text-muted">
        {t("lavoro.notifications.prefsIntro")}
        {!data.email_available && t("lavoro.notifications.noEmail")}
      </p>
      <table className="w-full border-collapse">
        <thead>
          <tr className="label h-8 text-left">
            <th scope="col" className="border-b border-rule pr-3">{t("lavoro.notifications.colEvent")}</th>
            <th scope="col" className="w-16 border-b border-rule text-center">{t("lavoro.notifications.colInApp")}</th>
            <th scope="col" className="w-16 border-b border-rule text-center">{t("lavoro.notifications.colEmail")}</th>
          </tr>
        </thead>
        <tbody>
          {data.kinds.map((k) => (
            <tr key={k.kind} className="border-b border-hair">
              <td className="py-2 pr-3">
                <span className="block font-medium">{KIND_LABELS[k.kind] ? t(KIND_LABELS[k.kind].label) : k.kind}</span>
                <span className="block text-[12px] text-muted">{KIND_LABELS[k.kind] ? t(KIND_LABELS[k.kind].hint) : ""}</span>
              </td>
              <td className="text-center">
                <input
                  type="checkbox"
                  className="size-4 accent-[var(--accent)]"
                  aria-label={t("lavoro.notifications.ariaInApp", { label: KIND_LABELS[k.kind] ? t(KIND_LABELS[k.kind].label) : k.kind })}
                  checked={k.in_app}
                  onChange={() => toggle(k.kind, "in_app")}
                />
              </td>
              <td className="text-center">
                <input
                  type="checkbox"
                  className="size-4 accent-[var(--accent)]"
                  aria-label={t("lavoro.notifications.ariaEmail", { label: KIND_LABELS[k.kind] ? t(KIND_LABELS[k.kind].label) : k.kind })}
                  checked={k.email && data.email_available}
                  disabled={!data.email_available}
                  onChange={() => toggle(k.kind, "email")}
                />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <ErrorNote error={save.error} />
    </section>
  );
}
