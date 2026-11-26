// Configurazione → Progetti (replica/progetti.md, §8): modello predefinito, richiesta che diventa
// progetto, metodo di avanzamento, programmi e ruoli del progetto (obbligatori, ereditati dalla
// richiesta, nell'ordine in cui compaiono). Modifica chi amministra l'organizzazione.
import { useEffect, useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Select } from "../../select";
import { PageHeader } from "../../shell";
import { ErrorNote, Loading, SectionTitle } from "../../ui";
import { useT } from "../../i18n/react";
import { projectsApi, type ProjectRole, type ProjectSettings, type Template } from "../../projectsApi";

const TEMPLATES: Template[] = ["agile", "waterfall", "mixed", "contract"];
const METHODS: ProjectSettings["progress_method"][] = ["weighted_estimate", "equal"];

export function ProjectSettingsPage() {
  const t = useT();
  return (
    <>
      <PageHeader path={t("progetti.voci.settings.path")} title={t("progetti.voci.settings.title")} />
      <div className="flex max-w-4xl flex-col gap-8 px-4 pb-12 md:px-7">
        <SettingsForm />
      </div>
    </>
  );
}

function SettingsForm() {
  const t = useT();
  const qc = useQueryClient();
  const st = useQuery({ queryKey: ["project-settings"], queryFn: projectsApi.settings });
  const [s, setS] = useState<ProjectSettings | null>(null);
  const [pick, setPick] = useState("");
  useEffect(() => setS(st.data ?? null), [st.data]);
  const save = useMutation({
    mutationFn: () => projectsApi.saveSettings({
      default_template: s!.default_template, auto_from_request: s!.auto_from_request, programs_enabled: s!.programs_enabled,
      progress_method: s!.progress_method, roles: s!.roles,
    }),
    onSuccess: (x) => {
      qc.setQueryData(["project-settings"], x);
      qc.invalidateQueries({ queryKey: ["projects"] });
    },
  });
  if (st.error) return <ErrorNote error={st.error} />;
  if (!s) return <Loading className="m-0 text-muted" />;
  const ro = !s.can_edit;
  const setRole = (i: number, p: Partial<ProjectRole>) => setS({ ...s, roles: s.roles.map((r, j) => (j === i ? { ...r, ...p } : r)) });
  const move = (i: number, d: -1 | 1) => {
    const roles = [...s.roles];
    [roles[i], roles[i + d]] = [roles[i + d], roles[i]];
    setS({ ...s, roles });
  };
  const free = s.available_roles.filter((a) => !s.roles.some((r) => r.key === a.key));
  return (
    <section className="flex flex-col gap-6" aria-label={t("progetti.voci.settings.title")}>
      <div className="flex flex-wrap gap-6">
        <label className="flex flex-col gap-1">
          <span className="label">{t("progetti.voci.settings.templateLabel")}</span>
          <Select className="field" disabled={ro} value={s.default_template} onChange={(e) => setS({ ...s, default_template: e.target.value as Template })}>
            {TEMPLATES.map((m) => <option key={m} value={m}>{t(`progetti.voci.settings.template.${m}`)}</option>)}
          </Select>
        </label>
        <label className="flex flex-col gap-1">
          <span className="label">{t("progetti.voci.settings.methodLabel")}</span>
          <Select className="field" disabled={ro} value={s.progress_method} onChange={(e) => setS({ ...s, progress_method: e.target.value as ProjectSettings["progress_method"] })}>
            {METHODS.map((m) => <option key={m} value={m}>{t(`progetti.voci.settings.method.${m}`)}</option>)}
          </Select>
        </label>
      </div>
      <Switch label={t("progetti.voci.settings.autoFromRequest")} help={t("progetti.voci.settings.autoFromRequestHelp")} disabled={ro} on={s.auto_from_request} onChange={(v) => setS({ ...s, auto_from_request: v })} />
      <Switch label={t("progetti.voci.settings.programs")} help={t("progetti.voci.settings.programsHelp")} disabled={ro} on={s.programs_enabled} onChange={(v) => setS({ ...s, programs_enabled: v })} />

      <div className="flex flex-col gap-2">
        <SectionTitle>{t("progetti.voci.settings.roles")}</SectionTitle>
        <p className="m-0 text-[12px] text-muted">{t("progetti.voci.settings.rolesHelp")}</p>
        {s.roles.length === 0 && <p className="m-0 text-muted">{t("progetti.voci.settings.noRoles")}</p>}
        <ul className="m-0 flex list-none flex-col p-0">
          {s.roles.map((r, i) => (
            <li key={r.key} className="flex flex-wrap items-center gap-x-5 gap-y-1 border-b border-hair py-2">
              <span className="min-w-40 flex-1 font-semibold">{r.name}</span>
              <label className="flex items-center gap-1.5">
                <input type="checkbox" className="size-5 accent-[var(--accent)]" disabled={ro} checked={r.required} onChange={(e) => setRole(i, { required: e.target.checked })} />
                {t("progetti.voci.settings.required")}
              </label>
              <label className="flex items-center gap-1.5">
                <input type="checkbox" className="size-5 accent-[var(--accent)]" disabled={ro} checked={r.inherit_from_request} onChange={(e) => setRole(i, { inherit_from_request: e.target.checked })} />
                {t("progetti.voci.settings.inherit")}
              </label>
              {!ro && (
                <span className="flex gap-1">
                  <button type="button" className="btn btn-quiet touch-44" disabled={i === 0} aria-label={t("progetti.voci.settings.up", { name: r.name })} onClick={() => move(i, -1)}>↑</button>
                  <button type="button" className="btn btn-quiet touch-44" disabled={i === s.roles.length - 1} aria-label={t("progetti.voci.settings.down", { name: r.name })} onClick={() => move(i, 1)}>↓</button>
                  <button type="button" className="btn btn-quiet touch-44" aria-label={t("progetti.voci.settings.remove", { name: r.name })} onClick={() => setS({ ...s, roles: s.roles.filter((_, j) => j !== i) })}>×</button>
                </span>
              )}
            </li>
          ))}
        </ul>
        {!ro && free.length > 0 && (
          <div className="flex flex-wrap items-end gap-2">
            <label className="flex flex-col gap-1">
              <span className="label">{t("progetti.voci.settings.addRolePick")}</span>
              <Select className="field" value={pick} onChange={(e) => setPick(e.target.value)}>
                <option value="">{t("progetti.voci.deps.pickNone")}</option>
                {free.map((a) => <option key={a.key} value={a.key}>{a.name}</option>)}
              </Select>
            </label>
            <button type="button" className="btn touch-44" disabled={!pick} onClick={() => {
              const a = free.find((x) => x.key === pick);
              if (a) setS({ ...s, roles: [...s.roles, { key: a.key, name: a.name, required: false, inherit_from_request: false }] });
              setPick("");
            }}>{t("progetti.voci.settings.addRole")}</button>
          </div>
        )}
      </div>

      {!ro && (
        <div className="flex items-center gap-2">
          <button className="btn btn-primary touch-44" disabled={save.isPending} onClick={() => save.mutate()}>{t("progetti.voci.settings.save")}</button>
          {save.isSuccess && <span className="text-[12px] text-muted" role="status">{t("progetti.voci.settings.saved")}</span>}
        </div>
      )}
      {ro && <p className="m-0 text-[12px] text-muted">{t("progetti.voci.settings.readOnly")}</p>}
      <ErrorNote error={save.error} />
    </section>
  );
}

function Switch({ label, help, on, disabled, onChange }: { label: string; help: string; on: boolean; disabled: boolean; onChange: (v: boolean) => void }): ReactNode {
  return (
    <label className="flex items-start gap-3">
      <input type="checkbox" role="switch" className="mt-0.5 size-5 accent-[var(--accent)]" disabled={disabled} checked={on} onChange={(e) => onChange(e.target.checked)} />
      <span className="flex flex-col">
        <span className="font-semibold">{label}</span>
        <span className="text-[12px] text-muted">{help}</span>
      </span>
    </label>
  );
}
