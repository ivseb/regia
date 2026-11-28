// Configurazione → Continuità (/configurazione/continuita): categorie d'impatto e livelli, livello
// intollerabile, orizzonti, tipi di test e soglie; si salva con la versione (409 se qualcuno ha
// salvato nel frattempo), con lo storico delle versioni. In fondo i controlli GRC iscritti al
// collettore `continuity.test`, a cui va l'evidenza dei test eseguiti.
import { useMemo, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError } from "../../api";
import { PageHeader, useMe } from "../../shell";
import { moduleOn } from "../../modules";
import { OptionSelect, Select } from "../../select";
import { ErrorNote, Loading, SectionTitle } from "../../ui";
import { useT } from "../../i18n/react";
import { fmtDateTime } from "../../i18n/format";
import { grcApi } from "../../grcApi";
import { continuityApi, fmtMinutes, type Category, type Config, type Horizon, type Settings, type TestType } from "../../continuityApi";
import { ContinuityError, DurationInput, NotReady } from "./ui";

export const TEST_COLLECTOR = "continuity.test";

/** Chiave proposta dal nome: minuscole, senza accenti, parole unite da trattino basso. */
export function keyFromName(name: string): string {
  return name
    .normalize("NFD")
    .replace(/[̀-ͯ]/g, "")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "_")
    .replace(/^_+|_+$/g, "")
    .slice(0, 40);
}

export function ContinuityConfigPage() {
  const t = useT();
  const s = useQuery({ queryKey: ["continuity", "settings"], queryFn: continuityApi.settings });
  return (
    <>
      <PageHeader path={t("continuita.config.path")} title={t("continuita.config.title")} />
      <div className="flex max-w-[960px] flex-col gap-6 px-4 pb-10 md:px-7">
        <p className="m-0 max-w-3xl text-muted">{t("continuita.config.intro")}</p>
        {s.isPending && <Loading />}
        <ContinuityError error={s.error} />
        {s.data && !s.data.ready && <NotReady can={s.data.can} />}
        {s.data?.ready && <Editor key={s.data.version} settings={s.data} />}
        {s.data?.ready && <Versions />}
        {s.data?.ready && <Collectors canEdit={s.data.can.admin} />}
      </div>
    </>
  );
}

