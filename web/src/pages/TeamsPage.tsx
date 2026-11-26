// Team e capacità (piano, passo 18; replica/work.md «Tempo e team»): le persone di ogni team
// con ruolo (responsabile o membro), funzione, quota di allocazione e ore a settimana, e le
// assenze semplici. La capacità del team è la somma delle ore per l'allocazione: la leggono il
// carico dei team, il timesheet e il report del tempo.
import { EntitySelect, Select } from "../select";
import { useMemo, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { PageHeader } from "../shell";
import { ErrorNote, date, Loading } from "../ui";
import { Person } from "./work-ui";
import { BoardColumnsEditor, SpaceModelCreate } from "./BoardColumnsEditor";
import { workApi } from "../workApi";
import { TempoTabs } from "./tempo-ui";
import { useT } from "../i18n/react";
import { Trans } from "../i18n/Trans";
import { loc } from "../i18n/format";
import { ABSENCE_KINDS, addDays, isoDay, tempoApi, type Absence, type TeamMember, type TeamView } from "../tempoApi";

const fmtH = (h: number) => h.toLocaleString(loc(), { maximumFractionDigits: 1 });

export function TeamsPage() {
  const t = useT();
  const qc = useQueryClient();
  const teams = useQuery({ queryKey: ["tempo", "teams"], queryFn: tempoApi.teams });
  const members = useQuery({ queryKey: ["members"], queryFn: api.members, staleTime: 300_000 });
  const [error, setError] = useState<unknown>(null);
  const refresh = (r: { items: TeamView[] }) => {
    qc.setQueryData(["tempo", "teams"], r);
    qc.invalidateQueries({ queryKey: ["teams"] });
    qc.invalidateQueries({ queryKey: ["work", "spaces"] });
  };
  const save = useMutation({
    mutationFn: (v: { team: string; m: Parameters<typeof tempoApi.saveTeamMember>[1] }) => tempoApi.saveTeamMember(v.team, v.m),
    onSuccess: (r) => (setError(null), refresh(r)),
    onError: setError,
  });
  const remove = useMutation({
    mutationFn: (v: { team: string; member: string }) => tempoApi.removeTeamMember(v.team, v.member),
    onSuccess: (r) => (setError(null), refresh(r)),
    onError: setError,
  });
  const createSpace = useMutation({
    mutationFn: (v: { team: string; model: string }) => workApi.createSpace(v.team, undefined, v.model),
    onSuccess: () => (setError(null), qc.invalidateQueries({ queryKey: ["tempo", "teams"] }), qc.invalidateQueries({ queryKey: ["work", "spaces"] })),
    onError: setError,
  });
  const people = (members.data?.items ?? []).filter((m) => m.member_type === "standard" || m.member_type === "guest");
  return (
    <>
      <PageHeader
        path={t("lavoro.teamspage.path")}
        title={t("lavoro.teamspage.title")}
        actions={
          <Link to="/configurazione/coinvolgi" className="btn no-underline">
            {t("configurazione.coinvolgi.button")}
          </Link>
        }
      />
      <TempoTabs active="team" />
      <div className="flex max-w-[1080px] flex-col gap-8 px-4 pt-4 pb-12 md:px-7">
        <p className="m-0 max-w-[68ch] text-muted">
          <Trans
            k="lavoro.teamspage.intro"
            slots={{
              link: (
                <Link to="/architettura" search={{ tab: "team" }} className="text-accent underline">
                  {t("lavoro.teamspage.architecture")}
                </Link>
              ),
            }}
          />
        </p>
        <ErrorNote error={teams.error ?? error} />
        {teams.isPending && <Loading className="m-0 text-muted" />}
        {teams.data?.items.length === 0 && <p className="m-0 text-muted">{t("lavoro.teamspage.noTeams")}</p>}
        {teams.data?.items.map((team) => (
          <TeamSection
            key={team.id}
            t={team}
            people={people}
            busy={save.isPending || remove.isPending}
            onSave={(m) => save.mutate({ team: team.id, m })}
            onRemove={(member) => remove.mutate({ team: team.id, member })}
            onCreateSpace={(model) => createSpace.mutate({ team: team.id, model })}
            creating={createSpace.isPending}
          />
        ))}
        <Absences canPickOthers />
      </div>
    </>
  );
}

function TeamSection({ t, people, busy, onSave, onRemove, onCreateSpace, creating }: { t: TeamView; people: { id: string; display_name: string }[]; busy: boolean; onSave: (m: Parameters<typeof tempoApi.saveTeamMember>[1]) => void; onRemove: (member: string) => void; onCreateSpace: (model: string) => void; creating: boolean }) {
  const tr = useT();
  const inTeam = new Set(t.members.map((m) => m.member_id));
  const free = people.filter((p) => !inTeam.has(p.id));
  const [add, setAdd] = useState("");
  const [pct, setPct] = useState("100");
  return (
    <section aria-label={t.name} className="flex flex-col gap-2">
      <div className="flex flex-wrap items-baseline gap-x-4 gap-y-1 border-b-2 border-rule pb-1.5">
        <h2 className="m-0 text-[17px] font-bold">{t.name}</h2>
        {t.space_key && (
          <Link to="/delivery/board" search={{ spazio: t.space_key }} className="font-mono text-[12px] text-accent no-underline hover:underline">
            {t.space_key}
          </Link>
        )}
        <span className="ml-auto font-mono text-[12.5px] tabular">
          {t.members.length ? (
            <>
              <Trans k="lavoro.teamspage.perWeek" slots={{ hours: <strong>{fmtH(t.hours)} h</strong> }} />{" "}
              <span className="text-muted">{tr("lavoro.teamspage.days", { days: fmtH(t.capacity_days) })}</span>
            </>
          ) : (
            <span className="text-muted">{tr("lavoro.teamspage.noPeople")}</span>
          )}
        </span>
      </div>
      {t.members.length > 0 && (
        <div role="table" aria-label={tr("lavoro.teamspage.peopleOf", { team: t.name })} className="flex flex-col">
          <div role="row" className="label hidden grid-cols-[minmax(0,1.6fr)_150px_minmax(0,1fr)_96px_110px_80px_44px] gap-3 py-1 text-muted md:grid">
            <span role="columnheader">{tr("lavoro.teamspage.person")}</span>
            <span role="columnheader">{tr("lavoro.teamspage.role")}</span>
            <span role="columnheader">{tr("lavoro.teamspage.function")}</span>
            <span role="columnheader">{tr("lavoro.teamspage.allocation")}</span>
            <span role="columnheader">{tr("lavoro.teamspage.weeklyHours")}</span>
            <span role="columnheader" className="text-right">
              {tr("lavoro.teamspage.inTeam")}
            </span>
            <span />
          </div>
          {t.members.map((m) => (
            <MemberRow key={m.member_id} m={m} editable={t.can_manage} busy={busy} onSave={(patch) => onSave({ member_id: m.member_id, lead: m.lead, role: m.role ?? "", allocation_pct: m.allocation_pct, ...patch })} onRemove={() => onRemove(m.member_id)} />
          ))}
        </div>
      )}
      {t.space_key && <BoardColumnsEditor spaceKey={t.space_key} teamName={t.name} />}
      {!t.space_key && t.can_manage && <SpaceModelCreate teamId={t.id} busy={creating} onCreate={onCreateSpace} />}
      {t.can_manage && (
        <form
          className="flex flex-wrap items-end gap-2 pt-2"
          onSubmit={(e) => {
            e.preventDefault();
            if (add) {
              onSave({ member_id: add, allocation_pct: Math.max(0, Math.min(100, Number(pct) || 100)) });
              setAdd("");
            }
          }}
        >
          <label className="flex min-w-[200px] flex-1 flex-col gap-1 md:max-w-[320px]">
            <span className="label text-muted">{tr("lavoro.teamspage.addPerson")}</span>
            <Select className="field py-1.5" value={add} onChange={(e) => setAdd(e.target.value)}>
              <option value="">{tr("lavoro.teamspage.choose")}</option>
              {free.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.display_name}
                </option>
              ))}
            </Select>
          </label>
          <label className="flex w-[100px] flex-col gap-1">
            <span className="label text-muted">{tr("lavoro.teamspage.allocationPct")}</span>
            <input className="field py-1.5 font-mono" inputMode="numeric" value={pct} onChange={(e) => setPct(e.target.value)} />
          </label>
          <button className="btn btn-primary" disabled={!add || busy}>
            {tr("lavoro.teamspage.add")}
          </button>
        </form>
      )}
    </section>
  );
}

