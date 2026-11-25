import { useState } from "react";
import { Select } from "../../select";
import type { Definition, ProcessMeta } from "../../api";
import { ConditionBuilder } from "./ConditionBuilder";
import { type StepInput } from "./model";
import { useT } from "../../i18n/react";

// Procedura «Aggiungi un passo»: dopo quale fase, come si chiama, chi la fa avanzare, se serve una
// condizione e se serve un controllo (gate) da superare. Tutto nello stesso riquadro: il gate si
// crea e si compila qui, senza andarlo a cercare più in basso nella pagina.

export function AddStep({
  def,
  meta,
  initialAfter,
  extraValues,
  onAdd,
  onClose,
}: {
  def: Definition;
  meta: ProcessMeta;
  /** Fase proposta come «dopo»; senza, l'ultima prima della fase finale. */
  initialAfter?: string;
  extraValues: Record<string, string[]>;
  onAdd: (s: StepInput) => void;
  onClose: () => void;
}) {
  const t = useT();
  const main = def.phases.filter((p) => !p.off_path);
  const fallback = [...main].reverse().find((p) => p.category !== "done" && p.category !== "cancelled") ?? main[main.length - 1];
  const [after, setAfter] = useState(initialAfter && main.some((p) => p.name === initialAfter) ? initialAfter : (fallback?.name ?? ""));
  const [name, setName] = useState("");
  const [roles, setRoles] = useState<string[]>([]);
  const [needWhen, setNeedWhen] = useState(false);
  const [when, setWhen] = useState("");
  const [needGate, setNeedGate] = useState(false);
  const [gate, setGate] = useState({ name: "", condition: "", message: "" });
  const next = main[main.findIndex((p) => p.name === after) + 1];
  const free = meta.roles.filter((r) => !roles.includes(r.key));
  const roleName = (k: string) => meta.roles.find((r) => r.key === k)?.name ?? k;
  const ready = name.trim() !== "" && (!needGate || gate.condition.trim() !== "");
  return (
    <form
      aria-label={t("configurazione.passo.titolo")}
      data-testid="aggiungi-passo"
      className="flex min-w-0 flex-col gap-4 border-y-2 border-rule py-4"
      onSubmit={(e) => {
        e.preventDefault();
        if (!ready) return;
        onAdd({ after, name, roles, when: needWhen ? when : "", gate: needGate ? { ...gate, name: gate.name.trim() || name.trim() } : null });
      }}
    >
      <h3 className="label m-0">{t("configurazione.passo.titolo")}</h3>
      <div className="grid min-w-0 gap-3 sm:grid-cols-2">
        <label className="flex min-w-0 flex-col gap-1">
          <span className="label">{t("configurazione.passo.dopo")}</span>
          <Select className="field" value={after} onChange={(e) => setAfter(e.target.value)}>
            {main.map((p) => (
              <option key={p.name} value={p.name}>
                {p.name}
              </option>
            ))}
          </Select>
        </label>
        <label className="flex min-w-0 flex-col gap-1">
          <span className="label">{t("configurazione.passo.nome")}</span>
          <input className="field" value={name} onChange={(e) => setName(e.target.value)} placeholder={t("configurazione.passo.nomeEsempio")} />
        </label>
      </div>
      {after && (
        <p className="m-0 text-[12.5px] text-muted" data-testid="passo-sintesi">
          {next ? t("configurazione.passo.sintesiTra", { after, next: next.name }) : t("configurazione.passo.sintesiInFondo", { after })}
        </p>
      )}

      <fieldset className="m-0 flex min-w-0 flex-col gap-1.5 border-0 p-0">
        <legend className="label mb-1 p-0">{t("configurazione.passo.chiLoFaAvanzare")}</legend>
        {roles.length === 0 ? (
          <p className="m-0 text-[12.5px] text-muted">{t("configurazione.passo.chiunque")}</p>
        ) : (
          <ul className="m-0 flex list-none flex-wrap gap-2 p-0">
            {roles.map((k) => (
              <li key={k} className="inline-flex items-center gap-1.5 border border-hair px-2 py-1 text-[13px]">
                {roleName(k)}
                <button type="button" className="touch-44 inline-flex items-center px-1 text-muted underline hover:text-danger" aria-label={t("configurazione.passo.togliRuolo", { name: roleName(k) })} onClick={() => setRoles(roles.filter((x) => x !== k))}>
                  ×
                </button>
              </li>
            ))}
          </ul>
        )}
        {free.length > 0 && (
          <Select className="field w-full sm:w-72" aria-label={t("configurazione.passo.aggiungiRuolo")} value="" onChange={(e) => e.target.value && setRoles([...roles, e.target.value])}>
            <option value="">{t("configurazione.passo.aggiungiRuolo")}</option>
            {free.map((r) => (
              <option key={r.key} value={r.key}>
                {r.name}
              </option>
            ))}
          </Select>
        )}
      </fieldset>

      <div className="flex min-w-0 flex-col gap-2">
        <label className="inline-flex min-h-11 items-center gap-2 text-[13px] md:min-h-0">
          <input type="checkbox" checked={needWhen} onChange={(e) => setNeedWhen(e.target.checked)} />
          {t("configurazione.passo.serveCondizione")}
        </label>
        {needWhen && (
          <ConditionBuilder label={t("configurazione.processEditor.soloSe")} value={when} onChange={setWhen} facts={meta.facts} type={meta.type.key} sample={meta.sample} empty={t("configurazione.passo.sempre")} extraValues={extraValues} />
        )}
      </div>

      <div className="flex min-w-0 flex-col gap-2">
        <label className="inline-flex min-h-11 items-center gap-2 text-[13px] md:min-h-0">
          <input type="checkbox" checked={needGate} onChange={(e) => setNeedGate(e.target.checked)} />
          {t("configurazione.passo.serveGate")}
        </label>
        {needGate && (
          <fieldset className="m-0 flex min-w-0 flex-col gap-2 border border-hair p-3" data-testid="passo-gate">
            <legend className="label px-1">{t("configurazione.passo.gate")}</legend>
            <label className="flex min-w-0 flex-col gap-1">
              <span className="label">{t("configurazione.passo.gateNome")}</span>
              <input className="field" value={gate.name} placeholder={name.trim() || t("configurazione.passo.gateNomeEsempio")} onChange={(e) => setGate({ ...gate, name: e.target.value })} />
            </label>
            <ConditionBuilder
              label={t("configurazione.processEditor.deveValere")}
              value={gate.condition}
              onChange={(v) => setGate({ ...gate, condition: v })}
              facts={meta.facts}
              type={meta.type.key}
              sample={meta.sample}
              empty={t("configurazione.processEditor.vaAggiuntaAlmenoUna")}
              extraValues={extraValues}
            />
            <label className="flex min-w-0 flex-col gap-1">
              <span className="label">{t("configurazione.processEditor.messaggioQuandoNonE")}</span>
              <input className="field" value={gate.message} placeholder={t("configurazione.processEditor.esServeUnaStima")} onChange={(e) => setGate({ ...gate, message: e.target.value })} />
            </label>
          </fieldset>
        )}
      </div>

      <div className="flex flex-wrap gap-2">
        <button className="btn btn-primary" disabled={!ready}>
          {t("configurazione.passo.aggiungi")}
        </button>
        <button type="button" className="btn" onClick={onClose}>
          {t("configurazione.common.annulla")}
        </button>
      </div>
    </form>
  );
}
