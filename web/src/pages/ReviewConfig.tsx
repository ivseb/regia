import { EntitySelect, Select } from "../select";
import { useMemo, useState } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { reviewsApi } from "../reviewsApi";
import { api, type ReviewConfig as Config, type ReviewOutcome, type ReviewTeam, type ReviewType } from "../api";
import { keyFrom } from "./processEditor/model";
import { ruleSentence, type RuleRefs } from "./processEditor/RuleSentence";
import { PageHeader } from "../shell";
import { ErrorNote, ROLE_NAMES } from "../ui";
import { QuestionnaireEditor } from "./questionnaireEditor/Editor";
import { useT } from "../i18n/react";
import { Trans } from "../i18n/Trans";
import { tr } from "../i18n/core";

// Configurazione → Valutazioni (piano, passo 4): team valutatori, tipi di valutazione e
// modelli di questionario. Il modello si modifica con l'editor visuale (passo 7), con
// anteprima dal vivo, bozza e pubblicazione; il JSON resta come vista avanzata.

export type ReviewConfigTab = "team" | "tipi" | "modelli";

export function ReviewConfigPage({ tab, template }: { tab: ReviewConfigTab; template?: string }) {
  const t = useT();
  const cfg = useQuery({ queryKey: ["review-config"], queryFn: reviewsApi.config });
  const tabs: [ReviewConfigTab, string][] = [
    ["team", t("configurazione.review.tabTeams")],
    ["tipi", t("configurazione.review.tabTypes")],
    ["modelli", t("configurazione.review.tabTemplates")],
  ];
  return (
    <>
      <PageHeader
        path={t("configurazione.path.assessments")}
        title={t("configurazione.review.valutazioni")}
        actions={
          <>
            <Link to="/configurazione/coinvolgi" className="btn no-underline">
              {t("configurazione.coinvolgi.button")}
            </Link>
            <a className="btn no-underline" href={reviewsApi.exportUrl()} download="valutazioni.json">
              {t("configurazione.common.esportaJson")}
            </a>
          </>
        }
      />
      <div role="tablist" aria-label={t("configurazione.common.sezioni")} className="flex gap-6 overflow-x-auto border-b border-hair px-4 md:px-7">
        {tabs.map(([k, l]) => (
          <Link
            key={k}
            to="/configurazione/valutazioni"
            search={{ tab: k }}
            role="tab"
            aria-selected={tab === k}
            className={`shrink-0 pb-2.5 no-underline ${tab === k ? "border-b-2 border-fg font-semibold text-fg" : "text-muted hover:text-fg"}`}
          >
            {l}
          </Link>
        ))}
      </div>
      <div className="px-4 py-5 md:px-7">
        <ErrorNote error={cfg.error} />
        {cfg.data && tab === "team" && <Teams cfg={cfg.data} />}
        {cfg.data && tab === "tipi" && <Types cfg={cfg.data} />}
        {cfg.data && tab === "modelli" && <Templates cfg={cfg.data} selected={template} />}
      </div>
    </>
  );
}

function useRefresh() {
  const qc = useQueryClient();
  return () => qc.invalidateQueries({ queryKey: ["review-config"] });
}

// ---------------------------------------------------------------------
// Team valutatori
// ---------------------------------------------------------------------

