// Colonne della board di un team (ondata 3, replica/usabilita/configurazione.md §3.14): ogni team
// ha le sue colonne (nome, colore, limite WIP, fasi dei processi, anche diverse per tipo), i modelli
// Scrum, Kanban e Supporto con anteprima, e il percorso «modifica → anteprima → pubblica».
import { useEffect, useMemo, useState } from "react";
import { Link } from "@tanstack/react-router";
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { OptionSelect, Select } from "../select";
import { ErrorNote } from "../ui";
import { useT } from "../i18n/react";
import { useStateName } from "../stateLabels";
import { workApi, type BoardConfig, type BoardModel, type BoardPreview, type BoardSettings, type Swimlane } from "../workApi";
import { Dialog } from "./work-ui";

const MODEL_NAME = {
  scrum: "lavoro.boardTeam.models.scrum",
  kanban: "lavoro.boardTeam.models.kanban",
  supporto: "lavoro.boardTeam.models.supporto",
} as const;
const MODEL_TEXT = {
  scrum: "lavoro.boardTeam.models.scrumText",
  kanban: "lavoro.boardTeam.models.kanbanText",
  supporto: "lavoro.boardTeam.models.supportoText",
} as const;
type ModelKey = keyof typeof MODEL_NAME;
const BUILTIN_MODELS = (Object.keys(MODEL_NAME) as ModelKey[]).map((k) => ({ key: k, name: k }));
const isModel = (k: string): k is ModelKey => k in MODEL_NAME;

export function useModelName() {
  const t = useT();
  return (m: { key: string; name: string }) => (isModel(m.key) ? t(MODEL_NAME[m.key]) : m.name);
}

const clone = (s: BoardSettings): BoardSettings => ({ ...s, columns: s.columns.map((c) => ({ ...c, phases: [...c.phases], type_phases: c.type_phases ? { ...c.type_phases } : undefined })) });

export function BoardColumnsEditor({ spaceKey, teamName }: { spaceKey: string; teamName: string }) {
  const t = useT();
  const modelName = useModelName();
  const qc = useQueryClient();
  const cfg = useQuery({ queryKey: ["work", "board-config", spaceKey], queryFn: () => workApi.boardConfig(spaceKey) });
  const [edit, setEdit] = useState<BoardSettings | null>(null);
  const [picking, setPicking] = useState(false);
  const [done, setDone] = useState(false);
  const after = (c: BoardConfig) => {
    qc.setQueryData(["work", "board-config", spaceKey], c);
    qc.invalidateQueries({ queryKey: ["work", "board", spaceKey] });
  };
  const save = useMutation({ mutationFn: (s: BoardSettings) => workApi.saveDraft(spaceKey, s), onSuccess: (c) => (after(c), setDone(false)) });
  const discard = useMutation({ mutationFn: () => workApi.discardDraft(spaceKey), onSuccess: (c) => (after(c), setEdit(null)) });
  const publish = useMutation({
    mutationFn: async (s: BoardSettings | null) => {
      if (s) await workApi.saveDraft(spaceKey, s);
      return workApi.publishBoard(spaceKey);
    },
    onSuccess: (c) => (after(c), setEdit(null), setDone(true)),
  });
  const data = cfg.data;
  if (cfg.isPending) return null;
  if (!data) return <ErrorNote error={cfg.error} />;
  const p = data.published;
  const modelInfo = data.models.find((m) => m.key === p.model);
  return (
    <section aria-label={t("lavoro.boardTeam.title")} data-testid="board-colonne" className="flex flex-col gap-2 border border-hair p-3">
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
        <h3 className="m-0 text-[14px] font-bold">{t("lavoro.boardTeam.title")}</h3>
        {modelInfo && <span className="label text-muted">{t("lavoro.boardTeam.modelBadge", { name: modelName(modelInfo) })}</span>}
        {p.no_sprint && <span className="label text-muted">{t("lavoro.boardTeam.noSprintBadge")}</span>}
      </div>
      <p className="m-0 text-[12.5px] text-muted">{p.custom ? p.columns.map((c) => c.name).join(" · ") : t("lavoro.boardTeam.default")}</p>
      {done && !data.draft && <p role="status" className="m-0 text-[12.5px] text-ok">{t("lavoro.boardTeam.published")}</p>}
      {data.draft && !edit && (
        <div role="status" className="flex flex-col gap-2 border-l-2 border-accent bg-sunken px-3 py-2">
          <span className="text-[13px]">{t("lavoro.boardTeam.draftBanner")}</span>
          <div className="flex flex-wrap gap-2">
            <button className="btn min-h-[44px]" onClick={() => setEdit(clone(data.draft!))}>{t("lavoro.boardTeam.continue")}</button>
            <button className="btn btn-primary min-h-[44px]" disabled={publish.isPending} onClick={() => publish.mutate(null)}>{t("lavoro.boardTeam.publish")}</button>
            <button className="btn min-h-[44px]" disabled={discard.isPending} onClick={() => discard.mutate()}>{t("lavoro.boardTeam.discard")}</button>
          </div>
        </div>
      )}
      {!edit && data.can_manage && (
        <div className="flex flex-wrap gap-2">
          <button className="btn min-h-[44px]" onClick={() => setEdit(clone(data.draft ?? { ...p, custom: true }))}>{t("lavoro.boardTeam.edit")}</button>
          <button className="btn min-h-[44px]" onClick={() => setPicking(true)}>{t("lavoro.boardTeam.applyModel")}</button>
          <Link to="/delivery/board" search={{ spazio: spaceKey }} className="btn inline-flex min-h-[44px] items-center no-underline">{t("lavoro.boardTeam.openBoard")}</Link>
        </div>
      )}
      <ErrorNote error={discard.error ?? publish.error} />
      {picking && (
        <ModelPicker
          spaceKey={spaceKey}
          models={data.models}
          onClose={() => setPicking(false)}
          onApply={(s) => {
            save.mutate(s, { onSuccess: (c) => (setEdit(clone(c.draft!)), setPicking(false)) });
          }}
          error={save.error}
        />
      )}
      {edit && (
        <Editor
          spaceKey={spaceKey}
          teamName={teamName}
          cfg={data}
          value={edit}
          onChange={setEdit}
          busy={save.isPending || publish.isPending}
          error={save.error ?? publish.error}
          onSave={() => save.mutate(edit)}
          onPublish={() => publish.mutate(edit)}
          onClose={() => setEdit(null)}
        />
      )}
    </section>
  );
}

