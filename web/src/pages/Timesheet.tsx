// Timesheet settimanale (piano, passo 18; replica/work.md «Tempo e team»): una griglia di
// giorni × elementi con inserimento da tastiera (frecce, Invio, Tab), totali di riga e di
// colonna, durate scritte a mano ("1h30", "1,5", "45m"). Sopra le ore di sempre: ogni cella
// è la somma delle registrazioni di quel giorno. Invio e approvazione sono facoltativi (regola
// del tenant); i periodi chiusi non si scrivono. Sotto i 768 px un giorno alla volta.
import { EntitySelect, Select } from "../select";
import { memo, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { PageHeader } from "../shell";
import { ErrorNote, date, Loading } from "../ui";
import { useNarrow } from "./timeline";
import { Dialog, TypeMark } from "./work-ui";
import { LoadBar, TempoTabs } from "./tempo-ui";
import { useT } from "../i18n/react";
import { Trans } from "../i18n/Trans";
import {
  ABSENCE_KINDS,
  addDays,
  cellText,
  dayLabel,
  mondayOf,
  parseMinutes,
  tempoApi,
  totalText,
  weekLabel,
  type PendingWeek,
  type TSRow,
  type TSSuggestion,
  type Timesheet as TS,
} from "../tempoApi";

export type TimesheetSearch = { settimana?: string; persona?: string };

export function Timesheet({ search, onSearch }: { search: TimesheetSearch; onSearch: (s: TimesheetSearch) => void }) {
  const t = useT();
  const qc = useQueryClient();
  const narrow = useNarrow();
  const week = mondayOf(search.settimana);
  const person = search.persona;
  const key = ["tempo", "ts", week, person ?? ""] as const;
  const ts = useQuery({ queryKey: key, queryFn: () => tempoApi.timesheet(week, person), placeholderData: (p) => p });
  const pending = useQuery({ queryKey: ["tempo", "pending"], queryFn: tempoApi.pending, staleTime: 30_000 });
  const [extra, setExtra] = useState<TSSuggestion[]>([]);
  const [error, setError] = useState<unknown>(null);
  const [rules, setRules] = useState(false);
  const [rejecting, setRejecting] = useState<{ member: string; week: string; name: string } | null>(null);
  const [day, setDay] = useState(() => Math.min(6, Math.max(0, (new Date().getDay() + 6) % 7)));
  useEffect(() => setExtra([]), [week, person]);
  const d = ts.data;

  // Le scritture vanno in coda, una dopo l'altra: l'ultima risposta è sempre quella giusta.
  const chain = useRef<Promise<unknown>>(Promise.resolve());
  const commit = useCallback(
    (row: TSRow, dayIdx: number, text: string) => {
      const date = addDays(week, dayIdx);
      setError(null);
      chain.current = chain.current
        .then(() => tempoApi.setCells(week, [{ element: row.key, date, duration: text }]))
        .then((fresh) => {
          qc.setQueryData(key, fresh);
          qc.invalidateQueries({ queryKey: ["work", "time"] });
        })
        .catch((e) => {
          setError(e);
          qc.invalidateQueries({ queryKey: key });
        });
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [week, qc, person],
  );

  const rows = useMemo<TSRow[]>(() => {
    const have = new Set((d?.rows ?? []).map((r) => r.id));
    const extras: TSRow[] = extra
      .filter((e) => !have.has(e.id))
      .map((e) => ({ id: e.id, key: e.key, title: e.title, type: e.type, state: e.state, category: "", days: [0, 0, 0, 0, 0, 0, 0], total: 0, estimate: null, logged: 0, assigned: false }));
    return [...(d?.rows ?? []), ...extras];
  }, [d?.rows, extra]);
  const addRow = (s: TSSuggestion) => setExtra((x) => (x.some((e) => e.id === s.id) ? x : [...x, s]));

  const copy = useMutation({
    mutationFn: () => tempoApi.copyWeek(week),
    onSuccess: (r) => {
      qc.setQueryData(key, r.timesheet);
      qc.invalidateQueries({ queryKey: ["work", "time"] });
    },
    onError: setError,
  });
  const after = (fresh: TS) => {
    qc.setQueryData(key, fresh);
    qc.invalidateQueries({ queryKey: ["tempo", "pending"] });
  };
  const submit = useMutation({ mutationFn: () => tempoApi.submit(week), onSuccess: after, onError: setError });
  const withdraw = useMutation({ mutationFn: () => tempoApi.withdraw(week), onSuccess: after, onError: setError });
  const decide = useMutation({
    mutationFn: (v: { member: string; week: string; approve: boolean; comment?: string }) => tempoApi.decide(v.member, v.week, v.approve, v.comment),
    onSuccess: (fresh) => {
      qc.invalidateQueries({ queryKey: ["tempo"] });
      qc.setQueryData(["tempo", "ts", fresh.week, fresh.member_id], fresh);
      setRejecting(null);
    },
    onError: setError,
  });
  const reopen = useMutation({
    mutationFn: () => tempoApi.reopen(d!.member_id, week),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["tempo"] }),
    onError: setError,
  });

  const go = (w: string) => onSearch({ settimana: w === mondayOf() ? undefined : w, persona: person });
  const thisWeek = week === mondayOf();
  const editable = !!d?.editable && !person;
  const canSubmit = !!d && d.mine && d.settings.require_approval && (d.status === "draft" || d.status === "rejected");

  return (
    <>
      <PageHeader
        path={t("lavoro.timesheet.path")}
        title={person && d ? t("lavoro.timesheet.titleOf", { name: d.member }) : t("lavoro.timesheet.title")}
        actions={
          d?.settings.can_edit && (
            <button className="btn" onClick={() => setRules(true)}>
              {t("lavoro.timesheet.rules")}
            </button>
          )
        }
      />
      <TempoTabs active="timesheet" />
      <div className="flex max-w-[1180px] flex-col gap-4 px-4 pt-4 pb-12 md:px-7">
        {person && (
          <p className="m-0 flex flex-wrap items-center gap-3 text-[13px]">
            <Link to="/tempo" search={{ settimana: search.settimana }} className="text-accent underline">
              {t("lavoro.timesheet.backToMine")}
            </Link>
            <span className="text-muted">{t("lavoro.timesheet.viewingOther", { name: d?.member ?? "…" })}</span>
          </p>
        )}
        {!person && (pending.data?.items.length ?? 0) > 0 && (
          <PendingPanel items={pending.data!.items} busy={decide.isPending} onOpen={(p) => onSearch({ settimana: p.week, persona: p.member_id })}
            onApprove={(p) => decide.mutate({ member: p.member_id, week: p.week, approve: true })}
            onReject={(p) => setRejecting({ member: p.member_id, week: p.week, name: p.member })} />
        )}

        <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
          <div className="flex items-center gap-1" role="group" aria-label={t("lavoro.timesheet.week")}>
            <button className="btn px-2.5 py-1.5" aria-label={t("lavoro.timesheet.prevWeek")} onClick={() => go(addDays(week, -7))}>
              ←
            </button>
            <input
              type="date"
              aria-label={t("lavoro.timesheet.goToWeek")}
              className="field w-[150px] py-1.5"
              value={week}
              onChange={(e) => e.target.value && go(mondayOf(e.target.value))}
            />
            <button className="btn px-2.5 py-1.5" aria-label={t("lavoro.timesheet.nextWeek")} onClick={() => go(addDays(week, 7))}>
              →
            </button>
          </div>
          <h2 className="m-0 text-[17px] font-bold tabular">{weekLabel(week)}</h2>
          {!thisWeek && (
            <button className="text-[12.5px] text-accent underline" onClick={() => go(mondayOf())}>
              {t("lavoro.timesheet.thisWeek")}
            </button>
          )}
          {d && (
            <span className={`border px-2 py-0.5 text-[11.5px] font-semibold ${d.status === "approved" ? "border-ok text-ok" : d.status === "rejected" ? "border-danger text-danger" : d.status === "submitted" ? "border-accent text-accent" : "border-hair text-muted"}`}>
              {d.settings.require_approval || d.status !== "draft" ? t(`lavoro.timesheet.status.${d.status}`) : t("lavoro.timesheet.saved")}
            </span>
          )}
          <span className="ml-auto flex flex-wrap items-center gap-2">
            {editable && (
              <CopyMenu
                busy={copy.isPending}
                disabled={!d || d.prev_total === 0}
                onRows={() => (d?.previous ?? []).forEach(addRow)}
                onAll={() => copy.mutate()}
                hint={d ? t("lavoro.timesheet.lastWeekTotal", { time: totalText(d.prev_total) }) : ""}
              />
            )}
            {canSubmit && d && (
              <button className="btn btn-primary" disabled={submit.isPending || d.total === 0} onClick={() => submit.mutate()}>
                {submit.isPending ? t("lavoro.timesheet.sending") : t("lavoro.timesheet.submitTo", { who: d.submit_to.join(", ") || t("lavoro.timesheet.approverFallback") })}
              </button>
            )}
            {d?.mine && d.status === "submitted" && (
              <button className="btn" disabled={withdraw.isPending} onClick={() => withdraw.mutate()}>
                {t("lavoro.timesheet.withdraw")}
              </button>
            )}
            {d?.can_decide && (
              <>
                <button className="btn btn-primary" disabled={decide.isPending} onClick={() => decide.mutate({ member: d.member_id, week, approve: true })}>
                  {t("lavoro.timesheet.approve")}
                </button>
                <button className="btn btn-danger" onClick={() => setRejecting({ member: d.member_id, week, name: d.member })}>
                  {t("lavoro.timesheet.reject")}
                </button>
              </>
            )}
            {d && !d.mine && d.status === "approved" && d.settings.can_edit && (
              <button className="btn" onClick={() => reopen.mutate()}>
                {t("lavoro.timesheet.reopen")}
              </button>
            )}
          </span>
        </div>

        {d?.status === "rejected" && d.comment && (
          <p role="status" className="m-0 border-2 border-danger px-3 py-2 text-[13px]">
            <Trans
              k="lavoro.timesheet.rejectedBody"
              params={{ comment: d.comment }}
              slots={{ head: <strong>{d.decided_by ? t("lavoro.timesheet.rejectedHeadBy", { name: d.decided_by }) : t("lavoro.timesheet.rejectedHead")}</strong> }}
            />
          </p>
        )}
        {d?.status === "submitted" && d.mine && (
          <p role="status" className="m-0 border border-hair px-3 py-2 text-[13px] text-muted">
            {t("lavoro.timesheet.waiting", { who: d.submit_to.join(", ") || t("lavoro.timesheet.waitingFallback") })}
          </p>
        )}
        {d?.status === "approved" && (
          <p role="status" className="m-0 border border-ok px-3 py-2 text-[13px]">
            {d.decided_by ? t("lavoro.timesheet.approvedBy", { name: d.decided_by }) : t("lavoro.timesheet.approved")}
          </p>
        )}
        {d && d.absences.length > 0 && (
          <p className="m-0 text-[12.5px] text-muted">
            {t("lavoro.timesheet.absencesWeek", {
              list: d.absences
                .map((a) => {
                  const kind = (ABSENCE_KINDS[a.kind] ?? a.kind).toLowerCase();
                  const span = a.to !== a.from ? t("lavoro.timesheet.absRange", { kind, from: date(a.from), to: date(a.to) }) : t("lavoro.timesheet.absFrom", { kind, from: date(a.from) });
                  return span + (a.hours_per_day ? t("lavoro.timesheet.absHours", { hours: a.hours_per_day }) : "");
                })
                .join("; "),
            })}
          </p>
        )}
        <ErrorNote error={ts.error ?? error} />
        {ts.isPending && <Loading className="m-0 text-muted" />}

        {d && narrow && (
          <DayView d={d} rows={rows} day={day} onDay={setDay} editable={editable} onCommit={commit} />
        )}
        {d && !narrow && <Grid d={d} rows={rows} editable={editable} onCommit={commit} />}

        {d && editable && (
          <AddPanel d={d} rows={rows} onAdd={addRow} />
        )}
        {d && d.mine && d.settings.require_approval === false && (
          <p className="m-0 text-[12px] text-muted">{t("lavoro.timesheet.autoSave")}</p>
        )}
      </div>
      {rules && d && <RulesDialog s={d.settings} onClose={() => setRules(false)} />}
      {rejecting && (
        <Dialog title={t("lavoro.timesheet.rejectTitle", { name: rejecting.name })} onClose={() => setRejecting(null)}>
          <RejectForm busy={decide.isPending} onSubmit={(comment) => decide.mutate({ member: rejecting.member, week: rejecting.week, approve: false, comment })} />
        </Dialog>
      )}
    </>
  );
}