function Teams({ cfg }: { cfg: Config }) {
  const t = useT();
  const [editing, setEditing] = useState<string | null>(null);
  return (
    <div className="flex flex-col gap-4">
      <p className="m-0 max-w-3xl text-[13px] text-muted">
        {t("configurazione.review.ogniTeamEUn")}
      </p>
      <div className="overflow-x-auto">
        <table className="w-full min-w-[720px] border-collapse tabular">
          <thead>
            <tr className="label h-9 text-left">
              <th className="border-b-2 border-rule pr-3">{t("configurazione.common.team")}</th>
              <th className="border-b-2 border-rule px-3">{t("configurazione.common.gruppo")}</th>
              <th className="border-b-2 border-rule px-3">{t("configurazione.review.responsabile")}</th>
              <th className="border-b-2 border-rule px-3 text-right">SLA</th>
              <th className="border-b-2 border-rule px-3 text-right">{t("configurazione.review.sollecito")}</th>
              <th className="border-b-2 border-rule px-3 text-right">{t("configurazione.common.escalation")}</th>
              <th className="border-b-2 border-rule pl-3" />
            </tr>
          </thead>
          <tbody>
            {cfg.teams.map((t) =>
              editing === t.id ? (
                <tr key={t.id}>
                  <td colSpan={7} className="border-b border-hair py-3">
                    <TeamForm cfg={cfg} team={t} onDone={() => setEditing(null)} />
                  </td>
                </tr>
              ) : (
                <tr key={t.id} className={`border-b border-hair ${t.archived ? "text-muted" : ""}`}>
                  <td className="py-2 pr-3">
                    <span className="font-semibold">{t.name}</span>
                    {t.archived && <span className="text-muted"> {tr("configurazione.review.archiviato")}</span>}
                    {t.description && <span className="block text-[12.5px] text-muted">{t.description}</span>}
                  </td>
                  <td className="px-3">
                    {t.group} <span className="text-[12px] text-muted">({t.members})</span>
                  </td>
                  <td className="px-3">{t.lead ?? <span className="text-muted">—</span>}</td>
                  <td className="px-3 text-right">{tr("configurazione.review.slaDays", { count: t.sla_days })}</td>
                  <td className="px-3 text-right">{tr("configurazione.review.reminderDays", { count: t.reminder_days })}</td>
                  <td className="px-3 text-right">{t.escalation_days ? tr("configurazione.review.escalationAfter", { count: t.escalation_days }) : "—"}</td>
                  <td className="pl-3 text-right">
                    {cfg.can_edit && (
                      <button className="text-[12.5px] text-accent underline" onClick={() => setEditing(t.id)}>
                        {tr("configurazione.common.modifica")}
                      </button>
                    )}
                  </td>
                </tr>
              ),
            )}
          </tbody>
        </table>
      </div>
      {cfg.can_edit && editing !== "new" && (
        <button className="btn self-start" onClick={() => setEditing("new")}>
          {t("configurazione.review.nuovoTeamValutatore")}
        </button>
      )}
      {editing === "new" && <TeamForm cfg={cfg} onDone={() => setEditing(null)} />}
    </div>
  );
}

function TeamForm({ cfg, team, onDone }: { cfg: Config; team?: ReviewTeam; onDone: () => void }) {
  const t = useT();
  const refresh = useRefresh();
  const [f, setF] = useState({
    key: team?.key ?? "",
    name: team?.name ?? "",
    description: team?.description ?? "",
    group_id: team?.group_id ?? "",
    lead_id: team?.lead_id ?? "",
    sla_days: team?.sla_days ?? 5,
    reminder_days: team?.reminder_days ?? 1,
    escalation_days: team?.escalation_days ?? 0,
    archived: team?.archived ?? false,
  });
  const save = useMutation({
    mutationFn: () => (team ? reviewsApi.updateTeam(team.id, { ...f, key: undefined }) : reviewsApi.createTeam(f)),
    onSuccess: () => {
      refresh();
      onDone();
    },
  });
  const num = (k: "sla_days" | "reminder_days" | "escalation_days") => (
    <input type="number" min={0} className="field tabular" value={f[k]} onChange={(e) => setF({ ...f, [k]: Number(e.target.value) })} />
  );
  return (
    <form
      className="grid max-w-3xl grid-cols-1 gap-3 sm:grid-cols-2"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      {!team && (
        <label className="flex flex-col gap-1">
          <span className="label">{t("configurazione.common.chiave")}</span>
          <input className="field font-mono" value={f.key} onChange={(e) => setF({ ...f, key: e.target.value })} placeholder={t("configurazione.review.esLegale")} required />
        </label>
      )}
      <label className="flex flex-col gap-1">
        <span className="label">{t("configurazione.common.nome")}</span>
        <input className="field" value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} required />
      </label>
      <label className="flex flex-col gap-1 sm:col-span-2">
        <span className="label">{t("configurazione.common.descrizione")}</span>
        <input className="field" value={f.description} onChange={(e) => setF({ ...f, description: e.target.value })} />
      </label>
      <label className="flex flex-col gap-1">
        <span className="label">{t("configurazione.review.gruppoDellePersone")}</span>
        <Select className="field" value={f.group_id} onChange={(e) => setF({ ...f, group_id: e.target.value })} required>
          <option value="">{t("configurazione.review.scegli")}</option>
          {cfg.groups.map((g) => (
            <option key={g.id} value={g.id}>
              {g.name} ({g.members})
            </option>
          ))}
        </Select>
      </label>
      <label className="flex flex-col gap-1">
        <span className="label">{t("configurazione.review.responsabileEscalation")}</span>
        <EntitySelect kind="member" noneLabel={t("configurazione.review.nessuno")} className="field" value={f.lead_id} onChange={(v) => setF({ ...f, lead_id: v })} />
      </label>
      <label className="flex flex-col gap-1">
        <span className="label">{t("configurazione.review.slaGiorniLavorativi")}</span>
        {num("sla_days")}
      </label>
      <label className="flex flex-col gap-1">
        <span className="label">{t("configurazione.review.sollecitoGiorniPrima")}</span>
        {num("reminder_days")}
      </label>
      <label className="flex flex-col gap-1">
        <span className="label">{t("configurazione.review.escalationDopoGiorniDi")}</span>
        {num("escalation_days")}
      </label>
      {team && (
        <label className="flex items-center gap-2 self-end pb-2">
          <input type="checkbox" checked={f.archived} onChange={(e) => setF({ ...f, archived: e.target.checked })} /> {t("configurazione.review.archiviatoCap")}
        </label>
      )}
      <div className="flex gap-2 sm:col-span-2">
        <button className="btn btn-primary" disabled={save.isPending}>
          {t("configurazione.common.salva")}
        </button>
        <button type="button" className="btn" onClick={onDone}>
          {t("configurazione.common.annulla")}
        </button>
      </div>
      <div className="sm:col-span-2">
        <ErrorNote error={save.error} />
      </div>
    </form>
  );
}