// ---------------------------------------------------------------------
// Modelli

function ModelPicker({ spaceKey, models, onClose, onApply, error }: { spaceKey: string; models: BoardModel[]; onClose: () => void; onApply: (s: BoardSettings) => void; error: unknown }) {
  const t = useT();
  const modelName = useModelName();
  const [pick, setPick] = useState("");
  const pv = useQuery({ queryKey: ["work", "board-model-preview", spaceKey, pick], queryFn: () => workApi.previewBoard(spaceKey, { model: pick }), enabled: !!pick });
  return (
    <Dialog title={t("lavoro.boardTeam.models.title")} onClose={onClose} wide>
      <p className="m-0 text-[12.5px] text-muted">{t("lavoro.boardTeam.models.help")}</p>
      <div role="radiogroup" aria-label={t("lavoro.boardTeam.models.title")} className="grid gap-2 md:grid-cols-3">
        {models.map((m) => (
          <button
            key={m.key}
            role="radio"
            aria-checked={pick === m.key}
            aria-label={t("lavoro.boardTeam.models.choose", { name: modelName(m) })}
            className={`flex min-h-[44px] flex-col items-start gap-1 border p-3 text-left ${pick === m.key ? "border-accent bg-sunken" : "border-hair"}`}
            onClick={() => setPick(m.key)}
          >
            <strong>{modelName(m)}</strong>
            <span className="text-[12.5px] text-muted">{isModel(m.key) ? t(MODEL_TEXT[m.key]) : m.description}</span>
          </button>
        ))}
      </div>
      {!pick && <p className="m-0 text-muted">{t("lavoro.boardTeam.models.pick")}</p>}
      {pick && pv.data && (
        <div data-testid="anteprima-modello" className="flex flex-col gap-2">
          <span className="label text-muted">{t("lavoro.boardTeam.models.preview")}</span>
          <PreviewColumns pv={pv.data} />
        </div>
      )}
      <ErrorNote error={pv.error ?? error} />
      <div className="flex flex-wrap gap-2">
        <button className="btn btn-primary min-h-[44px]" disabled={!pv.data} onClick={() => pv.data && onApply(pv.data.settings)}>
          {t("lavoro.boardTeam.models.apply")}
        </button>
        <button className="btn min-h-[44px]" onClick={onClose}>{t("lavoro.boardTeam.cancel")}</button>
      </div>
    </Dialog>
  );
}

