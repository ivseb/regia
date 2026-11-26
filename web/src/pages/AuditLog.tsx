// Configurazione → Registro di audit: eventi del tenant con filtri (azione, entità, autore,
// date) e verifica della catena di hash. Serve audit.read (anche tramite *).
import { EntitySelect, Select } from "../select";
import { useState, type FormEvent } from "react";
import { useInfiniteQuery, useMutation } from "@tanstack/react-query";
import { ApiError } from "../api";
import { adminApi, type AuditFilter, type AuditItem } from "../adminApi";
import { PageHeader } from "../shell";
import { ErrorNote, SectionTitle } from "../ui";
import { dateTime } from "./adminUi";
import { useT } from "../i18n/react";
import { tr } from "../i18n/core";
import { Trans } from "../i18n/Trans";

// Etichette italiane delle azioni più comuni; le altre si mostrano con la chiave.
const ACTIONS: Record<string, string> = {
  get "member.invite"() { return tr("configurazione.audit.action.member.invite"); },
  get "member.create"() { return tr("configurazione.audit.action.member.create"); },
  get "member.invitation_accept"() { return tr("configurazione.audit.action.member.invitation_accept"); },
  get "member.invitation_renew"() { return tr("configurazione.audit.action.member.invitation_renew"); },
  get "member.update"() { return tr("configurazione.audit.action.member.update"); },
  get "member.suspend"() { return tr("configurazione.audit.action.member.suspend"); },
  get "member.reactivate"() { return tr("configurazione.audit.action.member.reactivate"); },
  get "member.password_link"() { return tr("configurazione.audit.action.member.password_link"); },
  get "member.mfa_reset"() { return tr("configurazione.audit.action.member.mfa_reset"); },
  get "member.add"() { return tr("configurazione.audit.action.member.add"); },
  get "role.create"() { return tr("configurazione.audit.action.role.create"); },
  get "role.update"() { return tr("configurazione.audit.action.role.update"); },
  get "role.delete"() { return tr("configurazione.audit.action.role.delete"); },
  get "role.assign"() { return tr("configurazione.audit.action.role.assign"); },
  get "role.unassign"() { return tr("configurazione.audit.action.role.unassign"); },
  get "group.create"() { return tr("configurazione.audit.action.group.create"); },
  get "group.update"() { return tr("configurazione.audit.action.group.update"); },
  get "group.delete"() { return tr("configurazione.audit.action.group.delete"); },
  get "group.member_add"() { return tr("configurazione.audit.action.group.member_add"); },
  get "group.member_remove"() { return tr("configurazione.audit.action.group.member_remove"); },
  get "stakeholder_role.create"() { return tr("configurazione.audit.action.stakeholder_role.create"); },
  get "stakeholder_role.rename"() { return tr("configurazione.audit.action.stakeholder_role.rename"); },
  get "tenant.security.update"() { return tr("configurazione.audit.action.tenant.security.update"); },
  get "tenant.create"() { return tr("configurazione.audit.action.tenant.create"); },
  get "webhook.create"() { return tr("configurazione.audit.action.webhook.create"); },
  get "webhook.update"() { return tr("configurazione.audit.action.webhook.update"); },
  get "webhook.delete"() { return tr("configurazione.audit.action.webhook.delete"); },
  get "webhook.secret_rotate"() { return tr("configurazione.audit.action.webhook.secret_rotate"); },
  get "webhook.ping"() { return tr("configurazione.audit.action.webhook.ping"); },
  get "webhook.redeliver"() { return tr("configurazione.audit.action.webhook.redeliver"); },
  get "api_token.create"() { return tr("configurazione.audit.action.api_token.create"); },
  get "api_token.revoke"() { return tr("configurazione.audit.action.api_token.revoke"); },
  get "element.create"() { return tr("configurazione.audit.action.element.create"); },
  get "element.update"() { return tr("configurazione.audit.action.element.update"); },
  get "element.transition"() { return tr("configurazione.audit.action.element.transition"); },
  get "approval.decision"() { return tr("configurazione.audit.action.approval.decision"); },
};