function MemberRow({ m, editable, busy, onSave, onRemove }: { m: TeamMember; editable: boolean; busy: boolean; onSave: (patch: { lead?: boolean; role?: string; allocation_pct?: number; weekly_hours?: number; clear_hours?: boolean }) => void; onRemove: () => void }) {
  const t = useT();
  const [role, setRole] = useState(m.role ?? "");
  const [pct, setPct] = useState(String(m.allocation_pct));
  const [hours, setHours] = useState(m.weekly_hours != null ? String(m.weekly_hours) : "");
  return (
    <div role="row" className="grid grid-cols-2 items-center gap-x-3 gap-y-1.5 border-b border-hair py-2 md:grid-cols-[minmax(0,1.6fr)_150px_minmax(0,1fr)_96px_110px_80px_44px] md:gap-y-0 md:py-1.5">
      <span role="cell" className="col-span-2 flex min-w-0 items-center gap-2 md:col-span-1">
        <Person name={m.name} />
        <span className="min-w-0">
          <span className="block font-semibold break-words">{m.name}</span>
          {m.absent_now && <span className="text-[11.5px] text-flag">{t("lavoro.teamspage.absentToday")}</span>}
        </span>
      </span>
      <label role="cell" className="flex flex-col gap-0.5 md:block">
        <span className="label text-muted md:hidden">{t("lavoro.teamspage.role")}</span>
        <Select
          className="field py-1 text-[13px]"
          disabled={!editable || busy}
          value={m.lead ? "lead" : "member"}
          aria-label={t("lavoro.teamspage.roleOf", { name: m.name })}
          onChange={(e) => onSave({ lead: e.target.value === "lead" })}
        >
          <option value="member">{t("lavoro.teamspage.roleMember")}</option>
          <option value="lead">{t("lavoro.teamspage.roleLead")}</option>
        </Select>
      </label>
      <label role="cell" className="flex flex-col gap-0.5 md:block">
        <span className="label text-muted md:hidden">{t("lavoro.teamspage.function")}</span>
        <input
          className="field py-1 text-[13px]"
          disabled={!editable}
          maxLength={60}
          placeholder={t("lavoro.teamspage.functionPlaceholder")}
          aria-label={t("lavoro.teamspage.functionOf", { name: m.name })}
          value={role}
          onChange={(e) => setRole(e.target.value)}
          onBlur={() => role.trim() !== (m.role ?? "") && onSave({ role: role.trim() })}
        />
      </label>
      <label role="cell" className="flex flex-col gap-0.5 md:block">
        <span className="label text-muted md:hidden">{t("lavoro.teamspage.allocationPct")}</span>
        <input
          className="field py-1 text-right font-mono text-[13px]"
          disabled={!editable}
          inputMode="numeric"
          aria-label={t("lavoro.teamspage.allocationOf", { name: m.name })}
          value={pct}
          onChange={(e) => setPct(e.target.value)}
          onBlur={() => {
            const n = Math.max(0, Math.min(100, Math.round(Number(pct))));
            if (Number.isNaN(n)) return setPct(String(m.allocation_pct));
            setPct(String(n));
            if (n !== m.allocation_pct) onSave({ allocation_pct: n });
          }}
        />
      </label>
      <label role="cell" className="flex flex-col gap-0.5 md:block">
        <span className="label text-muted md:hidden">{t("lavoro.teamspage.weeklyHours")}</span>
        <input
          className="field py-1 text-right font-mono text-[13px]"
          disabled={!editable}
          inputMode="decimal"
          placeholder={String(m.person_hours)}
          aria-label={t("lavoro.teamspage.hoursOf", { name: m.name, default: m.person_hours })}
          value={hours}
          onChange={(e) => setHours(e.target.value)}
          onBlur={() => {
            const v = hours.trim();
            if (v === "") return m.weekly_hours != null ? onSave({ clear_hours: true }) : undefined;
            const n = Number(v.replace(",", "."));
            if (Number.isNaN(n) || n < 0 || n > 80) return setHours(m.weekly_hours != null ? String(m.weekly_hours) : "");
            if (n !== m.weekly_hours) onSave({ weekly_hours: n });
          }}
        />
      </label>
      <span role="cell" className="text-right font-mono text-[13px] tabular md:block">
        <span className="label mr-2 text-muted md:hidden">{t("lavoro.teamspage.inTeam")}</span>
        <strong>{fmtH(m.hours)} h</strong>
      </span>
      <span role="cell" className="col-span-2 text-right md:col-span-1">
        {editable && (
          <button className="min-h-[36px] px-2 text-[12.5px] text-danger underline disabled:opacity-50" disabled={busy} aria-label={t("lavoro.teamspage.removeFrom", { name: m.name })} onClick={onRemove}>
            {t("lavoro.teamspage.remove")}
          </button>
        )}
      </span>
    </div>
  );
}