// ---------------------------------------------------------------------
// Anteprima

function PreviewColumns({ pv }: { pv: BoardPreview }) {
  const t = useT();
  const stateName = useStateName();
  return (
    <div className="flex flex-col gap-2">
      <ul className="m-0 grid list-none gap-2 p-0 sm:grid-cols-2 lg:grid-cols-4">
        {pv.columns.map((c, i) => (
          <li key={i} className={`flex flex-col gap-0.5 border-t-2 px-2 py-1.5 ${c.over ? "border-flag" : "border-rule"}`} style={c.color && !c.over ? { borderColor: c.color } : undefined}>
            <span className="font-semibold">{stateName(c.name)}</span>
            <span className="font-mono text-[12px] tabular">
              {t("lavoro.boardTeam.preview.count", { n: c.count })}
              {c.wip > 0 && ` / ${c.wip}`}
            </span>
            {c.over && <span className="text-[12px] font-semibold text-flag">{t("lavoro.boardTeam.preview.over", { n: c.count, wip: c.wip })}</span>}
            <span className="text-[12px] text-muted">{c.phases.length ? c.phases.map((p) => stateName(p)).join(", ") : t("lavoro.boardTeam.preview.empty")}</span>
          </li>
        ))}
      </ul>
      {pv.unassigned.length > 0 ? (
        <p role="alert" data-testid="fasi-senza-colonna" className="m-0 text-[12.5px] text-flag">
          {t("lavoro.boardTeam.preview.unassigned", { phases: pv.unassigned.map((p) => stateName(p)).join(", ") })}
        </p>
      ) : (
        <p className="m-0 text-[12.5px] text-muted">{t("lavoro.boardTeam.preview.allAssigned")}</p>
      )}
      {pv.empty.length > 0 && <p className="m-0 text-[12.5px] text-flag">{t("lavoro.boardTeam.preview.emptyColumns", { names: pv.empty.join(", ") })}</p>}
      {pv.hidden > 0 && <p className="m-0 text-[12.5px] text-flag">{t("lavoro.boardTeam.preview.hidden", { n: pv.hidden })}</p>}
    </div>
  );
}

// ---------------------------------------------------------------------
// Editor

