// Mappatura guidata requisito ↔ controllo (ondata 3, proposta 4): a schermo diviso, a sinistra i
// requisiti senza controllo raggruppati per normativa, a destra i controlli con i suggerimenti per
// somiglianza di testo e di categoria; l'associazione si applica in blocco e non toglie quelle che
// ci sono già. Sul telefono una colonna alla volta.
import { Select } from "../select";
import { useMemo, useState } from "react";
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ErrorNote, SectionTitle, Loading } from "../ui";
import { livingApi, type Suggestion, type WorkControl } from "../grcLivingApi";
import { FrameworkTag } from "./grc-ui";
import { tr } from "../i18n/core";

export function MappingWorkbench() {
  const qc = useQueryClient();
  const wb = useQuery({ queryKey: ["grc", "workbench"], queryFn: livingApi.workbench });
  const [picked, setPicked] = useState<string[]>([]);
  const [control, setControl] = useState("");
  const [coverage, setCoverage] = useState<"full" | "partial">("full");
  const [search, setSearch] = useState("");
  const [step, setStep] = useState<"reqs" | "control">("reqs");
  const [done, setDone] = useState<{ count: number; control: string } | null>(null);
  const byId = useMemo(() => {
    const m = new Map<string, { ref: string }>();
    for (const f of wb.data?.frameworks ?? []) for (const r of f.requirements) m.set(r.id, { ref: r.ref });
    return m;
  }, [wb.data]);
  const ids = useMemo(() => [...picked].sort(), [picked]);
  const sugg = useQuery({
    queryKey: ["grc", "suggest", ids],
    queryFn: () => livingApi.suggest(ids),
    enabled: ids.length > 0,
    placeholderData: keepPreviousData,
  });
  const apply = useMutation({
    mutationFn: () => livingApi.mapBulk(control, ids.map((id) => byId.get(id)!.ref), coverage),
    onSuccess: () => {
      setDone({ count: ids.length, control });
      setPicked([]);
      setControl("");
      setStep("reqs");
      qc.invalidateQueries({ queryKey: ["grc"] });
    },
  });
  const d = wb.data;
  if (wb.error) return <ErrorNote error={wb.error} />;
  if (!d) return <Loading className="m-0 text-muted" />;
  const missing = d.frameworks.reduce((n, f) => n + f.missing, 0);
  const suggested = ids.length > 0 ? (sugg.data?.items ?? []) : [];
  const sugMap = new Map<string, Suggestion>(suggested.map((s) => [s.control, s]));
  const q = search.trim().toLowerCase();
  const toggle = (id: string) => {
    setDone(null);
    setPicked((p) => (p.includes(id) ? p.filter((x) => x !== id) : [...p, id]));
  };
  const ctrlByKey = new Map<string, WorkControl>(d.controls.map((c) => [c.key, c]));
  const others = d.controls.filter((c) => !sugMap.has(c.key) && (!q || `${c.key} ${c.title}`.toLowerCase().includes(q)));
  const listed = suggested.filter((s) => ctrlByKey.has(s.control));
  return (
    <section className="flex flex-col gap-3">
      <SectionTitle aside={<span className="font-mono text-[11.5px] text-muted tabular">{missing}</span>}>{tr("governance.controlli.map_title")}</SectionTitle>
      <p className="m-0 text-[12.5px] text-muted">{tr("governance.controlli.map_intro")}</p>
      {!d.can.edit && <p className="m-0 text-[12.5px] text-muted">{tr("governance.controlli.map_view_readonly")}</p>}
      {done && (
        <p role="status" className="m-0 border-l-2 border-ok pl-2 text-ok" data-testid="map-done">
          {tr("governance.controlli.map_done", { count: done.count, control: done.control })}
        </p>
      )}
      {missing === 0 ? (
        <p className="m-0 text-muted">{tr("governance.controlli.map_none")}</p>
      ) : (
        <div className="grid grid-cols-1 gap-6 md:grid-cols-2">
          <div className={`min-w-0 flex-col gap-3 ${step === "reqs" ? "flex" : "hidden md:flex"}`} data-testid="map-left">
            <span className="label text-muted">{tr("governance.controlli.map_requirements")}</span>
            {d.frameworks
              .filter((f) => f.missing > 0)
              .map((f) => {
                const all = f.requirements.map((r) => r.id);
                const allOn = all.every((id) => picked.includes(id));
                return (
                  <fieldset key={f.key} className="m-0 flex min-w-0 flex-col border-0 p-0">
                    <legend className="mb-1 flex w-full flex-wrap items-center justify-between gap-2 p-0">
                      <span className="flex items-center gap-2 font-semibold">
                        <FrameworkTag k={f.key} /> {f.name}
                        <span className="text-[12px] font-normal text-muted">{tr("governance.controlli.map_missing_count", { missing: f.missing, total: f.total })}</span>
                      </span>
                      <button
                        type="button"
                        className="touch-44 text-[12.5px] text-accent underline"
                        onClick={() => {
                          setDone(null);
                          setPicked((p) => (allOn ? p.filter((x) => !all.includes(x)) : [...new Set([...p, ...all])]));
                        }}
                      >
                        {allOn ? tr("governance.controlli.map_clear") : tr("governance.controlli.map_select_all")}
                      </button>
                    </legend>
                    {f.requirements.map((r) => (
                      <label key={r.id} className="flex min-h-[44px] cursor-pointer items-start gap-2 border-b border-hair py-2 md:min-h-0">
                        <input type="checkbox" className="mt-1 shrink-0" checked={picked.includes(r.id)} onChange={() => toggle(r.id)} />
                        <span className="min-w-0 text-[13px]">
                          <span className="font-mono text-[12px] text-accent">{r.req_ref}</span> {r.title}
                        </span>
                      </label>
                    ))}
                  </fieldset>
                );
              })}
            <div className="sticky bottom-0 flex flex-wrap items-center gap-2 bg-bg py-2 md:hidden">
              <span className="text-[12.5px] text-muted">{tr("governance.controlli.map_selected", { count: picked.length })}</span>
              <button className="btn btn-primary" disabled={picked.length === 0} onClick={() => setStep("control")}>{tr("governance.controlli.map_next")}</button>
            </div>
          </div>

          <div className={`min-w-0 flex-col gap-3 ${step === "control" ? "flex" : "hidden md:flex"}`} data-testid="map-right">
            <button type="button" className="btn self-start md:hidden" onClick={() => setStep("reqs")}>{tr("governance.controlli.map_back")}</button>
            <span className="label text-muted">{tr("governance.controlli.map_controls")}</span>
            <span className="text-[12.5px] text-muted">{tr("governance.controlli.map_selected", { count: picked.length })}</span>
            {picked.length > 0 && (
              <fieldset className="m-0 flex min-w-0 flex-col border-0 p-0">
                <legend className="label mb-1 p-0 text-muted">{tr("governance.controlli.map_suggested")}</legend>
                {listed.length === 0 && !sugg.isFetching && <p className="m-0 text-[12.5px] text-muted">{tr("governance.controlli.map_no_suggestions")}</p>}
                {listed.map((s) => (
                  <ControlOption key={s.control} c={ctrlByKey.get(s.control)!} sug={s} checked={control === s.control} onPick={() => setControl(s.control)} />
                ))}
              </fieldset>
            )}
            <fieldset className="m-0 flex min-w-0 flex-col border-0 p-0">
              <legend className="label mb-1 p-0 text-muted">{tr("governance.controlli.map_all_controls")}</legend>
              <input className="field mb-1" type="search" aria-label={tr("governance.controlli.map_search")} placeholder={tr("governance.controlli.map_search")} value={search} onChange={(e) => setSearch(e.target.value)} />
              {others.map((c) => (
                <ControlOption key={c.key} c={c} checked={control === c.key} onPick={() => setControl(c.key)} />
              ))}
            </fieldset>
            {d.can.edit && (
              <div className="sticky bottom-0 flex flex-col gap-2 bg-bg py-2">
                <label className="flex flex-col gap-1">
                  <span className="label text-muted">{tr("governance.controlli.map_coverage")}</span>
                  <Select className="field" value={coverage} onChange={(e) => setCoverage(e.target.value as "full" | "partial")}>
                    <option value="full">{tr("governance.controlli.cov_full")}</option>
                    <option value="partial">{tr("governance.controlli.cov_partial")}</option>
                  </Select>
                </label>
                <ErrorNote error={apply.error} />
                <button className="btn btn-primary" disabled={apply.isPending || picked.length === 0 || !control} onClick={() => apply.mutate()}>
                  {control && picked.length > 0 ? tr("governance.controlli.map_apply", { count: picked.length, control }) : picked.length === 0 ? tr("governance.controlli.map_pick_reqs") : tr("governance.controlli.map_pick_control")}
                </button>
              </div>
            )}
          </div>
        </div>
      )}
    </section>
  );
}

function ControlOption({ c, sug, checked, onPick }: { c: WorkControl; sug?: Suggestion; checked: boolean; onPick: () => void }) {
  return (
    <label className="flex min-h-[44px] cursor-pointer items-start gap-2 border-b border-hair py-2 md:min-h-0">
      <input type="radio" name="map-control" className="mt-1 shrink-0" checked={checked} onChange={onPick} />
      <span className="min-w-0 text-[13px]">
        <span className="font-mono text-[12px] text-accent">{c.key}</span> {c.title}
        <span className="flex flex-wrap gap-x-2 text-[12px] text-muted">
          {c.frameworks.map((f) => (
            <FrameworkTag key={f} k={f} />
          ))}
          {sug && <span>{tr("governance.controlli.map_score", { score: sug.score })}</span>}
          {sug && sug.words.length > 0 && <span>{tr("governance.controlli.map_reason_words", { words: sug.words.join(", ") })}</span>}
          {sug?.category === "chapter" && <span>{tr("governance.controlli.map_reason_chapter")}</span>}
          {sug?.category === "framework" && <span>{tr("governance.controlli.map_reason_framework")}</span>}
        </span>
      </span>
    </label>
  );
}