// ---------------------------------------------------------------------
// Cella

type CommitFn = (row: TSRow, dayIdx: number, text: string) => void;

/** Cella: mostra "1h30", si modifica con la tastiera e si salva quando si esce o con Invio. */
const CellInput = memo(function CellInput({
  row,
  dayIdx,
  minutes,
  disabled,
  title,
  big,
  onCommit,
  onMove,
}: {
  row: TSRow;
  dayIdx: number;
  minutes: number;
  disabled: boolean;
  title: string;
  big?: boolean;
  onCommit: CommitFn;
  onMove?: (dr: number, dd: number) => void;
}) {
  const t = useT();
  const [text, setText] = useState(cellText(minutes));
  const [focus, setFocus] = useState(false);
  const [bad, setBad] = useState(false);
  useEffect(() => {
    if (!focus) setText(cellText(minutes));
  }, [minutes, focus]);
  const finish = () => {
    const m = parseMinutes(text);
    if (m === null) {
      setBad(true);
      return false;
    }
    setBad(false);
    if (m !== minutes) onCommit(row, dayIdx, text.trim() || "0");
    return true;
  };
  return (
    <input
      data-cell={`${row.id}:${dayIdx}`}
      aria-label={title}
      aria-invalid={bad || undefined}
      title={bad ? t("lavoro.timesheet.badDuration") : title}
      disabled={disabled}
      inputMode="text"
      autoComplete="off"
      value={text}
      placeholder="·"
      onFocus={(e) => {
        setFocus(true);
        e.currentTarget.select();
      }}
      onBlur={() => {
        setFocus(false);
        if (!finish()) setText(cellText(minutes));
        setBad(false);
      }}
      onChange={(e) => setText(e.target.value)}
      onKeyDown={(e) => {
        const el = e.currentTarget;
        if (e.key === "Escape") {
          setText(cellText(minutes));
          el.blur();
        } else if (e.key === "Enter" || e.key === "ArrowDown") {
          e.preventDefault();
          if (finish()) onMove?.(1, 0);
        } else if (e.key === "ArrowUp") {
          e.preventDefault();
          if (finish()) onMove?.(-1, 0);
        } else if (e.key === "ArrowRight" && el.selectionStart === el.value.length && el.selectionEnd === el.value.length) {
          e.preventDefault();
          if (finish()) onMove?.(0, 1);
        } else if (e.key === "ArrowLeft" && el.selectionStart === 0 && el.selectionEnd === 0) {
          e.preventDefault();
          if (finish()) onMove?.(0, -1);
        }
      }}
      className={`box-border w-full border bg-transparent text-right font-mono tabular outline-none focus:border-accent focus:bg-field ${big ? "min-h-[44px] px-3 text-[16px]" : "px-1.5 py-1.5 text-[13px]"} ${bad ? "border-danger" : "border-transparent hover:border-hair"} disabled:cursor-not-allowed disabled:opacity-60`}
    />
  );
});

