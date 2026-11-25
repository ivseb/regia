// Mappa d'insieme dei processi: per ogni tipo di elemento con un processo pubblicato, le fasi in fila e,
// agganciati alle fasi, team, valutazioni (con la condizione in parole), approvazioni, controlli e azioni,
// più i collegamenti verso altri tipi. Sola lettura per tutti; per chi configura ogni elemento porta alla
// sua pagina di configurazione. Da telefono le fasi diventano un elenco verticale. Dati: mappaApi.ts.
import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { mappaApi, type MapItem, type MapPhase, type MapProcess } from "../mappaApi";
import { useMe, PageHeader } from "../shell";
import { ErrorNote, Loading } from "../ui";
import { describeCondition } from "./processEditor/model";
import { useT } from "../i18n/react";
import type { Key } from "../i18n/core";

type T = ReturnType<typeof useT>;

export function MappaProcessi() {
  const t = useT();
  const me = useMe();
  const perms = me.data?.active_tenant?.permissions ?? [];
  const canConfig = perms.includes("*") || perms.includes("process.admin");
  const admin = perms.includes("*") || perms.includes("tenant.admin");
  const q = useQuery({ queryKey: ["mappa-processi"], queryFn: mappaApi.get });
  return (
    <>
      <PageHeader
        path={t("mappa.path")}
        title={t("mappa.title")}
        actions={
          <>
            {admin && (
              <Link to="/configurazione/percorso" className="btn no-underline">
                {t("mappa.percorso")}
              </Link>
            )}
            {canConfig && (
              <Link to="/processi" className="btn no-underline">
                {t("mappa.configura")}
              </Link>
            )}
          </>
        }
      />
      <div className="flex flex-col gap-8 px-4 pb-12 md:px-7">
        <div className="flex flex-col gap-1">
          <p className="m-0 max-w-3xl text-muted">{t("mappa.intro")}</p>
          {canConfig && <p className="m-0 text-[12.5px] text-muted">{t("mappa.legenda")}</p>}
        </div>
        {q.isPending && <Loading />}
        {!q.isPending && !q.data && <ErrorNote error={q.error} />}
        {q.data && q.data.processes.length === 0 && <p className="m-0 text-muted" role="status" data-testid="mappa-vuota">{t("mappa.vuota")}</p>}
        {q.data?.processes.map((p) => <ProcessMapView key={p.key} p={p} canConfig={canConfig} />)}
        {q.data && q.data.unpublished.length > 0 && (
          <section className="flex flex-col gap-1" data-testid="mappa-non-pubblicati">
            <h2 className="m-0 text-[16px] font-bold">{t("mappa.nonPubblicati")}</h2>
            <p className="m-0 text-[13px] text-muted">{t("mappa.nonPubblicatiTesto")}</p>
            <ul className="m-0 flex list-none flex-col p-0">
              {q.data.unpublished.map((u) => (
                <li key={u.key} className="border-b border-hair py-2 text-[13.5px]">
                  {canConfig ? (
                    <Link to="/processi/$key" params={{ key: u.key }} className="touch-44 inline-flex items-center text-accent underline">
                      {u.name}
                    </Link>
                  ) : (
                    u.name
                  )}
                  <span className="text-muted"> · {u.type_name}</span>
                </li>
              ))}
            </ul>
          </section>
        )}
      </div>
    </>
  );
}

function ProcessMapView({ p, canConfig }: { p: MapProcess; canConfig: boolean }) {
  const t = useT();
  const main = p.phases.filter((f) => !f.off_path);
  const off = p.phases.filter((f) => f.off_path);
  const phaseLabel = (name: string) => name;
  const links = p.links.map((l, i) => {
    const text =
      l.kind === "epic"
        ? t("mappa.link.epic", { type: p.type_name, phase: phaseLabel(l.phase) })
        : t(l.scope === "impact" ? "mappa.link.review.impact" : "mappa.link.review.request", { type: p.type_name, phase: phaseLabel(l.phase) });
    return <li key={i}>{text}</li>;
  });
  return (
    <section className="flex flex-col gap-3" data-testid={`mappa-processo-${p.key}`} aria-labelledby={`mappa-${p.key}`}>
      <div className="flex flex-wrap items-baseline gap-x-3 border-b-2 border-rule pb-1.5">
        <h2 id={`mappa-${p.key}`} className="m-0 text-[20px] font-bold">{p.type_name}</h2>
        {canConfig ? (
          <Link to="/processi/$key" params={{ key: p.key }} className="touch-44 inline-flex items-center text-[14px] text-accent underline" aria-label={t("mappa.apri", { name: p.name })}>
            {p.name}
          </Link>
        ) : (
          <span className="text-[14px]">{p.name}</span>
        )}
        <span className="font-mono text-[11.5px] text-muted">{t("mappa.versione", { n: p.version })}</span>
      </div>
      {p.applies_when && <p className="m-0 text-[13px] text-muted">{t("mappa.siApplica", { cond: describeCondition(p.applies_when) })}</p>}
      <ol className="m-0 flex list-none flex-col gap-0 p-0 md:flex-row md:gap-0 md:overflow-x-auto md:pb-2" data-testid={`mappa-fasi-${p.key}`}>
        {main.map((f, i) => (
          <PhaseView key={f.name} f={f} n={i + 1} p={p} canConfig={canConfig} arrow={i < main.length - 1 || off.length > 0} />
        ))}
        {off.map((f, i) => (
          <PhaseView key={f.name} f={f} n={main.length + i + 1} p={p} canConfig={canConfig} arrow={i < off.length - 1} />
        ))}
      </ol>
      <div className="flex flex-col gap-1 text-[13px]">
        <div className="flex flex-wrap gap-x-2">
          <span className="font-semibold">{t("mappa.team")}:</span>
          <span data-testid={`mappa-team-${p.key}`}>{p.teams.length ? p.teams.join(", ") : t("mappa.nessunTeam")}</span>
        </div>
        <div className="flex flex-col gap-0.5">
          <span className="font-semibold">{t("mappa.collegamenti")}</span>
          {links.length ? <ul className="m-0 list-none p-0 text-muted" data-testid={`mappa-collegamenti-${p.key}`}>{links}</ul> : <span className="text-muted">{t("mappa.nessunCollegamento")}</span>}
        </div>
      </div>
    </section>
  );
}

