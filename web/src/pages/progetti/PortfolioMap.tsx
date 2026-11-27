// Progetti, passo 6: mappa dei semafori del portafoglio (replica/progetti.md §6 «Programma e portafoglio»).
// I progetti che l'utente legge, raggruppati per programma, con un semaforo per ambito: tempi, budget,
// perimetro, rischi e conflitti. I semafori li deduce il server dagli stessi numeri della Panoramica; qui si
// filtrano. Il budget di chi non lo vede resta «non visibile».
import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { projectKeys, projectsApi, type AreaSignal, type PortfolioProject, type Signal, type Signals } from "../../projectsApi";
import { Select } from "../../select";
import { EmptyState } from "../../emptyState";
import { ErrorNote, Loading } from "../../ui";
import { useT } from "../../i18n/react";
import { ItemLink, ProgressBar, SignalMark, SignalName } from "./shared";

const AREAS: (keyof Signals)[] = ["schedule", "budget", "scope", "risk", "conflicts"];
const rank: Record<AreaSignal, number> = { hidden: 0, none: 0, green: 1, amber: 2, red: 3 };

function AreaCell({ value }: { value: AreaSignal }) {
  const t = useT();
  if (value === "hidden" || value === "none") {
    return <span className="text-[12.5px] text-muted">{t(`progetti.passo6.map.${value}`)}</span>;
  }
  return <SignalName value={value} className="text-[12.5px]" />;
}

type Filter = { program: string; signal: "" | "red" | "problems"; area: "" | keyof Signals; text: string };

function matches(p: PortfolioProject, f: Filter): boolean {
  if (f.program === "-" && p.program) return false;
  if (f.program && f.program !== "-" && p.program?.id !== f.program) return false;
  if (f.signal === "red" && p.signal !== "red") return false;
  if (f.signal === "problems" && p.signal !== "red" && p.signal !== "amber") return false;
  if (f.area && rank[p.signals[f.area]] < 2) return false;
  const q = f.text.trim().toLowerCase();
  if (q && !`${p.key} ${p.title} ${p.project_manager ?? ""}`.toLowerCase().includes(q)) return false;
  return true;
}

export function PortfolioMap() {
  const t = useT();
  const [closed, setClosed] = useState(false);
  const [group, setGroup] = useState(true);
  const [f, setF] = useState<Filter>({ program: "", signal: "", area: "", text: "" });
  const q = useQuery({ queryKey: projectKeys.portfolio(closed), queryFn: () => projectsApi.portfolio(closed) });
  const rows = useMemo(() => (q.data?.projects ?? []).filter((p) => matches(p, f)), [q.data, f]);
  const programs = q.data?.programs ?? [];
  const groups = useMemo(() => {
    if (!group || programs.length === 0) return [{ id: "all", title: "", items: rows }];
    const out = programs.map((pg) => ({ id: pg.id, title: `${pg.key} ${pg.title}`, items: rows.filter((r) => r.program?.id === pg.id) }));
    out.push({ id: "-", title: t("progetti.passo6.map.noProgram"), items: rows.filter((r) => !r.program) });
    return out.filter((g) => g.items.length > 0);
  }, [rows, programs, group, t]);
  const counts = useMemo(() => {
    const c: Record<Signal, number> = { red: 0, amber: 0, green: 0, none: 0 };
    for (const p of rows) c[p.signal]++;
    return c;
  }, [rows]);
  return (
    <div className="flex flex-col gap-4" data-testid="mappa-semafori">
      <p className="m-0 text-[12.5px] text-muted">{t("progetti.passo6.map.help")}</p>
      <div className="flex flex-wrap items-end gap-x-4 gap-y-2" role="group" aria-label={t("progetti.passo6.map.filters")}>
        <label className="flex flex-col gap-1">
          <span className="label">{t("progetti.passo6.map.search")}</span>
          <input type="search" className="field w-full sm:w-56" value={f.text} onChange={(e) => setF({ ...f, text: e.target.value })} />
        </label>
        {programs.length > 0 && (
          <label className="flex flex-col gap-1">
            <span className="label">{t("progetti.passo6.map.program")}</span>
            <Select className="field w-full sm:w-56" aria-label={t("progetti.passo6.map.program")} value={f.program} onChange={(e) => setF({ ...f, program: e.target.value })}>
              <option value="">{t("progetti.passo6.map.allPrograms")}</option>
              {programs.map((pg) => (
                <option key={pg.id} value={pg.id}>{`${pg.key} ${pg.title}`}</option>
              ))}
              <option value="-">{t("progetti.passo6.map.noProgram")}</option>
            </Select>
          </label>
        )}
        <label className="flex flex-col gap-1">
          <span className="label">{t("progetti.passo6.map.signal")}</span>
          <Select className="field w-full sm:w-48" aria-label={t("progetti.passo6.map.signal")} value={f.signal} onChange={(e) => setF({ ...f, signal: e.target.value as Filter["signal"] })}>
            <option value="">{t("progetti.passo6.map.allSignals")}</option>
            <option value="problems">{t("progetti.passo6.map.problems")}</option>
            <option value="red">{t("progetti.passo6.map.onlyRed")}</option>
          </Select>
        </label>
        <label className="flex flex-col gap-1">
          <span className="label">{t("progetti.passo6.map.area")}</span>
          <Select className="field w-full sm:w-48" aria-label={t("progetti.passo6.map.area")} value={f.area} onChange={(e) => setF({ ...f, area: e.target.value as Filter["area"] })}>
            <option value="">{t("progetti.passo6.map.allAreas")}</option>
            {AREAS.map((a) => (
              <option key={a} value={a}>{t(`progetti.passo6.map.areas.${a}`)}</option>
            ))}
          </Select>
        </label>
        {programs.length > 0 && (
          <label className="flex min-h-[44px] items-center gap-2 text-[12.5px] md:min-h-0">
            <input type="checkbox" checked={group} onChange={(e) => setGroup(e.target.checked)} />
            {t("progetti.passo6.map.group")}
          </label>
        )}
        <label className="flex min-h-[44px] items-center gap-2 text-[12.5px] md:min-h-0">
          <input type="checkbox" checked={closed} onChange={(e) => setClosed(e.target.checked)} />
          {t("progetti.list.includeClosed")}
        </label>
      </div>
      {q.error && <ErrorNote error={q.error} />}
      {!q.data && !q.error && <Loading className="m-0 text-muted" />}
      {q.data && (
        <p className="m-0 flex flex-wrap gap-x-4 text-[12.5px]" aria-live="polite" data-testid="mappa-conteggi">
          <span>{t("progetti.passo6.map.count", { count: rows.length })}</span>
          {(["red", "amber", "green"] as const).map((s) => (
            <span key={s} className="inline-flex items-center gap-1.5">
              <SignalMark value={s} />
              {t(`progetti.common.signal.${s}`)} {counts[s]}
            </span>
          ))}
        </p>
      )}
      {q.data && rows.length === 0 && <EmptyState title={t("progetti.passo6.map.emptyTitle")} text={t("progetti.passo6.map.empty")} />}
      {groups.map((g) =>
        g.items.length === 0 ? null : (
          <section key={g.id} className="flex flex-col gap-2" aria-label={g.title || t("progetti.passo6.map.title")}>
            {g.title && <h3 className="label m-0 border-b-2 border-rule pb-1.5">{g.title}</h3>}
            <Table items={g.items} />
          </section>
        ),
      )}
    </div>
  );
}