/** Sposta il fuoco di righe e giorni, saltando le celle disattivate. */
function useMover(rows: TSRow[], skip: (r: TSRow, d: number) => boolean, root: React.RefObject<HTMLElement | null>) {
  return useCallback(
    (row: TSRow, dayIdx: number, dr: number, dd: number) => {
      let r = rows.findIndex((x) => x.id === row.id) + dr;
      let d = dayIdx + dd;
      for (let guard = 0; guard < 60; guard++) {
        if (r < 0 || r >= rows.length || d < 0 || d > 6) return;
        if (!skip(rows[r], d)) {
          root.current?.querySelector<HTMLInputElement>(`[data-cell="${rows[r].id}:${d}"]`)?.focus();
          return;
        }
        r += dr;
        d += dd;
      }
    },
    [rows, skip, root],
  );
}

// ---------------------------------------------------------------------
// Griglia (desktop)

function Grid({ d, rows, editable, onCommit }: { d: TS; rows: TSRow[]; editable: boolean; onCommit: CommitFn }) {
  const t = useT();
  const root = useRef<HTMLDivElement>(null);
  const blocked = useCallback((_r: TSRow, i: number) => !editable || d.days[i].locked || d.days[i].future, [d.days, editable]);
  const move = useMover(rows, blocked, root);
  return (
    <div ref={root} className="overflow-x-auto">
      <table className="w-full min-w-[820px] border-collapse text-[13px]">
        <thead>
          <tr className="border-b-2 border-rule text-left align-bottom">
            <th scope="col" className="label py-1.5 pr-3">
              {t("lavoro.timesheet.item")}
            </th>
            {d.days.map((x) => {
              const l = dayLabel(x.date);
              return (
                <th key={x.date} scope="col" className={`w-[84px] px-1 py-1.5 text-right ${!x.workday || x.holiday ? "bg-hair/30" : ""}`}>
                  <span className="label block">{l.name}</span>
                  <span className="block font-mono text-[12px] font-normal tabular text-muted">{l.num} {l.month}</span>
                  {x.locked && <span className="block text-[10.5px] font-normal text-muted">{t("lavoro.timesheet.locked")}</span>}
                  {x.holiday && <span className="block text-[10.5px] font-normal text-muted">{t("lavoro.timesheet.holiday")}</span>}
                  {x.absence > 0 && !x.holiday && <span className="block text-[10.5px] font-normal text-flag">{t("lavoro.timesheet.absent", { time: totalText(x.absence) })}</span>}
                </th>
              );
            })}
            <th scope="col" className="label w-[84px] py-1.5 pl-2 text-right">
              {t("lavoro.timesheet.total")}
            </th>
          </tr>
        </thead>
        <tbody>
          {rows.length === 0 && (
            <tr>
              <td colSpan={9} className="py-6 text-muted">
                {editable ? t("lavoro.timesheet.noRowsEditable") : t("lavoro.timesheet.noHours")}
              </td>
            </tr>
          )}
          {rows.map((r) => (
            <tr key={r.id} className="border-b border-hair hover:bg-sel/40">
              <th scope="row" className="min-w-0 py-1 pr-3 text-left font-normal">
                <span className="flex min-w-0 items-center gap-2">
                  <TypeMark type={r.type} />
                  <Link to="/e/$key" params={{ key: r.key }} className="shrink-0 font-mono text-[12px] text-accent no-underline hover:underline">
                    {r.key}
                  </Link>
                  <span className="min-w-0 break-words" title={r.title}>
                    {r.title}
                  </span>
                </span>
                {(r.estimate != null || r.logged > 0) && (
                  <span className="block pl-[26px] font-mono text-[11px] text-muted tabular">
                    {r.estimate != null ? t("lavoro.timesheet.estimatePrefix", { time: totalText(r.estimate) }) : ""}
                    {t("lavoro.timesheet.loggedLabel", { time: totalText(r.logged) })}
                    {r.estimate != null && r.logged > r.estimate ? t("lavoro.timesheet.overEstimate") : ""}
                  </span>
                )}
              </th>
              {d.days.map((x, i) => (
                <td key={x.date} className={`px-0.5 ${r.days[i] > 0 ? "tinta m-work" : !x.workday || x.holiday ? "bg-hair/30" : ""}`}>
                  <CellInput
                    row={r}
                    dayIdx={i}
                    minutes={r.days[i]}
                    disabled={blocked(r, i)}
                    title={`${r.key}, ${dayLabel(x.date).name} ${dayLabel(x.date).num}`}
                    onCommit={onCommit}
                    onMove={(dr, dd) => move(r, i, dr, dd)}
                  />
                </td>
              ))}
              <td className="pl-2 text-right font-mono tabular font-semibold">{totalText(r.total)}</td>
            </tr>
          ))}
        </tbody>
        <tfoot>
          <tr className="border-t-2 border-rule align-top">
            <th scope="row" className="label py-2 pr-3 text-left">
              {t("lavoro.timesheet.total")}
            </th>
            {d.days.map((x) => (
              <td key={x.date} className={`px-1 py-2 text-right ${!x.workday || x.holiday ? "bg-hair/30" : ""}`}>
                <span className={`block font-mono tabular font-semibold ${x.total > x.capacity && x.capacity > 0 ? "text-flag" : ""}`}>{totalText(x.total)}</span>
                {x.capacity > 0 && <span className="block font-mono text-[11px] text-muted tabular">{t("lavoro.timesheet.of", { capacity: totalText(x.capacity) })}</span>}
              </td>
            ))}
            <td className="py-2 pl-2 text-right">
              <span className="block font-mono tabular font-semibold">{totalText(d.total)}</span>
              <span className="block font-mono text-[11px] text-muted tabular">{t("lavoro.timesheet.of", { capacity: totalText(d.capacity) })}</span>
            </td>
          </tr>
          <tr>
            <td colSpan={9} className="pt-1">
              <LoadBar minutes={d.total} capacity={d.capacity} label={t("lavoro.timesheet.loggedOf", { time: totalText(d.total), capacity: totalText(d.capacity) })} />
            </td>
          </tr>
        </tfoot>
      </table>
    </div>
  );
}

