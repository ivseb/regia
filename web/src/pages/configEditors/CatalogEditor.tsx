// Editor visuale del catalogo dei requisiti (replica/usabilita.md, proposte 6 e 8): voci con
// verifica e verificatore, baseline con condizione scritta col costruttore e voci scelte da
// menu, importazione CSV con anteprima. Il JSON resta nel riquadro «Avanzato». Bozza,
// pubblicazione e problemi del server come prima; i problemi stanno accanto alla riga giusta.
import { useEffect, useMemo, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import type { Problem } from "../../api";
import { requirementsApi, type Baseline, type Catalog, type CatalogDefinition, type CatalogItem, type RequirementConfig } from "../../requirementsApi";
import { OptionSelect, Select } from "../../select";
import { ErrorNote, SectionTitle } from "../../ui";
import { useT } from "../../i18n/react";
import { Trans } from "../../i18n/Trans";
import { useDebounced, type CondCount } from "../../casaApi";
import { ConditionBuilder } from "../processEditor/ConditionBuilder";
import { AdvancedJson, RowProblems, RowTools, move, problemsOutside, replaceAt } from "./common";
import { csvToItems, lastVerifier, mergeItems, type CsvRow } from "./csvVoci";

const ROW_PATH = /^(items|baselines)\[\d+\]/;
const SEARCH_FROM = 8;

export function CatalogEditor({ catalogKey, cfg, c, initial, onChanged }: { catalogKey: string; cfg: RequirementConfig; c: Catalog | undefined; initial: CatalogDefinition; onChanged: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const [def, setDef] = useState<CatalogDefinition>(initial);
  const [dirty, setDirty] = useState(false);
  const [notes, setNotes] = useState("");
  const [live, setLive] = useState<Problem[]>([]);
  const [csv, setCsv] = useState(false);
  const edit = (next: CatalogDefinition) => {
    setDef(next);
    setDirty(true);
  };

  // Mentre si modifica, i problemi si chiedono al server (come prima, ma sulla definizione a oggetti).
  const wanted = useDebounced(dirty ? def : null, 400);
  useEffect(() => {
    if (!wanted) return;
    let alive = true;
    requirementsApi.validate(wanted).then(
      (r) => alive && setLive(r.problems),
      () => undefined,
    );
    return () => {
      alive = false;
    };
  }, [wanted]);
  const saved = c?.draft_problems ?? [];
  const problems = (dirty ? live : saved).filter((p) => p.level === "error");

  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["requirement-config"] });
    onChanged();
  };
  const save = useMutation({
    mutationFn: () => requirementsApi.saveDraft(catalogKey, def, notes),
    onSuccess: () => {
      setDirty(false);
      refresh();
    },
  });
  const discard = useMutation({
    mutationFn: () => requirementsApi.discardDraft(catalogKey),
    onSuccess: refresh,
  });
  const publish = useMutation({
    mutationFn: () => requirementsApi.publish(catalogKey, notes),
    onSuccess: () => {
      setNotes("");
      refresh();
    },
  });

  const setItems = (items: CatalogItem[]) => edit({ ...def, items });
  const setBaselines = (baselines: Baseline[]) => edit({ ...def, baselines });
  const itemChanged = (i: number, patch: Partial<CatalogItem>) => {
    const old = def.items[i].key;
    const items = replaceAt(def.items, i, patch);
    // Una chiave rinominata si porta dietro i riferimenti delle baseline.
    const baselines = patch.key !== undefined && patch.key !== old ? def.baselines.map((b) => ({ ...b, items: b.items.map((k) => (k === old ? patch.key! : k)) })) : def.baselines;
    edit({ ...def, items, baselines });
  };
  const removeItem = (i: number) => {
    const key = def.items[i].key;
    edit({ ...def, items: def.items.filter((_, a) => a !== i), baselines: def.baselines.map((b) => ({ ...b, items: b.items.filter((k) => k !== key) })) });
  };
  const addItem = () => {
    let n = def.items.length + 1;
    while (def.items.some((x) => x.key === `REQ-${String(n).padStart(2, "0")}`)) n++;
    setItems([...def.items, { key: `REQ-${String(n).padStart(2, "0")}`, title: "", category: "", verification: { method: "manual", ...lastVerifier(def.items) } }]);
  };
  const addBaseline = () => {
    let n = def.baselines.length + 1;
    while (def.baselines.some((x) => x.key === `baseline_${n}`)) n++;
    setBaselines([...def.baselines, { key: `baseline_${n}`, name: "", scope: "application", when: "", items: [] }]);
  };

  const outside = problemsOutside(problems, ROW_PATH);
  const itemOptions = useMemo(() => def.items.map((it) => ({ value: it.key, label: `${it.key} · ${it.title || "—"}` })), [def.items]);
  const [q, setQ] = useState("");
  const needle = q.trim().toLowerCase();
  const shown = def.items.map((it, i) => ({ it, i })).filter(({ it }) => !needle || `${it.key} ${it.title} ${it.category ?? ""}`.toLowerCase().includes(needle));

  return (
    <section className="flex min-w-0 flex-col gap-5" aria-label={t("configurazione.requirements.bozza2")}>
      <SectionTitle aside={c?.draft ? <span className="text-[12px] text-muted">{t("configurazione.processEditor.draftVersion", { version: c.draft.version })}</span> : undefined}>
        {c?.draft ? t("configurazione.requirements.bozza2") : t("configurazione.requirements.nuovaVersione")}
      </SectionTitle>
      <p className="m-0 max-w-3xl text-[12.5px] text-muted">{t("configurazione.catalogEditor.help")}</p>

      <div className="grid max-w-3xl grid-cols-1 gap-3 md:grid-cols-2">
        <label className="flex flex-col gap-1">
          <span className="label">{t("configurazione.scorecardEditor.name")}</span>
          <input className="field" value={def.name} onChange={(e) => edit({ ...def, name: e.target.value })} />
        </label>
        <label className="flex flex-col gap-1">
          <span className="label">{t("configurazione.scorecardEditor.description")}</span>
          <input className="field" value={def.description ?? ""} onChange={(e) => edit({ ...def, description: e.target.value })} />
        </label>
      </div>
      {outside.length > 0 && (
        <ul className="m-0 list-none border-l-2 border-danger p-0 pl-2.5 text-[12.5px] text-danger">
          {outside.map((p, i) => (
            <li key={i}>
              <span className="font-mono">{p.path}</span> {p.message}
            </li>
          ))}
        </ul>
      )}

      <div className="flex flex-col gap-3">
        <SectionTitle aside={<span className="font-mono text-[11.5px] text-muted tabular">{def.items.length}</span>}>{t("configurazione.requirements.voci")}</SectionTitle>
        <div className="flex flex-wrap items-end gap-2">
          {def.items.length >= SEARCH_FROM && (
            <label className="flex min-w-0 flex-1 flex-col gap-1 md:max-w-sm">
              <span className="label">{t("configurazione.catalogEditor.searchItems")}</span>
              <input className="field" type="search" value={q} onChange={(e) => setQ(e.target.value)} />
            </label>
          )}
          <button type="button" className="btn" onClick={addItem}>
            {t("configurazione.catalogEditor.addItem")}
          </button>
          <button type="button" className="btn" aria-expanded={csv} onClick={() => setCsv(!csv)}>
            {t("configurazione.catalogEditor.importCsv")}
          </button>
        </div>
        {csv && (
          <CsvImport
            onApply={(rows) => {
              setItems(mergeItems(def.items, rows));
              setCsv(false);
            }}
            existing={def.items.map((x) => x.key)}
            onClose={() => setCsv(false)}
          />
        )}
        {needle && <p className="m-0 text-[12.5px] text-muted">{t("configurazione.catalogEditor.shownOf", { shown: shown.length, total: def.items.length })}</p>}
        <ol className="m-0 flex list-none flex-col gap-3 p-0">
          {shown.map(({ it, i }) => (
            <ItemCard
              key={i}
              i={i}
              it={it}
              count={def.items.length}
              cfg={cfg}
              problems={problems}
              onChange={(patch) => itemChanged(i, patch)}
              onMove={needle ? undefined : (to) => setItems(move(def.items, i, to))}
              onRemove={() => removeItem(i)}
            />
          ))}
        </ol>
      </div>

      <div className="flex flex-col gap-3">
        <SectionTitle aside={<span className="font-mono text-[11.5px] text-muted tabular">{def.baselines.length}</span>}>{t("configurazione.requirements.baseline")}</SectionTitle>
        <ol className="m-0 flex list-none flex-col gap-3 p-0">
          {def.baselines.map((b, i) => (
            <BaselineCard
              key={i}
              i={i}
              b={b}
              count={def.baselines.length}
              options={itemOptions}
              problems={problems}
              onChange={(patch) => setBaselines(replaceAt(def.baselines, i, patch))}
              onMove={(to) => setBaselines(move(def.baselines, i, to))}
              onRemove={() => setBaselines(def.baselines.filter((_, a) => a !== i))}
            />
          ))}
        </ol>
        <div>
          <button type="button" className="btn" onClick={addBaseline}>
            {t("configurazione.catalogEditor.addBaseline")}
          </button>
        </div>
      </div>

      <AdvancedJson value={def} label={t("configurazione.requirements.definizioneDelCatalogoIn")} onApply={edit} />

      {problems.length === 0 && dirty && <p className="m-0 text-[12.5px] text-ok">{t("configurazione.requirements.nessunProblema")}</p>}
      <label className="flex flex-col gap-1">
        <span className="label">{t("configurazione.common.noteDellaVersione")}</span>
        <input className="field" value={notes} onChange={(e) => setNotes(e.target.value)} />
      </label>
      <span className="flex flex-wrap items-center gap-2">
        <button className="btn" disabled={save.isPending || !dirty} onClick={() => save.mutate()}>
          {t("configurazione.common.salvaLaBozza")}
        </button>
        {c?.draft && (
          <>
            <button className="btn btn-primary" disabled={publish.isPending || dirty || (c.draft_problems ?? []).some((p) => p.level === "error")} onClick={() => publish.mutate()}>
              {t("configurazione.requirements.pubblica")}
            </button>
            <button className="btn btn-danger" disabled={discard.isPending} onClick={() => discard.mutate()}>
              {t("configurazione.common.scartaLaBozza")}
            </button>
          </>
        )}
        {dirty && <span className="text-[12.5px] text-flag">{t("configurazione.common.modificheNonSalvate")}</span>}
      </span>
      <ErrorNote error={save.error ?? publish.error ?? discard.error} />
    </section>
  );
}

