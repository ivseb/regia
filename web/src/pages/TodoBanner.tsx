// Striscia in testa alle vecchie code (Le mie approvazioni, Le mie valutazioni, Da verificare): restano
// raggiungibili, ma ciò che aspetta la persona è ora in «Da fare».
import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { dafareApi } from "../dafareApi";
import { useT } from "../i18n/react";

export function TodoBanner() {
  const t = useT();
  const count = useQuery({ queryKey: ["todo", "count"], queryFn: dafareApi.count });
  return (
    <div role="note" className="flex flex-wrap items-center gap-x-4 gap-y-1 border-b border-hair bg-sel/40 px-4 py-2 md:px-7">
      <span className="text-[13px]">{t("dafare.old.banner")}</span>
      <Link to="/da-fare" className="touch-44 inline-flex items-center text-[13px] font-semibold text-accent underline">
        {t("dafare.old.link")}
        {!!count.data?.total && <span className="ml-1.5 font-mono text-[11.5px] tabular">{count.data.total}</span>}
      </Link>
    </div>
  );
}
