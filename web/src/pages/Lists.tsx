// Liste generiche con filtri visuali, viste salvate e raggruppamento (piano, passo 16):
// impatti, applicazioni, asset, rischi, incidenti, controlli, documenti.
import { Link, useNavigate } from "@tanstack/react-router";
import { listRoute } from "../router";
import { ElementList } from "../views/ElementList";
import { LISTS } from "../views/lists";
import { Trans } from "../i18n/Trans";
import { useT } from "../i18n/react";

export function Lists() {
  const t = useT();
  const { lista } = listRoute.useParams();
  const search = listRoute.useSearch();
  const navigate = useNavigate({ from: listRoute.fullPath });
  const config = LISTS[lista];
  if (!config || lista === "richieste") {
    return (
      <div className="px-4 py-10 md:px-7">
        <h1 className="m-0 text-[28px] font-bold">{t("lavoro.lists.notFound")}</h1>
        <p className="text-muted">
          <Trans
            k="lavoro.lists.available"
            slots={{
              lists: (
                <>
                  {Object.values(LISTS).filter((c) => c.page !== "richieste").map((c, i) => (
                    <span key={c.page}>{i > 0 && ", "}<Link to="/elenco/$lista" params={{ lista: c.page }}>{c.title.toLowerCase()}</Link></span>
                  ))}
                </>
              ),
            }}
          />
        </p>
      </div>
    );
  }
  return <ElementList key={config.page} config={config} search={search} onSearch={(s) => navigate({ search: s })} />;
}
