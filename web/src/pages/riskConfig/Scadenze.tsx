import { Select } from "../../select";
import { useT } from "../../i18n/react";
import type { Due, Stage } from "../../riskApi";
import { dueAmount, dueOf, dueText, dueUnit, ordered, parseDue, timeline, type DueUnit } from "./model";

// Scadenze delle tappe di notifica di un incidente: ogni termine è una riga «entro N ore/giorni da…»
// (menu per il punto di partenza e per l'unità), con una linea del tempo che mostra quando scadono.

/** Termini di una tappa come righe. `earlier` sono le tappe che la precedono (si può partire dalla loro scadenza). */
export function DueRows({ st, earlier, onChange }: { st: Stage; earlier: Stage[]; onChange: (d: Due[]) => void }) {
  const t = useT();
  const starts: [string, string][] = [
    ["detected", t("configurazione.rischi.da.detected")],
    ["classified", t("configurazione.rischi.da.classified")],
    ...earlier.map((e): [string, string] => [`stage:${e.key}`, t("configurazione.rischi.da.stage", { name: e.name })]),
  ];
  const set = (i: number, d: Due) => onChange(st.due.map((x, j) => (j === i ? d : x)));
  return (
    <div className="flex min-w-0 flex-col gap-1.5">
      {st.due.length === 0 && <p className="m-0 text-[12.5px] text-muted">{t("configurazione.rischi.nessunTermine")}</p>}
      {st.due.map((d, i) => (
        <div key={i} className="flex min-w-0 flex-wrap items-center gap-2" data-testid="termine">
          <span className="text-[12.5px] text-muted">{i === 0 ? t("configurazione.rischi.entro") : t("configurazione.rischi.oppureEntro")}</span>
          <input
            type="number"
            min={0}
            className="field w-20 tabular"
            aria-label={t("configurazione.rischi.quantita", { stage: st.name, n: i + 1 })}
            value={dueAmount(d)}
            onChange={(e) => set(i, dueOf(d.from, Number(e.target.value), dueUnit(d)))}
          />
          <Select className="field w-auto" aria-label={t("configurazione.rischi.unita", { stage: st.name, n: i + 1 })} value={dueUnit(d)} onChange={(e) => set(i, dueOf(d.from, dueAmount(d), e.target.value as DueUnit))}>
            <option value="hours">{t("configurazione.rischi.ore")}</option>
            <option value="days">{t("configurazione.rischi.giorni")}</option>
          </Select>
          <Select className="field w-auto min-w-48" aria-label={t("configurazione.rischi.partenza", { stage: st.name, n: i + 1 })} value={d.from} onChange={(e) => set(i, { ...d, from: e.target.value })}>
            {!starts.some(([v]) => v === d.from) && <option value={d.from}>{d.from}</option>}
            {starts.map(([v, l]) => (
              <option key={v} value={v}>
                {l}
              </option>
            ))}
          </Select>
          <button type="button" className="touch-44 inline-flex items-center text-[12px] text-muted underline hover:text-danger" onClick={() => onChange(st.due.filter((_, j) => j !== i))}>
            {t("configurazione.common.togli2")}
          </button>
        </div>
      ))}
      <button type="button" className="touch-44 inline-flex items-center self-start text-[12.5px] font-semibold text-accent hover:underline" onClick={() => onChange([...st.due, { from: "detected", hours: 24 }])}>
        {t("configurazione.rischi.aggiungiTermine")}
      </button>
    </div>
  );
}

/** Testo grezzo dei termini («detected + 24h, classified + 3g»), chiuso: si applica uscendo dal campo. */
export function DueAdvanced({ st, onChange }: { st: Stage; onChange: (d: Due[]) => void }) {
  const t = useT();
  const text = dueText(st.due);
  return (
    <details className="text-[12.5px]">
      <summary className="touch-44 inline-flex cursor-pointer items-center text-muted">{t("configurazione.rischi.avanzato")}</summary>
      <label className="mt-1 flex flex-col gap-1">
        <span className="text-muted">{t("configurazione.rischi.avanzatoTermini")}</span>
        <input key={text} className="field font-mono text-[12.5px]" aria-label={t("configurazione.risk.termini")} defaultValue={text} onBlur={(e) => e.target.value !== text && onChange(parseDue(e.target.value))} />
      </label>
    </details>
  );
}

const fmt = (h: number, t: ReturnType<typeof useT>) => (h % 24 === 0 && h >= 48 ? t("configurazione.rischi.fmtGiorni", { count: h / 24 }) : t("configurazione.rischi.fmtOre", { count: h }));

/** Linea del tempo: una barra per tappa, lunga quanto manca alla scadenza dal rilevamento. */
export function Timeline({ stages }: { stages: Stage[] }) {
  const t = useT();
  const ticks = timeline(stages);
  const shown = ordered(ticks);
  const max = Math.max(1, ...shown.map((x) => x.hours!));
  const none = ticks.filter((x) => x.hours === null);
  if (stages.length === 0) return null;
  return (
    <figure className="m-0 flex min-w-0 flex-col gap-1.5 border-y border-hair py-2" data-testid="linea-del-tempo" aria-label={t("configurazione.rischi.lineaDelTempo")}>
      <figcaption className="label">{t("configurazione.rischi.lineaDelTempo")}</figcaption>
      <ol className="m-0 flex list-none flex-col gap-1 p-0">
        {shown.map((x) => (
          <li key={x.key} className="grid min-w-0 grid-cols-[minmax(0,9rem)_minmax(0,1fr)_4.5rem] items-center gap-2 text-[12.5px] sm:grid-cols-[minmax(0,14rem)_minmax(0,1fr)_5rem]">
            <span className="min-w-0 [overflow-wrap:anywhere]" title={x.name}>
              {x.name}
            </span>
            <span className="h-2.5 bg-hair" aria-hidden="true">
              <span className="block h-full bg-flag" style={{ width: `${Math.max(2, (x.hours! / max) * 100)}%` }} />
            </span>
            <span className="text-right font-mono text-[12px]">{fmt(x.hours!, t)}</span>
          </li>
        ))}
      </ol>
      {none.length > 0 && <p className="m-0 text-[12px] text-muted">{t("configurazione.rischi.senzaScadenza", { names: none.map((x) => x.name).join(", ") })}</p>}
      <p className="m-0 text-[11.5px] text-muted">{t("configurazione.rischi.lineaNota")}</p>
    </figure>
  );
}