// ---------------------------------------------------------------------
// Tipi di valutazione
// ---------------------------------------------------------------------

function Types({ cfg }: { cfg: Config }) {
  const t = useT();
  const [editing, setEditing] = useState<string | null>(null);
  return (
    <div className="flex flex-col gap-4">
      <p className="m-0 max-w-3xl text-[13px] text-muted">
        <Trans k="configurazione.regole.tipiIntro" slots={{ link: <Link to="/processi">{t("configurazione.review.processi")}</Link> }} />
      </p>
      <ul className="m-0 list-none p-0">
        {cfg.types.map((t) => (
          <li key={t.id} className="border-b border-hair py-3">
            {editing === t.id ? (
              <TypeForm cfg={cfg} type={t} onDone={() => setEditing(null)} />
            ) : (
              <div className="flex flex-wrap items-start justify-between gap-3">
                <div className="flex min-w-0 flex-col gap-1">
                  <span>
                    <span className="text-[15px] font-semibold">{t.name}</span> <span className="font-mono text-[12px] text-muted">{t.key}</span>
                    {t.archived && <span className="text-muted"> {tr("configurazione.review.archiviato")}</span>}
                  </span>
                  {t.description && <span className="text-[13px] text-muted">{t.description}</span>}
                  <span className="flex flex-wrap gap-x-4 gap-y-1 text-[12.5px]">
                    <span>{tr("configurazione.review.teamNamed", { name: t.team })}</span>
                    <span>{t.scope === "impact" ? tr("configurazione.review.perApplicazioneImpatto") : tr("configurazione.review.perRichiesta")}</span>
                    <span>
                      {tr("configurazione.review.templateLabel")}{" "}
                      <Link to="/configurazione/valutazioni" search={{ tab: "modelli", modello: t.template_key }}>
                        {t.template ?? t.template_key}
                      </Link>
                      {t.template_version ? ` v${t.template_version}` : tr("configurazione.review.nonPubblicato")}
                    </span>
                    <span>{t.mandatory ? tr("configurazione.common.obbligatoria") : tr("configurazione.review.facoltativa")}</span>
                    <span>{tr("configurazione.review.decides", { who: t.deciders === "lead" ? tr("configurazione.review.ilResponsabile") : tr("configurazione.review.ilTeam") })}</span>
                  </span>
                  <WhenOpens type={t} cfg={cfg} />
                  <span className="text-[12.5px] text-muted">
                    {tr("configurazione.review.outcomesFill", { outcomes: t.outcomes.map((o) => o.label).join(", "), roles: t.owner_roles.map((r) => ROLE_NAMES[r] ?? r).join(" → ") })}
                  </span>
                </div>
                {cfg.can_edit && (
                  <button className="text-[12.5px] text-accent underline" onClick={() => setEditing(t.id)}>
                    {tr("configurazione.common.modifica")}
                  </button>
                )}
              </div>
            )}
          </li>
        ))}
      </ul>
      {cfg.can_edit && editing !== "new" && (
        <button className="btn self-start" onClick={() => setEditing("new")}>
          {t("configurazione.review.nuovoTipoDiValutazione")}
        </button>
      )}
      {editing === "new" && <TypeForm cfg={cfg} onDone={() => setEditing(null)} />}
    </div>
  );
}