function Editor({ settings }: { settings: Settings }) {
  const t = useT();
  const qc = useQueryClient();
  const canEdit = settings.can.admin;
  const [c, setC] = useState<Config>(() => structuredClone(settings.config));
  const known = useMemo(
    () => ({
      cat: new Set(settings.config.categories.map((x) => x.key)),
      hor: new Set(settings.config.horizons.map((x) => x.key)),
      type: new Set(settings.config.test_types.map((x) => x.key)),
    }),
    [settings.config],
  );
  const dirty = JSON.stringify(c) !== JSON.stringify(settings.config);
  const save = useMutation({
    mutationFn: () => continuityApi.saveSettings(settings.version, withKeys(c)),
    onSuccess: (d) => {
      qc.setQueryData(["continuity", "settings"], d);
      void qc.invalidateQueries({ queryKey: ["continuity"] });
    },
  });
  const conflict = save.error instanceof ApiError && save.error.status === 409;
  const setCat = (i: number, p: Partial<Category>) => setC((x) => ({ ...x, categories: x.categories.map((k, j) => (j === i ? { ...k, ...p } : k)) }));
  const setHor = (i: number, p: Partial<Horizon>) => setC((x) => ({ ...x, horizons: x.horizons.map((k, j) => (j === i ? { ...k, ...p } : k)) }));
  const setType = (i: number, p: Partial<TestType>) => setC((x) => ({ ...x, test_types: x.test_types.map((k, j) => (j === i ? { ...k, ...p } : k)) }));
  const num = (v: string) => (v.trim() === "" ? 0 : Math.max(0, Math.round(Number(v)) || 0));

  return (
    <form
      className="flex flex-col gap-6"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      <p className="m-0 text-[12.5px] text-muted">
        {t("continuita.config.version", { v: settings.version })}
        {settings.updated_at && ` · ${t("continuita.config.updated", { at: fmtDateTime(settings.updated_at), by: settings.updated_by ?? "—" })}`}
      </p>
      {!canEdit && <p className="m-0 text-[12.5px] text-muted">{t("continuita.config.readOnly")}</p>}

      {/* Categorie e livelli */}
      <section className="flex flex-col gap-3" aria-label={t("continuita.config.categories")}>
        <SectionTitle>{t("continuita.config.categories")}</SectionTitle>
        <p className="m-0 text-[12.5px] text-muted">{t("continuita.config.categoriesHelp")}</p>
        {c.categories.map((cat, i) => (
          <fieldset key={i} className="m-0 flex flex-col gap-2 border-0 border-b border-hair p-0 pb-3">
            <legend className="sr-only">{cat.name || t("continuita.config.newCategory")}</legend>
            <div className="flex flex-wrap items-end gap-3">
              <label className="flex min-w-0 flex-[1_1_200px] flex-col gap-1">
                <span className="label">{t("continuita.config.name")}</span>
                <input className="field" required value={cat.name} disabled={!canEdit} onChange={(e) => setCat(i, { name: e.target.value })} />
              </label>
              <KeyField value={cat.key} locked={known.cat.has(cat.key)} disabled={!canEdit} fallback={keyFromName(cat.name)} onChange={(k) => setCat(i, { key: k })} />
              <label className="flex w-full flex-col gap-1 sm:w-56">
                <span className="label">{t("continuita.config.intolerable")}</span>
                <Select className="field" value={String(cat.intolerable)} disabled={!canEdit} onChange={(e) => setCat(i, { intolerable: Number(e.target.value) })}>
                  {cat.levels.map((l) => (
                    <option key={l.value} value={String(l.value)}>
                      {`${l.value} · ${l.label}`}
                    </option>
                  ))}
                </Select>
              </label>
              {canEdit && (
                <button
                  type="button"
                  className="bg-transparent p-0 pb-2 text-[12px] text-muted underline hover:text-danger"
                  onClick={() => setC((x) => ({ ...x, categories: x.categories.filter((_, j) => j !== i) }))}
                >
                  {t("continuita.config.remove")}
                </button>
              )}
            </div>
            <div className="flex flex-col gap-1.5">
              <span className="label">{t("continuita.config.levels")}</span>
              <ol className="m-0 flex list-none flex-col gap-1.5 p-0">
                {cat.levels.map((l, li) => (
                  <li key={li} className="flex items-center gap-2">
                    <span className="w-6 text-right font-mono text-[12px] text-muted tabular">{l.value}</span>
                    <input
                      className="field min-w-0 flex-1"
                      required
                      aria-label={t("continuita.config.levelLabel", { n: l.value })}
                      value={l.label}
                      disabled={!canEdit}
                      onChange={(e) => setCat(i, { levels: cat.levels.map((x, j) => (j === li ? { ...x, label: e.target.value } : x)) })}
                    />
                    {canEdit && li === cat.levels.length - 1 && cat.levels.length > 2 && (
                      <button
                        type="button"
                        className="bg-transparent p-0 text-[12px] text-muted underline hover:text-danger"
                        onClick={() => {
                          const levels = cat.levels.slice(0, -1);
                          setCat(i, { levels, intolerable: Math.min(cat.intolerable, levels[levels.length - 1].value) });
                        }}
                      >
                        {t("continuita.config.remove")}
                      </button>
                    )}
                  </li>
                ))}
              </ol>
              {canEdit && (
                <button
                  type="button"
                  className="btn self-start py-1 text-[12.5px]"
                  onClick={() => setCat(i, { levels: [...cat.levels, { value: (cat.levels[cat.levels.length - 1]?.value ?? 0) + 1, label: "" }] })}
                >
                  {t("continuita.config.addLevel")}
                </button>
              )}
            </div>
          </fieldset>
        ))}
        {canEdit && (
          <button
            type="button"
            className="btn self-start"
            onClick={() =>
              setC((x) => ({
                ...x,
                categories: [...x.categories, { key: "", name: "", intolerable: 4, levels: [1, 2, 3, 4, 5].map((v) => ({ value: v, label: "" })) }],
              }))
            }
          >
            {t("continuita.config.addCategory")}
          </button>
        )}
      </section>

      {/* Orizzonti */}
      <section className="flex flex-col gap-3" aria-label={t("continuita.config.horizons")}>
        <SectionTitle>{t("continuita.config.horizons")}</SectionTitle>
        <p className="m-0 text-[12.5px] text-muted">{t("continuita.config.horizonsHelp")}</p>
        <ul className="m-0 flex list-none flex-col gap-3 p-0">
          {c.horizons.map((h, i) => (
            <li key={i} className="flex flex-wrap items-end gap-3 border-b border-hair pb-3">
              <label className="flex min-w-0 flex-[1_1_160px] flex-col gap-1">
                <span className="label">{t("continuita.config.name")}</span>
                <input className="field" required value={h.name} disabled={!canEdit} onChange={(e) => setHor(i, { name: e.target.value })} />
              </label>
              <KeyField value={h.key} locked={known.hor.has(h.key)} disabled={!canEdit} fallback={keyFromName(h.name)} onChange={(k) => setHor(i, { key: k })} />
              <DurationInput label={t("continuita.config.duration")} value={h.minutes} disabled={!canEdit} onChange={(m) => setHor(i, { minutes: m ?? 0 })} />
              {canEdit && (
                <button
                  type="button"
                  className="bg-transparent p-0 pb-2 text-[12px] text-muted underline hover:text-danger"
                  onClick={() => setC((x) => ({ ...x, horizons: x.horizons.filter((_, j) => j !== i) }))}
                >
                  {t("continuita.config.remove")}
                </button>
              )}
            </li>
          ))}
        </ul>
        {canEdit && (
          <button type="button" className="btn self-start" onClick={() => setC((x) => ({ ...x, horizons: [...x.horizons, { key: "", name: "", minutes: 60 }] }))}>
            {t("continuita.config.addHorizon")}
          </button>
        )}
      </section>

      {/* Tipi di test */}
      <section className="flex flex-col gap-3" aria-label={t("continuita.config.testTypes")}>
        <SectionTitle>{t("continuita.config.testTypes")}</SectionTitle>
        <ul className="m-0 flex list-none flex-col gap-3 p-0">
          {c.test_types.map((x, i) => (
            <li key={i} className="flex flex-col gap-2 border-b border-hair pb-3">
              <div className="flex flex-wrap items-end gap-3">
                <label className="flex min-w-0 flex-[1_1_200px] flex-col gap-1">
                  <span className="label">{t("continuita.config.name")}</span>
                  <input className="field" required value={x.name} disabled={!canEdit} onChange={(e) => setType(i, { name: e.target.value })} />
                </label>
                <KeyField value={x.key} locked={known.type.has(x.key)} disabled={!canEdit} fallback={keyFromName(x.name)} onChange={(k) => setType(i, { key: k })} />
                {canEdit && (
                  <button
                    type="button"
                    className="bg-transparent p-0 pb-2 text-[12px] text-muted underline hover:text-danger"
                    onClick={() => setC((y) => ({ ...y, test_types: y.test_types.filter((_, j) => j !== i) }))}
                  >
                    {t("continuita.config.remove")}
                  </button>
                )}
              </div>
              <label className="flex flex-col gap-1">
                <span className="label">{t("continuita.config.description")}</span>
                <input className="field" value={x.description} disabled={!canEdit} onChange={(e) => setType(i, { description: e.target.value })} />
              </label>
            </li>
          ))}
        </ul>
        {canEdit && (
          <button type="button" className="btn self-start" onClick={() => setC((y) => ({ ...y, test_types: [...y.test_types, { key: "", name: "", description: "" }] }))}>
            {t("continuita.config.addTestType")}
          </button>
        )}
      </section>

      {/* Soglie */}
      <section className="flex flex-col gap-3" aria-label={t("continuita.config.thresholds")}>
        <SectionTitle>{t("continuita.config.thresholds")}</SectionTitle>
        <div className="flex flex-wrap items-end gap-5">
          <label className="flex w-44 flex-col gap-1">
            <span className="label">{t("continuita.config.testMaxAge")}</span>
            <input className="field tabular" inputMode="numeric" value={String(c.test_max_age_days)} disabled={!canEdit} onChange={(e) => setC((x) => ({ ...x, test_max_age_days: num(e.target.value) }))} />
          </label>
          <label className="flex w-44 flex-col gap-1">
            <span className="label">{t("continuita.config.biaReview")}</span>
            <input className="field tabular" inputMode="numeric" value={String(c.bia_review_months)} disabled={!canEdit} onChange={(e) => setC((x) => ({ ...x, bia_review_months: num(e.target.value) }))} />
          </label>
          <DurationInput
            label={t("continuita.config.criticalMtpd")}
            value={c.critical_mtpd_minutes}
            disabled={!canEdit}
            onChange={(m) => setC((x) => ({ ...x, critical_mtpd_minutes: m ?? 0 }))}
          />
        </div>
        <p className="m-0 text-[12.5px] text-muted">{t("continuita.config.criticalHelp", { limit: fmtMinutes(c.critical_mtpd_minutes) })}</p>
      </section>

      {canEdit && (
        <div className="flex flex-col gap-2">
          {conflict ? (
            <div role="alert" className="flex flex-col gap-2 border-l-4 border-danger pl-3">
              <p className="m-0">{t("continuita.config.conflict")}</p>
              <button type="button" className="btn self-start" onClick={() => void qc.invalidateQueries({ queryKey: ["continuity", "settings"] })}>
                {t("continuita.config.reload")}
              </button>
            </div>
          ) : (
            <ErrorNote error={save.error} />
          )}
          <div className="flex flex-wrap gap-2">
            <button className="btn btn-primary min-h-[44px]" disabled={!dirty || save.isPending}>
              {save.isPending ? t("continuita.common.saving") : t("continuita.config.save")}
            </button>
            {dirty && (
              <button type="button" className="btn min-h-[44px]" onClick={() => setC(structuredClone(settings.config))}>
                {t("continuita.common.discard")}
              </button>
            )}
          </div>
          {dirty && <p className="m-0 text-[12px] text-muted">{t("continuita.config.saveHint")}</p>}
        </div>
      )}
    </form>
  );
}