// ---------------------------------------------------------------------
// Un giorno alla volta (telefono)

function DayView({ d, rows, day, onDay, editable, onCommit }: { d: TS; rows: TSRow[]; day: number; onDay: (i: number) => void; editable: boolean; onCommit: CommitFn }) {
  const t = useT();
  const root = useRef<HTMLDivElement>(null);
  const x = d.days[day];
  const off = !editable || x.locked || x.future;
  const move = useMover(rows, () => off, root);
  const l = dayLabel(x.date);
  return (
    <div ref={root} className="flex flex-col gap-3">
      <div role="tablist" aria-label={t("lavoro.timesheet.days")} className="grid grid-cols-7 gap-1">
        {d.days.map((y, i) => {
          const dl = dayLabel(y.date);
          return (
            <button
              key={y.date}
              role="tab"
              aria-selected={day === i}
              onClick={() => onDay(i)}
              className={`flex min-h-[52px] flex-col items-center justify-center border px-0.5 text-[12px] ${day === i ? "border-fg bg-fg text-bg" : y.total ? "tinta m-work border-hair" : "border-hair"} ${!y.workday || y.holiday ? "opacity-70" : ""}`}
            >
              <span className="label">{dl.name}</span>
              <span className="font-mono tabular">{dl.num}</span>
              <span className={`font-mono text-[10.5px] tabular ${day === i ? "" : "text-muted"}`}>{y.total ? totalText(y.total) : "·"}</span>
            </button>
          );
        })}
      </div>
      <p className="m-0 flex items-baseline gap-2 text-[13px]">
        <strong>
          {l.name} {l.num} {l.month}
        </strong>
        <span className="font-mono text-muted tabular">
          {totalText(x.total)}
          {x.capacity > 0 ? ` ${t("lavoro.timesheet.of", { capacity: totalText(x.capacity) })}` : ""}
        </span>
        {x.locked && <span className="text-muted">{t("lavoro.timesheet.lockedPeriod")}</span>}
        {x.holiday && <span className="text-muted">{t("lavoro.timesheet.holidayDot")}</span>}
        {x.absence > 0 && !x.holiday && <span className="text-flag">{t("lavoro.timesheet.absentDot", { time: totalText(x.absence) })}</span>}
      </p>
      <ul className="m-0 flex list-none flex-col p-0">
        {rows.length === 0 && <li className="py-3 text-muted">{t("lavoro.timesheet.noRows")}</li>}
        {rows.map((r) => (
          <li key={r.id} className="flex min-h-[56px] items-center gap-3 border-b border-hair py-1.5">
            <span className="flex min-w-0 flex-1 flex-col">
              <span className="flex items-center gap-2">
                <TypeMark type={r.type} />
                <Link to="/e/$key" params={{ key: r.key }} className="font-mono text-[12px] text-accent no-underline">
                  {r.key}
                </Link>
                <span className="font-mono text-[11px] text-muted tabular">{t("lavoro.timesheet.inWeek", { time: totalText(r.total) })}</span>
              </span>
              <span className="min-w-0 break-words">{r.title}</span>
            </span>
            <span className="w-[104px] shrink-0">
              <CellInput row={r} dayIdx={day} minutes={r.days[day]} disabled={off} title={`${r.key}, ${l.name} ${l.num}`} big onCommit={onCommit} onMove={(dr, dd) => move(r, day, dr, dd)} />
            </span>
          </li>
        ))}
      </ul>
      <LoadBar minutes={d.total} capacity={d.capacity} label={t("lavoro.timesheet.weekOf", { time: totalText(d.total), capacity: totalText(d.capacity) })} />
      <p className="m-0 font-mono text-[12px] text-muted tabular">
        {t("lavoro.timesheet.weekOf", { time: totalText(d.total), capacity: totalText(d.capacity) })}
      </p>
    </div>
  );
}

