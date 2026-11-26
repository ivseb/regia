import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { useStateName } from "../stateLabels";
import { Link, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { TagChips } from "../tags";
import { withLabel } from "../query";
import { api, collab, type Element, type ElementDetail } from "../api";
import { useMe } from "../shell";
import { pathVisible } from "../modules";
import { BulkBar, SelectBox, useSelection } from "./BulkBar";
import { workApi } from "../workApi";
import { requestsRoute } from "../router";
import { FollowButton } from "./Collaboration";
import { CustomFields } from "../fields";
import { ElementList } from "../views/ElementList";
import { LISTS } from "../views/lists";
import { useTypeNames } from "../typesApi";
import { Transitions, useRefreshElement } from "./ElementSections";
import { EmptyState } from "../emptyState";
import { FaseChip, Persona } from "../faseChip";
import { DRIVERS, ElementTitle, ErrorNote, SectionTitle, StageGate, ago, date, euroRange, Loading } from "../ui";
import { useT } from "../i18n/react";
import { Trans } from "../i18n/Trans";

/** Le richieste: barra della query, filtri visuali, viste salvate, lista o tabella con
 *  raggruppamento (views/ElementList.tsx, piano passo 16) e anteprima a destra. */
export function Requests() {
  const t = useT();
  const stateName = useStateName();
  const search = requestsRoute.useSearch();
  const navigate = useNavigate({ from: requestsRoute.fullPath });
  const type = useQuery({ queryKey: ["type", "business_request"], queryFn: () => api.type("business_request"), staleTime: 300_000 });
  const tn = useTypeNames();
  const sel = search.sel;
  const qc = useQueryClient();
  const me = useMe();
  const meId = me.data?.active_tenant?.member_id ?? "";
  const canWrite = !!me.data?.active_tenant?.permissions.some((p) => p === "*" || p === "element.write");
  // Portale dei richiedenti: per vedere ciò che vedono loro (stesse regole della voce che stava nel menu).
  const canPortal = !!me.data?.active_tenant?.permissions.some((p) => p === "*" || p === "element.write" || p === "portal.submit") && pathVisible(me.data, "/portale");
  const members = useQuery({ queryKey: ["members"], queryFn: api.members, staleTime: 300_000 });
  const names = useMemo(() => new Map((members.data?.items ?? []).map((m) => [m.id, m.display_name])), [members.data]);
  // Selezione multipla (ondata 2): le richieste scelte si prendono in carico, si assegnano, cambiano fase insieme.
  const [listed, setListed] = useState<string[]>([]);
  const selection = useSelection(listed);
  const [taken, setTaken] = useState("");
  const take = useMutation({
    mutationFn: (el: Element) => workApi.assign(el.key, meId),
    onSuccess: (_r, el) => {
      setTaken(t("dafare.req.taken", { key: el.key }));
      qc.invalidateQueries({ queryKey: ["elements"] });
      qc.invalidateQueries({ queryKey: ["element"] });
    },
  });

  // Nella tabella il titolo apre l'anteprima accanto, come nella lista.
  const renderTitle = useCallback(
    (el: Element) => (
      <Link to="/richieste" search={{ ...search, sel: el.key }} className="font-medium text-fg no-underline hover:underline">
        <ElementTitle type={el.type} title={el.title} />
      </Link>
    ),
    [search],
  );

  return (
    <>
    <ElementList
      config={LISTS.richieste}
      search={search}
      onSearch={(s) => navigate({ search: { ...s, sel: s.sel ?? (s.v === search.v ? sel : undefined) } })}
      selection={canWrite ? { ids: selection.sel, onChange: (ids) => selection.set([...ids]) } : undefined}
      actions={
        // Le azioni stanno qui e in ⌘K, non nel menu (menu snello, nav.ts).
        <>
          {canPortal && (
            <Link to="/portale" className="btn no-underline">
              {t("shell.nav.portal", { type: tn.plural("business_request", t("shell.nav.requests")).toLowerCase() })}
            </Link>
          )}
          {canWrite && (
            <Link to="/richieste/nuova" className="btn btn-primary no-underline">
              {tn.create("business_request", t("lavoro.requests.typeName"))}
            </Link>
          )}
        </>
      }
      renderTitle={renderTitle}
      renderList={({ items, sel: cur, empty, query, onQuery }) => (
        <>
        <ListOrder items={items} onOrder={setListed} />
        {canWrite && selection.size === 0 && items.length > 1 && <p className="m-0 px-1 pb-2 text-[12.5px] text-muted">{t("dafare.req.selectHint")}</p>}
        {taken && <p role="status" className="m-0 px-1 pb-2 text-[12.5px] font-semibold text-ok">{taken}</p>}
        <table className="w-full min-w-[640px] border-collapse tabular">
          <thead>
            <tr className="label h-9 text-left">
              {canWrite && (
                <th scope="col" className="w-8 border-b-2 border-rule pr-2">
                  <input
                    type="checkbox"
                    className="size-4 cursor-pointer accent-[var(--accent)]"
                    aria-label={t("dafare.req.selectAll")}
                    checked={items.length > 0 && items.every((el) => selection.sel.has(el.id))}
                    onChange={(e) => selection.set(e.target.checked ? items.map((el) => el.id) : [])}
                  />
                </th>
              )}
              <th scope="col" className="border-b-2 border-rule pr-3">ID</th>
              <th scope="col" className="border-b-2 border-rule px-3">{tn.name("business_request", t("lavoro.requests.typeName"))}</th>
              <th scope="col" className="border-b-2 border-rule px-3">Stage-gate{/* i18n-ok: termine uguale in inglese */}</th>
              <th scope="col" className="border-b-2 border-rule px-3">{t("dafare.req.colOwner")}</th>
              <th scope="col" className="border-b-2 border-rule pr-6 pl-3 text-right">{t("lavoro.requests.colUpdated")}</th>
            </tr>
          </thead>
          <tbody>
            {items.map((el) => {
              const active = cur === el.key;
              return (
                <tr key={el.id} className={`border-b border-hair ${active || selection.sel.has(el.id) ? "bg-sel" : "hover:bg-sel/50"}`}>
                  {canWrite && (
                    <td className="py-2.5 pr-2 align-top">
                      <SelectBox id={el.id} checked={selection.sel.has(el.id)} label={t("dafare.req.select", { key: el.key })} onToggle={selection.toggle} />
                    </td>
                  )}
                  <td className="py-2.5 pr-3 align-top font-mono text-[12px] font-medium whitespace-nowrap">
                    <Link to="/e/$key" params={{ key: el.key }} className="text-accent no-underline hover:underline" title={t("lavoro.requests.openTitle")}>
                      {el.key}
                    </Link>
                  </td>
                  <td className="px-3 py-2.5">
                    <Link
                      to="/richieste"
                      search={{ ...search, sel: el.key }}
                      className="font-medium text-fg no-underline hover:underline"
                      aria-current={active ? "true" : undefined}
                    >
                      <ElementTitle type={el.type} title={el.title} />
                    </Link>
                    {el.tags && el.tags.length > 0 && <TagChips tags={el.tags} onPick={(n) => onQuery(withLabel(query, n))} className="mt-1 block" />}
                  </td>
                  <td className="px-3 py-2 whitespace-nowrap">
                    {type.data ? <FaseChip states={type.data.states} current={el.state} /> : stateName(el.state)}
                  </td>
                  <td className="px-3 py-2 text-[12.5px] whitespace-nowrap">
                    {el.assignee_id ? (
                      <Persona name={names.get(el.assignee_id) ?? "—"} />
                    ) : canWrite && meId ? (
                      <button className="btn btn-quiet py-0.5 text-[12.5px]" disabled={take.isPending} onClick={() => take.mutate(el)}>
                        {t("dafare.req.take")}
                      </button>
                    ) : (
                      <span className="text-muted">{t("dafare.req.noOwner")}</span>
                    )}
                  </td>
                  <td className="py-2.5 pr-6 pl-3 text-right text-[12px] whitespace-nowrap text-muted">{ago(el.updated_at)}</td>
                </tr>
              );
            })}
            {empty && (
              <tr>
                <td colSpan={canWrite ? 6 : 5} className="py-6">
                  {search.q === undefined && search.g === undefined ? (
                    <EmptyState
                      title={t("lavoro.requests.emptyTitle", { name: tn.plural("business_request", t("lavoro.requests.typePlural")).toLowerCase() })}
                      text={t("lavoro.requests.emptyText")}
                      action={<Link to="/richieste/nuova" className="btn btn-primary no-underline">{tn.create("business_request", t("lavoro.requests.typeName"))}</Link>}
                    />
                  ) : (
                    <span className="text-muted">{t("lavoro.requests.noResults")}</span>
                  )}
                </td>
              </tr>
            )}
          </tbody>
        </table>
        </>
      )}
      extraTabs={
        <>
          <button role="tab" aria-selected="false" aria-disabled="true" className="pb-2.5 text-muted" title={t("lavoro.requests.soon")}>Board{/* i18n-ok: nome della sezione, uguale in inglese */}</button>
          <Link to="/roadmap" role="tab" aria-selected="false" className="pb-2.5 text-muted no-underline hover:text-fg">Roadmap{/* i18n-ok: nome della sezione, uguale in inglese */}</Link>
        </>
      }
      detail={sel ? <Detail refKey={sel} /> : undefined}
    />
      {canWrite && <BulkBar ids={[...selection.sel]} space="DEM" requests onClear={selection.clear} />}
    </>
  );
}

/** Comunica alla pagina l'ordine delle richieste mostrate (serve alla selezione con Maiusc). */
function ListOrder({ items, onOrder }: { items: Element[]; onOrder: (ids: string[]) => void }) {
  const key = items.map((i) => i.id).join(",");
  useEffect(() => onOrder(key ? key.split(",") : []), [key, onOrder]);
  return null;
}

/** Anteprima veloce dell'elemento selezionato nella lista: titolo, fase, passaggi, pochi
 *  campi chiave e gli ultimi commenti. Il dettaglio completo è nella pagina /e/CHIAVE. */
function Detail({ refKey }: { refKey: string }) {
  const t = useT();
  const stateName = useStateName();
  const qc = useQueryClient();
  const me = useMe();
  const meId = me.data?.active_tenant?.member_id ?? "";
  const canWrite = !!me.data?.active_tenant?.permissions.some((p) => p === "*" || p === "element.write");
  const members = useQuery({ queryKey: ["members"], queryFn: api.members, staleTime: 300_000 });
  const assign = useMutation({
    mutationFn: (memberId: string) => workApi.assign(refKey, memberId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["element", refKey] });
      qc.invalidateQueries({ queryKey: ["elements"] });
    },
  });
  const detail = useQuery({ queryKey: ["element", refKey], queryFn: () => api.element(refKey) });
  const type = useQuery({ queryKey: ["type", "business_request"], queryFn: () => api.type("business_request"), staleTime: 300_000 });
  const refresh = useRefreshElement(refKey);
  const d = detail.data;
  const key = d ? d.element.key || d.element.id : refKey;
  return (
    <aside aria-label={t("lavoro.requests.preview", { key: refKey })} className="flex flex-[1_1_380px] flex-col gap-4 border-l border-rule px-4 py-5 md:px-6">
      {detail.isPending && <Loading className="text-muted" />}
      <ErrorNote error={detail.error} />
      {d && (
        <>
          <div className="flex flex-col gap-2">
            <div className="flex items-center justify-between gap-3">
              <Link to="/e/$key" params={{ key }} className="font-mono text-[12px] font-medium text-accent no-underline hover:underline">
                {d.element.key}
              </Link>
              <span className="flex items-center gap-2">
                <FollowButton refKey={key} />
                <Link to="/e/$key" params={{ key }} className="btn py-1 text-[12.5px] no-underline">
                  {t("lavoro.requests.open")}
                </Link>
              </span>
            </div>
            <h2 className="m-0 text-[20px] leading-[1.15] font-bold tracking-[-0.015em]">
              <Link to="/e/$key" params={{ key }} className="text-fg no-underline hover:underline">
                <ElementTitle type={d.element.type} title={d.element.title} />
              </Link>
            </h2>
            {d.demand?.is_mandatory && (
              <span className="self-start border border-flag px-1.5 py-px text-[12px] font-semibold text-flag">
                {d.demand.regulatory_deadline ? t("lavoro.requests.mandatoryDeadline", { date: date(d.demand.regulatory_deadline) }) : t("lavoro.requests.mandatory")}
              </span>
            )}
          </div>
          {(d.phases.length > 0 || type.data) && (
            <div className="flex flex-col gap-1.5">
              <StageGate states={d.phases.length > 0 ? d.phases : type.data!.states} current={d.element.state} size="lg" />
              <p className="m-0 text-[12.5px] text-muted">
                <Trans k="lavoro.requests.currentStage" slots={{ stage: <span className="font-semibold text-fg">{stateName(d.element.state, d.phases.find((p) => p.name === d.element.state)?.labels)}</span> }} />
                {d.process && <> · {d.process.name} <span className="font-mono tabular">v{d.process.version}</span></>}
              </p>
            </div>
          )}
          <Transitions detail={d} onDone={refresh} />
          <p className="m-0 flex flex-wrap items-center gap-x-3 gap-y-1 text-[12.5px]">
            <span className="text-muted">{t("dafare.req.owner")}</span>
            {d.element.assignee_id ? (
              <>
                <span className="font-medium">{members.data?.items.find((m) => m.id === d.element.assignee_id)?.display_name ?? "—"}</span>
                {canWrite && d.element.assignee_id === meId && (
                  <button className="btn btn-quiet py-0.5 text-[12.5px]" disabled={assign.isPending} onClick={() => assign.mutate("")}>{t("dafare.req.release")}</button>
                )}
              </>
            ) : (
              <>
                <span className="text-muted">{t("dafare.req.noOwner")}</span>
                {canWrite && meId && (
                  <button className="btn py-0.5 text-[12.5px]" disabled={assign.isPending} onClick={() => assign.mutate(meId)}>{t("dafare.req.take")}</button>
                )}
              </>
            )}
          </p>
          <ErrorNote error={assign.error} />
          <KeyFacts detail={d} />
          <LastComments refKey={key} />
          <Link to="/e/$key" params={{ key }} className="self-start text-[12.5px] text-accent underline">
            {t("lavoro.requests.openFull")}
          </Link>
        </>
      )}
    </aside>
  );
}