const th = "label border-b-2 border-rule px-2 py-2 text-left font-semibold text-muted";
const td = "border-b border-hair px-2 py-2 align-top";

function Table({ items }: { items: PortfolioProject[] }) {
  const t = useT();
  return (
    <>
      <table className="hidden w-full border-collapse text-[13px] md:table" aria-label={t("progetti.passo6.map.title")}>
        <thead>
          <tr>
            <th className={th}>{t("progetti.list.project")}</th>
            <th className={th}>{t("progetti.passo6.map.overall")}</th>
            {AREAS.map((a) => (
              <th key={a} className={th}>{t(`progetti.passo6.map.areas.${a}`)}</th>
            ))}
            <th className={th}>{t("progetti.list.progress")}</th>
          </tr>
        </thead>
        <tbody>
          {items.map((p) => (
            <tr key={p.id} data-testid="mappa-progetto" data-key={p.key}>
              <td className={td}>
                <ItemLink itemKey={p.key} />
                <div className="font-semibold">{p.title}</div>
                <div className="text-[12px] text-muted">{p.project_manager ?? t("progetti.list.noPm")}</div>
              </td>
              <td className={td}><SignalName value={p.signal} className="text-[12.5px]" /></td>
              {AREAS.map((a) => (
                <td key={a} className={td} data-area={a}><AreaCell value={p.signals[a]} /></td>
              ))}
              <td className={`${td} min-w-[110px]`}>
                <div className="flex items-center gap-2">
                  <ProgressBar percent={p.progress} />
                  <span className="tabular w-9 shrink-0 text-right">{Math.round(p.progress)}%</span>
                </div>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <ul className="m-0 flex list-none flex-col p-0 md:hidden" aria-label={t("progetti.passo6.map.title")}>
        {items.map((p) => (
          <li key={p.id} className="flex flex-col gap-1.5 border-b border-hair py-3" data-testid="mappa-progetto-mobile">
            <div className="flex items-baseline justify-between gap-3">
              <ItemLink itemKey={p.key} />
              <SignalName value={p.signal} className="text-[12.5px]" />
            </div>
            <span className="font-semibold">{p.title}</span>
            <dl className="m-0 grid grid-cols-2 gap-x-3 gap-y-1">
              {AREAS.map((a) => (
                <div key={a} className="flex min-w-0 flex-col">
                  <dt className="text-[11.5px] text-muted">{t(`progetti.passo6.map.areas.${a}`)}</dt>
                  <dd className="m-0"><AreaCell value={p.signals[a]} /></dd>
                </div>
              ))}
            </dl>
          </li>
        ))}
      </ul>
    </>
  );
}