function ItemCard({
  i,
  it,
  count,
  cfg,
  problems,
  onChange,
  onMove,
  onRemove,
}: {
  i: number;
  it: CatalogItem;
  count: number;
  cfg: RequirementConfig;
  problems: Problem[];
  onChange: (patch: Partial<CatalogItem>) => void;
  onMove?: (to: number) => void;
  onRemove: () => void;
}) {
  const t = useT();
  const v = it.verification;
  const name = it.key || String(i + 1);
  const who = v.role && !v.team ? "role" : "team";
  const setV = (patch: Partial<CatalogItem["verification"]>) => onChange({ verification: { ...v, ...patch } });
  return (
    <li className="flex flex-col gap-3 border border-hair p-3" data-testid="voce" aria-label={name}>
      <div className="flex items-start justify-between gap-2">
        <span className="font-mono text-[12px] text-muted tabular">{i + 1}</span>
        <RowTools i={i} count={count} name={name} onMove={onMove} onRemove={onRemove} />
      </div>
      <div className="grid grid-cols-1 gap-3 md:grid-cols-[minmax(0,1fr)_minmax(0,3fr)_minmax(0,1.2fr)]">
        <label className="flex flex-col gap-1">
          <span className="label">{t("configurazione.scorecardEditor.key")}</span>
          <input className="field font-mono" value={it.key} onChange={(e) => onChange({ key: e.target.value })} />
        </label>
        <label className="flex flex-col gap-1">
          <span className="label">{t("configurazione.catalogEditor.title")}</span>
          <input className="field" value={it.title} onChange={(e) => onChange({ title: e.target.value })} />
        </label>
        <label className="flex flex-col gap-1">
          <span className="label">{t("configurazione.catalogEditor.category")}</span>
          <input className="field" value={it.category ?? ""} onChange={(e) => onChange({ category: e.target.value })} />
        </label>
        <label className="flex flex-col gap-1 md:col-span-3">
          <span className="label">{t("configurazione.catalogEditor.text")}</span>
          <textarea className="field min-h-[56px]" value={it.text ?? ""} onChange={(e) => onChange({ text: e.target.value })} />
        </label>
      </div>
      <div className="grid grid-cols-1 gap-3 md:grid-cols-3">
        <div className="flex flex-col gap-1">
          <span className="label" id={`vm-${i}`}>
            {t("configurazione.catalogEditor.method")}
          </span>
          <Select aria-labelledby={`vm-${i}`} className="field" value={v.method} onChange={(e) => setV({ method: e.target.value as "manual" | "evidence", check: e.target.value === "manual" ? undefined : v.check })}>
            <option value="manual">{t("configurazione.catalogEditor.methodManual")}</option>
            <option value="evidence">{t("configurazione.catalogEditor.methodEvidence")}</option>
          </Select>
        </div>
        {v.method === "evidence" && (
          <label className="flex flex-col gap-1">
            <span className="label">{t("configurazione.catalogEditor.check")}</span>
            <input className="field font-mono" value={v.check ?? ""} onChange={(e) => setV({ check: e.target.value })} />
          </label>
        )}
        <label className={`flex flex-col gap-1 ${v.method === "evidence" ? "" : "md:col-span-2"}`}>
          <span className="label">{t("configurazione.catalogEditor.criterion")}</span>
          <input className="field" value={v.criterion ?? ""} onChange={(e) => setV({ criterion: e.target.value })} />
        </label>
        <div className="flex flex-col gap-1">
          <span className="label" id={`vw-${i}`}>
            {t("configurazione.catalogEditor.verifier")}
          </span>
          <Select aria-labelledby={`vw-${i}`} className="field" value={who} onChange={(e) => setV(e.target.value === "role" ? { role: v.role ?? "", team: undefined } : { team: v.team ?? "", role: undefined })}>
            <option value="team">{t("configurazione.catalogEditor.byTeam")}</option>
            <option value="role">{t("configurazione.catalogEditor.byRole")}</option>
          </Select>
        </div>
        <div className="flex flex-col gap-1 md:col-span-2">
          <span className="label" id={`vv-${i}`}>
            {who === "role" ? t("configurazione.catalogEditor.role") : t("configurazione.catalogEditor.team")}
          </span>
          {who === "role" ? (
            <OptionSelect
              aria-labelledby={`vv-${i}`}
              options={cfg.roles.map((r) => ({ value: r.key, label: r.name }))}
              value={v.role ?? ""}
              noneLabel={t("configurazione.catalogEditor.none")}
              onChange={(role) => setV({ role: role || undefined })}
            />
          ) : (
            <OptionSelect
              aria-labelledby={`vv-${i}`}
              options={cfg.teams.filter((x) => !x.archived || x.key === v.team).map((x) => ({ value: x.key, label: x.name }))}
              value={v.team ?? ""}
              noneLabel={t("configurazione.catalogEditor.none")}
              onChange={(team) => setV({ team: team || undefined })}
            />
          )}
        </div>
      </div>
      <RowProblems problems={problems} prefix={`items[${i}]`} />
    </li>
  );
}