/** Pochi dati chiave: quelli della domanda e i primi campi personalizzati valorizzati. */
function KeyFacts({ detail }: { detail: ElementDetail }) {
  const t = useT();
  const tn = useTypeNames();
  const dm = detail.demand;
  const rows: [string, ReactNode][] = [];
  if (dm) {
    rows.push([t("lavoro.requests.driver"), dm.driver ? DRIVERS[dm.driver] ?? dm.driver : "—"]);
    rows.push([t("lavoro.requests.estimate"), euroRange(dm.estimate_low_cents, dm.estimate_high_cents)]);
  }
  if (detail.impact) {
    rows.push([tn.name("business_request", t("lavoro.requests.typeName")), detail.impact.request_key]);
    rows.push([t("lavoro.requests.application"), detail.impact.application]);
  }
  return (
    <section className="flex flex-col gap-2" aria-label={t("lavoro.requests.keyData")}>
      {rows.length > 0 && (
        <dl className="m-0 grid grid-cols-[minmax(84px,38%)_minmax(0,1fr)]">
          {rows.map(([k, v]) => (
            <div key={k} className="contents">
              <dt className="border-b border-hair py-1.5 text-muted">{k}</dt>
              <dd className="m-0 border-b border-hair py-1.5 font-medium tabular">{v}</dd>
            </div>
          ))}
        </dl>
      )}
      <CustomFields detail={detail} only={4} emptyNote={false} />
    </section>
  );
}

