import { EntitySelect, Select } from "../select";
import { useEffect, useMemo, useState, type FormEvent } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, portfolioApi, type AgendaItem, type BodyInput, type GovBody, type MeetingDetail, type MeetingStatus, type MeetingSummary } from "../api";
import { PageHeader } from "../shell";
import { ErrorNote, SectionTitle, euro } from "../ui";
import { fmtScore } from "./Evaluation";
import { useT } from "../i18n/react";
import type { Key } from "../i18n/core";
import { fmtDate, loc } from "../i18n/format";
import { PortfolioTabs } from "./portafoglio-ui";

// Governance (demand.md §7, D06): organi con presidente, segretario, membri e quorum;
// calendario delle sedute; dettaglio della seduta con ordine del giorno proposto dalle
// approvazioni attive dell'organo, presenze, decisioni e verbale.

const MEETING_STATUS: Record<MeetingStatus, Key> = {
  planned: "lavoro.governance.status.planned",
  convened: "lavoro.governance.status.convened",
  held: "lavoro.governance.status.held",
  minuted: "lavoro.governance.status.minuted",
  cancelled: "lavoro.governance.status.cancelled",
};
const STEPS: MeetingStatus[] = ["planned", "convened", "held", "minuted"];

const OUTCOMES: Record<string, Key> = {
  approved: "lavoro.governance.outcome.approved",
  approved_with_conditions: "lavoro.governance.outcome.approved_with_conditions",
  rejected: "lavoro.governance.outcome.rejected",
  deferred: "lavoro.governance.outcome.deferred",
  informational: "lavoro.governance.outcome.informational",
};

const pad = (n: number) => String(n).padStart(2, "0");
const ymd = (d: Date) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;