function Editor({ spaceKey, teamName, cfg, value, onChange, busy, error, onSave, onPublish, onClose }: {
  spaceKey: string;
  teamName: string;
  cfg: BoardConfig;
  value: BoardSettings;
  onChange: (s: BoardSettings) => void;
  busy: boolean;
  error: unknown;
  onSave: () => void;
  onPublish: () => void;
  onClose: () => void;
}) {
  const t = useT();
  const stateName = useStateName();
  const [drag, setDrag] = useState<number | null>(null);
  const [debounced, setDebounced] = useState(value);
  useEffect(() => {
    const id = setTimeout(() => setDebounced(value), 250);
    return () => clearTimeout(id);
  }, [value]);
  const pv = useQuery({
    queryKey: ["work", "board-preview", spaceKey, JSON.stringify(debounced)],
    queryFn: () => workApi.previewBoard(spaceKey, { settings: debounced }),
    placeholderData: keepPreviousData,
  });
  const phaseOpts = useMemo(() => cfg.phases.map((p) => ({ value: p.name, label: stateName(p.name), detail: p.types.join(", ") })), [cfg.phases, stateName]);
  const setCol = (i: number, patch: Partial<BoardSettings["columns"][number]>) => onChange({ ...value, columns: value.columns.map((c, j) => (j === i ? { ...c, ...patch } : c)) });
  const move = (from: number, to: number) => {
    if (to < 0 || to >= value.columns.length || from === to) return;
    const cols = [...value.columns];
    cols.splice(to, 0, cols.splice(from, 1)[0]);
    onChange({ ...value, columns: cols });
  };
  const multiType = value.types.length > 1;
  return (
    <div data-testid="editor-colonne" className="flex flex-col gap-3 border-t border-hair pt-3">
      <h4 className="m-0 text-[14px] font-bold">{t("lavoro.boardTeam.editor.title", { team: teamName })}</h4>
      <ol className="m-0 flex list-none flex-col gap-2 p-0">
        {value.columns.map((c, i) => {
          const label = c.name || t("lavoro.boardTeam.editor.column", { n: i + 1 });
          return (
            <li
              key={i}
              data-testid="colonna-editor"
              className={`flex flex-col gap-2 border p-2 ${drag === i ? "border-accent" : "border-hair"}`}
              onDragOver={(e) => drag !== null && e.preventDefault()}
              onDrop={(e) => {
                e.preventDefault();
                if (drag !== null) move(drag, i);
                setDrag(null);
              }}
            >
              <div className="flex flex-wrap items-end gap-2">
                <button
                  type="button"
                  draggable
                  aria-label={t("lavoro.boardTeam.editor.drag", { name: label })}
                  className="min-h-[44px] min-w-[44px] cursor-grab border border-hair"
                  onDragStart={(e) => {
                    setDrag(i);
                    e.dataTransfer.effectAllowed = "move";
                    e.dataTransfer.setData("text/plain", String(i));
                  }}
                  onDragEnd={() => setDrag(null)}
                >
                  ⠿
                </button>
                <input
                  className="field min-h-[44px] min-w-0 flex-1 basis-[160px]"
                  aria-label={t("lavoro.boardTeam.editor.name", { n: i + 1 })}
                  maxLength={60}
                  value={c.name}
                  onChange={(e) => setCol(i, { name: e.target.value })}
                />
                <label className="flex flex-col gap-1">
                  <span className="label text-muted">{t("lavoro.boardTeam.editor.wipLabel")}</span>
                  <input
                    className="field min-h-[44px] w-20 text-right font-mono"
                    type="number"
                    min={0}
                    max={999}
                    aria-label={t("lavoro.boardTeam.editor.wip", { name: label })}
                    value={c.wip}
                    onChange={(e) => setCol(i, { wip: Math.max(0, Math.min(999, Number(e.target.value) || 0)) })}
                  />
                </label>
                <input
                  type="color"
                  className="min-h-[44px] w-11 border border-hair bg-transparent p-0.5"
                  aria-label={t("lavoro.boardTeam.editor.color", { name: label })}
                  value={c.color || "#8a8f98"}
                  onChange={(e) => setCol(i, { color: e.target.value })}
                />
                <button type="button" className="btn min-h-[44px] min-w-[44px]" disabled={i === 0} aria-label={t("lavoro.boardTeam.editor.up", { name: label })} onClick={() => move(i, i - 1)}>↑</button>
                <button type="button" className="btn min-h-[44px] min-w-[44px]" disabled={i === value.columns.length - 1} aria-label={t("lavoro.boardTeam.editor.down", { name: label })} onClick={() => move(i, i + 1)}>↓</button>
                <button type="button" className="btn min-h-[44px] min-w-[44px] text-danger" aria-label={t("lavoro.boardTeam.editor.remove", { name: label })} onClick={() => onChange({ ...value, columns: value.columns.filter((_, j) => j !== i) })}>×</button>
              </div>
              <div className="flex flex-col gap-1">
                <span className="label text-muted">{t("lavoro.boardTeam.editor.phasesLabel")}</span>
                <OptionSelect
                  multiple
                  value={c.phases}
                  onChange={(v) => setCol(i, { phases: v })}
                  options={phaseOpts}
                  placeholder={t("lavoro.boardTeam.editor.phasesPlaceholder")}
                  aria-label={t("lavoro.boardTeam.editor.phases", { name: label })}
                />
              </div>
              {multiType && (
                <details className="text-[12.5px]">
                  <summary className="min-h-[44px] cursor-pointer py-2 text-muted">{t("lavoro.boardTeam.editor.perType")}</summary>
                  <p className="m-0 pb-2 text-muted">{t("lavoro.boardTeam.editor.perTypeHelp")}</p>
                  <div className="flex flex-col gap-2">
                    {value.types.map((ty) => (
                      <label key={ty} className="flex flex-col gap-1">
                        <span className="label text-muted">{ty}</span>
                        <OptionSelect
                          multiple
                          value={c.type_phases?.[ty] ?? []}
                          onChange={(v) => {
                            const tp = { ...(c.type_phases ?? {}) };
                            if (v.length) tp[ty] = v;
                            else delete tp[ty];
                            setCol(i, { type_phases: Object.keys(tp).length ? tp : undefined });
                          }}
                          options={cfg.phases.filter((p) => p.types.includes(ty)).map((p) => ({ value: p.name, label: stateName(p.name) }))}
                          placeholder={t("lavoro.boardTeam.editor.phasesPlaceholder")}
                          aria-label={t("lavoro.boardTeam.editor.perTypeOf", { type: ty, name: label })}
                        />
                      </label>
                    ))}
                  </div>
                </details>
              )}
            </li>
          );
        })}
      </ol>
      <div>
        <button
          type="button"
          className="btn min-h-[44px]"
          onClick={() => onChange({ ...value, columns: [...value.columns, { name: t("lavoro.boardTeam.editor.newName"), phases: [], wip: 0 }] })}
        >
          {t("lavoro.boardTeam.editor.add")}
        </button>
      </div>
      <div className="flex flex-wrap gap-3">
        <label className="flex flex-col gap-1">
          <span className="label text-muted">{t("lavoro.boardTeam.editor.swimlane")}</span>
          <Select className="field min-h-[44px] w-auto" value={value.swimlane} onChange={(e) => onChange({ ...value, swimlane: e.target.value as Swimlane })}>
            <option value="none">{t("lavoro.boardTeam.editor.swimNone")}</option>
            <option value="assignee">{t("lavoro.boardTeam.editor.swimAssignee")}</option>
            <option value="priority">{t("lavoro.boardTeam.editor.swimPriority")}</option>
          </Select>
        </label>
        <label className="flex flex-col gap-1">
          <span className="label text-muted">{t("lavoro.boardTeam.editor.sort")}</span>
          <Select className="field min-h-[44px] w-auto" value={value.sort ?? ""} onChange={(e) => onChange({ ...value, sort: e.target.value })}>
            <option value="">{t("lavoro.boardTeam.editor.sortManual")}</option>
            <option value="due">{t("lavoro.boardTeam.editor.sortDue")}</option>
          </Select>
        </label>
        <label className="flex min-h-[44px] items-center gap-2 self-end">
          <input type="checkbox" checked={!!value.no_sprint} onChange={(e) => onChange({ ...value, no_sprint: e.target.checked })} />
          <span>{t("lavoro.boardTeam.editor.noSprint")}</span>
        </label>
      </div>
      <div data-testid="anteprima-colonne" className="flex flex-col gap-2">
        <span className="label text-muted">{t("lavoro.boardTeam.preview.title")}</span>
        {pv.data && <PreviewColumns pv={pv.data} />}
      </div>
      <ErrorNote error={error ?? pv.error} />
      <div className="flex flex-wrap gap-2">
        <button className="btn btn-primary min-h-[44px]" disabled={busy || value.columns.length === 0} onClick={onPublish}>{t("lavoro.boardTeam.publish")}</button>
        <button className="btn min-h-[44px]" disabled={busy} onClick={onSave}>{t("lavoro.boardTeam.saveDraft")}</button>
        <button className="btn min-h-[44px]" onClick={onClose}>{t("lavoro.boardTeam.cancel")}</button>
      </div>
    </div>
  );
}