const ACTION_GROUPS: { v: string; label: string }[] = [
  { v: "", get label() { return tr("configurazione.audit.group.all"); } },
  { v: "member", get label() { return tr("configurazione.audit.group.member"); } },
  { v: "role", get label() { return tr("configurazione.audit.group.role"); } },
  { v: "group", get label() { return tr("configurazione.audit.group.group"); } },
  { v: "tenant", get label() { return tr("configurazione.audit.group.tenant"); } },
  { v: "webhook", get label() { return tr("configurazione.audit.group.webhook"); } },
  { v: "api_token", get label() { return tr("configurazione.audit.group.api_token"); } },
  { v: "element", get label() { return tr("configurazione.audit.group.element"); } },
  { v: "approval", get label() { return tr("configurazione.audit.group.approval"); } },
  { v: "process", get label() { return tr("configurazione.audit.group.process"); } },
  { v: "finance", get label() { return tr("configurazione.audit.group.finance"); } },
];

export function AuditLog() {
  const t = useT();
  const [draft, setDraft] = useState<AuditFilter>({});
  const [filter, setFilter] = useState<AuditFilter>({});
  const page = useInfiniteQuery({
    queryKey: ["audit", filter],
    queryFn: ({ pageParam }) => adminApi.audit(filter, pageParam),
    initialPageParam: undefined as number | undefined,
    getNextPageParam: (last) => last.next_before,
  });
  const verify = useMutation({ mutationFn: adminApi.verifyAudit });
  const actors: Record<string, string> = Object.assign({}, ...(page.data?.pages ?? []).map((p) => p.actors ?? {}));
  const items = (page.data?.pages ?? []).flatMap((p) => p.items);

  function submit(e: FormEvent) {
    e.preventDefault();
    setFilter({ ...draft });
  }

  if (page.error instanceof ApiError && page.error.status === 403) {
    return (
      <>
        <PageHeader path={t("configurazione.path.audit")} title={t("configurazione.audit.registroDiAudit")} />
        <p className="px-4 text-muted md:px-7">{t("configurazione.audit.serveIlPermessoLeggere")}</p>
      </>
    );
  }
  const v = verify.data;
  return (
    <>
      <PageHeader path={t("configurazione.path.audit")} title={t("configurazione.audit.registroDiAudit")} />
      <div className="flex max-w-[1100px] flex-col gap-6 px-4 pb-10 md:px-7">
        <section className="flex flex-col gap-2">
          <SectionTitle>{t("configurazione.audit.integrita")}</SectionTitle>
          <p className="m-0 max-w-[680px] text-muted">
            {t("configurazione.audit.ogniEventoContieneL")}
          </p>
          <div className="flex flex-wrap items-center gap-3">
            <button className="btn" disabled={verify.isPending} onClick={() => verify.mutate()}>
              {verify.isPending ? t("configurazione.audit.verificaInCorso") : t("configurazione.audit.verificaLaCatena")}
            </button>
            {v && (
              <span role="status" className={v.intact ? "font-semibold text-ok" : "font-semibold text-danger"}>
                {v.intact ? t("configurazione.audit.chainOk", { count: v.entries }) : t("configurazione.audit.chainBroken", { seq: v.broken_seq ?? "" })}
              </span>
            )}
          </div>
          {v?.last_hash && (
            <span className="break-all font-mono text-[11.5px] text-muted">
              {t("configurazione.audit.lastEvent", { seq: v.last_seq ?? "", hash: v.last_hash })}
            </span>
          )}
          <ErrorNote error={verify.error} />
        </section>

        <form onSubmit={submit} className="grid grid-cols-1 gap-3 border-b border-hair pb-4 sm:grid-cols-2 lg:grid-cols-[1.2fr_1.2fr_1fr_1fr_auto] lg:items-end">
          <label className="flex flex-col gap-1.5">
            <span className="label">{t("configurazione.audit.azione")}</span>
            <Select className="field" value={draft.action ?? ""} onChange={(e) => setDraft({ ...draft, action: e.target.value || undefined })}>
              {ACTION_GROUPS.map((a) => (
                <option key={a.v} value={a.v}>{a.label}</option>
              ))}
            </Select>
          </label>
          <label className="flex flex-col gap-1.5">
            <span className="label">{t("configurazione.audit.autore")}</span>
            <EntitySelect kind="member" noneLabel={t("configurazione.audit.chiunque")} className="field" value={draft.actor ?? ""} onChange={(v) => setDraft({ ...draft, actor: v || undefined })} />
          </label>
          <label className="flex flex-col gap-1.5">
            <span className="label">{t("configurazione.common.dal")}</span>
            <input className="field" type="date" value={draft.from ?? ""} onChange={(e) => setDraft({ ...draft, from: e.target.value || undefined })} />
          </label>
          <label className="flex flex-col gap-1.5">
            <span className="label">Al</span>
            <input className="field" type="date" value={draft.to ?? ""} onChange={(e) => setDraft({ ...draft, to: e.target.value || undefined })} />
          </label>
          <div className="flex gap-2">
            <button className="btn btn-primary">{t("configurazione.audit.filtra")}</button>
            <button
              type="button"
              className="btn"
              onClick={() => {
                setDraft({});
                setFilter({});
              }}
            >
              {t("configurazione.common.azzera")}
            </button>
          </div>
          {filter.entity_id && (
            <p className="m-0 text-[12.5px] sm:col-span-2 lg:col-span-5">
              <Trans k="configurazione.audit.onlyEntity" slots={{ id: <span className="font-mono">{filter.entity_id}</span> }} />{" "}
              <button type="button" className="text-muted underline" onClick={() => { const f = { ...filter, entity_id: undefined, entity_type: undefined }; setFilter(f); setDraft(f); }}>
                {t("configurazione.common.togli2")}
              </button>
            </p>
          )}
        </form>

        <ErrorNote error={page.error} />
        <ol className="m-0 flex list-none flex-col p-0">
          {items.map((it) => (
            <AuditRow
              key={it.seq}
              it={it}
              actor={it.actor_member_id ? actors[it.actor_member_id] : undefined}
              onEntity={() => {
                const f = { ...filter, entity_type: it.entity_type, entity_id: it.entity_id ?? undefined };
                setFilter(f);
                setDraft(f);
              }}
            />
          ))}
          {!page.isPending && items.length === 0 && <li className="py-8 text-muted">{t("configurazione.audit.nessunEventoConQuesti")}</li>}
        </ol>
        {page.hasNextPage && (
          <div>
            <button className="btn" disabled={page.isFetchingNextPage} onClick={() => page.fetchNextPage()}>
              {page.isFetchingNextPage ? t("configurazione.audit.caricamento") : t("configurazione.audit.eventiPrecedenti")}
            </button>
          </div>
        )}
      </div>
    </>
  );
}