export function dayTime(iso: string): string {
  const d = new Date(iso);
  return `${fmtDate(d, { weekday: "short", day: "numeric", month: "short" })} · ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

export function Governance() {
  const t = useT();
  const bodies = useQuery({ queryKey: ["bodies"], queryFn: portfolioApi.bodies });
  const [month, setMonth] = useState(() => {
    const d = new Date();
    return new Date(d.getFullYear(), d.getMonth(), 1);
  });
  const next = new Date(month.getFullYear(), month.getMonth() + 1, 1);
  const meetings = useQuery({ queryKey: ["meetings", ymd(month)], queryFn: () => portfolioApi.meetings(ymd(month), ymd(next)) });
  const upcoming = useQuery({ queryKey: ["meetings", "upcoming"], queryFn: () => portfolioApi.meetings(ymd(new Date())) });
  // Il calendario si apre sul mese corrente; se è vuoto e c'è una seduta in programma più avanti,
  // si apre sul mese di quella (finché la persona non sceglie un mese da sé).
  const [touched, setTouched] = useState(false);
  const nextMeeting = (upcoming.data?.items ?? []).find((m) => m.status !== "cancelled");
  useEffect(() => {
    if (touched || !nextMeeting || !meetings.data || meetings.data.items.length > 0) return;
    const d = new Date(nextMeeting.scheduled_at);
    const target = new Date(d.getFullYear(), d.getMonth(), 1);
    if (target.getTime() > month.getTime()) setMonth(target);
  }, [touched, nextMeeting, meetings.data, month]);
  const [creating, setCreating] = useState(false);
  const [editing, setEditing] = useState<GovBody | "new" | null>(null);
  const manageable = (bodies.data?.items ?? []).filter((b) => b.can_manage && !b.archived);
  const me = useQuery({ queryKey: ["ranking"], queryFn: portfolioApi.ranking }); // can_admin
  const canAdmin = !!me.data?.can_admin;

  return (
    <>
      <PageHeader
        path={t("lavoro.governance.path")}
        title={t("lavoro.governance.title")}
        actions={
          manageable.length > 0 && (
            <button className="btn btn-primary" onClick={() => setCreating((c) => !c)}>
              {creating ? t("lavoro.governance.close") : t("lavoro.governance.newMeeting")}
            </button>
          )
        }
      />
      <PortfolioTabs />
      <div className="flex flex-col gap-7 px-4 pb-10 md:px-7">
        <ErrorNote error={bodies.error ?? meetings.error} />
        {creating && <NewMeeting bodies={manageable} onDone={() => setCreating(false)} />}

        <section className="flex flex-col gap-2">
          <SectionTitle
            aside={
              canAdmin && (
                <button className="text-[12px] text-accent underline" onClick={() => setEditing("new")}>
                  {t("lavoro.governance.newBody")}
                </button>
              )
            }
          >
            {t("lavoro.governance.bodies")}
          </SectionTitle>
          {editing === "new" && <BodyForm onDone={() => setEditing(null)} />}
          <ul className="m-0 flex list-none flex-col p-0">
            {(bodies.data?.items ?? []).map((b) => (
              <li key={b.id} className="flex flex-col gap-1 border-b border-hair py-2.5">
                <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
                  <span className="text-[15px] font-semibold">{b.name}</span>
                  <span className="text-[12px] text-muted">{b.kind_name}</span>
                  {b.archived && <span className="text-[12px] text-muted">{t("lavoro.governance.archived")}</span>}
                  {b.pending > 0 && (
                    <span className="font-mono text-[12px] font-semibold text-accent tabular">
                      {t("lavoro.governance.pending", { count: b.pending })}
                    </span>
                  )}
                  {canAdmin && (
                    <button className="ml-auto text-[12px] text-accent underline" onClick={() => setEditing(editing === b ? null : b)}>
                      {editing === b ? t("lavoro.governance.close") : t("lavoro.governance.edit")}
                    </button>
                  )}
                </div>
                <div className="flex flex-wrap gap-x-5 gap-y-0.5 text-[12.5px]">
                  <span>
                    <span className="text-muted">{t("lavoro.governance.chair")}</span> {b.chair ?? "—"}
                  </span>
                  <span>
                    <span className="text-muted">{t("lavoro.governance.secretary")}</span> {b.secretary ?? "—"}
                  </span>
                  <span>
                    <span className="text-muted">{t("lavoro.governance.members")}</span>{" "}
                    <span className="tabular">
                      {t("lavoro.governance.membersCount", { total: b.effective.length, voting: b.effective.filter((m) => m.voting).length })}
                    </span>
                    {b.groups.length > 0 && <span className="text-muted">{t("lavoro.governance.groupsList", { groups: b.groups.map((g) => g.name).join(", ") })}</span>}
                  </span>
                  <span>
                    <span className="text-muted">{t("lavoro.governance.quorum")}</span> <span className="font-mono tabular">{b.quorum_required}</span>
                    {b.quorum == null && <span className="text-muted">{t("lavoro.governance.majority")}</span>}
                  </span>
                  <span>
                    <span className="text-muted">{t("lavoro.governance.decision")}</span> {b.inline_decision ? t("lavoro.governance.decisionInline") : t("lavoro.governance.decisionAfter")}
                  </span>
                  <span>
                    <span className="text-muted">{t("lavoro.governance.nextMeeting")}</span> {b.next_meeting ? dayTime(b.next_meeting) : "—"}
                  </span>
                </div>
                {editing === b && <BodyForm body={b} onDone={() => setEditing(null)} />}
              </li>
            ))}
          </ul>
        </section>

        <section className="flex flex-col gap-2">
          <SectionTitle>{t("lavoro.governance.upcoming")}</SectionTitle>
          <MeetingList items={(upcoming.data?.items ?? []).filter((m) => m.status !== "cancelled").slice(0, 8)} empty={t("lavoro.governance.noUpcoming")} />
        </section>

        <section className="flex flex-col gap-3">
          <SectionTitle
            aside={
              <span className="flex items-center gap-1">
                <button className="btn px-2 py-0.5" aria-label={t("lavoro.governance.prevMonth")} onClick={() => { setTouched(true); setMonth(new Date(month.getFullYear(), month.getMonth() - 1, 1)); }}>
                  ←
                </button>
                <span className="w-32 text-center text-[13px] font-semibold capitalize">
                  {fmtDate(month, { month: "long", year: "numeric" })}
                </span>
                <button className="btn px-2 py-0.5" aria-label={t("lavoro.governance.nextMonth")} onClick={() => { setTouched(true); setMonth(next); }}>
                  →
                </button>
              </span>
            }
          >
            {t("lavoro.governance.calendar")}
          </SectionTitle>
          <MonthGrid month={month} items={meetings.data?.items ?? []} />
          <MeetingList
            items={meetings.data?.items ?? []}
            empty={nextMeeting ? t("lavoro.governance.noMeetingsNext", { when: dayTime(nextMeeting.scheduled_at) }) : t("lavoro.governance.noMeetings")}
          />
        </section>

      </div>
    </>
  );
}

/** Griglia del mese (da tablet in su): una cella per giorno, le sedute come righe brevi. */
function MonthGrid({ month, items }: { month: Date; items: MeetingSummary[] }) {
  const t = useT();
  const first = (month.getDay() + 6) % 7; // lunedì = 0
  const days = new Date(month.getFullYear(), month.getMonth() + 1, 0).getDate();
  const today = ymd(new Date());
  const cells: (number | null)[] = [...Array(first).fill(null), ...Array.from({ length: days }, (_, i) => i + 1)];
  while (cells.length % 7) cells.push(null);
  const byDay = new Map<number, MeetingSummary[]>();
  for (const m of items) {
    const d = new Date(m.scheduled_at).getDate();
    byDay.set(d, [...(byDay.get(d) ?? []), m]);
  }
  return (
    <div className="hidden grid-cols-7 border-t border-l border-hair md:grid">
      {Array.from({ length: 7 }, (_, i) => fmtDate(new Date(2024, 0, 1 + i), { weekday: "short" })).map((d) => (
        <span key={d} className="label border-r border-b border-hair px-2 py-1 text-muted">
          {d}
        </span>
      ))}
      {cells.map((d, i) => {
        const iso = d ? ymd(new Date(month.getFullYear(), month.getMonth(), d)) : "";
        return (
          <div key={i} className={`min-h-16 border-r border-b border-hair p-1.5 ${i % 7 >= 5 ? "bg-hair/25" : ""}`}>
            {d && (
              <span className={`font-mono text-[11.5px] tabular ${iso === today ? "bg-accent px-1 font-semibold text-accent-fg" : "text-muted"}`}>{d}</span>
            )}
            {(d ? byDay.get(d) ?? [] : []).map((m) => (
              <Link
                key={m.id}
                to="/governance/sedute/$id"
                params={{ id: m.id }}
                className={`mt-1 block truncate border-l-2 pl-1 text-[11.5px] no-underline hover:underline ${
                  m.status === "cancelled" ? "border-muted text-muted line-through" : "border-accent text-fg"
                }`}
                title={`${m.title} · ${t(MEETING_STATUS[m.status])}`}
              >
                <span className="font-mono tabular">{dayTime(m.scheduled_at).split(" · ")[1]}</span> {m.body}
              </Link>
            ))}
          </div>
        );
      })}
    </div>
  );
}

function MeetingList({ items, empty }: { items: MeetingSummary[]; empty: string }) {
  const t = useT();
  if (items.length === 0) return <p className="m-0 text-muted">{empty}</p>;
  return (
    <ul className="m-0 flex list-none flex-col p-0">
      {items.map((m) => (
        <li key={m.id} className="border-b border-hair">
          <Link to="/governance/sedute/$id" params={{ id: m.id }} className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5 py-2 text-fg no-underline hover:bg-sel/50">
            <span className="w-36 shrink-0 font-mono text-[12.5px] tabular">{dayTime(m.scheduled_at)}</span>
            <span className="min-w-0 flex-1">
              <span className="font-semibold">{m.title}</span> <span className="text-[12px] text-muted">{m.body}</span>
            </span>
            <span className="font-mono text-[12px] text-muted tabular">
              {t("lavoro.governance.decidedCount", { decided: m.decided, total: m.items })}
            </span>
            <span className={`w-24 text-right text-[12px] ${m.status === "minuted" ? "font-semibold" : m.status === "cancelled" ? "text-muted" : "text-accent"}`}>
              {t(MEETING_STATUS[m.status])}
            </span>
          </Link>
        </li>
      ))}
    </ul>
  );
}

function NewMeeting({ bodies, onDone }: { bodies: GovBody[]; onDone: () => void }) {
  const t = useT();
  const nav = useNavigate();
  const qc = useQueryClient();
  const [body, setBody] = useState(bodies[0]?.id ?? "");
  const [title, setTitle] = useState("");
  const [when, setWhen] = useState("");
  const [location, setLocation] = useState("");
  const [link, setLink] = useState("");
  const [propose, setPropose] = useState(true);
  const create = useMutation({
    mutationFn: () => portfolioApi.createMeeting({ body_id: body, title, scheduled_at: new Date(when).toISOString(), location, link, propose }),
    onSuccess: (m) => {
      qc.invalidateQueries({ queryKey: ["meetings"] });
      qc.invalidateQueries({ queryKey: ["bodies"] });
      onDone();
      nav({ to: "/governance/sedute/$id", params: { id: m.id } });
    },
  });
  const sel = bodies.find((b) => b.id === body);
  return (
    <form
      className="grid grid-cols-1 gap-3 border-l-2 border-accent pl-3 md:grid-cols-2"
      onSubmit={(e: FormEvent) => {
        e.preventDefault();
        create.mutate();
      }}
    >
      <label className="flex flex-col gap-1">
        <span className="label">{t("lavoro.governance.form.body")}</span>
        <Select className="field" value={body} onChange={(e) => setBody(e.target.value)}>
          {bodies.map((b) => (
            <option key={b.id} value={b.id}>
              {b.name}
            </option>
          ))}
        </Select>
      </label>
      <label className="flex flex-col gap-1">
        <span className="label">{t("lavoro.governance.form.when")}</span>
        <input className="field" type="datetime-local" required value={when} onChange={(e) => setWhen(e.target.value)} />
      </label>
      <label className="flex flex-col gap-1">
        <span className="label">{t("lavoro.governance.form.title")}</span>
        <input className="field" placeholder={sel ? t("lavoro.governance.form.titlePh", { name: sel.name }) : ""} value={title} onChange={(e) => setTitle(e.target.value)} />
      </label>
      <label className="flex flex-col gap-1">
        <span className="label">{t("lavoro.governance.form.location")}</span>
        <input className="field" value={location} onChange={(e) => setLocation(e.target.value)} />
      </label>
      <label className="flex flex-col gap-1">
        <span className="label">{t("lavoro.governance.form.link")}</span>
        <input className="field" type="url" placeholder="https://" value={link} onChange={(e) => setLink(e.target.value)} />
      </label>
      <label className="flex items-center gap-2 self-end pb-2">
        <input type="checkbox" checked={propose} onChange={(e) => setPropose(e.target.checked)} />
        {sel && sel.pending > 0 ? t("lavoro.governance.form.proposePending", { count: sel.pending }) : t("lavoro.governance.form.propose")}
      </label>
      <div className="flex gap-2 md:col-span-2">
        <button className="btn btn-primary" disabled={create.isPending || !body || !when}>
          {t("lavoro.governance.form.schedule")}
        </button>
      </div>
      <div className="md:col-span-2">
        <ErrorNote error={create.error} />
      </div>
    </form>
  );
}

function BodyForm({ body, onDone }: { body?: GovBody; onDone: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const people = useQuery({ queryKey: ["members"], queryFn: api.members });
  const catalog = useQuery({ queryKey: ["bodies"], queryFn: portfolioApi.bodies }).data;
  const kinds = catalog?.kinds ?? {};
  const [groups, setGroups] = useState<Record<string, boolean>>(() => Object.fromEntries((body?.groups ?? []).map((g) => [g.group_id, g.voting])));
  const [name, setName] = useState(body?.name ?? "");
  const [kind, setKind] = useState(body?.kind ?? "steering");
  const [chair, setChair] = useState(body?.chair_id ?? "");
  const [secretary, setSecretary] = useState(body?.secretary_id ?? "");
  const [quorum, setQuorum] = useState(body?.quorum != null ? String(body.quorum) : "");
  const [inline, setInline] = useState(body?.inline_decision ?? true);
  const [members, setMembers] = useState<Record<string, boolean>>(() =>
    Object.fromEntries((body?.members ?? []).map((m) => [m.member_id, m.voting])),
  );
  const save = useMutation({
    mutationFn: () => {
      const input: BodyInput = {
        name,
        kind,
        chair_id: chair,
        secretary_id: secretary || undefined,
        quorum: quorum ? Number(quorum) : null,
        inline_decision: inline,
        members: Object.entries(members).map(([member_id, voting]) => ({ member_id, voting })),
        groups: Object.entries(groups).map(([group_id, voting]) => ({ group_id, voting })),
        archived: body?.archived ?? false,
      };
      return body ? portfolioApi.updateBody(body.id, input) : portfolioApi.createBody(input);
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["bodies"] });
      onDone();
    },
  });
  const list = people.data?.items.filter((m) => m.member_type !== "requester") ?? [];
  return (
    <form
      className="mt-2 flex flex-col gap-3 border-l-2 border-accent pl-3"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      <div className="grid grid-cols-1 gap-3 md:grid-cols-3">
        <label className="flex flex-col gap-1">
          <span className="label">{t("lavoro.governance.body.name")}</span>
          <input className="field" required value={name} onChange={(e) => setName(e.target.value)} />
        </label>
        <label className="flex flex-col gap-1">
          <span className="label">{t("lavoro.governance.body.type")}</span>
          <Select className="field" value={kind} onChange={(e) => setKind(e.target.value)}>
            {Object.entries(kinds).map(([k, v]) => (
              <option key={k} value={k}>
                {v}
              </option>
            ))}
            {!kinds[kind] && <option value={kind}>{kind}</option>}
          </Select>
        </label>
        <label className="flex flex-col gap-1">
          <span className="label">{t("lavoro.governance.body.quorum")}</span>
          <input className="field tabular" inputMode="numeric" value={quorum} onChange={(e) => setQuorum(e.target.value)} />
        </label>
        <label className="flex flex-col gap-1">
          <span className="label">{t("lavoro.governance.chair")}</span>
          <EntitySelect kind="member" filter={(o) => !o.requester} noneLabel="—" className="field" required value={chair} onChange={(v) => setChair(v)} />
        </label>
        <label className="flex flex-col gap-1">
          <span className="label">{t("lavoro.governance.secretary")}</span>
          <EntitySelect kind="member" filter={(o) => !o.requester} noneLabel="—" className="field" value={secretary} onChange={(v) => setSecretary(v)} />
        </label>
      </div>
      <label className="flex items-start gap-2">
        <input type="checkbox" className="mt-1" checked={inline} onChange={(e) => setInline(e.target.checked)} />
        <span className="flex flex-col">
          <span>{t("lavoro.governance.body.inline")}</span>
          <span className="text-[12.5px] text-muted">
            {t("lavoro.governance.body.inlineHint")}
          </span>
        </span>
      </label>
      <fieldset className="m-0 flex flex-col gap-1 border-0 p-0">
        <legend className="label pb-1">{t("lavoro.governance.members")}</legend>
        <div className="grid grid-cols-1 gap-x-6 sm:grid-cols-2 lg:grid-cols-3">
          {list.map((m) => {
            const on = m.id in members || m.id === chair;
            return (
              <div key={m.id} className="flex items-center gap-2 border-b border-hair py-1">
                <label className="flex flex-1 items-center gap-2">
                  <input
                    type="checkbox"
                    checked={on}
                    disabled={m.id === chair}
                    onChange={(e) =>
                      setMembers((s) => {
                        const n = { ...s };
                        if (e.target.checked) n[m.id] = true;
                        else delete n[m.id];
                        return n;
                      })
                    }
                  />
                  {m.display_name}
                </label>
                {on && (
                  <label className="flex items-center gap-1 text-[12px] text-muted">
                    <input
                      type="checkbox"
                      checked={m.id === chair || members[m.id] !== false}
                      disabled={m.id === chair}
                      onChange={(e) => setMembers((s) => ({ ...s, [m.id]: e.target.checked }))}
                    />
                    {t("lavoro.governance.body.votes")}
                  </label>
                )}
              </div>
            );
          })}
        </div>
      </fieldset>
      {(catalog?.groups.length ?? 0) > 0 && (
        <fieldset className="m-0 flex flex-col gap-1 border-0 p-0">
          <legend className="label pb-1">{t("lavoro.governance.body.groups")}</legend>
          <p className="m-0 pb-1 text-[12.5px] text-muted">
            {t("lavoro.governance.body.groupsHint")}
          </p>
          <div className="grid grid-cols-1 gap-x-6 sm:grid-cols-2 lg:grid-cols-3">
            {catalog?.groups.map((g) => {
              const on = g.id in groups;
              return (
                <div key={g.id} className="flex items-center gap-2 border-b border-hair py-1">
                  <label className="flex flex-1 items-center gap-2">
                    <input
                      type="checkbox"
                      checked={on}
                      onChange={(e) =>
                        setGroups((s) => {
                          const n = { ...s };
                          if (e.target.checked) n[g.id] = true;
                          else delete n[g.id];
                          return n;
                        })
                      }
                    />
                    <span>
                      {g.name} <span className="text-[12px] text-muted">({g.members})</span>
                    </span>
                  </label>
                  {on && (
                    <label className="flex items-center gap-1 text-[12px] text-muted">
                      <input type="checkbox" checked={groups[g.id] !== false} onChange={(e) => setGroups((s) => ({ ...s, [g.id]: e.target.checked }))} />
                      {t("lavoro.governance.body.groupVote")}
                    </label>
                  )}
                </div>
              );
            })}
          </div>
        </fieldset>
      )}
      {body && body.effective.some((m) => m.via_group) && (
        <p className="m-0 text-[12.5px] text-muted">
          {t("lavoro.governance.body.sitting", { count: body.effective.length, viaGroup: body.effective.filter((m) => m.via_group).length })}
        </p>
      )}
      <div className="flex gap-2">
        <button className="btn btn-primary" disabled={save.isPending}>
          {body ? t("lavoro.governance.body.save") : t("lavoro.governance.body.create")}
        </button>
        <button type="button" className="btn" onClick={onDone}>
          {t("lavoro.governance.cancel")}
        </button>
      </div>
      <ErrorNote error={save.error} />
    </form>
  );
}

// ---------------------------------------------------------------------------
// Dettaglio della seduta
// ---------------------------------------------------------------------------

export function MeetingPage({ id }: { id: string }) {
  const t = useT();
  const qc = useQueryClient();
  const mt = useQuery({ queryKey: ["meeting", id], queryFn: () => portfolioApi.meeting(id) });
  const set = (m: MeetingDetail) => {
    qc.setQueryData(["meeting", id], m);
    qc.invalidateQueries({ queryKey: ["meetings"] });
    qc.invalidateQueries({ queryKey: ["bodies"] });
    qc.invalidateQueries({ queryKey: ["search"] });
    qc.invalidateQueries({ queryKey: ["inbox"] });
    qc.invalidateQueries({ queryKey: ["element"] });
  };
  const status = useMutation({ mutationFn: (s: MeetingStatus) => portfolioApi.meetingStatus(id, s), onSuccess: set });
  if (mt.error) return <div className="p-4 md:p-7"><ErrorNote error={mt.error} /></div>;
  if (!mt.data) return null;
  const m = mt.data;
  const d = new Date(m.scheduled_at);
  const when = t("lavoro.governance.when", { date: fmtDate(d, { weekday: "long", day: "numeric", month: "long", year: "numeric" }), time: `${pad(d.getHours())}:${pad(d.getMinutes())}` });
  const idx = STEPS.indexOf(m.status);
  const decided = m.items.filter((i) => i.outcome).length;

  return (
    <>
      <PageHeader
        path={t("lavoro.governance.meetingPath", { body: m.body.toLowerCase() })}
        title={m.title}
        actions={
          m.can_manage && (
            <div className="flex flex-wrap gap-2">
              {m.status === "planned" && (
                <button className="btn btn-primary" disabled={status.isPending} onClick={() => status.mutate("convened")}>
                  {t("lavoro.governance.convene")}
                </button>
              )}
              {(m.status === "planned" || m.status === "convened") && (
                <>
                  <button className={`btn ${m.status === "convened" ? "btn-primary" : ""}`} disabled={status.isPending} onClick={() => status.mutate("held")}>
                    {t("lavoro.governance.open")}
                  </button>
                  <button className="btn btn-danger" disabled={status.isPending} onClick={() => status.mutate("cancelled")}>
                    {t("lavoro.governance.cancel")}
                  </button>
                </>
              )}
            </div>
          )
        }
      />
      <div className="flex flex-col gap-1.5 px-4 md:px-7">
        <p className="m-0 flex flex-wrap gap-x-3 text-[13px]">
          <span className="first-letter:uppercase">{when}</span>
          {m.location && <span className="text-muted">{m.location}</span>}
          {m.link && (
            <a href={m.link} target="_blank" rel="noreferrer noopener" className="text-accent">
              {t("lavoro.governance.linkText")}
            </a>
          )}
        </p>
        {m.status === "cancelled" ? (
          <span className="font-semibold text-danger">{t("lavoro.governance.canceledTitle")}</span>
        ) : (
          <span className="flex max-w-xl flex-col gap-1" aria-label={t("lavoro.governance.statusAria", { status: t(MEETING_STATUS[m.status]) })}>
            <span className="flex gap-0.5" aria-hidden="true">
              {STEPS.map((s, i) => (
                <span key={s} className={`h-1.5 flex-1 ${i < idx ? "bg-fg" : i === idx ? "bg-accent" : "bg-hair"}`} />
              ))}
            </span>
            <span className="flex text-[11.5px]">
              {STEPS.map((s, i) => (
                <span key={s} className={`flex-1 ${i === idx ? "font-semibold text-accent" : "text-muted"}`}>
                  {t(MEETING_STATUS[s])}
                </span>
              ))}
            </span>
          </span>
        )}
        <ErrorNote error={status.error} />
      </div>
      <div className="flex flex-wrap gap-x-10 gap-y-7 px-4 py-6 md:px-7">
        <div className="flex min-w-0 flex-[999_1_520px] flex-col gap-6">
          <section className="flex flex-col gap-2">
            <SectionTitle aside={<span className="font-mono text-[12px] text-muted tabular">{t("lavoro.governance.decidedCount", { decided, total: m.items.length })}</span>}>
              {t("lavoro.governance.agenda")}
            </SectionTitle>
            {m.items.length === 0 && <p className="m-0 text-muted">{t("lavoro.governance.noAgenda")}</p>}
            <ol className="m-0 flex list-none flex-col p-0">
              {m.items.map((it, i) => (
                <AgendaRow key={it.element_id} m={m} it={it} n={i + 1} onDone={set} />
              ))}
            </ol>
            {m.can_manage && m.status !== "minuted" && m.status !== "cancelled" && <Proposals m={m} onDone={set} />}
          </section>
        </div>
        <div className="flex min-w-0 flex-[1_1_320px] flex-col gap-6">
          <Attendance m={m} onDone={set} />
          <Minutes m={m} onDone={set} />
        </div>
      </div>
    </>
  );
}

function AgendaRow({ m, it, n, onDone }: { m: MeetingDetail; it: AgendaItem; n: number; onDone: (m: MeetingDetail) => void }) {
  const t = useT();
  const [mode, setMode] = useState<string | null>(null);
  const [note, setNote] = useState("");
  const [conditions, setConditions] = useState("");
  const decide = useMutation({
    mutationFn: (decision: string) => portfolioApi.decide(m.id, it.key, decision, note, conditions),
    onSuccess: (data) => {
      setMode(null);
      onDone(data);
    },
  });
  const remove = useMutation({ mutationFn: () => portfolioApi.removeItem(m.id, it.key), onSuccess: onDone });
  const canDecide = m.can_decide && m.status === "held" && !it.outcome;
  const good = it.outcome?.startsWith("approved");
  return (
    <li className="flex flex-col gap-2 border-b border-hair py-3">
      <div className="flex items-start gap-3">
        <span className="w-6 shrink-0 font-mono text-[15px] font-bold tabular">{n}</span>
        <span className="flex min-w-0 flex-1 flex-col gap-0.5">
          <Link to="/e/$key" params={{ key: it.key }} className="font-semibold text-fg no-underline hover:underline">
            {it.title}
          </Link>
          <span className="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[12px]">
            <span className="font-mono text-accent">{it.key}</span>
            <span className="text-muted">{it.state}</span>
            {it.amount_cents != null && <span className="font-mono tabular">{euro(it.amount_cents)}</span>}
            {it.score != null && <span className="font-mono text-muted tabular">{t("lavoro.governance.score", { score: fmtScore(it.score) })}</span>}
            {it.is_mandatory && <span className="border border-flag px-1 text-[10.5px] font-semibold text-flag">{t("lavoro.governance.mandatory")}</span>}
            {it.step_active && !it.outcome && <span className="text-accent">{t("lavoro.governance.waitingBoard")}</span>}
            {it.proposed && <span className="text-muted">{t("lavoro.governance.proposed")}</span>}
          </span>
        </span>
        {it.outcome ? (
          <span className={`shrink-0 border px-1.5 py-0.5 text-[12px] font-semibold ${good ? "border-fg bg-fg text-bg" : it.outcome === "informational" ? "border-muted text-muted" : "border-danger text-danger"}`}>
            {t(OUTCOMES[it.outcome])}
          </span>
        ) : (
          m.can_manage &&
          m.status !== "minuted" && (
            <button className="shrink-0 text-[12px] text-muted underline hover:text-danger" disabled={remove.isPending} onClick={() => remove.mutate()}>
              {t("lavoro.governance.remove")}
            </button>
          )
        )}
      </div>
      {it.outcome && (it.conditions || it.note) && (
        <p className="m-0 pl-9 text-[12.5px] text-muted">
          {it.conditions && t("lavoro.governance.conditionsIs", { conditions: it.conditions })}
          {it.note}
          {it.decided_by && t("lavoro.governance.recordedBy", { name: it.decided_by })}
        </p>
      )}
      {canDecide && (
        <div className="flex flex-col gap-2 pl-9">
          {!it.step_active && (
            <span className="text-[12px] text-muted">{t("lavoro.governance.noStep")}</span>
          )}
          {!mode ? (
            <div className="flex flex-wrap gap-2">
              <button className="btn btn-primary py-1.5" disabled={decide.isPending || !m.quorum.reached} onClick={() => decide.mutate("approved")}>
                {t("lavoro.governance.approve")}
              </button>
              <button className="btn py-1.5" disabled={!m.quorum.reached} onClick={() => setMode("approved_with_conditions")}>
                {t("lavoro.governance.withConditions")}
              </button>
              <button className="btn py-1.5" disabled={!m.quorum.reached} onClick={() => setMode("deferred")}>
                {t("lavoro.governance.defer")}
              </button>
              <button className="btn btn-danger py-1.5" disabled={!m.quorum.reached} onClick={() => setMode("rejected")}>
                {t("lavoro.governance.reject")}
              </button>
              <button className="btn py-1.5" disabled={decide.isPending} onClick={() => setMode("informational")}>
                {t("lavoro.governance.acknowledge")}
              </button>
              {!m.quorum.reached && <span className="self-center text-[12px] text-danger">{t("lavoro.governance.noQuorum")}</span>}
            </div>
          ) : (
            <form
              className="flex flex-col gap-2"
              onSubmit={(e) => {
                e.preventDefault();
                decide.mutate(mode);
              }}
            >
              {mode === "approved_with_conditions" && (
                <label className="flex flex-col gap-1">
                  <span className="label">{t("lavoro.governance.conditions")}</span>
                  <textarea className="field min-h-14" required value={conditions} onChange={(e) => setConditions(e.target.value)} />
                </label>
              )}
              <label className="flex flex-col gap-1">
                <span className="label">{mode === "rejected" || mode === "deferred" ? t("lavoro.governance.reason") : t("lavoro.governance.notes")}</span>
                <textarea className="field min-h-14" required={mode === "rejected" || mode === "deferred"} value={note} onChange={(e) => setNote(e.target.value)} />
              </label>
              <div className="flex gap-2">
                <button className={`btn ${mode === "rejected" ? "btn-danger" : "btn-primary"}`} disabled={decide.isPending}>
                  {t("lavoro.governance.record", { outcome: t(OUTCOMES[mode] ?? "lavoro.governance.outcome.approved") })}
                </button>
                <button type="button" className="btn" onClick={() => setMode(null)}>
                  {t("lavoro.governance.cancel")}
                </button>
              </div>
            </form>
          )}
          <ErrorNote error={decide.error} />
        </div>
      )}
      <ErrorNote error={remove.error} />
    </li>
  );
}

function Proposals({ m, onDone }: { m: MeetingDetail; onDone: (m: MeetingDetail) => void }) {
  const t = useT();
  const [ref, setRef] = useState("");
  const add = useMutation({
    mutationFn: (key: string) => portfolioApi.addItem(m.id, key),
    onSuccess: (d) => {
      setRef("");
      onDone(d);
    },
  });
  return (
    <div className="flex flex-col gap-2 pt-2">
      {m.proposals.length > 0 && (
        <>
          <span className="label text-muted">{t("lavoro.governance.waitingDecision")}</span>
          <ul className="m-0 flex list-none flex-col p-0">
            {m.proposals.map((p) => (
              <li key={p.element_id} className="flex items-center gap-3 border-b border-hair py-1.5 text-[13px]">
                <span className="font-mono text-[12px] text-accent">{p.key}</span>
                <span className="min-w-0 flex-1 break-words">{p.title}</span>
                <span className="font-mono text-[12px] tabular">{euro(p.amount_cents)}</span>
                <button className="btn py-1" disabled={add.isPending} onClick={() => add.mutate(p.key)}>
                  {t("lavoro.governance.add")}
                </button>
              </li>
            ))}
          </ul>
        </>
      )}
      <form
        className="flex gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          if (ref.trim()) add.mutate(ref.trim());
        }}
      >
        <input className="field max-w-48 font-mono text-[12.5px]" placeholder="DEM-12" aria-label={t("lavoro.governance.addKeyAria")} value={ref} onChange={(e) => setRef(e.target.value)} />
        <button className="btn" disabled={add.isPending || !ref.trim()}>
          {t("lavoro.governance.addItem")}
        </button>
      </form>
      <ErrorNote error={add.error} />
    </div>
  );
}

function Attendance({ m, onDone }: { m: MeetingDetail; onDone: (m: MeetingDetail) => void }) {
  const t = useT();
  const save = useMutation({
    mutationFn: (p: { member_id: string; present: boolean }) => portfolioApi.attendance(m.id, [p]),
    onSuccess: onDone,
  });
  const editable = m.can_manage && (m.status === "planned" || m.status === "convened" || m.status === "held");
  const q = m.quorum;
  return (
    <section className="flex flex-col gap-2">
      <SectionTitle>{t("lavoro.governance.attendance")}</SectionTitle>
      <p className={`m-0 flex items-baseline gap-2 ${q.reached ? "text-ok" : "text-danger"}`}>
        <span className="font-mono text-[22px] font-bold tabular">
          {q.present}/{q.required}
        </span>
        <span className="text-[12.5px]">
          {t("lavoro.governance.quorumLine", { required: q.required, voting: q.voting, status: q.reached ? t("lavoro.governance.quorumReached") : t("lavoro.governance.quorumNot") })}
        </span>
      </p>
      <ul className="m-0 flex list-none flex-col p-0">
        {m.attendance.map((a) => (
          <li key={a.member_id} className="flex items-center gap-2 border-b border-hair py-1.5">
            <label className="flex flex-1 items-center gap-2">
              <input
                type="checkbox"
                checked={a.present}
                disabled={!editable || save.isPending}
                onChange={(e) => save.mutate({ member_id: a.member_id, present: e.target.checked })}
              />
              <span>{a.name}</span>
              <span className="text-[12px] text-muted">
                {a.member_id === m.chair_id ? t("lavoro.governance.roleChair") : a.member_id === m.secretary_id ? t("lavoro.governance.roleSecretary") : ""}
                {!a.voting ? t("lavoro.governance.noVote") : ""}
              </span>
            </label>
            <span className={`text-[12px] ${a.present ? "font-semibold" : "text-muted"}`}>{a.present ? t("lavoro.governance.present") : t("lavoro.governance.absent")}</span>
          </li>
        ))}
      </ul>
      <ErrorNote error={save.error} />
    </section>
  );
}

function Minutes({ m, onDone }: { m: MeetingDetail; onDone: (m: MeetingDetail) => void }) {
  const t = useT();
  const [text, setText] = useState(m.minutes ?? m.minutes_draft);
  const [dirty, setDirty] = useState(false);
  useEffect(() => {
    if (!dirty) setText(m.minutes ?? m.minutes_draft);
  }, [m.minutes, m.minutes_draft, dirty]);
  const save = useMutation({
    mutationFn: () => portfolioApi.saveMinutes(m.id, text),
    onSuccess: (d) => {
      setDirty(false);
      onDone(d);
    },
  });
  const approve = useMutation({
    mutationFn: async () => {
      if (dirty) await portfolioApi.saveMinutes(m.id, text);
      return portfolioApi.approveMinutes(m.id);
    },
    onSuccess: (d) => {
      setDirty(false);
      onDone(d);
    },
  });
  const changedFromDraft = useMemo(() => m.minutes != null && m.minutes !== m.minutes_draft, [m.minutes, m.minutes_draft]);

  if (m.status === "minuted") {
    return (
      <section className="flex flex-col gap-2">
        <SectionTitle aside={<span className="text-[12px] font-semibold">{t("lavoro.governance.minutesApproved")}</span>}>{t("lavoro.governance.minutes")}</SectionTitle>
        <pre className="m-0 font-sans text-[13px] leading-relaxed whitespace-pre-wrap">{m.minutes}</pre>
        <p className="m-0 border-t border-hair pt-2 text-[12px] text-muted">
          {t("lavoro.governance.approvedBy", { name: m.minutes_approved_by ?? "—", date: m.minutes_approved_at ? new Date(m.minutes_approved_at).toLocaleString(loc()) : "—" })}
          <br />
          <span className="font-mono break-all">SHA-256 {m.minutes_hash}</span>
        </p>
      </section>
    );
  }
  const editable = m.can_decide && m.status === "held";
  return (
    <section className="flex flex-col gap-2">
      <SectionTitle aside={changedFromDraft && <span className="text-[12px] text-muted">{t("lavoro.governance.draftEdited")}</span>}>{t("lavoro.governance.minutes")}</SectionTitle>
      {!editable ? (
        <>
          <p className="m-0 text-[12.5px] text-muted">
            {m.status === "cancelled"
              ? t("lavoro.governance.minutesCanceled")
              : t("lavoro.governance.minutesDraftInfo")}
          </p>
          {m.status !== "cancelled" && <pre className="m-0 max-h-72 overflow-auto border-l-2 border-hair pl-3 font-sans text-[12.5px] leading-relaxed whitespace-pre-wrap text-muted">{m.minutes_draft}</pre>}
        </>
      ) : (
        <>
          <textarea
            className="field min-h-[300px] text-[12.5px] leading-relaxed"
            aria-label={t("lavoro.governance.minutesAria")}
            value={text}
            onChange={(e) => {
              setText(e.target.value);
              setDirty(true);
            }}
          />
          <div className="flex flex-wrap gap-2">
            <button className="btn" disabled={save.isPending || !dirty} onClick={() => save.mutate()}>
              {t("lavoro.governance.saveDraft")}
            </button>
            <button
              className="btn"
              onClick={() => {
                setText(m.minutes_draft);
                setDirty(true);
              }}
            >
              {t("lavoro.governance.regenerate")}
            </button>
            {m.can_approve && (
              <button className="btn btn-primary" disabled={approve.isPending} onClick={() => approve.mutate()}>
                {t("lavoro.governance.approveMinutes")}
              </button>
            )}
          </div>
          {!m.can_approve && <p className="m-0 text-[12px] text-muted">{m.chair ? t("lavoro.governance.chairApprovesNamed", { chair: m.chair }) : t("lavoro.governance.chairApproves")}</p>}
          <ErrorNote error={save.error ?? approve.error} />
        </>
      )}
    </section>
  );
}