/** Gli ultimi tre commenti, in sola lettura. */
function LastComments({ refKey }: { refKey: string }) {
  const t = useT();
  const list = useQuery({ queryKey: ["comments", refKey], queryFn: () => collab.comments(refKey) });
  const items = (list.data?.items ?? []).slice(-3);
  if (!list.data) return null;
  return (
    <section className="flex flex-col gap-2" aria-label={t("lavoro.requests.lastComments")}>
      <SectionTitle aside={<span className="font-mono text-[11.5px] text-muted tabular">{list.data.items.length || ""}</span>}>{t("lavoro.requests.lastComments")}</SectionTitle>
      {items.length === 0 && <p className="m-0 text-[12.5px] text-muted">{t("lavoro.requests.noComments")}</p>}
      <ol className="m-0 flex list-none flex-col gap-2 p-0">
        {items.map((c) => (
          <li key={c.id} className="flex flex-col gap-0.5 border-b border-hair pb-2 text-[12.5px]">
            <span>
              <span className="font-semibold">{c.author_name}</span> <span className="text-muted">{ago(c.created_at)}</span>
            </span>
            <span className="break-words text-fg">{c.body}</span>
          </li>
        ))}
      </ol>
      <Link to="/e/$key" params={{ key: refKey }} hash="commenti" className="self-start text-[12.5px] text-accent underline">
        {t("lavoro.requests.commentFull")}
      </Link>
    </section>
  );
}
