// Configurazione → indice (replica/usabilita.md, proposta 7A): una casa unica per tutte le
// impostazioni, per obiettivo e con ricerca, comprese quelle che stanno nelle pagine operative.
import { useMemo, useState } from "react";
import { Link } from "@tanstack/react-router";
import { PageHeader } from "../shell";
import { useMe } from "../shell";
import { useT } from "../i18n/react";
import { GOALS, configEntries, goalDescription, goalTitle, searchEntries } from "../casa/indice";

export function ConfigHome() {
  const t = useT();
  const me = useMe();
  const perms = me.data?.active_tenant?.permissions ?? [];
  const admin = perms.includes("*") || perms.includes("tenant.admin");
  const [q, setQ] = useState("");
  const all = useMemo(() => configEntries(me.data), [me.data, t]);
  const shown = useMemo(() => searchEntries(all, q), [all, q]);
  return (
    <>
      <PageHeader path={t("casa.indice.path")} title={t("casa.indice.title")} />
      <div className="flex flex-col gap-6 px-4 pb-12 md:px-7">
        <p className="m-0 max-w-3xl text-muted">{t("casa.indice.intro")}</p>
        <div className="grid grid-cols-1 gap-3 md:max-w-3xl md:grid-cols-2" data-testid="config-ingressi">
          {admin && (
            <Link to="/configurazione/percorso" className="flex min-h-11 flex-col gap-1 border-2 border-rule p-3 text-fg no-underline hover:text-accent" data-testid="ingresso-percorso">
              <span className="text-[16px] font-bold">{t("percorso.ingresso.title")}</span>
              <span className="text-[12.5px] text-muted">{t("percorso.ingresso.text")}</span>
            </Link>
          )}
          <Link to="/mappa-processi" className="flex min-h-11 flex-col gap-1 border-2 border-rule p-3 text-fg no-underline hover:text-accent" data-testid="ingresso-mappa">
            <span className="text-[16px] font-bold">{t("mappa.ingresso.title")}</span>
            <span className="text-[12.5px] text-muted">{t("mappa.ingresso.text")}</span>
          </Link>
        </div>
        <label className="flex max-w-xl flex-col gap-1">
          <span className="label">{t("casa.indice.search")}</span>
          <input type="search" className="field" value={q} placeholder={t("casa.indice.searchPlaceholder")} onChange={(e) => setQ(e.target.value)} />
        </label>
        {shown.length === 0 && <p className="m-0 text-muted" role="status">{t("casa.indice.none")}</p>}
        {GOALS.map((g) => {
          const items = shown.filter((e) => e.goal === g);
          if (items.length === 0) return null;
          return (
            <section key={g} className="flex flex-col" aria-labelledby={`obiettivo-${g}`} data-testid={`obiettivo-${g}`}>
              <div className="flex flex-wrap items-baseline gap-x-3 border-b-2 border-rule pb-1.5">
                <h2 id={`obiettivo-${g}`} className="m-0 text-[18px] font-bold">{goalTitle(g)}</h2>
                <span className="text-[12.5px] text-muted">{goalDescription(g)}</span>
                <span className="ml-auto font-mono text-[11.5px] text-muted tabular">{t("casa.indice.count", { count: items.length })}</span>
              </div>
              <ul className="m-0 grid list-none grid-cols-1 gap-x-8 p-0 md:grid-cols-2">
                {items.map((e) => (
                  <li key={e.id} className="border-b border-hair">
                    <Link
                      to={e.to}
                      search={e.search as never}
                      className="flex min-h-11 flex-col gap-0.5 py-2.5 text-fg no-underline hover:text-accent"
                      aria-label={t("casa.indice.open", { name: e.title })}
                    >
                      <span className="font-semibold">{e.title}</span>
                      <span className="text-[12.5px] text-muted">{e.description}</span>
                    </Link>
                  </li>
                ))}
              </ul>
            </section>
          );
        })}
      </div>
    </>
  );
}