/** Le voci nuove senza chiave prendono quella proposta dal nome. */
function withKeys(c: Config): Config {
  return { ...c, categories: fillKeys(c.categories), horizons: fillKeys(c.horizons), test_types: fillKeys(c.test_types) };
}

function fillKeys<T extends { key: string; name: string }>(xs: T[]): T[] {
  return xs.map((x) => (x.key.trim() ? x : { ...x, key: keyFromName(x.name) }));
}

/** Chiave della voce: fissa per quelle già salvate (le BIA la usano), libera per le nuove. */
function KeyField({ value, locked, disabled, fallback, onChange }: { value: string; locked: boolean; disabled: boolean; fallback: string; onChange: (k: string) => void }) {
  const t = useT();
  if (locked)
    return (
      <span className="flex flex-col gap-1">
        <span className="label">{t("continuita.config.key")}</span>
        <span className="py-1.5 font-mono text-[12.5px] text-muted">{value}</span>
      </span>
    );
  return (
    <label className="flex w-40 flex-col gap-1">
      <span className="label">{t("continuita.config.key")}</span>
      <input className="field font-mono text-[12.5px]" value={value} placeholder={fallback} disabled={disabled} onChange={(e) => onChange(keyFromName(e.target.value))} />
    </label>
  );
}

function Versions() {
  const t = useT();
  const [open, setOpen] = useState<number | null>(null);
  const v = useQuery({ queryKey: ["continuity", "settings", "versions"], queryFn: continuityApi.settingsVersions });
  const items = v.data?.items ?? [];
  return (
    <section className="flex flex-col gap-2" aria-label={t("continuita.config.history")}>
      <SectionTitle>{t("continuita.config.history")}</SectionTitle>
      <ErrorNote error={v.error} />
      {v.data && items.length === 0 && <p className="m-0 text-[13px] text-muted">{t("continuita.config.noHistory")}</p>}
      <ul className="m-0 flex list-none flex-col p-0">
        {items.map((x) => (
          <li key={x.version} className="flex flex-col gap-1 border-b border-hair py-1.5 text-[12.5px]">
            <button type="button" className="flex flex-wrap items-baseline gap-x-3 bg-transparent p-0 text-left" aria-expanded={open === x.version} onClick={() => setOpen(open === x.version ? null : x.version)}>
              <span className="font-mono font-semibold tabular">v{x.version}</span>
              <span className="text-muted">
                {fmtDateTime(x.at)} · {x.by ?? "—"}
              </span>
              <span className="text-muted">
                {t("continuita.config.historyLine", { categories: x.config.categories.length, horizons: x.config.horizons.length, types: x.config.test_types.length })}
              </span>
            </button>
            {open === x.version && (
              <div className="flex flex-col gap-0.5 pl-3 text-muted">
                <span>{x.config.categories.map((c) => `${c.name} (${c.intolerable})`).join(", ")}</span>
                <span>{x.config.horizons.map((h) => `${h.name} ${fmtMinutes(h.minutes)}`).join(", ")}</span>
                <span>{x.config.test_types.map((h) => h.name).join(", ")}</span>
                <span>
                  {t("continuita.config.historyThresholds", {
                    days: x.config.test_max_age_days,
                    months: x.config.bia_review_months,
                    limit: fmtMinutes(x.config.critical_mtpd_minutes),
                  })}
                </span>
              </div>
            )}
          </li>
        ))}
      </ul>
    </section>
  );
}