// ---------------------------------------------------------------------
// Aggiungere righe: ricerca e suggerimenti

function AddPanel({ d, rows, onAdd }: { d: TS; rows: TSRow[]; onAdd: (s: TSSuggestion) => void }) {
  const t = useT();
  const [text, setText] = useState("");
  const [debounced, setDebounced] = useState("");
  useEffect(() => {
    const timer = setTimeout(() => setDebounced(text.trim()), 200);
    return () => clearTimeout(timer);
  }, [text]);
  const have = new Set(rows.map((r) => r.id));
  const found = useQuery({
    queryKey: ["tempo", "find", debounced],
    queryFn: () => {
      const words = debounced.replace(/[:",*]/g, " ").split(/\s+/).filter(Boolean);
      const last = words.pop() ?? "";
      const tail = last.length >= 2 ? `${last}*` : last;
      return api.search(`tipo: epic, story, task, bug ${[...words, tail].join(" ")}`, undefined, 8);
    },
    enabled: debounced.length >= 2,
    staleTime: 30_000,
  });
  const assigned = d.assigned.filter((s) => !have.has(s.id));
  const previous = d.previous.filter((s) => !have.has(s.id));
  return (
    <section aria-label={t("lavoro.timesheet.addItems")} className="flex flex-col gap-3 border-t border-hair pt-4">
      <div className="relative max-w-[520px]">
        <label className="flex flex-col gap-1">
          <span className="label text-muted">{t("lavoro.timesheet.addItem")}</span>
          <input className="field" placeholder={t("lavoro.timesheet.searchPlaceholder")} value={text} onChange={(e) => setText(e.target.value)} />
        </label>
        {debounced.length >= 2 && (
          <ul className="m-0 mt-1 flex max-h-[280px] list-none flex-col overflow-y-auto border border-rule bg-bg p-0">
            {found.isPending && <li className="px-3 py-2 text-muted">{t("lavoro.timesheet.searching")}</li>}
            {found.data?.items.length === 0 && <li className="px-3 py-2 text-muted">{t("lavoro.timesheet.noneFound")}</li>}
            {found.data?.items.map((e) => (
              <li key={e.id}>
                <button
                  className="flex min-h-[44px] w-full items-center gap-2 border-b border-hair px-3 py-1.5 text-left hover:bg-sel disabled:opacity-50"
                  disabled={have.has(e.id)}
                  onClick={() => {
                    onAdd({ id: e.id, key: e.key, title: e.title, type: e.type, state: e.state, minutes: 0 });
                    setText("");
                  }}
                >
                  <TypeMark type={e.type} />
                  <span className="font-mono text-[12px] text-accent">{e.key}</span>
                  <span className="min-w-0 flex-1 break-words">{e.title}</span>
                  <span className="shrink-0 text-[11.5px] text-muted">{have.has(e.id) ? t("lavoro.timesheet.alreadyIn") : e.state}</span>
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
      <div className="grid gap-4 md:grid-cols-2">
        <Suggestions title={t("lavoro.timesheet.assignedToYou")} empty={t("lavoro.timesheet.assignedEmpty")} items={assigned} onAdd={onAdd} />
        <Suggestions title={t("lavoro.timesheet.workedLastWeek")} empty={t("lavoro.timesheet.workedEmpty")} items={previous} onAdd={onAdd} showMinutes />
      </div>
    </section>
  );
}

function Suggestions({ title, empty, items, onAdd, showMinutes }: { title: string; empty: string; items: TSSuggestion[]; onAdd: (s: TSSuggestion) => void; showMinutes?: boolean }) {
  const t = useT();
  const [all, setAll] = useState(false);
  const shown = all ? items : items.slice(0, 5);
  return (
    <div className="flex min-w-0 flex-col gap-1">
      <h3 className="label m-0 text-muted">
        {title} <span className="font-mono tabular">{items.length}</span>
      </h3>
      {items.length === 0 && <p className="m-0 text-[12.5px] text-muted">{empty}</p>}
      <ul className="m-0 flex list-none flex-col p-0">
        {shown.map((s) => (
          <li key={s.id}>
            <button className="flex min-h-[40px] w-full items-center gap-2 border-b border-hair px-1 py-1 text-left hover:bg-sel/60" onClick={() => onAdd(s)}>
              <span aria-hidden="true" className="text-accent">
                +
              </span>
              <TypeMark type={s.type} />
              <span className="font-mono text-[12px] text-accent">{s.key}</span>
              <span className="min-w-0 flex-1 break-words">{s.title}</span>
              {showMinutes && <span className="shrink-0 font-mono text-[11.5px] text-muted tabular">{totalText(s.minutes)}</span>}
              {!showMinutes && <span className="shrink-0 text-[11.5px] text-muted">{s.state}</span>}
            </button>
          </li>
        ))}
      </ul>
      {items.length > 5 && (
        <button className="self-start text-[12.5px] text-accent underline" onClick={() => setAll(!all)}>
          {all ? t("lavoro.timesheet.less") : t("lavoro.timesheet.showAll", { count: items.length })}
        </button>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------
// Copia

function CopyMenu({ busy, disabled, hint, onRows, onAll }: { busy: boolean; disabled: boolean; hint: string; onRows: () => void; onAll: () => void }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  return (
    <span className="relative">
      <button className="btn" aria-haspopup="menu" aria-expanded={open} disabled={disabled || busy} onClick={() => setOpen(!open)}>
        {busy ? t("lavoro.timesheet.copying") : t("lavoro.timesheet.copyLastWeek")} <span aria-hidden="true">▾</span>
      </button>
      {open && (
        <div role="menu" className="absolute right-0 z-30 mt-1 flex w-[280px] flex-col border-2 border-rule bg-bg" onMouseLeave={() => setOpen(false)}>
          <button
            role="menuitem"
            className="px-3 py-2 text-left hover:bg-sel"
            onClick={() => {
              setOpen(false);
              onRows();
            }}
          >
            <strong className="block">{t("lavoro.timesheet.copyRowsOnly")}</strong>
            <span className="text-[12px] text-muted">{t("lavoro.timesheet.copyRowsOnlyHint")}</span>
          </button>
          <button
            role="menuitem"
            className="border-t border-hair px-3 py-2 text-left hover:bg-sel"
            onClick={() => {
              setOpen(false);
              onAll();
            }}
          >
            <strong className="block">{t("lavoro.timesheet.copyRowsHours")}</strong>
            <span className="text-[12px] text-muted">{t("lavoro.timesheet.copyRowsHoursHint", { hint })}</span>
          </button>
        </div>
      )}
    </span>
  );
}

// ---------------------------------------------------------------------
// Approvazioni

function PendingPanel({ items, busy, onOpen, onApprove, onReject }: { items: PendingWeek[]; busy: boolean; onOpen: (p: PendingWeek) => void; onApprove: (p: PendingWeek) => void; onReject: (p: PendingWeek) => void }) {
  const t = useT();
  return (
    <section aria-label={t("lavoro.timesheet.toApprove")} className="flex flex-col gap-1 border-2 border-rule p-3">
      <h2 className="m-0 text-[15px] font-bold">
        {t("lavoro.timesheet.toApprove")} <span className="font-mono text-[12px] font-normal text-muted tabular">{items.length}</span>
      </h2>
      <ul className="m-0 flex list-none flex-col p-0">
        {items.map((p) => (
          <li key={p.member_id + p.week} className="flex flex-wrap items-center gap-x-4 gap-y-1 border-b border-hair py-2 last:border-b-0">
            <span className="min-w-[140px] flex-1">
              <strong>{p.member}</strong> <span className="text-muted">· {weekLabel(p.week)}</span>
            </span>
            <span className="font-mono text-[12.5px] tabular">
              {totalText(p.minutes)}
              {p.capacity > 0 && <span className="text-muted"> {t("lavoro.timesheet.of", { capacity: totalText(p.capacity) })}</span>}
            </span>
            <span className="flex gap-2">
              <button className="btn px-2.5 py-1" onClick={() => onOpen(p)}>
                {t("lavoro.timesheet.look")}
              </button>
              <button className="btn btn-primary px-2.5 py-1" disabled={busy} onClick={() => onApprove(p)}>
                {t("lavoro.timesheet.approve")}
              </button>
              <button className="btn btn-danger px-2.5 py-1" onClick={() => onReject(p)}>
                {t("lavoro.timesheet.reject")}
              </button>
            </span>
          </li>
        ))}
      </ul>
    </section>
  );
}

function RejectForm({ busy, onSubmit }: { busy: boolean; onSubmit: (comment: string) => void }) {
  const t = useT();
  const [text, setText] = useState("");
  return (
    <form
      className="flex flex-col gap-2"
      onSubmit={(e) => {
        e.preventDefault();
        if (text.trim()) onSubmit(text.trim());
      }}
    >
      <label className="flex flex-col gap-1">
        <span className="label text-muted">{t("lavoro.timesheet.reason")}</span>
        <textarea className="field" rows={3} required maxLength={500} value={text} onChange={(e) => setText(e.target.value)} />
      </label>
      <button className="btn btn-danger self-start" disabled={busy || !text.trim()}>
        {t("lavoro.timesheet.reject")}
      </button>
    </form>
  );
}

// ---------------------------------------------------------------------
// Regole del tenant

function RulesDialog({ s, onClose }: { s: TS["settings"]; onClose: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const [req, setReq] = useState(s.require_approval);
  const [mode, setMode] = useState<string>(s.approver_mode);
  const [who, setWho] = useState(s.approver_id ?? "");
  const [lock, setLock] = useState(s.locked_until ?? "");
  const [max, setMax] = useState(String(s.max_day_minutes / 60));
  const save = useMutation({
    mutationFn: () =>
      tempoApi.saveSettings({ require_approval: req, approver_mode: mode, approver_id: mode === "member" ? who : "", locked_until: lock, max_day_minutes: Math.round(parseFloat(max.replace(",", ".")) * 60) }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["tempo"] });
      onClose();
    },
  });
  const endOfLastMonth = () => {
    const n = new Date();
    const d = new Date(n.getFullYear(), n.getMonth(), 0);
    setLock(`${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`);
  };
  return (
    <Dialog title={t("lavoro.timesheet.rulesTitle")} onClose={onClose} wide>
      <form
        className="flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault();
          save.mutate();
        }}
      >
        <fieldset className="m-0 flex flex-col gap-2 border-0 p-0">
          <legend className="label mb-1 p-0 text-muted">{t("lavoro.timesheet.approvalSection")}</legend>
          <label className="flex items-start gap-2">
            <input type="checkbox" className="mt-1" checked={req} onChange={(e) => setReq(e.target.checked)} />
            <span>
              {t("lavoro.timesheet.requireApproval")}
              <span className="block text-[12px] text-muted">{t("lavoro.timesheet.requireApprovalHint")}</span>
            </span>
          </label>
          {req && (
            <div className="flex flex-col gap-2 pl-6">
              <label className="flex flex-col gap-1">
                <span className="label text-muted">{t("lavoro.timesheet.whoApproves")}</span>
                <Select className="field" value={mode} onChange={(e) => setMode(e.target.value)}>
                  <option value="team_lead">{t("lavoro.timesheet.modeTeamLead")}</option>
                  <option value="member">{t("lavoro.timesheet.modeMember")}</option>
                  <option value="admin">{t("lavoro.timesheet.modeAdmin")}</option>
                </Select>
              </label>
              {mode === "member" && (
                <label className="flex flex-col gap-1">
                  <span className="label text-muted">{t("lavoro.timesheet.person")}</span>
                  <EntitySelect kind="member" noneLabel={t("lavoro.timesheet.choose")} className="field" required value={who} onChange={(v) => setWho(v)} />
                </label>
              )}
              <p className="m-0 text-[12px] text-muted">{t("lavoro.timesheet.approverNote")}</p>
            </div>
          )}
        </fieldset>
        <fieldset className="m-0 flex flex-col gap-2 border-0 p-0">
          <legend className="label mb-1 p-0 text-muted">{t("lavoro.timesheet.closedPeriod")}</legend>
          <div className="flex flex-wrap items-end gap-2">
            <label className="flex flex-col gap-1">
              <span className="text-[12.5px]">{t("lavoro.timesheet.noWriteUntil")}</span>
              <input type="date" className="field w-[170px]" value={lock} onChange={(e) => setLock(e.target.value)} />
            </label>
            <button type="button" className="btn" onClick={endOfLastMonth}>
              {t("lavoro.timesheet.endLastMonth")}
            </button>
            <button type="button" className="btn" disabled={!lock} onClick={() => setLock("")}>
              {t("lavoro.timesheet.noLock")}
            </button>
          </div>
          <p className="m-0 text-[12px] text-muted">{t("lavoro.timesheet.lockNote")}</p>
        </fieldset>
        <label className="flex max-w-[220px] flex-col gap-1">
          <span className="label text-muted">{t("lavoro.timesheet.maxHours")}</span>
          <input className="field font-mono" inputMode="decimal" value={max} onChange={(e) => setMax(e.target.value)} />
        </label>
        <ErrorNote error={save.error} />
        <button className="btn btn-primary self-start" disabled={save.isPending}>
          {save.isPending ? t("lavoro.timesheet.saving") : t("lavoro.timesheet.saveRules")}
        </button>
      </form>
    </Dialog>
  );
}