/** Scelta del modello alla creazione dello spazio di un team (nessun modello = una colonna per fase). */
export function SpaceModelCreate({ teamId, busy, onCreate }: { teamId: string; busy: boolean; onCreate: (model: string) => void }) {
  const t = useT();
  const modelName = useModelName();
  const [model, setModel] = useState("");
  const models = BUILTIN_MODELS;
  return (
    <form
      className="flex flex-wrap items-end gap-2"
      data-testid={`crea-spazio-${teamId}`}
      onSubmit={(e) => {
        e.preventDefault();
        onCreate(model);
      }}
    >
      <p className="m-0 w-full text-[12.5px] text-muted">{t("lavoro.boardTeam.noSpace")}</p>
      <label className="flex min-w-[200px] flex-col gap-1">
        <span className="label text-muted">{t("lavoro.boardTeam.models.onCreate")}</span>
        <Select className="field min-h-[44px]" value={model} onChange={(e) => setModel(e.target.value)}>
          <option value="">{t("lavoro.boardTeam.models.noModel")}</option>
          {models.map((m) => (
            <option key={m.key} value={m.key}>
              {modelName(m)}
            </option>
          ))}
        </Select>
      </label>
      <button className="btn btn-primary min-h-[44px]" disabled={busy}>{t("lavoro.boardTeam.models.createSpace")}</button>
    </form>
  );
}
