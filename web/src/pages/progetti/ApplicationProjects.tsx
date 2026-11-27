// Progetti, passo 6: scheda «Progetti» dell'applicazione (replica/progetti.md §6, «Componente»). I work package
// dei progetti che toccano l'applicazione, nel tempo, con fase, team e modo di consegna, e i conflitti tra
// progetti aperti nello stesso periodo (gli stessi del piano, passo 4). Le voci non leggibili restano con le date.
import type { ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { projectKeys, projectsApi, type AppProjects, type AppWork } from "../../projectsApi";
import { EmptyState } from "../../emptyState";
import { ErrorNote, Loading, SectionTitle, date } from "../../ui";
import { useT } from "../../i18n/react";
import * as cal from "../../calendar";
import { ItemLink } from "./shared";

function range(a: string | null, b: string | null, none: string): string {
  if (a && b) return `${date(a)} – ${date(b)}`;
  return a || b ? date(a ?? b) : none;
}

export function ApplicationProjects({ appKey }: { appKey: string }) {
  const t = useT();
  const q = useQuery({ queryKey: projectKeys.application(appKey), queryFn: () => projectsApi.applicationProjects(appKey) });
  if (q.error) return <ErrorNote error={q.error} />;
  if (!q.data) return <Loading className="m-0 text-muted" />;
  const v = q.data;
  if (v.items.length === 0) return <EmptyState title={t("progetti.passo6.app.title")} text={t("progetti.passo6.app.empty")} />;
  const inConflict = new Set(v.conflicts.flatMap((c) => [c.a, c.b]));
  return (
    <div className="flex max-w-[1100px] flex-col gap-6" data-testid="applicazione-progetti">
      <p className="m-0 text-[13px] text-muted">{t("progetti.passo6.app.help")}</p>
      {v.conflicts.length > 0 && <Conflicts v={v} />}
      <section className="flex min-w-0 flex-col gap-2" aria-label={t("progetti.passo6.app.title")}>
        <SectionTitle>{t("progetti.passo6.app.title")}</SectionTitle>
        <Timeline items={v.items} inConflict={inConflict} />
      </section>
    </div>
  );
}

function who(a: AppWork, t: ReturnType<typeof useT>): { project: string; wp: string } {
  if (!a.project) return { project: t("progetti.restricted"), wp: t("progetti.restricted") };
  return { project: `${a.project.key} ${a.project.title}`, wp: a.work_package ? `${a.work_package.key} ${a.work_package.title}` : t("progetti.restricted") };
}

function Conflicts({ v }: { v: AppProjects }) {
  const t = useT();
  const byId = new Map(v.items.map((a) => [a.id, a]));
  return (
    <section className="flex flex-col gap-2 border-l-2 border-flag pl-3" aria-label={t("progetti.passo6.app.conflicts")} data-testid="applicazione-conflitti">
      <SectionTitle>{t("progetti.passo6.app.conflicts")}</SectionTitle>
      <ul className="m-0 flex list-none flex-col gap-1.5 p-0 text-[13px]">
        {v.conflicts.map((c) => {
          const a = byId.get(c.a);
          const b = byId.get(c.b);
          if (!a || !b) return null;
          return (
            <li key={`${c.a}-${c.b}`} className="flex items-start gap-2">
              <span aria-hidden="true" className="font-semibold text-flag">▲</span>
              <span>
                {t("progetti.passo6.app.conflict", { a: who(a, t).project, b: who(b, t).project, from: c.from ? date(c.from) : "…", until: c.until ? date(c.until) : "…" })}
              </span>
            </li>
          );
        })}
      </ul>
    </section>
  );
}

function monthStart(x: number): number {
  const d = new Date(x);
  return Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), 1);
}
function nextMonth(x: number): number {
  const d = new Date(x);
  return Date.UTC(d.getUTCFullYear(), d.getUTCMonth() + 1, 1);
}

function Details({ a }: { a: AppWork }) {
  const t = useT();
  const parts = [a.phase, a.team, a.delivery_mode ? t(`progetti.common.mode.${a.delivery_mode}`) : null, a.state].filter(Boolean);
  return <span className="text-[12px] text-muted">{parts.join(" · ")}</span>;
}

