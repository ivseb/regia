// Procedura guidata «Coinvolgi un team» (replica/prossimi-passi.md): quale team, quando entra in gioco,
// cosa fa, e un'anteprima in parole semplici prima di scrivere. Scrive da sola il tipo di valutazione,
// la regola del processo e la nuova versione, senza passare da cinque pagine di configurazione.
import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery } from "@tanstack/react-query";
import { api } from "../api";
import { EntitySelect, Select } from "../select";
import { PageHeader } from "../shell";
import { ErrorNote, Loading } from "../ui";
import { useT } from "../i18n/react";
import { coinvolgiApi, type CoinvolgiInput, type CoinvolgiResult } from "../coinvolgiApi";
import { ConditionBuilder } from "./processEditor/ConditionBuilder";
import { describeCondition } from "./processEditor/model";

const STEPS = ["team", "quando", "cosa", "conferma"] as const;
type Step = (typeof STEPS)[number];

export function CoinvolgiPage() {
  const t = useT();
  const [step, setStep] = useState<Step>("team");
  const [teamMode, setTeamMode] = useState<"existing" | "new">("existing");
  const [teamId, setTeamId] = useState("");
  const [newTeam, setNewTeam] = useState("");
  const [process, setProcess] = useState("");
  const [phase, setPhase] = useState("");
  const [when, setWhen] = useState("");
  const [review, setReview] = useState(true);
  const [work, setWork] = useState(false);
  const [mandatory, setMandatory] = useState(true);
  const [done, setDone] = useState<CoinvolgiResult | null>(null);

  const processes = useQuery({ queryKey: ["processes"], queryFn: api.processes, staleTime: 30_000 });
  const requestProcesses = (processes.data?.items ?? []).filter((p) => p.type === "business_request" && !p.archived && p.versions.some((v) => v.status === "published"));
  const chosen = process || requestProcesses[0]?.key || "";
  const detail = useQuery({ queryKey: ["process", chosen], queryFn: () => api.process(chosen), enabled: !!chosen });
  const phases = (detail.data?.current?.phases ?? []).filter((p) => !p.off_path);

  const input = (): CoinvolgiInput => ({
    ...(teamMode === "existing" ? { team_id: teamId } : { new_team: newTeam.trim() }),
    process: chosen,
    phase,
    when,
    review,
    work,
    mandatory,
  });
  const preview = useMutation({ mutationFn: () => coinvolgiApi.preview(input()) });
  const apply = useMutation({ mutationFn: () => coinvolgiApi.apply(input()), onSuccess: setDone });

  const teamOk = teamMode === "existing" ? !!teamId : newTeam.trim().length > 0;
  const idx = STEPS.indexOf(step);
  const goto = (s: Step) => {
    preview.reset();
    apply.reset();
    setStep(s);
    if (s === "conferma") preview.mutate();
  };
  const canNext = step === "team" ? teamOk : step === "quando" ? !!chosen : step === "cosa" ? review || work : false;

  return (
    <>
      <PageHeader path={t("configurazione.coinvolgi.path")} title={t("configurazione.coinvolgi.title")} />
      <div className="flex max-w-[760px] flex-col gap-5 px-4 pb-12 md:px-7">
        {done ? (
          <Done r={done} />
        ) : (
          <>
            <p className="m-0 text-muted">{t("configurazione.coinvolgi.intro")}</p>
            <ol aria-label={t("configurazione.coinvolgi.passi")} className="m-0 flex list-none flex-wrap gap-x-5 gap-y-1 p-0 text-[13px]">
              {STEPS.map((s, i) => (
                <li key={s} aria-current={s === step ? "step" : undefined} className={s === step ? "font-semibold text-fg" : "text-muted"}>
                  {t("configurazione.coinvolgi.passo", { n: i + 1, name: t(`configurazione.coinvolgi.passo.${s}`) })}
                </li>
              ))}
            </ol>

            {step === "team" && (
              <fieldset className="m-0 flex flex-col gap-3 border-0 p-0">
                <legend className="mb-1 text-[17px] font-bold">{t("configurazione.coinvolgi.team.titolo")}</legend>
                <label className="flex items-center gap-2">
                  <input type="radio" name="team-mode" checked={teamMode === "existing"} onChange={() => setTeamMode("existing")} />
                  {t("configurazione.coinvolgi.team.esistente")}
                </label>
                {teamMode === "existing" && (
                  <EntitySelect kind="team" aria-label={t("configurazione.coinvolgi.team.scegli")} className="field max-w-[420px]" value={teamId} onChange={setTeamId} />
                )}
                <label className="flex items-center gap-2">
                  <input type="radio" name="team-mode" checked={teamMode === "new"} onChange={() => setTeamMode("new")} />
                  {t("configurazione.coinvolgi.team.nuovo")}
                </label>
                {teamMode === "new" && (
                  <input className="field max-w-[420px]" aria-label={t("configurazione.coinvolgi.team.nome")} placeholder={t("configurazione.coinvolgi.team.nome")} value={newTeam} maxLength={120} onChange={(e) => setNewTeam(e.target.value)} />
                )}
              </fieldset>
            )}

            {step === "quando" && (
              <div className="flex flex-col gap-3">
                <h2 className="m-0 text-[17px] font-bold">{t("configurazione.coinvolgi.quando.titolo")}</h2>
                {processes.isPending && <Loading className="m-0 text-muted" />}
                <ErrorNote error={processes.error} />
                {!processes.isPending && requestProcesses.length === 0 && <p className="m-0 text-muted">{t("configurazione.coinvolgi.quando.nessunProcesso")}</p>}
                {requestProcesses.length > 0 && (
                  <>
                    <label className="flex max-w-[420px] flex-col gap-1">
                      <span className="label">{t("configurazione.coinvolgi.quando.processo")}</span>
                      <Select
                        aria-label={t("configurazione.coinvolgi.quando.processo")}
                        className="field"
                        value={chosen}
                        onChange={(e) => {
                          setProcess(e.target.value);
                          setPhase("");
                        }}
                      >
                        {requestProcesses.map((p) => (
                          <option key={p.key} value={p.key}>
                            {p.name}
                          </option>
                        ))}
                      </Select>
                    </label>
                    <label className="flex max-w-[420px] flex-col gap-1">
                      <span className="label">{t("configurazione.coinvolgi.quando.fase")}</span>
                      <Select aria-label={t("configurazione.coinvolgi.quando.fase")} className="field" value={phase} onChange={(e) => setPhase(e.target.value)}>
                        <option value="">{t("configurazione.coinvolgi.quando.allaCreazione")}</option>
                        {phases.map((p) => (
                          <option key={p.name} value={p.name}>
                            {t("configurazione.coinvolgi.quando.entrando", { phase: p.name })}
                          </option>
                        ))}
                      </Select>
                    </label>
                    <div className="flex flex-col gap-1">
                      <span className="label">{t("configurazione.coinvolgi.quando.condizione")}</span>
                      <p className="m-0 text-[13px] text-muted">{t("configurazione.coinvolgi.quando.condizioneAiuto")}</p>
                      <ConditionBuilder value={when} onChange={setWhen} type="business_request" noun={t("configurazione.coinvolgi.quando.richieste")} label={t("configurazione.coinvolgi.quando.condizione")} />
                    </div>
                  </>
                )}
              </div>
            )}

            {step === "cosa" && (
              <fieldset className="m-0 flex flex-col gap-3 border-0 p-0">
                <legend className="mb-1 text-[17px] font-bold">{t("configurazione.coinvolgi.cosa.titolo")}</legend>
                <label className="flex items-start gap-2">
                  <input type="checkbox" className="mt-1" checked={review} onChange={(e) => setReview(e.target.checked)} />
                  <span>
                    <strong>{t("configurazione.coinvolgi.cosa.parere")}</strong>
                    <span className="block text-[13px] text-muted">{t("configurazione.coinvolgi.cosa.parereAiuto")}</span>
                  </span>
                </label>
                {review && (
                  <label className="ml-6 flex items-center gap-2">
                    <input type="checkbox" checked={mandatory} onChange={(e) => setMandatory(e.target.checked)} />
                    {t("configurazione.coinvolgi.cosa.obbligatorio")}
                  </label>
                )}
                <label className="flex items-start gap-2">
                  <input type="checkbox" className="mt-1" checked={work} onChange={(e) => setWork(e.target.checked)} />
                  <span>
                    <strong>{t("configurazione.coinvolgi.cosa.lavoro")}</strong>
                    <span className="block text-[13px] text-muted">{t("configurazione.coinvolgi.cosa.lavoroAiuto")}</span>
                  </span>
                </label>
              </fieldset>
            )}

            {step === "conferma" && (
              <div className="flex flex-col gap-3">
                <h2 className="m-0 text-[17px] font-bold">{t("configurazione.coinvolgi.conferma.titolo")}</h2>
                {preview.isPending && <Loading className="m-0 text-muted" />}
                {preview.data && <Summary r={preview.data} />}
                <ErrorNote error={preview.error ?? apply.error} />
              </div>
            )}

            <div className="flex flex-wrap gap-2 pt-1">
              {idx > 0 && (
                <button type="button" className="btn min-h-11 md:min-h-0" onClick={() => goto(STEPS[idx - 1])}>
                  {t("configurazione.coinvolgi.indietro")}
                </button>
              )}
              {step !== "conferma" && (
                <button type="button" className="btn btn-primary min-h-11 md:min-h-0" disabled={!canNext} onClick={() => goto(STEPS[idx + 1])}>
                  {t("configurazione.coinvolgi.avanti")}
                </button>
              )}
              {step === "conferma" && (
                <button type="button" className="btn btn-primary min-h-11 md:min-h-0" disabled={!preview.data || apply.isPending} onClick={() => apply.mutate()}>
                  {t("configurazione.coinvolgi.conferma.scrivi")}
                </button>
              )}
            </div>
          </>
        )}
      </div>
    </>
  );
}

