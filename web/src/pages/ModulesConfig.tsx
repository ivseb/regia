// Configurazione → Moduli: quali parti del prodotto usa l'organizzazione. Un modulo spento
// sparisce da menu, ⌘K e pagine e le sue API rispondono 404; i dati restano e tornano
// visibili riaccendendolo (replica/usabilita.md, sezione 6 punto 5).
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError } from "../api";
import { modulesApi, type ModuleKey } from "../modules";
import { PageHeader } from "../shell";
import { ErrorNote, Loading, SectionTitle } from "../ui";
import { useT } from "../i18n/react";

export function ModulesConfig() {
  const t = useT();
  const qc = useQueryClient();
  const list = useQuery({ queryKey: ["tenant-modules"], queryFn: modulesApi.list });
  const [draft, setDraft] = useState<Partial<Record<ModuleKey, boolean>>>({});
  const save = useMutation({
    mutationFn: () => modulesApi.save(draft),
    onSuccess: (data) => {
      qc.setQueryData(["tenant-modules"], data);
      // Menu, palette e pagine leggono i moduli accesi da /me.
      qc.invalidateQueries({ queryKey: ["me"] });
      setDraft({});
    },
  });

  if (list.error instanceof ApiError && list.error.status === 403) {
    return (
      <>
        <PageHeader path={t("configurazione.path.modules")} title={t("configurazione.modules.moduli")} />
        <p className="px-4 text-muted md:px-7">{t("configurazione.common.serveIlPermessoAmministrare")}</p>
      </>
    );
  }
  const items = list.data?.items ?? [];
  const on = (key: ModuleKey, stored: boolean) => draft[key] ?? stored;
  const changes = items.filter((m) => on(m.key, m.enabled) !== m.enabled);
  const turningOff = changes.filter((m) => !on(m.key, m.enabled));
  const turningOn = changes.filter((m) => on(m.key, m.enabled));

  return (
    <>
      <PageHeader path={t("configurazione.path.modules")} title={t("configurazione.modules.moduli")} />
      <div className="flex max-w-[760px] flex-col gap-5 px-4 pb-8 md:px-7">
        <p className="m-0 text-muted">
          {t("configurazione.modules.scegliQualiPartiDi")}
        </p>
        {list.isPending && <Loading />}
        <ErrorNote error={list.error} />
        <section aria-label={t("configurazione.modules.moduliDellOrganizzazione")} className="flex flex-col">
          <SectionTitle>{t("configurazione.modules.moduli")}</SectionTitle>
          {items.map((m) => {
            const checked = on(m.key, m.enabled);
            const id = `modulo-${m.key}`;
            return (
              <div key={m.key} className="flex items-start gap-3 border-b border-hair py-3">
                <input
                  id={id}
                  type="checkbox"
                  checked={checked}
                  onChange={(e) => setDraft((d) => ({ ...d, [m.key]: e.target.checked }))}
                  className="mt-0.5"
                />
                <div className="flex min-w-0 flex-1 flex-col gap-1">
                  <label htmlFor={id} className="cursor-pointer font-semibold">{m.name}</label>
                  <span className="text-muted">{m.description}</span>
                  <span className="text-[12px] text-muted">{t("configurazione.modules.ifOff", { effect: m.effect })}</span>
                </div>
                <span className={`shrink-0 font-mono text-[11.5px] ${checked ? "text-ok" : "text-muted"}`}>{checked ? t("configurazione.modules.on") : t("configurazione.modules.off")}</span>
              </div>
            );
          })}
        </section>
        {changes.length > 0 && (
          <div role="status" className="flex flex-col gap-1 border border-rule p-3">
            <span className="label">{t("configurazione.common.cosaCambia")}</span>
            {turningOff.length > 0 && <span>{t("configurazione.modules.turningOff", { names: turningOff.map((m) => m.name).join(", ") })}</span>}
            {turningOn.length > 0 && <span>{t("configurazione.modules.turningOn", { names: turningOn.map((m) => m.name).join(", ") })}</span>}
          </div>
        )}
        <ErrorNote error={save.error} />
        <div className="flex gap-2">
          <button className="btn btn-primary" disabled={changes.length === 0 || save.isPending} onClick={() => save.mutate()}>
            {save.isPending ? t("configurazione.common.salvataggio") : t("configurazione.modules.salvaLeModifiche")}
          </button>
          {changes.length > 0 && (
            <button className="btn" onClick={() => setDraft({})}>{t("configurazione.common.annulla")}</button>
          )}
        </div>
        <p className="m-0 text-[12px] text-muted">
          {t("configurazione.modules.ilRevisoreRuoloAuditor")}
        </p>
      </div>
    </>
  );
}