const POLARITY: Record<string, string> = {
  get positive() { return tr("configurazione.review.polarity.positive"); },
  get conditional() { return tr("configurazione.review.polarity.conditional"); },
  get negative() { return tr("configurazione.review.polarity.negative"); },
};

/** «Quando si apre»: le regole dei processi che aprono questo tipo di valutazione, come frasi. */
function WhenOpens({ type, cfg }: { type: ReviewType; cfg: Config }) {
  const t = useT();
  const nav = useNavigate();
  const [adding, setAdding] = useState(false);
  const [process, setProcess] = useState("");
  const mine = (cfg.triggers ?? []).filter((x) => x.review_type === type.key);
  const refs: RuleRefs = { reviewTypes: cfg.types.map((x) => ({ key: x.key, name: x.name, scope: x.scope })), catalogs: [], loaded: true };
  const procs = useQuery({ queryKey: ["processes"], queryFn: api.processes, enabled: adding });
  const choices = (procs.data?.items ?? []).filter((p) => !p.archived && (p.type === "business_request" || p.type === "impact"));
  const chosen = process || choices[0]?.key || "";
  return (
    <div className="flex flex-col gap-1 text-[12.5px]" data-testid="quando-si-apre">
      <span className="label">{t("casa.review.whenOpens")}</span>
      {mine.length === 0 ? (
        <span className="text-muted">{t("configurazione.regole.nessunaRegolaApre")}</span>
      ) : (
        <ul className="m-0 flex list-none flex-col gap-1.5 p-0">
          {mine.map((x) => (
            <li key={`${x.process_key}-${x.rule_key}`} className="flex flex-col gap-0.5" data-testid="quando-si-apre-regola">
              <span>{ruleSentence(t, { on: x.on, when: x.when, actions: [{ kind: "open_review", ref: type.key, when: x.action_when }] }, refs)}</span>
              <Link to="/processi/$key" params={{ key: x.process_key }} search={{ regola: x.rule_key }} className="inline-flex min-h-11 items-center self-start md:min-h-0">
                {x.process_name}: {t("configurazione.regole.apriLaRegola")}
              </Link>
            </li>
          ))}
        </ul>
      )}
      {cfg.can_edit && !adding && (
        <button className="btn min-h-11 self-start md:min-h-0" onClick={() => setAdding(true)}>
          {t("configurazione.regole.aggiungiRegola")}
        </button>
      )}
      {adding && (
        <div className="flex flex-col gap-2 border-l-2 border-hair pl-3" role="group" aria-label={t("configurazione.regole.aggiungiRegola")}>
          {procs.isPending ? null : choices.length === 0 ? (
            <span className="text-muted">{t("configurazione.regole.nessunProcesso")}</span>
          ) : (
            <>
              <span className="label">{t("configurazione.regole.inQualeProcesso")}</span>
              <Select aria-label={t("configurazione.regole.inQualeProcesso")} className="field" value={chosen} onChange={(e) => setProcess(e.target.value)}>
                {choices.map((p) => (
                  <option key={p.key} value={p.key}>
                    {p.name}
                  </option>
                ))}
              </Select>
              <div className="flex flex-wrap gap-2">
                <button className="btn btn-primary min-h-11 md:min-h-0" onClick={() => nav({ to: "/processi/$key", params: { key: chosen }, search: { nuova: `open_review:${type.key}` } })}>
                  {t("configurazione.regole.imposta")}
                </button>
                <button className="btn min-h-11 md:min-h-0" onClick={() => setAdding(false)}>
                  {t("configurazione.regole.annulla")}
                </button>
              </div>
            </>
          )}
        </div>
      )}
    </div>
  );
}