function BaselineCard({
  i,
  b,
  count,
  options,
  problems,
  onChange,
  onMove,
  onRemove,
}: {
  i: number;
  b: Baseline;
  count: number;
  options: { value: string; label: string }[];
  problems: Problem[];
  onChange: (patch: Partial<Baseline>) => void;
  onMove: (to: number) => void;
  onRemove: () => void;
}) {
  const t = useT();
  const name = b.name || b.key;
  const type = b.scope === "project" ? "business_request" : "application";
  const noun = b.scope === "project" ? t("configurazione.requirements.richiesteAperte") : t("configurazione.requirements.applicazioniAttive");
  const countItems = async (cond: string): Promise<CondCount> => {
    const r = await requirementsApi.preview(cond, b.scope);
    const hit = r.items.filter((x) => x.matched);
    return { ok: true, matched: hit.length, total: r.items.length, items: hit.slice(0, 5).map((x) => ({ key: x.code ?? "", title: x.title })) };
  };
  return (
    <li className="flex flex-col gap-3 border border-hair p-3" data-testid="baseline" aria-label={name}>
      <div className="flex items-start justify-between gap-2">
        <span className="font-mono text-[12px] text-muted tabular">{i + 1}</span>
        <RowTools i={i} count={count} name={name} onMove={onMove} onRemove={onRemove} />
      </div>
      <div className="grid grid-cols-1 gap-3 md:grid-cols-[minmax(0,1fr)_minmax(0,2fr)_minmax(0,1.2fr)]">
        <label className="flex flex-col gap-1">
          <span className="label">{t("configurazione.scorecardEditor.key")}</span>
          <input className="field font-mono" value={b.key} onChange={(e) => onChange({ key: e.target.value })} />
        </label>
        <label className="flex flex-col gap-1">
          <span className="label">{t("configurazione.scorecardEditor.name")}</span>
          <input className="field" value={b.name} onChange={(e) => onChange({ name: e.target.value })} />
        </label>
        <div className="flex flex-col gap-1">
          <span className="label" id={`bs-${i}`}>
            {t("configurazione.catalogEditor.scope")}
          </span>
          <Select aria-labelledby={`bs-${i}`} className="field" value={b.scope} onChange={(e) => onChange({ scope: e.target.value as Baseline["scope"] })}>
            <option value="application">{t("configurazione.requirements.perApplicazione")}</option>
            <option value="project">{t("configurazione.requirements.project")}</option>
          </Select>
        </div>
      </div>
      <ConditionBuilder label={t("configurazione.catalogEditor.appliesWhen")} value={b.when ?? ""} onChange={(when) => onChange({ when })} type={type} count={countItems} noun={noun} empty={t("casa.requirements.allApply")} />
      <div className="flex flex-col gap-1">
        <span className="label" id={`bi-${i}`}>
          {t("configurazione.catalogEditor.baselineItems", { n: b.items.length })}
        </span>
        <OptionSelect multiple aria-labelledby={`bi-${i}`} options={options} value={b.items} onChange={(items) => onChange({ items })} placeholder={t("configurazione.catalogEditor.addItems")} />
      </div>
      <RowProblems problems={problems} prefix={`baselines[${i}]`} />
    </li>
  );
}