function Label({ a, inConflict }: { a: AppWork; inConflict: boolean }): ReactNode {
  const t = useT();
  return (
    <div className="flex min-w-0 flex-col gap-0.5">
      <span className="flex flex-wrap items-center gap-x-2">
        {a.project ? <ItemLink itemKey={a.project.key} /> : <span className="text-[12.5px] text-muted">{t("progetti.restricted")}</span>}
        {inConflict && (
          <span className="inline-flex items-center gap-1 border border-flag px-1 text-[10.5px] font-semibold text-flag">
            <span aria-hidden="true">▲</span>
            {t("progetti.fornitori.conflict.mark")}
          </span>
        )}
        {!a.open && <span className="text-[11px] text-muted">{t("progetti.passo6.app.closed")}</span>}
      </span>
      <span className="truncate text-[12.5px] font-semibold" title={a.project?.title}>
        {a.project?.title}
      </span>
      <span className="truncate text-[12.5px]">{a.work_package ? a.work_package.title : t("progetti.restricted")}</span>
      <Details a={a} />
    </div>
  );
}

function Timeline({ items, inConflict }: { items: AppWork[]; inConflict: Set<string> }) {
  const t = useT();
  const none = t("progetti.common.none");
  const pts: number[] = [];
  for (const a of items) for (const d of [a.start, a.end]) if (d) pts.push(cal.parse(d));
  const hasDates = pts.length > 0;
  const from = hasDates ? monthStart(Math.min(...pts)) : 0;
  const to = hasDates ? nextMonth(Math.max(...pts)) : 1;
  const total = Math.max(1, cal.diffDays(from, to));
  const pct = (d: number) => `${(cal.diffDays(from, d) / total) * 100}%`;
  const width = (a: number, b: number) => `${Math.max(0.6, ((cal.diffDays(a, b) + 1) / total) * 100)}%`;
  const months: number[] = [];
  for (let m = from; m < to; m = nextMonth(m)) months.push(m);
  const step = months.length > 30 ? 6 : months.length > 14 ? 3 : months.length > 8 ? 2 : 1;
  const today = cal.today();
  const showToday = hasDates && today >= from && today <= to;
  return (
    <>
      <div className="hidden md:block" role="group" aria-label={t("progetti.passo6.app.timeline")}>
        <div className="grid grid-cols-[260px_1fr] gap-x-3">
          <div className="label self-end pb-1 text-muted">{t("progetti.passo6.app.workPackage")}</div>
          <div className="relative h-7 border-b border-rule" aria-hidden="true">
            {months.map((m, i) => (
              <span key={m} className="absolute top-0 h-full border-l border-hair pl-1 text-[11.5px] text-muted" style={{ left: pct(m) }}>
                {i % step === 0 && (
                  <>
                    {cal.monthName(m)}
                    {(i === 0 || new Date(m).getUTCMonth() === 0) && <span className="tabular ml-1">{new Date(m).getUTCFullYear()}</span>}
                  </>
                )}
              </span>
            ))}
          </div>
          {items.map((a) => {
            const s = a.start ?? a.end;
            const e = a.end ?? a.start;
            const conflict = inConflict.has(a.id);
            return (
              <div key={a.id} className="contents" data-testid="applicazione-wp">
                <div className="border-b border-hair py-1.5">
                  <Label a={a} inConflict={conflict} />
                </div>
                <div className="relative border-b border-hair">
                  {months.map((m) => (
                    <span key={m} aria-hidden="true" className="absolute inset-y-0 border-l border-hair/60" style={{ left: pct(m) }} />
                  ))}
                  {showToday && <span aria-hidden="true" className="absolute inset-y-0 border-l-2 border-accent" style={{ left: pct(today) }} />}
                  {s && e ? (
                    <span
                      role="img"
                      aria-label={range(a.start, a.end, none)}
                      title={range(a.start, a.end, none)}
                      className={`absolute top-1/2 block h-3 -translate-y-1/2 border ${conflict ? "border-flag bg-flag/60" : a.open ? "border-accent bg-accent/70" : "border-muted bg-muted/30"}`}
                      style={{ left: pct(cal.parse(s)), width: width(cal.parse(s), cal.parse(e)) }}
                    />
                  ) : (
                    <span className="absolute top-1/2 left-2 -translate-y-1/2 text-[12px] text-muted">{t("progetti.passo6.app.noDates")}</span>
                  )}
                </div>
              </div>
            );
          })}
        </div>
      </div>
      <ul className="m-0 flex list-none flex-col p-0 md:hidden" aria-label={t("progetti.passo6.app.title")}>
        {items.map((a) => (
          <li key={a.id} className="flex flex-col gap-1 border-b border-hair py-3" data-testid="applicazione-wp-mobile">
            <Label a={a} inConflict={inConflict.has(a.id)} />
            <span className="tabular text-[12.5px]">{range(a.start, a.end, t("progetti.passo6.app.noDates"))}</span>
          </li>
        ))}
      </ul>
    </>
  );
}