/** Esiti completi: quelli senza nome si scartano, quelli nuovi prendono la chiave dal nome. */
function cleanOutcomes(rows: ReviewOutcome[]): ReviewOutcome[] {
  const taken = rows.map((r) => r.key).filter(Boolean);
  return rows
    .filter((r) => r.label.trim())
    .map((r) => {
      if (r.key) return { ...r, label: r.label.trim() };
      const key = keyFrom(r.label, taken);
      taken.push(key);
      return { ...r, key, label: r.label.trim() };
    });
}

/** Esiti ammessi come righe: nome, effetto (favorevole, con riserva, contrario) e togli. */
function OutcomeRows({ rows, onChange }: { rows: ReviewOutcome[]; onChange: (r: ReviewOutcome[]) => void }) {
  const t = useT();
  const set = (i: number, p: Partial<ReviewOutcome>) => onChange(rows.map((r, j) => (j === i ? { ...r, ...p } : r)));
  return (
    <fieldset className="m-0 flex min-w-0 flex-col gap-1.5 border-0 p-0 sm:col-span-2">
      <legend className="label mb-1 p-0">{t("casa.review.outcomes")}</legend>
      <ul className="m-0 flex list-none flex-col gap-1.5 p-0" data-testid="esiti">
        {rows.map((r, i) => (
          <li key={i} className="flex flex-wrap items-center gap-2">
            <input className="field min-w-0 flex-[1_1_200px]" aria-label={t("casa.review.outcomeName", { n: i + 1 })} value={r.label} placeholder={t("casa.review.outcomeNamePh")} onChange={(e) => set(i, { label: e.target.value })} />
            <Select className="field w-auto" aria-label={t("casa.review.outcomeEffect", { n: i + 1 })} value={r.polarity} onChange={(e) => set(i, { polarity: e.target.value as ReviewOutcome["polarity"] })}>
              {(["positive", "conditional", "negative"] as const).map((p) => (
                <option key={p} value={p}>
                  {POLARITY[p]}
                </option>
              ))}
            </Select>
            <button type="button" className="text-[12px] text-muted underline hover:text-danger" disabled={rows.length <= 1} onClick={() => onChange(rows.filter((_, j) => j !== i))}>
              {t("configurazione.common.togli")}
            </button>
          </li>
        ))}
      </ul>
      <button type="button" className="self-start text-[12.5px] font-semibold text-accent hover:underline" onClick={() => onChange([...rows, { key: "", label: "", polarity: "positive" }])}>
        {t("casa.review.addOutcome")}
      </button>
    </fieldset>
  );
}

/** Ruoli che compilano, in ordine di preferenza: un menu per riga, con su e giù. */
function RoleRows({ rows, onChange }: { rows: string[]; onChange: (r: string[]) => void }) {
  const t = useT();
  const known = Object.entries(ROLE_NAMES);
  const move = (i: number, d: -1 | 1) => {
    const j = i + d;
    if (j < 0 || j >= rows.length) return;
    const c = [...rows];
    [c[i], c[j]] = [c[j], c[i]];
    onChange(c);
  };
  return (
    <fieldset className="m-0 flex min-w-0 flex-col gap-1.5 border-0 p-0 sm:col-span-2">
      <legend className="label mb-1 p-0">{t("configurazione.review.ruoliCheCompilanoIn")}</legend>
      {rows.length === 0 && <p className="m-0 text-[12.5px] text-muted">{t("casa.review.rolesDefault")}</p>}
      <ul className="m-0 flex list-none flex-col gap-1.5 p-0" data-testid="ruoli-compilano">
        {rows.map((r, i) => (
          <li key={i} className="flex flex-wrap items-center gap-2">
            <span className="w-5 text-right font-mono text-[12px] text-muted">{i + 1}</span>
            <Select className="field w-auto min-w-48" aria-label={t("casa.review.roleN", { n: i + 1 })} value={r} onChange={(e) => onChange(rows.map((x, j) => (j === i ? e.target.value : x)))}>
              {!known.some(([k]) => k === r) && <option value={r}>{r}</option>}
              {known.map(([k, name]) => (
                <option key={k} value={k}>
                  {name}
                </option>
              ))}
            </Select>
            <button type="button" className="btn px-2 py-0.5" disabled={i === 0} aria-label={t("casa.review.roleUp", { n: i + 1 })} onClick={() => move(i, -1)}>
              ↑
            </button>
            <button type="button" className="btn px-2 py-0.5" disabled={i === rows.length - 1} aria-label={t("casa.review.roleDown", { n: i + 1 })} onClick={() => move(i, 1)}>
              ↓
            </button>
            <button type="button" className="text-[12px] text-muted underline hover:text-danger" onClick={() => onChange(rows.filter((_, j) => j !== i))}>
              {t("configurazione.common.togli")}
            </button>
          </li>
        ))}
      </ul>
      <button type="button" className="self-start text-[12.5px] font-semibold text-accent hover:underline" onClick={() => onChange([...rows, known.find(([k]) => !rows.includes(k))?.[0] ?? ""])}>
        {t("casa.review.addRole")}
      </button>
    </fieldset>
  );
}