/** L'anteprima in parole semplici: una riga per ogni cosa che verrà scritta. */
function Summary({ r }: { r: CoinvolgiResult }) {
  const t = useT();
  const when = r.rule.when ? describeCondition(r.rule.when) : "";
  const event = r.rule.phase ? t("configurazione.coinvolgi.sintesi.entrando", { phase: r.rule.phase }) : t("configurazione.coinvolgi.sintesi.allaCreazione");
  const does = [r.review && t("configurazione.coinvolgi.sintesi.apreParere"), r.work && t("configurazione.coinvolgi.sintesi.creaImpatto", { team: r.team })].filter(Boolean).join(t("configurazione.coinvolgi.sintesi.e"));
  return (
    <div className="flex flex-col gap-3">
      <p className="m-0">{t(r.applied ? "configurazione.coinvolgi.sintesi.introFatto" : "configurazione.coinvolgi.sintesi.intro")}</p>
      <ul className="m-0 flex list-disc flex-col gap-1.5 pl-5">
        {r.team_new && <li>{t("configurazione.coinvolgi.sintesi.teamNuovo", { team: r.team })}</li>}
        {r.review?.review_team_new && <li>{t("configurazione.coinvolgi.sintesi.teamValutatore", { team: r.team })}</li>}
        {r.review && <li>{t(r.review.mandatory ? "configurazione.coinvolgi.sintesi.tipoObbligatorio" : "configurazione.coinvolgi.sintesi.tipoFacoltativo", { name: r.review.type_name })}</li>}
        {r.review && <li>{t("configurazione.coinvolgi.sintesi.questionario")}</li>}
        <li>
          {t(when ? "configurazione.coinvolgi.sintesi.regolaCondizione" : "configurazione.coinvolgi.sintesi.regola", { process: r.rule.process_name, event, condition: when, does })}
        </li>
        <li>{t("configurazione.coinvolgi.sintesi.versione", { n: r.version, process: r.rule.process_name })}</li>
      </ul>
      {r.warnings.includes("group_empty") && <p className="m-0 text-[13px] text-muted">{t("configurazione.coinvolgi.sintesi.gruppoVuoto", { team: r.team })}</p>}
      <p className="m-0 text-[13px] text-muted">{t("configurazione.coinvolgi.sintesi.avviate")}</p>
    </div>
  );
}

function Done({ r }: { r: CoinvolgiResult }) {
  const t = useT();
  return (
    <div className="flex flex-col gap-3" role="status">
      <h2 className="m-0 text-[17px] font-bold">{t("configurazione.coinvolgi.fatto.titolo", { team: r.team })}</h2>
      <Summary r={r} />
      <div className="flex flex-wrap gap-x-5 gap-y-2">
        <Link to="/processi/$key" params={{ key: r.rule.process }} search={{ regola: r.rule.key }} className="text-accent underline">
          {t("configurazione.coinvolgi.fatto.vediRegola")}
        </Link>
        {r.review && (
          <Link to="/configurazione/valutazioni" search={{ tab: "modelli", modello: r.review.template_key }} className="text-accent underline">
            {t("configurazione.coinvolgi.fatto.modificaQuestionario")}
          </Link>
        )}
        <Link to="/configurazione/coinvolgi" className="text-accent underline" reloadDocument>
          {t("configurazione.coinvolgi.fatto.altro")}
        </Link>
      </div>
    </div>
  );
}