function PhaseView({ f, n, p, canConfig, arrow }: { f: MapPhase; n: number; p: MapProcess; canConfig: boolean; arrow: boolean }) {
  const t = useT();
  return (
    <li className="flex md:w-60 md:shrink-0" data-testid={`mappa-fase-${p.key}-${n}`}>
      <div className={`flex min-w-0 flex-1 flex-col gap-2 border-l-2 py-1 pl-3 md:border-l-0 md:border-t-2 md:pl-0 md:pr-3 md:pt-2 ${f.off_path ? "border-dashed border-hair" : "border-rule"} pb-4 md:pb-1`}>
        <div className="flex flex-wrap items-baseline gap-x-2">
          <span className="font-mono text-[11.5px] text-muted">{n}</span>
          <span className="font-semibold">{f.name}</span>
          {f.initial && <span className="font-mono text-[11.5px] text-muted">{t("mappa.iniziale")}</span>}
          {f.off_path && <span className="font-mono text-[11.5px] text-muted">{t("mappa.fuoriPercorso")}</span>}
        </div>
        {f.items.map((it, i) => <ItemView key={`${it.kind}-${it.key}-${i}`} it={it} p={p} canConfig={canConfig} />)}
      </div>
      {arrow && <span aria-hidden="true" className="hidden pt-1 text-muted md:block">→</span>}
    </li>
  );
}

function actionText(it: MapItem, t: T): string {
  if (it.key === "create_epic") return t("mappa.azione.create_epic");
  if (it.key === "assign_role") return t("mappa.azione.assign_role", { role: it.key, team: it.team ?? "" });
  return t("mappa.azione.altra", { kind: it.key });
}

function ItemView({ it, p, canConfig }: { it: MapItem; p: MapProcess; canConfig: boolean }) {
  const t = useT();
  const title = it.kind === "action" ? actionText(it, t) : it.name;
  const body = (
    <>
      <span className="flex flex-wrap items-baseline gap-x-2">
        <span className="font-mono text-[11px] uppercase tracking-wide text-muted">{t(`mappa.kind.${it.kind}` as Key)}</span>
        <span className="font-semibold">{title}</span>
      </span>
      <span className="text-[12px] text-muted">
        {t(`mappa.on.${it.on}` as Key)}
        {it.kind === "review" && it.scope && ` · ${t(`mappa.scope.${it.scope}` as Key)}`}
        {it.kind === "review" && ` · ${it.mandatory ? t("mappa.obbligatoria") : t("mappa.facoltativa")}`}
      </span>
      {it.when && <span className="text-[12px]">{t("mappa.quando", { cond: describeCondition(it.when) })}</span>}
      {it.kind === "review" && it.team && <span className="text-[12px]">{it.team}</span>}
      {it.kind === "approval" && it.steps?.map((who, i) => <span key={i} className="text-[12px]">{t("mappa.passo", { n: i + 1, who: who.join(", ") })}</span>)}
      {it.missing && <span className="text-[12px] font-semibold text-danger">{it.kind === "approval" ? t("mappa.approvaSenza") : t("mappa.mancante")}</span>}
    </>
  );
  const cls = "flex flex-col gap-0.5 border border-hair px-2 py-1.5 text-[13px] text-fg no-underline";
  const id = `mappa-voce-${it.kind}-${it.key}`;
  if (!canConfig) return <div className={cls} data-testid={id}>{body}</div>;
  if (it.kind === "review") {
    return (
      <Link to="/configurazione/valutazioni" search={{ tab: "tipi" } as never} className={`${cls} hover:border-accent`} data-testid={id} aria-label={t("mappa.apri", { name: title })}>
        {body}
      </Link>
    );
  }
  return (
    <Link to="/processi/$key" params={{ key: p.key }} search={(it.rule ? { regola: it.rule } : {}) as never} className={`${cls} hover:border-accent`} data-testid={id} aria-label={t("mappa.apri", { name: title })}>
      {body}
    </Link>
  );
}