function AuditRow({ it, actor, onEntity }: { it: AuditItem; actor?: string; onEntity: () => void }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const payload = Object.keys(it.payload ?? {}).length > 0;
  return (
    <li className="grid grid-cols-1 gap-x-4 gap-y-0.5 border-b border-hair py-2.5 sm:grid-cols-[150px_minmax(0,1fr)_minmax(0,220px)]">
      <span className="font-mono text-[12px] text-muted tabular">
        <span className="text-fg">#{it.seq}</span> {dateTime(it.at)}
      </span>
      <span className="min-w-0">
        <span className="font-medium">{ACTIONS[it.action] ?? it.action}</span>{" "}
        <span className="font-mono text-[11.5px] text-muted">{it.action}</span>
        {it.entity_id && (
          <button type="button" className="block max-w-full min-w-0 [overflow-wrap:anywhere] text-left font-mono text-[11.5px] text-muted hover:text-accent" title={t("configurazione.audit.mostraSoloQuestaEntita")} onClick={onEntity}>
            {it.entity_type} {it.entity_id}
          </button>
        )}
        {payload && (
          <>
            <button type="button" className="text-[12px] text-muted underline" aria-expanded={open} onClick={() => setOpen((o) => !o)}>
              {open ? t("configurazione.audit.nascondiIDettagli") : t("configurazione.audit.details")}
            </button>
            {open && <pre className="m-0 mt-1 overflow-x-auto border-l-2 border-hair pl-3 font-mono text-[11.5px] whitespace-pre-wrap break-all">{JSON.stringify(it.payload, null, 2)}</pre>}
          </>
        )}
      </span>
      <span className="text-[12.5px] sm:text-right">{actor ?? (it.actor_member_id ? t("configurazione.audit.membroNonPiuPresente") : t("configurazione.audit.system"))}</span>
    </li>
  );
}