function TypeForm({ cfg, type, onDone }: { cfg: Config; type?: ReviewType; onDone: () => void }) {
  const t = useT();
  const refresh = useRefresh();
  const [f, setF] = useState({
    key: type?.key ?? "",
    name: type?.name ?? "",
    description: type?.description ?? "",
    team_id: type?.team_id ?? "",
    scope: type?.scope ?? "request",
    template_key: type?.template_key ?? "",
    mandatory: type?.mandatory ?? true,
    deciders: type?.deciders ?? "team",
    owner_roles: type?.owner_roles ?? [],
    outcomes: (type?.outcomes ?? cfg.outcomes_default).map((o) => ({ ...o })),
    archived: type?.archived ?? false,
  });
  const save = useMutation({
    mutationFn: () => {
      const body = {
        ...f,
        key: type ? undefined : f.key,
        outcomes: cleanOutcomes(f.outcomes),
        owner_roles: f.owner_roles.filter(Boolean).length ? f.owner_roles.filter(Boolean) : undefined,
      };
      return type ? reviewsApi.updateType(type.id, body) : reviewsApi.createType(body);
    },
    onSuccess: () => {
      refresh();
      onDone();
    },
  });
  return (
    <form
      className="grid max-w-3xl grid-cols-1 gap-3 sm:grid-cols-2"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      {!type && (
        <label className="flex flex-col gap-1">
          <span className="label">{t("configurazione.common.chiave")}</span>
          <input className="field font-mono" value={f.key} onChange={(e) => setF({ ...f, key: e.target.value })} placeholder={t("configurazione.review.esParereLegale")} required />
        </label>
      )}
      <label className="flex flex-col gap-1">
        <span className="label">{t("configurazione.common.nome")}</span>
        <input className="field" value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} required />
      </label>
      <label className="flex flex-col gap-1 sm:col-span-2">
        <span className="label">{t("configurazione.common.descrizione")}</span>
        <input className="field" value={f.description} onChange={(e) => setF({ ...f, description: e.target.value })} />
      </label>
      <label className="flex flex-col gap-1">
        <span className="label">{t("configurazione.common.team")}</span>
        <Select className="field" value={f.team_id} onChange={(e) => setF({ ...f, team_id: e.target.value })} required>
          <option value="">{t("configurazione.review.scegli")}</option>
          {cfg.teams.filter((t) => !t.archived || t.id === f.team_id).map((t) => (
            <option key={t.id} value={t.id}>
              {t.name}
            </option>
          ))}
        </Select>
      </label>
      <label className="flex flex-col gap-1">
        <span className="label">{t("configurazione.review.ambito")}</span>
        <Select className="field" value={f.scope} onChange={(e) => setF({ ...f, scope: e.target.value as "request" | "impact" })}>
          <option value="request">{t("configurazione.review.interaRichiesta")}</option>
          <option value="impact">{t("configurazione.review.ogniApplicazioneImpatto")}</option>
        </Select>
      </label>
      <label className="flex flex-col gap-1">
        <span className="label">{t("configurazione.review.modelloDiQuestionario")}</span>
        <Select className="field" value={f.template_key} onChange={(e) => setF({ ...f, template_key: e.target.value })} required>
          <option value="">{t("configurazione.review.scegli")}</option>
          {cfg.templates.map((t) => (
            <option key={t.key} value={t.key}>
              {t.name} ({t.key})
            </option>
          ))}
        </Select>
      </label>
      <label className="flex flex-col gap-1">
        <span className="label">{t("configurazione.review.chiDecideLEsito")}</span>
        <Select className="field" value={f.deciders} onChange={(e) => setF({ ...f, deciders: e.target.value as "team" | "lead" })}>
          <option value="team">{t("configurazione.review.iMembriDelTeam")}</option>
          <option value="lead">{t("configurazione.review.soloIlResponsabileDel")}</option>
        </Select>
      </label>
      <label className="flex items-center gap-2">
        <input type="checkbox" checked={f.mandatory} onChange={(e) => setF({ ...f, mandatory: e.target.checked })} /> {t("configurazione.review.mandatoryBlocks")}
      </label>
      {type && (
        <label className="flex items-center gap-2">
          <input type="checkbox" checked={f.archived} onChange={(e) => setF({ ...f, archived: e.target.checked })} /> {t("configurazione.review.archiviatoCap")}
        </label>
      )}
      <RoleRows rows={f.owner_roles} onChange={(owner_roles) => setF({ ...f, owner_roles })} />
      <OutcomeRows rows={f.outcomes} onChange={(outcomes) => setF({ ...f, outcomes })} />
      <div className="flex gap-2 sm:col-span-2">
        <button className="btn btn-primary" disabled={save.isPending}>
          {t("configurazione.common.salva")}
        </button>
        <button type="button" className="btn" onClick={onDone}>
          {t("configurazione.common.annulla")}
        </button>
      </div>
      <div className="sm:col-span-2">
        <ErrorNote error={save.error} />
      </div>
    </form>
  );
}

