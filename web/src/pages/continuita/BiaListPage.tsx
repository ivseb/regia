// Elenco delle BIA (/architettura/continuita/bia): l'albero delle capability con lo stato della
// BIA, RTO e MTPD in vigore, applicazioni critiche e «Avvia la BIA» dove manca.
import { useEffect, useRef } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ErrorNote, Loading, SectionTitle, date } from "../../ui";
import { useT } from "../../i18n/react";
import { useStateName } from "../../stateLabels";
import { EaPage } from "../ea/tabs";
import { continuityApi, type BiaListItem, type Can } from "../../continuityApi";
import { ContinuityError, ContinuityNav, Minutes } from "./ui";

export function BiaListPage({ capability }: { capability?: string }) {
  const t = useT();
  const list = useQuery({ queryKey: ["continuity", "bia"], queryFn: continuityApi.biaList });
  const items = list.data?.items ?? [];
  const withBia = items.filter((i) => i.bia).length;
  return (
    <EaPage active="continuita" title={t("continuita.bia.title")}>
      <ContinuityNav active="bia" />
      <p className="m-0 max-w-3xl text-muted">{t("continuita.bia.intro")}</p>
      {list.isPending && <Loading />}
      <ContinuityError error={list.error} />
      {list.data && (
        <section className="flex flex-col gap-2" aria-label={t("continuita.bia.title")}>
          <SectionTitle aside={<span className="text-[12px] text-muted tabular">{t("continuita.bia.count", { n: withBia, total: items.length })}</span>}>
            {t("continuita.bia.tree")}
          </SectionTitle>
          {items.length === 0 && <p className="m-0 text-muted">{t("continuita.bia.empty")}</p>}
          <div className="hidden grid-cols-[minmax(0,1fr)_9rem_6rem_6rem_7rem_8rem] gap-x-3 border-b border-hair pb-1 text-[11.5px] text-muted md:grid" aria-hidden="true">
            <span>{t("continuita.bia.colCapability")}</span>
            <span>{t("continuita.bia.colState")}</span>
            <span>{t("continuita.bia.colRto")}</span>
            <span>{t("continuita.bia.colMtpd")}</span>
            <span>{t("continuita.bia.colApps")}</span>
            <span />
          </div>
          <ul className="m-0 flex list-none flex-col p-0">
            {items.map((i) => (
              <BiaRow key={i.capability.id} item={i} can={list.data.can} highlight={capability === i.capability.id || capability === i.capability.key} />
            ))}
          </ul>
        </section>
      )}
    </EaPage>
  );
}

function BiaRow({ item, can, highlight }: { item: BiaListItem; can: Can; highlight: boolean }) {
  const t = useT();
  const stateName = useStateName();
  const nav = useNavigate();
  const qc = useQueryClient();
  const ref = useRef<HTMLLIElement>(null);
  useEffect(() => {
    if (highlight) ref.current?.scrollIntoView({ block: "center" });
  }, [highlight]);
  const start = useMutation({
    mutationFn: () => continuityApi.startBia(item.capability.key || item.capability.id),
    onSuccess: (b) => {
      void qc.invalidateQueries({ queryKey: ["continuity"] });
      nav({ to: "/e/$key", params: { key: b.key } });
    },
  });
  const c = item.capability;
  const b = item.bia;
  return (
    <li
      ref={ref}
      className={`grid grid-cols-1 items-baseline gap-x-3 gap-y-1 border-b border-hair py-2 md:grid-cols-[minmax(0,1fr)_9rem_6rem_6rem_7rem_8rem] ${highlight ? "bg-sel" : ""}`}
    >
      <span className="min-w-0 break-words" style={{ paddingLeft: `${Math.min(c.depth, 6) * 16}px` }}>
        <Link to="/e/$key" params={{ key: c.key }} className={`text-fg no-underline hover:underline ${c.depth === 0 ? "font-semibold" : ""}`}>
          {c.title}
        </Link>
        {c.code && <span className="ml-1.5 font-mono text-[11.5px] text-muted">{c.code}</span>}
        {b?.critical && <span className="ml-2 border border-flag px-1 text-[10.5px] font-semibold text-flag">{t("continuita.bia.critical")}</span>}
      </span>
      <span className="text-[12.5px]" style={{ paddingLeft: `${Math.min(c.depth, 6) * 16}px` }}>
        {b ? (
          <Link to="/e/$key" params={{ key: b.key }} className="no-underline hover:underline">
            <span className="font-mono text-[11.5px] text-muted">{b.key}</span> {stateName(b.state)}
          </Link>
        ) : (
          <span className="text-muted">{t("continuita.bia.none")}</span>
        )}
        {b?.review_due && <span className="block text-[11.5px] text-muted">{t("continuita.bia.reviewDue", { date: date(b.review_due) })}</span>}
      </span>
      <span className="text-[12.5px]">
        <span className="text-muted md:hidden">{t("continuita.bia.colRto")} </span>
        <Minutes m={b?.rto_minutes} />
      </span>
      <span className="text-[12.5px]">
        <span className="text-muted md:hidden">{t("continuita.bia.colMtpd")} </span>
        <Minutes m={b?.mtpd_minutes} />
      </span>
      <span className="text-[12.5px] tabular">
        {t("continuita.bia.apps", { critical: item.critical_applications, total: item.applications })}
      </span>
      <span className="flex flex-col gap-1">
        {!b && can.write && (
          <button type="button" className="btn self-start py-1 text-[12.5px]" disabled={start.isPending} onClick={() => start.mutate()}>
            {t("continuita.bia.start")}
          </button>
        )}
        <ErrorNote error={start.error} />
      </span>
    </li>
  );
}