/** Controlli GRC iscritti al collettore dei test: ricevono l'evidenza quando un test è eseguito. */
function Collectors({ canEdit }: { canEdit: boolean }) {
  const t = useT();
  const qc = useQueryClient();
  const me = useMe().data;
  const grcOn = moduleOn(me, "grc");
  const list = useQuery({ queryKey: ["grc", "controls"], queryFn: grcApi.controls, enabled: grcOn });
  // L'elenco dei controlli non riporta i collettori: si leggono dal dettaglio di ognuno (al massimo 200).
  const controls = (list.data?.items ?? []).slice(0, 200);
  const details = useQueries({
    queries: controls.map((c) => ({ queryKey: ["grc", "control", c.key], queryFn: () => grcApi.control(c.key), staleTime: 60_000 })),
  });
  const loaded = details.every((d) => !d.isPending);
  const enrolled = details.flatMap((d) => (d.data && d.data.collectors.includes(TEST_COLLECTOR) ? [d.data] : []));
  const [pick, setPick] = useState("");
  const toggle = useMutation({
    mutationFn: async ({ key, add }: { key: string; add: boolean }) => {
      const cur = (await grcApi.control(key)).collectors;
      const next = add ? [...new Set([...cur, TEST_COLLECTOR])] : cur.filter((x) => x !== TEST_COLLECTOR);
      return grcApi.updateControl(key, { collectors: next });
    },
    onSuccess: (v) => {
      qc.setQueryData(["grc", "control", v.key], v);
      void qc.invalidateQueries({ queryKey: ["grc", "control", v.key] });
      setPick("");
    },
  });
  if (!grcOn) return null;
  const enrolledKeys = new Set(enrolled.map((c) => c.key));
  return (
    <section className="flex flex-col gap-2" aria-label={t("continuita.config.collectors")}>
      <SectionTitle>{t("continuita.config.collectors")}</SectionTitle>
      <p className="m-0 text-[12.5px] text-muted">{t("continuita.config.collectorsHelp", { collector: TEST_COLLECTOR })}</p>
      <ErrorNote error={list.error} />
      {(list.isPending || !loaded) && <Loading rows={2} />}
      {list.data && loaded && enrolled.length === 0 && <p className="m-0 text-[13px] text-muted">{t("continuita.config.noCollectors")}</p>}
      <ul className="m-0 flex list-none flex-col p-0">
        {enrolled.map((c) => (
          <li key={c.key} className="flex flex-wrap items-baseline gap-x-3 border-b border-hair py-1.5 text-[12.5px]">
            <Link to="/e/$key" params={{ key: c.key }} className="font-mono">
              {c.key}
            </Link>
            <span className="min-w-0 flex-[1_1_200px]">{c.title}</span>
            {canEdit && (
              <button type="button" className="bg-transparent p-0 text-[12px] text-muted underline hover:text-danger" disabled={toggle.isPending} onClick={() => toggle.mutate({ key: c.key, add: false })}>
                {t("continuita.config.unenroll")}
              </button>
            )}
          </li>
        ))}
      </ul>
      {canEdit && list.data && (
        <form
          className="flex flex-wrap gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            if (pick) toggle.mutate({ key: pick, add: true });
          }}
        >
          <OptionSelect
            className="field min-w-0 flex-[1_1_240px]"
            aria-label={t("continuita.config.pickControl")}
            noneLabel={t("continuita.config.pickControl")}
            options={controls.filter((c) => !enrolledKeys.has(c.key)).map((c) => ({ value: c.key, label: c.title, detail: c.key }))}
            value={pick}
            onChange={(v: string) => setPick(v)}
          />
          <button className="btn" disabled={!pick || toggle.isPending}>
            {t("continuita.config.enroll")}
          </button>
        </form>
      )}
      <ErrorNote error={toggle.error} />
    </section>
  );
}
