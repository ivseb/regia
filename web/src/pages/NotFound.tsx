import { useEffect } from "react";
import { Link, useRouterState } from "@tanstack/react-router";
import { PageHeader } from "../shell";
import { useT } from "../i18n/react";
import { Trans } from "../i18n/Trans";

/** Pagina «non trovato» dentro la shell: dice quale indirizzo non esiste e dove andare. */
export function NotFound() {
  const t = useT();
  const path = useRouterState({ select: (s) => s.location.pathname });
  useEffect(() => {
    document.title = t("common.notFound.docTitle");
  }, [t]);
  return (
    <div>
      <PageHeader path="404" title={t("common.notFound.title")} />
      <div className="flex max-w-[60ch] flex-col gap-3 px-4 pb-8 md:px-7">
        <p className="m-0">
          <Trans k="common.notFound.body" slots={{ path: <span className="font-mono">{path}</span> }} />
        </p>
        <p className="m-0 text-muted">{t("common.notFound.hint")}</p>
        <div className="flex flex-wrap gap-2">
          <Link to="/" className="btn btn-primary no-underline">{t("common.notFound.home")}</Link>
          <Link to="/richieste" className="btn no-underline">{t("common.notFound.requests")}</Link>
          <Link to="/architettura" className="btn no-underline">{t("common.notFound.architecture")}</Link>
        </div>
      </div>
    </div>
  );
}
