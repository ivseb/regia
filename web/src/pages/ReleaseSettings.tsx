// Configurazione → DevSecOps, sezione «Rilasci» (caricata con lazy da Devsecops.tsx):
// chiusura degli elementi con le parole chiave dei commit, eccezioni per spazio, sezioni della
// certificazione del rilascio accese e in ordine. Un solo «Salva»; senza permesso, sola lettura.
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { rilasciApi, CERT_SECTIONS, type CertSection, type ReleaseSettings } from "../rilasciApi";
import { ErrorNote, Loading, SectionTitle } from "../ui";
import { Select } from "../select";
import { useT } from "../i18n/react";
import type { Key } from "../i18n/core";
import { moveRow, sectionRows, settingsInput, spaceKey, type SectionRow, type SpaceRow } from "../certificazione";

const SECTION_NAME: Record<CertSection, Key> = {
  change: "rilasci.certificazione.sezione.change",
  releases: "rilasci.certificazione.sezione.releases",
  items: "rilasci.certificazione.sezione.items",
  tests: "rilasci.certificazione.sezione.tests",
  sonar: "rilasci.certificazione.sezione.sonar",
  security: "rilasci.certificazione.sezione.security",
  gate: "rilasci.certificazione.sezione.gate",
};

export function ReleaseSettingsSection() {
  const t = useT();
  const q = useQuery({ queryKey: ["release-settings"], queryFn: rilasciApi.settings });
  return (
    <section className="flex min-w-0 flex-col gap-4" aria-label={t("rilasci.certificazione.impostazioni.titolo")}>
      <SectionTitle>{t("rilasci.certificazione.impostazioni.titolo")}</SectionTitle>
      <ErrorNote error={q.error} />
      {!q.data && !q.error && <Loading rows={4} />}
      {/* Senza key: dopo il salvataggio il modulo resta com'è (con «Salvato» o con l'errore). */}
      {q.data && <SettingsForm s={q.data} />}
    </section>
  );
}