// ---------------------------------------------------------------------
// Assenze

function Absences({ canPickOthers }: { canPickOthers: boolean }) {
  const t = useT();
  const qc = useQueryClient();
  const today = isoDay(new Date());
  const abs = useQuery({ queryKey: ["tempo", "absences"], queryFn: () => tempoApi.absences({ from: addDays(today, -7), to: addDays(today, 120) }) });
  const [who, setWho] = useState("");
  const [from, setFrom] = useState(today);
  const [to, setTo] = useState("");
  const [kind, setKind] = useState("ferie");
  const [hours, setHours] = useState("");
  const [note, setNote] = useState("");
  const add = useMutation({
    mutationFn: () =>
      tempoApi.createAbsence({ member_id: who || undefined, from, to: to || from, kind, hours_per_day: hours.trim() ? Number(hours.replace(",", ".")) : null, note }),
    onSuccess: () => {
      setNote("");
      setHours("");
      setTo("");
      qc.invalidateQueries({ queryKey: ["tempo"] });
      qc.invalidateQueries({ queryKey: ["teams"] });
    },
  });
  const del = useMutation({ mutationFn: (a: Absence) => tempoApi.deleteAbsence(a.id), onSuccess: () => qc.invalidateQueries({ queryKey: ["tempo"] }) });
  const byMember = useMemo(() => abs.data?.items ?? [], [abs.data]);
  return (
    <section aria-label={t("lavoro.teamspage.absences")} className="flex flex-col gap-3">
      <h2 className="m-0 border-b-2 border-rule pb-1.5 text-[17px] font-bold">{t("lavoro.teamspage.absences")}</h2>
      <p className="m-0 max-w-[68ch] text-[12.5px] text-muted">
        {t("lavoro.teamspage.absencesHelp")}
      </p>
      <form
        className="grid grid-cols-2 items-end gap-2 md:grid-cols-[minmax(0,1.4fr)_150px_150px_130px_90px_minmax(0,1.2fr)_auto]"
        onSubmit={(e) => {
          e.preventDefault();
          add.mutate();
        }}
      >
        <label className="col-span-2 flex flex-col gap-1 md:col-span-1">
          <span className="label text-muted">{t("lavoro.teamspage.absPerson")}</span>
          <EntitySelect kind="member" className="field py-1.5" noneLabel={t("lavoro.teamspage.absMe")} filter={(o) => !o.requester} disabled={!canPickOthers} value={who} onChange={setWho} />
        </label>
        <label className="flex flex-col gap-1">
          <span className="label text-muted">{t("lavoro.teamspage.absFrom")}</span>
          <input type="date" required className="field py-1.5" value={from} onChange={(e) => setFrom(e.target.value)} />
        </label>
        <label className="flex flex-col gap-1">
          <span className="label text-muted">{t("lavoro.teamspage.absTo")}</span>
          <input type="date" className="field py-1.5" min={from} value={to} onChange={(e) => setTo(e.target.value)} />
        </label>
        <label className="flex flex-col gap-1">
          <span className="label text-muted">{t("lavoro.teamspage.absKind")}</span>
          <Select className="field py-1.5" value={kind} onChange={(e) => setKind(e.target.value)}>
            {Object.entries(ABSENCE_KINDS).map(([k, l]) => (
              <option key={k} value={k}>
                {l}
              </option>
            ))}
          </Select>
        </label>
        <label className="flex flex-col gap-1">
          <span className="label text-muted">{t("lavoro.teamspage.absHours")}</span>
          <input className="field py-1.5 font-mono" inputMode="decimal" placeholder={t("lavoro.teamspage.absAll")} value={hours} onChange={(e) => setHours(e.target.value)} />
        </label>
        <label className="col-span-2 flex flex-col gap-1 md:col-span-1">
          <span className="label text-muted">{t("lavoro.teamspage.absNote")}</span>
          <input className="field py-1.5" maxLength={300} value={note} onChange={(e) => setNote(e.target.value)} />
        </label>
        <button className="btn btn-primary col-span-2 md:col-span-1" disabled={add.isPending}>
          {t("lavoro.teamspage.add")}
        </button>
      </form>
      <ErrorNote error={add.error ?? del.error ?? abs.error} />
      <ul className="m-0 flex list-none flex-col p-0">
        {byMember.length === 0 && !abs.isPending && <li className="py-2 text-muted">{t("lavoro.teamspage.absNone")}</li>}
        {byMember.map((a) => (
          <li key={a.id} className="flex flex-wrap items-center gap-x-4 gap-y-0.5 border-b border-hair py-2">
            <span className="min-w-[140px] font-semibold">{a.member}</span>
            <span className="font-mono text-[12.5px] tabular">
              {date(a.from)}
              {a.to !== a.from ? ` – ${date(a.to)}` : ""}
            </span>
            <span className="text-[12.5px]">
              {ABSENCE_KINDS[a.kind] ?? a.kind}
              {a.hours_per_day ? t("lavoro.teamspage.absHoursPerDay", { hours: fmtH(a.hours_per_day) }) : ""}
            </span>
            {a.note && <span className="min-w-0 flex-1 break-words text-[12.5px] text-muted">{a.note}</span>}
            <button className="ml-auto min-h-[36px] px-2 text-[12.5px] text-danger underline" onClick={() => del.mutate(a)}>
              {t("lavoro.teamspage.absDelete")}
            </button>
          </li>
        ))}
      </ul>
    </section>
  );
}