/** Importa CSV: si incolla o si sceglie un file, si vede l'anteprima (nuove, aggiornate, da correggere), poi si applica alla bozza. */
function CsvImport({ existing, onApply, onClose }: { existing: string[]; onApply: (rows: CsvRow[]) => void; onClose: () => void }) {
  const t = useT();
  const [text, setText] = useState("");
  const rows = useMemo(() => csvToItems(text), [text]);
  const valid = rows.filter((r) => !r.error);
  const dup = (k: string) => existing.includes(k);
  return (
    <div className="flex flex-col gap-2 border border-hair p-3" data-testid="csv-import">
      <p className="m-0 text-[12.5px] text-muted">
        {/* i18n-ok: nomi delle colonne del CSV, fissi */}
        <Trans k="configurazione.catalogEditor.csvHelp" slots={{ cols: <span className="font-mono">key,title,text,category,method</span> }} />
      </p>
      <label className="flex flex-col gap-1">
        <span className="label">{t("configurazione.catalogEditor.csvFile")}</span>
        <input type="file" accept=".csv,text/csv,text/plain" className="text-[12.5px]" onChange={(e) => e.target.files?.[0]?.text().then(setText)} />
      </label>
      <label className="flex flex-col gap-1">
        <span className="label">{t("configurazione.catalogEditor.csvText")}</span>
        <textarea className="field min-h-[100px] font-mono text-[12px]" spellCheck={false} value={text} onChange={(e) => setText(e.target.value)} />
      </label>
      {rows.length > 0 && (
        <>
          <p className="m-0 text-[12.5px]" role="status">
            {t("configurazione.catalogEditor.csvSummary", { valid: valid.length, total: rows.length, update: valid.filter((r) => dup(r.item.key)).length })}
          </p>
          <ul className="m-0 flex max-h-64 list-none flex-col overflow-y-auto p-0 text-[12.5px]" data-testid="csv-anteprima">
            {rows.map((r) => (
              <li key={r.line} className="flex flex-wrap items-baseline gap-x-2 border-b border-hair py-1">
                <span className="font-mono text-muted">{r.line}</span>
                <span className="font-mono">{r.item.key || "—"}</span>
                <span className="min-w-0 flex-1">{r.item.title || "—"}</span>
                <span className={r.error ? "text-danger" : "text-muted"}>{r.error ? t(`configurazione.catalogEditor.csvError.${r.error}`) : dup(r.item.key) ? t("configurazione.catalogEditor.csvUpdate") : t("configurazione.catalogEditor.csvNew")}</span>
              </li>
            ))}
          </ul>
        </>
      )}
      <div className="flex flex-wrap gap-2">
        <button type="button" className="btn btn-primary" disabled={valid.length === 0} onClick={() => onApply(rows)}>
          {t("configurazione.catalogEditor.csvApply", { count: valid.length })}
        </button>
        <button type="button" className="btn" onClick={onClose}>
          {t("configurazione.common.annulla")}
        </button>
      </div>
    </div>
  );
}
