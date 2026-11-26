// Schermata per chi ha solo organizzazioni sospese: dice chiaramente cosa succede e a chi rivolgersi,
// invece di un errore generico. Vedi docs/sviluppo/organizzazioni.md.
import { useT } from "../i18n/react";

export function OrganizationSuspended({ names }: { names: string[] }) {
  const t = useT();
  return (
    <section className="flex max-w-[640px] flex-col gap-3 p-4 md:p-8" data-testid="organizzazione-sospesa" aria-labelledby="org-sospesa-titolo">
      <h1 id="org-sospesa-titolo" className="m-0 text-[22px] font-bold">{t("organizzazioni.sospesa.title")}</h1>
      <p className="m-0 [overflow-wrap:anywhere]">
        {t("organizzazioni.sospesa.testo", { count: names.length, name: names.length === 1 ? names[0] : names.map((n) => `«${n}»`).join(", ") })}
      </p>
    </section>
  );
}