function SettingsForm({ s }: { s: ReleaseSettings }) {
  const t = useT();
  const qc = useQueryClient();
  const ro = !s.can_configure;
  const [close, setClose] = useState(s.close_on_keywords);
  const [spaces, setSpaces] = useState<SpaceRow[]>(s.spaces.map((sp) => ({ key: sp.key, close: sp.close_on_keywords })));
  const [rows, setRows] = useState<SectionRow[]>(() => sectionRows(s.cert_sections, CERT_SECTIONS));
  const names = new Map(s.spaces.map((sp) => [sp.key.toUpperCase(), sp.name]));
  const save = useMutation({
    mutationFn: () => rilasciApi.saveSettings(settingsInput(close, rows, spaces)),
    onSuccess: (res) => {
      qc.setQueryData(["release-settings"], res);
      qc.invalidateQueries({ queryKey: ["certification"] });
    },
  });
  const setSpace = (i: number, patch: Partial<SpaceRow>) => setSpaces((l) => l.map((r, j) => (j === i ? { ...r, ...patch } : r)));
  return (
    <form
      className="flex min-w-0 flex-col gap-5"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      {ro && <p className="m-0 text-[12.5px] text-muted">{t("rilasci.certificazione.impostazioni.solaLettura")}</p>}

      <label className="flex max-w-3xl items-start gap-3">
        <input type="checkbox" role="switch" className="mt-0.5 size-5 shrink-0 accent-[var(--accent)]" disabled={ro} checked={close} onChange={(e) => setClose(e.target.checked)} />
        <span className="flex min-w-0 flex-col">
          <span className="font-semibold">{t("rilasci.certificazione.impostazioni.chiusura")}</span>
          <span className="text-[12px] text-muted">{t("rilasci.certificazione.impostazioni.chiusuraAiuto")}</span>
        </span>
      </label>

      <div className="flex min-w-0 flex-col gap-1.5">
        <span className="label">{t("rilasci.certificazione.impostazioni.eccezioni")}</span>
        <span className="text-[12px] text-muted">{t("rilasci.certificazione.impostazioni.eccezioniAiuto")}</span>
        {spaces.length === 0 && <p className="m-0 text-[12.5px] text-muted">{t("rilasci.certificazione.impostazioni.nessunaEccezione")}</p>}
        <ul className="m-0 flex max-w-xl list-none flex-col gap-1.5 p-0">
          {spaces.map((sp, i) => {
            const k = spaceKey(sp.key);
            const name = names.get(k);
            return (
              <li key={i} className="grid min-w-0 grid-cols-[minmax(0,1fr)_minmax(0,10rem)_auto] items-center gap-2">
                <span className="flex min-w-0 flex-col">
                  <input
                    aria-label={t("rilasci.certificazione.impostazioni.spazio")}
                    className="field py-1 font-mono uppercase"
                    disabled={ro}
                    value={sp.key}
                    onChange={(e) => setSpace(i, { key: spaceKey(e.target.value) })}
                  />
                  {name && <span className="truncate text-[11.5px] text-muted">{name}</span>}
                </span>
                <Select
                  aria-label={t("rilasci.certificazione.impostazioni.chiudeSpazio", { key: k || "—" })}
                  className="field py-1"
                  disabled={ro}
                  value={sp.close ? "yes" : "no"}
                  onChange={(e) => setSpace(i, { close: e.target.value === "yes" })}
                >
                  <option value="yes">{t("rilasci.certificazione.impostazioni.si")}</option>
                  <option value="no">{t("rilasci.certificazione.impostazioni.no")}</option>
                </Select>
                {!ro ? (
                  <button
                    type="button"
                    className="text-[12px] text-muted underline hover:text-danger"
                    aria-label={t("rilasci.certificazione.impostazioni.togliSpazio", { key: k || "—" })}
                    onClick={() => setSpaces((l) => l.filter((_, j) => j !== i))}
                  >
                    {t("rilasci.certificazione.impostazioni.togli")}
                  </button>
                ) : (
                  <span />
                )}
              </li>
            );
          })}
        </ul>
        {!ro && (
          <button type="button" className="self-start text-[12.5px] text-accent underline" onClick={() => setSpaces((l) => [...l, { key: "", close: !close }])}>
            {t("rilasci.certificazione.impostazioni.aggiungiSpazio")}
          </button>
        )}
      </div>

      <div className="flex min-w-0 flex-col gap-1.5">
        <span className="label">{t("rilasci.certificazione.impostazioni.sezioni")}</span>
        <span className="text-[12px] text-muted">{t("rilasci.certificazione.impostazioni.sezioniAiuto")}</span>
        <ol className="m-0 flex max-w-md list-none flex-col p-0">
          {rows.map((r, i) => {
            const name = t(SECTION_NAME[r.key]);
            return (
              <li key={r.key} className="flex min-w-0 items-center gap-2 border-b border-hair py-1">
                <label className="flex min-w-0 flex-1 items-center gap-2">
                  <input
                    type="checkbox"
                    disabled={ro}
                    checked={r.on}
                    onChange={(e) => setRows((l) => l.map((x, j) => (j === i ? { ...x, on: e.target.checked } : x)))}
                  />
                  <span className={`min-w-0 break-words ${r.on ? "" : "text-muted"}`}>{name}</span>
                </label>
                {!ro && (
                  <span className="flex shrink-0 gap-1">
                    <button type="button" className="btn px-2 py-0.5" aria-label={t("rilasci.certificazione.impostazioni.su", { name })} disabled={i === 0} onClick={() => setRows((l) => moveRow(l, i, -1))}>
                      ↑
                    </button>
                    <button
                      type="button"
                      className="btn px-2 py-0.5"
                      aria-label={t("rilasci.certificazione.impostazioni.giu", { name })}
                      disabled={i === rows.length - 1}
                      onClick={() => setRows((l) => moveRow(l, i, 1))}
                    >
                      ↓
                    </button>
                  </span>
                )}
              </li>
            );
          })}
        </ol>
      </div>

      {!ro && (
        <span className="flex items-center gap-3">
          <button className="btn btn-primary" disabled={save.isPending}>{t("rilasci.certificazione.impostazioni.salva")}</button>
          {save.isSuccess && <span role="status" className="text-[12.5px] text-ok">{t("rilasci.certificazione.impostazioni.salvato")}</span>}
        </span>
      )}
      <ErrorNote error={save.error} />
    </form>
  );
}
