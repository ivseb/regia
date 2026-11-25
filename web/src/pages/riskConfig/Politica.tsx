import { EntitySelect, Select } from "../../select";
import { ROLE_NAMES } from "../../ui";
import { useT } from "../../i18n/react";
import type { Approver } from "../../riskApi";
import { approverKind, approverOf, approverValue, parseSteps, stepsText, type ApproverKind, type Step } from "./model";

// Accettazione del rischio residuo: per ogni regola i passi (uno dopo l'altro) e, in ogni passo, gli
// approvatori (tutti devono approvare). Ruolo, organo e persona si scelgono da un menu: niente «a + b → c».

/** Passi come righe: «Passo 1», gli approvatori del passo, «aggiungi un approvatore» e «aggiungi un passo». */
export function StepsEditor({ steps, onChange, disabled }: { steps: Step[]; onChange: (s: Step[]) => void; disabled?: boolean }) {
  const t = useT();
  const roles: [string, string][] = [
    ...Object.entries(ROLE_NAMES),
    ["risk_owner", t("configurazione.rischi.ruolo.risk_owner")],
    ["risk_committee", t("configurazione.rischi.ruolo.risk_committee")],
    ["incident_lead", t("configurazione.rischi.ruolo.incident_lead")],
    ["provider_owner", t("configurazione.rischi.ruolo.provider_owner")],
  ];
  const setStep = (i: number, approvers: Approver[]) => onChange(steps.map((s, j) => (j === i ? { approvers } : s)));
  const kinds: [ApproverKind, string][] = [
    ["role", t("configurazione.rischi.approvatore.role")],
    ["body", t("configurazione.rischi.approvatore.body")],
    ["member", t("configurazione.rischi.approvatore.member")],
  ];
  return (
    <ol className="m-0 flex min-w-0 list-none flex-col gap-2 p-0" data-testid="passi-accettazione">
      {steps.map((s, i) => (
        <li key={i} className="flex min-w-0 flex-col gap-1.5 border-l-2 border-hair pl-3">
          <span className="label">{t("configurazione.rischi.passoN", { n: i + 1 })}</span>
          {s.approvers.map((a, k) => {
            const kind = approverKind(a);
            const set = (n: Approver) => setStep(i, s.approvers.map((x, j) => (j === k ? n : x)));
            return (
              <div key={k} className="flex min-w-0 flex-wrap items-center gap-2">
                {k > 0 && <span className="text-[12px] text-muted">{t("configurazione.rischi.eAnche")}</span>}
                <Select className="field w-auto" aria-label={t("configurazione.rischi.tipoApprovatore", { n: i + 1 })} disabled={disabled} value={kind} onChange={(e) => set(approverOf(e.target.value as ApproverKind))}>
                  {kinds.map(([v, l]) => (
                    <option key={v} value={v}>
                      {l}
                    </option>
                  ))}
                </Select>
                {kind === "role" && (
                  <Select className="field w-auto min-w-44" aria-label={t("configurazione.rischi.ruoloDelPasso", { n: i + 1 })} disabled={disabled} value={approverValue(a)} onChange={(e) => set({ role: e.target.value })}>
                    <option value="">{t("configurazione.common.scegli")}</option>
                    {approverValue(a) && !roles.some(([r]) => r === approverValue(a)) && <option value={approverValue(a)}>{approverValue(a)}</option>}
                    {roles.map(([r, name]) => (
                      <option key={r} value={r}>
                        {name}
                      </option>
                    ))}
                  </Select>
                )}
                {kind === "body" && <input className="field min-w-44" aria-label={t("configurazione.rischi.organoDelPasso", { n: i + 1 })} disabled={disabled} value={a.body ?? ""} onChange={(e) => set({ body: e.target.value })} placeholder={t("configurazione.rischi.organoEsempio")} />}
                {kind === "member" && <EntitySelect kind="member" className="field min-w-44" aria-label={t("configurazione.rischi.personaDelPasso", { n: i + 1 })} disabled={disabled} value={a.member ?? ""} onChange={(v) => set({ member: v })} />}
                {!disabled && (s.approvers.length > 1 || steps.length > 1) && (
                  <button
                    type="button"
                    className="touch-44 inline-flex items-center text-[12px] text-muted underline hover:text-danger"
                    onClick={() => (s.approvers.length > 1 ? setStep(i, s.approvers.filter((_, j) => j !== k)) : onChange(steps.filter((_, j) => j !== i)))}
                  >
                    {t("configurazione.common.togli2")}
                  </button>
                )}
              </div>
            );
          })}
          {!disabled && (
            <button type="button" className="touch-44 inline-flex items-center self-start text-[12.5px] font-semibold text-accent hover:underline" onClick={() => setStep(i, [...s.approvers, { role: "" }])}>
              {t("configurazione.rischi.aggiungiApprovatore")}
            </button>
          )}
        </li>
      ))}
      {!disabled && (
        <li>
          <button type="button" className="touch-44 inline-flex items-center text-[12.5px] font-semibold text-accent hover:underline" onClick={() => onChange([...steps, { approvers: [{ role: "" }] }])}>
            {t("configurazione.rischi.aggiungiPasso")}
          </button>
        </li>
      )}
    </ol>
  );
}

/** Testo grezzo dei passi, chiuso: per chi lo preferisce o deve incollarlo. Si applica uscendo dal campo. */
export function StepsAdvanced({ steps, onChange, disabled }: { steps: Step[]; onChange: (s: Step[]) => void; disabled?: boolean }) {
  const t = useT();
  const text = stepsText(steps);
  return (
    <details className="text-[12.5px]">
      <summary className="touch-44 inline-flex cursor-pointer items-center text-muted">{t("configurazione.rischi.avanzato")}</summary>
      <label className="mt-1 flex flex-col gap-1">
        <span className="text-muted">{t("configurazione.rischi.avanzatoPassi")}</span>
        <input
          key={text}
          className="field font-mono text-[12.5px]"
          aria-label={t("configurazione.risk.passi")}
          disabled={disabled}
          defaultValue={text}
          onBlur={(e) => e.target.value !== text && onChange(parseSteps(e.target.value))}
        />
      </label>
    </details>
  );
}