// ---------------------------------------------------------------------
// Modelli di questionario: JSON con anteprima dal vivo
// ---------------------------------------------------------------------

function Templates({ cfg, selected }: { cfg: Config; selected?: string }) {
  const t = useT();
  // Esiti noti: quelli dei tipi di valutazione e quelli di default, senza doppioni.
  const outcomes = useMemo(() => {
    const m = new Map<string, ReviewOutcome>();
    for (const o of [...cfg.outcomes_default, ...cfg.types.flatMap((t) => t.outcomes)]) if (!m.has(o.key)) m.set(o.key, o);
    return [...m.values()];
  }, [cfg]);
  const [newKey, setNewKey] = useState("");
  const current = selected ?? cfg.templates[0]?.key;
  return (
    <div className="flex flex-col gap-5">
      <ul className="m-0 flex list-none flex-wrap gap-x-5 gap-y-2 border-b border-hair p-0 pb-3">
        {cfg.templates.map((t) => (
          <li key={t.key}>
            <Link
              to="/configurazione/valutazioni"
              search={{ tab: "modelli", modello: t.key }}
              className={`no-underline ${t.key === current ? "font-semibold text-accent" : "text-fg hover:text-accent"}`}
            >
              {t.name}
            </Link>{" "}
            <span className="font-mono text-[11.5px] text-muted">
              {t.current ? `v${t.current.version}` : "—"}
              {t.draft ? ` · ${tr("configurazione.processEditor.draftVersion", { version: t.draft.version })}` : ""}
            </span>
          </li>
        ))}
        {cfg.can_edit && (
          <li className="flex items-center gap-2">
            <input
              className="field w-[170px] py-1 font-mono text-[12.5px]"
              placeholder="chiave_nuovo_modello"
              value={newKey}
              onChange={(e) => setNewKey(e.target.value)}
            />
            {newKey && (
              <Link to="/configurazione/valutazioni" search={{ tab: "modelli", modello: newKey }} className="text-[12.5px]">
                {t("configurazione.common.crea")}
              </Link>
            )}
          </li>
        )}
      </ul>
      {current && <QuestionnaireEditor key={current} templateKey={current} canEdit={cfg.can_edit} outcomes={outcomes} />}
    </div>
  );
}
