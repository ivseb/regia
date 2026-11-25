// Configurazione → Persone e accessi (piano, passo 15): membri, inviti, sospensioni,
// reimpostazione di password e MFA, ruoli personalizzati, gruppi, ruoli sugli oggetti e
// politica di sicurezza. Serve tenant.admin. Le regole stanno nel backend
// (internal/platform/admin): qui si mostrano le conseguenze prima di agire.
import { EntitySelect, Select } from "../select";
import { useEffect, useMemo, useRef, useState, type FormEvent, type ReactNode } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError } from "../api";
import {
  absoluteLink,
  adminApi,
  type AdminGroup,
  type AdminMember,
  type AdminRole,
  type OneTimeLink,
  type Permission,
  type Security,
} from "../adminApi";
import { PageHeader } from "../shell";
import { ErrorNote, SectionTitle, ago } from "../ui";
import { ConfirmButton, CopyBox, KV, MutError, SidePanel, Tag, dateTime } from "./adminUi";
import { AccessExplainSection, GroupInheritanceSection } from "./AdminAccess";
import { useT } from "../i18n/react";
import { Trans } from "../i18n/Trans";
import { tr } from "../i18n/core";

export type AdminTab = "membri" | "ruoli" | "gruppi" | "oggetti" | "sicurezza";
export const ADMIN_TABS: AdminTab[] = ["membri", "ruoli", "gruppi", "oggetti", "sicurezza"];
const TAB_LABELS: Record<AdminTab, string> = {
  get membri() { return tr("configurazione.admin.tab.membri"); },
  get ruoli() { return tr("configurazione.admin.tab.ruoli"); },
  get gruppi() { return tr("configurazione.admin.tab.gruppi"); },
  get oggetti() { return tr("configurazione.admin.tab.oggetti"); },
  get sicurezza() { return tr("configurazione.admin.tab.sicurezza"); },
};

const TYPE_LABELS: Record<string, string> = {
  get standard() { return tr("configurazione.admin.memberType.standard"); },
  get requester() { return tr("configurazione.admin.memberType.requester"); },
  get guest() { return tr("configurazione.admin.memberType.guest"); },
  get auditor() { return tr("configurazione.admin.memberType.auditor"); },
  get service() { return tr("configurazione.admin.memberType.service"); },
};
const STATUS_LABELS: Record<string, string> = {
  get invited() { return tr("configurazione.admin.status.invited"); },
  get active() { return tr("configurazione.admin.status.active"); },
  get suspended() { return tr("configurazione.admin.status.suspended"); },
};

function invalidateAll(qc: ReturnType<typeof useQueryClient>) {
  for (const k of ["admin-members", "admin-member", "admin-roles", "admin-groups", "admin-stakeholder", "admin-security"]) {
    qc.invalidateQueries({ queryKey: [k] });
  }
}

export function AdminPage({ tab, selected }: { tab: AdminTab; selected?: string }) {
  const t = useT();
  const navigate = useNavigate();
  const go = (s: { tab?: AdminTab; sel?: string }) => navigate({ to: "/configurazione/persone", search: { tab: s.tab ?? tab, sel: s.sel } });
  const probe = useQuery({ queryKey: ["admin-security"], queryFn: adminApi.security });
  if (probe.error instanceof ApiError && probe.error.status === 403) {
    return (
      <>
        <PageHeader path={t("configurazione.path.people")} title={t("configurazione.admin.personeEAccessi")} />
        <p className="px-4 text-muted md:px-7">{t("configurazione.common.serveIlPermessoAmministrare")}</p>
      </>
    );
  }
  return (
    <>
      <PageHeader path={t("configurazione.path.people")} title={t("configurazione.admin.personeEAccessi")} />
      <div role="tablist" aria-label={t("configurazione.common.sezioni")} className="flex gap-6 overflow-x-auto border-b border-hair px-4 md:px-7">
        {ADMIN_TABS.map((k) => (
          <Link
            key={k}
            to="/configurazione/persone"
            search={{ tab: k }}
            role="tab"
            aria-selected={tab === k}
            className={`shrink-0 pb-2.5 no-underline ${tab === k ? "border-b-2 border-fg font-semibold text-fg" : "text-muted hover:text-fg"}`}
          >
            {TAB_LABELS[k]}
          </Link>
        ))}
      </div>
      {tab === "membri" && <MembersTab selected={selected} onSelect={(sel) => go({ sel })} />}
      {tab === "ruoli" && <RolesTab selected={selected} onSelect={(sel) => go({ sel })} />}
      {tab === "gruppi" && <GroupsTab selected={selected} onSelect={(sel) => go({ sel })} />}
      {tab === "oggetti" && <StakeholderTab />}
      {tab === "sicurezza" && <SecurityTab />}
    </>
  );
}

/** Porta in vista il pannello di dettaglio sul telefono. */
function useScrollOnMobile(dep: string | undefined) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!dep || window.innerWidth >= 768) return;
    // Dopo il ripristino dello scroll del router, che riporta la pagina in cima.
    const t = setTimeout(() => ref.current?.scrollIntoView({ block: "start" }), 80);
    return () => clearTimeout(t);
  }, [dep]);
  return ref;
}

// ---------------------------------------------------------------------------
// Membri
// ---------------------------------------------------------------------------

const NEW = "nuovo";

function MembersTab({ selected, onSelect }: { selected?: string; onSelect: (id?: string) => void }) {
  const t = useT();
  const [text, setText] = useState("");
  const [search, setSearch] = useState("");
  const [status, setStatus] = useState("");
  useEffect(() => {
    const t = setTimeout(() => setSearch(text.trim()), 250);
    return () => clearTimeout(t);
  }, [text]);
  const list = useQuery({ queryKey: ["admin-members", search], queryFn: () => adminApi.members(search) });
  const items = (list.data?.items ?? []).filter((m) => !status || m.status === status);
  const panel = useScrollOnMobile(selected);
  return (
    <>
      <div className="flex flex-wrap items-center gap-3 border-b border-hair px-4 py-3 md:px-7">
        <label className="flex min-w-[200px] flex-1 sm:max-w-xs">
          <span className="sr-only">{t("configurazione.common.cerca")}</span>
          <input className="field" placeholder={t("configurazione.admin.cercaPerNomeO")} value={text} onChange={(e) => setText(e.target.value)} />
        </label>
        <label className="flex items-center gap-2">
          <span className="label">{t("configurazione.admin.stato")}</span>
          <Select className="field w-auto" value={status} onChange={(e) => setStatus(e.target.value)}>
            <option value="">{t("configurazione.admin.tutti")}</option>
            <option value="active">{t("configurazione.admin.attivi")}</option>
            <option value="invited">{t("configurazione.admin.invitati")}</option>
            <option value="suspended">{t("configurazione.admin.sospesi")}</option>
          </Select>
        </label>
        <span className="font-mono text-[11.5px] text-muted tabular">{items.length}</span>
        <button className="btn btn-primary ml-auto" onClick={() => onSelect(NEW)}>{t("configurazione.admin.aggiungiPersona")}</button>
      </div>
      <div className="flex flex-wrap">
        <div className="min-w-0 flex-[999_1_560px] px-4 md:px-7">
          <ErrorNote error={list.error} />
          <table className="w-full border-collapse tabular">
            <thead>
              <tr className="label h-9 text-left">
                <th scope="col" className="border-b-2 border-rule pr-3">{t("configurazione.common.persona")}</th>
                <th scope="col" className="hidden border-b-2 border-rule px-3 lg:table-cell">{t("configurazione.admin.ruoli")}</th>
                <th scope="col" className="hidden border-b-2 border-rule px-3 sm:table-cell">{t("configurazione.admin.accesso")}</th>
                <th scope="col" className="border-b-2 border-rule pl-3">{t("configurazione.admin.stato")}</th>
              </tr>
            </thead>
            <tbody>
              {items.map((m) => (
                <MemberRow key={m.id} m={m} on={selected === m.id} me={list.data?.me} onSelect={() => onSelect(m.id)} />
              ))}
              {!list.isPending && items.length === 0 && (
                <tr>
                  <td colSpan={4} className="py-8 text-muted">{t("configurazione.admin.nessunaPersonaTrovata")}</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
        {selected && (
          <div ref={panel} className="flex min-w-0 scroll-mt-14 flex-[1_1_400px]">
            <SidePanel label={t("configurazione.admin.dettaglioDellaPersona")} onClose={() => onSelect(undefined)}>
              {selected === NEW ? (
                <AddMember ssoOnly={!!list.data?.sso_only} emailAvailable={!!list.data?.email_available} onDone={(id) => onSelect(id)} />
              ) : (
                <MemberPanel key={selected} id={selected} me={list.data?.me} ssoOnly={!!list.data?.sso_only} />
              )}
            </SidePanel>
          </div>
        )}
      </div>
    </>
  );
}

function accessLabel(m: AdminMember): string {
  if (!m.has_account) return tr("configurazione.admin.noAccount");
  const parts = [];
  if (m.has_password) parts.push("password");
  if (m.sso) parts.push("SSO");
  return parts.length ? parts.join(tr("configurazione.admin.and")) : tr("configurazione.admin.toActivate");
}

function MemberRow({ m, on, me, onSelect }: { m: AdminMember; on: boolean; me?: string; onSelect: () => void }) {
  const t = useT();
  return (
    <tr className={`border-b border-hair align-top ${on ? "bg-sel" : "hover:bg-sel/50"}`}>
      <td className="py-2.5 pr-3">
        <button className="text-left font-medium text-fg hover:underline" aria-current={on ? "true" : undefined} onClick={onSelect}>
          {m.display_name}
        </button>
        {m.id === me && <span className="text-muted"> {t("configurazione.admin.tu")}</span>}
        <span className="block break-all text-[12px] text-muted">{m.email}</span>
        <span className="mt-1 flex flex-wrap gap-1">
          {m.admin && <Tag tone="accent">{t("configurazione.admin.amministratore")}</Tag>}
          {m.member_type !== "standard" && <Tag>{TYPE_LABELS[m.member_type] ?? m.member_type}</Tag>}
        </span>
      </td>
      <td className="hidden px-3 py-2.5 text-[12.5px] lg:table-cell">
        {m.roles.length === 0 ? (
          <span className="text-muted">{t("configurazione.admin.nessunRuolo")}</span>
        ) : (
          m.roles.map((r) => (
            <span key={r.assignment_id} className="block">
              {r.name}
              {r.via_group && <span className="text-muted">{t("configurazione.admin.viaGroup", { group: r.via_group })}</span>}
            </span>
          ))
        )}
      </td>
      <td className="hidden px-3 py-2.5 text-[12.5px] sm:table-cell">
        {accessLabel(m)}
        {m.mfa && <span className="text-ok"> · MFA</span>}
        <span className="block text-muted">{m.last_login_at ? t("configurazione.admin.lastLoginAt", { when: ago(m.last_login_at) }) : t("configurazione.admin.maiEntrato")}</span>
      </td>
      <td className="py-2.5 pl-3 text-[12.5px]">
        <StatusTag status={m.status} />
      </td>
    </tr>
  );
}

function StatusTag({ status }: { status: string }) {
  const tone = status === "active" ? "ok" : status === "suspended" ? "danger" : "flag";
  return <Tag tone={tone}>{STATUS_LABELS[status] ?? status}</Tag>;
}

function LinkResult({ link, emailed, onDone }: { link: OneTimeLink; emailed: boolean; onDone: () => void }) {
  const t = useT();
  const what = link.kind === "invite" ? t("configurazione.admin.inviteLink") : t("configurazione.admin.passwordLink");
  return (
    <CopyBox
      title={t("configurazione.admin.linkTitle", { what })}
      value={absoluteLink(link)}
      note={
        <>
          {t("configurazione.admin.linkNote", { date: dateTime(link.expires_at), sent: emailed ? t("configurazione.admin.ePartitoAnchePer") : t("configurazione.admin.nonEPartitoPer") })}
        </>
      }
      onDone={onDone}
    />
  );
}

function AddMember({ ssoOnly, emailAvailable, onDone }: { ssoOnly: boolean; emailAvailable: boolean; onDone: (id?: string) => void }) {
  const t = useT();
  const qc = useQueryClient();
  const roles = useQuery({ queryKey: ["admin-roles"], queryFn: adminApi.roles });
  const groups = useQuery({ queryKey: ["admin-groups"], queryFn: adminApi.groups });
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [type, setType] = useState("standard");
  const [mode, setMode] = useState<"invite" | "create">("invite");
  const [roleIDs, setRoleIDs] = useState<string[]>([]);
  const [groupIDs, setGroupIDs] = useState<string[]>([]);
  const add = useMutation({
    mutationFn: () => adminApi.addMember({ email: email.trim(), display_name: name, member_type: type, mode, role_ids: roleIDs, group_ids: groupIDs }),
    onSuccess: () => invalidateAll(qc),
  });
  function submit(e: FormEvent) {
    e.preventDefault();
    add.mutate();
  }
  if (add.data) {
    return (
      <div className="flex flex-col gap-4">
        <SectionTitle>{mode === "invite" ? t("configurazione.admin.invitoCreato") : t("configurazione.admin.personaCreata")}</SectionTitle>
        {add.data.link ? (
          <LinkResult link={add.data.link} emailed={add.data.emailed} onDone={() => onDone(add.data!.member_id)} />
        ) : (
          <>
            <p className="m-0">
              {add.data.new_account
                ? t("configurazione.admin.lAccountEntreraCon")
                : t("configurazione.admin.laPersonaHaGia")}
            </p>
            <div>
              <button className="btn" onClick={() => onDone(add.data!.member_id)}>{t("configurazione.admin.apriLaScheda")}</button>
            </div>
          </>
        )}
      </div>
    );
  }
  const assignable = (roles.data?.items ?? []).filter((r) => (type === "requester" ? r.key === "requester" || !r.system : true));
  return (
    <form onSubmit={submit} className="flex flex-col gap-4">
      <SectionTitle>{t("configurazione.admin.nuovaPersona")}</SectionTitle>
      <label className="flex flex-col gap-1.5">
        <span className="label">{t("configurazione.admin.email")}</span>
        <input className="field" type="email" required autoComplete="off" value={email} onChange={(e) => setEmail(e.target.value)} />
      </label>
      <label className="flex flex-col gap-1.5">
        <span className="label">{t("configurazione.admin.nomeVisualizzato")}</span>
        <input className="field" maxLength={120} placeholder={t("configurazione.admin.nomeECognome")} value={name} onChange={(e) => setName(e.target.value)} />
      </label>
      <fieldset className="m-0 flex flex-col gap-1.5 border-0 p-0">
        <legend className="label mb-1.5 p-0">{t("configurazione.common.tipo")}</legend>
        <label className="flex items-start gap-2">
          <input type="radio" name="tipo" className="mt-0.5" checked={type === "standard"} onChange={() => setType("standard")} />
          <span>{t("configurazione.admin.member")} <span className="text-muted">{t("configurazione.admin.lavoraNellApplicazioneCon")}</span></span>
        </label>
        <label className="flex items-start gap-2">
          <input type="radio" name="tipo" className="mt-0.5" checked={type === "requester"} onChange={() => { setType("requester"); setRoleIDs([]); }} />
          <span>{t("configurazione.admin.portalRequester")} <span className="text-muted">{t("configurazione.admin.apreESegueSolo")}</span></span>
        </label>
      </fieldset>
      <fieldset className="m-0 flex flex-col gap-1.5 border-0 p-0">
        <legend className="label mb-1.5 p-0">{t("configurazione.admin.come")}</legend>
        <label className="flex items-start gap-2">
          <input type="radio" name="modo" className="mt-0.5" checked={mode === "invite"} onChange={() => setMode("invite")} />
          <span>
            {t("configurazione.admin.inviteWithLink")} <span className="text-muted">{emailAvailable ? t("configurazione.admin.inviteHintEmail") : t("configurazione.admin.inviteHint")}</span>
          </span>
        </label>
        <label className="flex items-start gap-2">
          <input type="radio" name="modo" className="mt-0.5" checked={mode === "create"} onChange={() => setMode("create")} />
          <span>
            {t("configurazione.admin.createNow")} <span className="text-muted">{ssoOnly ? t("configurazione.admin.createHintSso") : t("configurazione.admin.createHintLink")}</span>
          </span>
        </label>
      </fieldset>
      <fieldset className="m-0 flex flex-col gap-1.5 border-0 p-0">
        <legend className="label mb-1.5 p-0">{t("configurazione.admin.ruoli")}</legend>
        <span className="text-[12px] text-muted">{t("configurazione.admin.noChoice", { role: type === "requester" ? t("configurazione.admin.richiedentePortale") : t("configurazione.admin.lettore") })}</span>
        <div className="grid grid-cols-1 gap-1 sm:grid-cols-2">
          {assignable.map((r) => (
            <label key={r.id} className="flex items-start gap-2">
              <input
                type="checkbox"
                className="mt-0.5"
                checked={roleIDs.includes(r.id)}
                onChange={(e) => setRoleIDs((c) => (e.target.checked ? [...c, r.id] : c.filter((x) => x !== r.id)))}
              />
              <span>
                {r.name}
                {r.admin && <span className="text-flag"> {t("configurazione.admin.amministrazione")}</span>}
              </span>
            </label>
          ))}
        </div>
      </fieldset>
      <fieldset className="m-0 flex flex-col gap-1.5 border-0 p-0">
        <legend className="label mb-1.5 p-0">{t("configurazione.admin.gruppi")}</legend>
        <div className="grid grid-cols-1 gap-1 sm:grid-cols-2">
          {(groups.data?.items ?? [])
            .filter((g) => !g.system)
            .map((g) => (
              <label key={g.id} className="flex items-start gap-2">
                <input
                  type="checkbox"
                  className="mt-0.5"
                  checked={groupIDs.includes(g.id)}
                  onChange={(e) => setGroupIDs((c) => (e.target.checked ? [...c, g.id] : c.filter((x) => x !== g.id)))}
                />
                <span>{g.name}</span>
              </label>
            ))}
        </div>
        <span className="text-[12px] text-muted">{t("configurazione.admin.tuttiIMembriSi")}</span>
      </fieldset>
      <ErrorNote error={add.error} />
      <div className="flex flex-wrap gap-2">
        <button className="btn btn-primary" disabled={add.isPending || !email.trim()}>
          {add.isPending ? t("configurazione.common.salvataggio") : mode === "invite" ? t("configurazione.admin.invita") : t("configurazione.common.crea")}
        </button>
        <button type="button" className="btn" onClick={() => onDone(undefined)}>{t("configurazione.common.annulla")}</button>
      </div>
    </form>
  );
}

function MemberPanel({ id, me, ssoOnly }: { id: string; me?: string; ssoOnly: boolean }) {
  const t = useT();
  const qc = useQueryClient();
  const m = useQuery({ queryKey: ["admin-member", id], queryFn: () => adminApi.member(id) });
  const roles = useQuery({ queryKey: ["admin-roles"], queryFn: adminApi.roles });
  const groups = useQuery({ queryKey: ["admin-groups"], queryFn: adminApi.groups });
  const [link, setLink] = useState<{ link: OneTimeLink; emailed: boolean } | null>(null);
  const [name, setName] = useState<string | null>(null);
  const [addRole, setAddRole] = useState("");
  const [addGroup, setAddGroup] = useState("");
  const done = { onSuccess: () => invalidateAll(qc) };
  const suspend = useMutation({ mutationFn: () => adminApi.suspend(id), ...done });
  const reactivate = useMutation({ mutationFn: () => adminApi.reactivate(id), ...done });
  const setType = useMutation({ mutationFn: (t: string) => adminApi.updateMember(id, { member_type: t }), ...done });
  const rename = useMutation({ mutationFn: (n: string) => adminApi.updateMember(id, { display_name: n }), onSuccess: () => { setName(null); invalidateAll(qc); } });
  const invite = useMutation({ mutationFn: () => adminApi.resendInvite(id), onSuccess: (r) => { setLink(r); invalidateAll(qc); } });
  const pwLink = useMutation({ mutationFn: () => adminApi.passwordLink(id), onSuccess: (r) => setLink(r) });
  const mfaReset = useMutation({ mutationFn: () => adminApi.resetMfa(id), ...done });
  const assign = useMutation({ mutationFn: (roleID: string) => adminApi.assign(roleID, { member_id: id }), onSuccess: () => { setAddRole(""); invalidateAll(qc); } });
  const unassign = useMutation({ mutationFn: adminApi.unassign, ...done });
  const join = useMutation({ mutationFn: (g: string) => adminApi.addToGroup(g, id), onSuccess: () => { setAddGroup(""); invalidateAll(qc); } });
  const leave = useMutation({ mutationFn: (g: string) => adminApi.removeFromGroup(g, id), ...done });

  if (m.error) return <ErrorNote error={m.error} />;
  if (!m.data) return null;
  const x = m.data;
  const self = x.id === me;
  const direct = x.roles.filter((r) => !r.via_group);
  const viaGroup = x.roles.filter((r) => r.via_group);
  const errors = [suspend.error, reactivate.error, setType.error, rename.error, invite.error, pwLink.error, mfaReset.error, assign.error, unassign.error, join.error, leave.error];
  return (
    <div className="flex flex-col gap-5">
      <div className="flex flex-col gap-1">
        {name === null ? (
          <h2 className="m-0 flex flex-wrap items-baseline gap-2 text-[22px] font-bold">
            {x.display_name}
            <button type="button" className="text-[12px] font-normal text-muted underline" onClick={() => setName(x.display_name)}>{t("configurazione.admin.rinomina")}</button>
          </h2>
        ) : (
          <form
            className="flex flex-wrap gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              rename.mutate(name);
            }}
          >
            <input className="field min-w-0 flex-1" aria-label={t("configurazione.admin.nomeVisualizzato")} value={name} onChange={(e) => setName(e.target.value)} />
            <button className="btn btn-primary py-1.5" disabled={rename.isPending}>{t("configurazione.common.salva")}</button>
            <button type="button" className="btn py-1.5" onClick={() => setName(null)}>{t("configurazione.common.annulla")}</button>
          </form>
        )}
        <span className="break-all text-muted">{x.email}</span>
        <span className="flex flex-wrap gap-1">
          <StatusTag status={x.status} />
          {x.admin && <Tag tone="accent">{t("configurazione.admin.amministratore")}</Tag>}
        </span>
      </div>

      {link && <LinkResult link={link.link} emailed={link.emailed} onDone={() => setLink(null)} />}
      <MutError errors={errors} />

      <section className="flex flex-col">
        <SectionTitle>{t("configurazione.admin.accesso")}</SectionTitle>
        <dl className="m-0">
          <KV label={t("configurazione.admin.metodo")}>{accessLabel(x)}</KV>
          <KV label={t("configurazione.admin.secondoFattore")}>
            {x.mfa ? <span className="text-ok">{t("configurazione.admin.attivoAppDiAutenticazione")}</span> : x.sso && !x.has_password ? t("configurazione.admin.loGestisceIlProvider") : t("configurazione.common.nonAttivo")}
          </KV>
          <KV label={t("configurazione.admin.ultimoAccesso")}>{x.last_login_at ? dateTime(x.last_login_at) : t("configurazione.admin.never")}</KV>
          <KV label={t("configurazione.admin.sessioniAperte")}>{x.sessions}</KV>
          <KV label={t("configurazione.admin.tokenApiValidi")}>{x.tokens}</KV>
          {x.status === "invited" && <KV label={t("configurazione.admin.invito")}>{x.invite_expires_at ? t("configurazione.admin.validUntil", { date: dateTime(x.invite_expires_at) }) : t("configurazione.admin.expired")}</KV>}
        </dl>
        {x.other_tenants && (
          <p className="mt-2 mb-0 border-l-2 border-flag pl-3 text-[12.5px]">
            {t("configurazione.admin.lAccountValeAnche")}
          </p>
        )}
        <div className="mt-3 flex flex-wrap gap-2">
          {x.status === "invited" && (
            <button className="btn py-1.5" disabled={invite.isPending} onClick={() => invite.mutate()}>{t("configurazione.admin.nuovoLinkDInvito")}</button>
          )}
          {x.status === "active" && x.has_account && !ssoOnly && (
            <ConfirmButton
              label={t("configurazione.admin.linkPerLaPassword")}
              confirm={t("configurazione.admin.generaIlLink")}
              disabled={x.other_tenants}
              title={x.other_tenants ? t("configurazione.admin.accountCondivisoConAltre") : undefined}
              pending={pwLink.isPending}
              onConfirm={() => pwLink.mutate()}
            />
          )}
          {x.mfa && (
            <ConfirmButton
              label={t("configurazione.admin.reimpostaLMfa")}
              confirm={t("configurazione.admin.togliLMfaE")}
              danger
              disabled={x.other_tenants}
              pending={mfaReset.isPending}
              onConfirm={() => mfaReset.mutate()}
            />
          )}
        </div>
      </section>

      <section className="flex flex-col gap-2">
        <SectionTitle>{t("configurazione.common.tipo")}</SectionTitle>
        <div className="flex flex-wrap items-center gap-2">
          <span>{TYPE_LABELS[x.member_type] ?? x.member_type}</span>
          {(x.member_type === "standard" || x.member_type === "requester") && (
            <ConfirmButton
              label={x.member_type === t("configurazione.gate.standard") ? t("configurazione.admin.rendiRichiedenteDelPortale") : t("configurazione.admin.rendiMembroPieno")}
              confirm={x.member_type === t("configurazione.gate.standard") ? t("configurazione.admin.confermaIRuoliDiretti") : t("configurazione.admin.confermaIlRuoloDiventa")}
              pending={setType.isPending}
              onConfirm={() => setType.mutate(x.member_type === "standard" ? "requester" : "standard")}
            />
          )}
        </div>
      </section>

      <section className="flex flex-col gap-2">
        <SectionTitle>{t("configurazione.admin.ruoli")}</SectionTitle>
        <ul className="m-0 flex list-none flex-col p-0">
          {direct.map((r) => (
            <li key={r.assignment_id} className="flex items-center gap-2 border-b border-hair py-1.5">
              <span className="flex-1">
                {r.name}
                {r.admin && <span className="text-flag"> {t("configurazione.admin.amministrazione")}</span>}
              </span>
              <button type="button" className="text-[12px] text-muted underline hover:text-danger" onClick={() => unassign.mutate(r.assignment_id)}>
                {t("configurazione.common.togli2")}
              </button>
            </li>
          ))}
          {viaGroup.map((r) => (
            <li key={r.assignment_id + r.via_group} className="flex items-center gap-2 border-b border-hair py-1.5">
              <span className="flex-1">
                {r.name} <span className="text-muted">{t("configurazione.admin.fromGroup", { group: r.via_group ?? "" })}</span>
              </span>
            </li>
          ))}
          {x.roles.length === 0 && <li className="py-1.5 text-muted">{t("configurazione.admin.nessunRuoloVedeSolo")}</li>}
        </ul>
        <form
          className="flex flex-wrap gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            if (addRole) assign.mutate(addRole);
          }}
        >
          <Select className="field min-w-0 flex-1" aria-label={t("configurazione.admin.ruoloDaAssegnare")} value={addRole} onChange={(e) => setAddRole(e.target.value)}>
            <option value="">{t("configurazione.admin.assegnaUnRuolo")}</option>
            {(roles.data?.items ?? [])
              .filter((r) => !direct.some((d) => d.id === r.id))
              .map((r) => (
                <option key={r.id} value={r.id}>{r.name}</option>
              ))}
          </Select>
          <button className="btn py-1.5" disabled={!addRole || assign.isPending}>{t("configurazione.admin.assegna")}</button>
        </form>
      </section>

      <section className="flex flex-col gap-2">
        <SectionTitle>{t("configurazione.admin.gruppi")}</SectionTitle>
        <ul className="m-0 flex list-none flex-col p-0">
          {x.groups.map((g) => (
            <li key={g.id} className="flex items-center gap-2 border-b border-hair py-1.5">
              <span className="flex-1">{g.name}</span>
              {!g.system && (
                <button type="button" className="text-[12px] text-muted underline hover:text-danger" onClick={() => leave.mutate(g.id)}>
                  {t("configurazione.common.togli2")}
                </button>
              )}
            </li>
          ))}
        </ul>
        <form
          className="flex flex-wrap gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            if (addGroup) join.mutate(addGroup);
          }}
        >
          <Select className="field min-w-0 flex-1" aria-label={t("configurazione.common.gruppo")} value={addGroup} onChange={(e) => setAddGroup(e.target.value)}>
            <option value="">{t("configurazione.admin.aggiungiAUnGruppo")}</option>
            {(groups.data?.items ?? [])
              .filter((g) => !g.system && !x.groups.some((y) => y.id === g.id))
              .map((g) => (
                <option key={g.id} value={g.id}>{g.name}</option>
              ))}
          </Select>
          <button className="btn py-1.5" disabled={!addGroup || join.isPending}>{t("configurazione.common.aggiungi")}</button>
        </form>
      </section>

      <AccessExplainSection id={x.id} />

      {x.teams.length > 0 && (
        <section className="flex flex-col gap-2">
          <SectionTitle>{t("configurazione.common.team")}</SectionTitle>
          <p className="m-0">{x.teams.map((t) => t.name).join(", ")}</p>
          <span className="text-[12px] text-muted">{t("configurazione.admin.iTeamSiGestiscono")}</span>
        </section>
      )}

      <section className="flex flex-col gap-2">
        <SectionTitle>{t("configurazione.admin.stato")}</SectionTitle>
        {x.status === "suspended" ? (
          <div>
            <button className="btn py-1.5" disabled={reactivate.isPending} onClick={() => reactivate.mutate()}>{t("configurazione.common.riattiva")}</button>
          </div>
        ) : self ? (
          <p className="m-0 text-muted">{t("configurazione.admin.nonPuoiSospendereTe")}</p>
        ) : (
          <div className="flex flex-col gap-1.5">
            <span className="text-[12.5px] text-muted">
              {t("configurazione.admin.laSospensioneChiudeLe")}
            </span>
            <div>
              <ConfirmButton label={t("configurazione.admin.sospendi")} confirm={t("configurazione.admin.sospendiOra")} danger pending={suspend.isPending} onConfirm={() => suspend.mutate()} />
            </div>
          </div>
        )}
      </section>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Ruoli
// ---------------------------------------------------------------------------

function RolesTab({ selected, onSelect }: { selected?: string; onSelect: (id?: string) => void }) {
  const t = useT();
  const roles = useQuery({ queryKey: ["admin-roles"], queryFn: adminApi.roles });
  const [copyOf, setCopyOf] = useState<AdminRole | null>(null);
  const panel = useScrollOnMobile(selected);
  const items = roles.data?.items ?? [];
  const role = items.find((r) => r.id === selected);
  return (
    <>
      <div className="flex flex-wrap items-center gap-3 border-b border-hair px-4 py-3 md:px-7">
        <p className="m-0 max-w-[620px] flex-1 text-muted">
          {t("configurazione.admin.iRuoliDiSistema")}
        </p>
        <button className="btn btn-primary" onClick={() => { setCopyOf(null); onSelect(NEW); }}>{t("configurazione.admin.nuovoRuolo")}</button>
      </div>
      <div className="flex flex-wrap">
        <div className="min-w-0 flex-[999_1_520px] px-4 md:px-7">
          <ErrorNote error={roles.error} />
          <ul className="m-0 flex list-none flex-col p-0">
            {items.map((r) => (
              <li key={r.id} className={`border-b border-hair py-2.5 ${selected === r.id ? "bg-sel" : ""}`}>
                <button className="flex w-full flex-wrap items-baseline gap-x-2 text-left" aria-current={selected === r.id ? "true" : undefined} onClick={() => onSelect(r.id)}>
                  <span className="font-medium hover:underline">{r.name}</span>
                  {r.system ? <Tag>{t("configurazione.common.diSistema")}</Tag> : <Tag tone="fg">{t("configurazione.admin.personalizzato")}</Tag>}
                  {r.admin && <Tag tone="accent">{t("configurazione.admin.amministrazione2")}</Tag>}
                  <span className="ml-auto font-mono text-[11.5px] text-muted tabular">
                    {t("configurazione.admin.peopleCount", { count: r.members.length })} · {t("configurazione.admin.groupCount", { count: r.groups.length })}
                  </span>
                </button>
                <span className="block text-[12px] text-muted">
                  {r.permissions.includes("*") ? t("configurazione.admin.tuttiIPermessi") : t("configurazione.admin.permissionCount", { count: r.permissions.length })}
                  {r.description && <> · {r.description}</>}
                </span>
                {!r.permissions.includes("*") && r.permissions.length > 0 && (
                  <span className="block text-[12px] text-muted" data-testid="riepilogo-permessi">
                    {permissionSummary(r.permissions, roles.data?.permissions ?? [])}
                  </span>
                )}
              </li>
            ))}
          </ul>
        </div>
        {selected && roles.data && (
          <div ref={panel} className="flex min-w-0 scroll-mt-14 flex-[1_1_420px]">
            <SidePanel label={t("configurazione.admin.dettaglioDelRuolo")} onClose={() => onSelect(undefined)}>
              {selected === NEW ? (
                <RoleForm key={"new" + (copyOf?.id ?? "")} copyOf={copyOf ?? undefined} catalog={roles.data.permissions} onSaved={(id) => onSelect(id)} onCancel={() => onSelect(undefined)} />
              ) : role ? (
                <RolePanel
                  key={role.id}
                  role={role}
                  catalog={roles.data.permissions}
                  onDuplicate={() => { setCopyOf(role); onSelect(NEW); }}
                  onDeleted={() => onSelect(undefined)}
                />
              ) : (
                <p className="text-muted">{t("configurazione.admin.ruoloInesistente")}</p>
              )}
            </SidePanel>
          </div>
        )}
      </div>
    </>
  );
}

/** I permessi di un ruolo per nome («Leggere gli elementi, Modificare gli elementi, e altri 3»). */
export function permissionSummary(keys: string[], catalog: Permission[], max = 4): string {
  const names = keys.map((k) => catalog.find((p) => p.key === k)?.name ?? k);
  if (names.length <= max) return names.join(", ");
  return tr("configurazione.admin.andMore", { names: names.slice(0, max).join(", "), count: names.length - max });
}

function PermissionList({ catalog, value, onChange, readOnly }: { catalog: Permission[]; value: string[]; onChange?: (v: string[]) => void; readOnly?: boolean }) {
  const t = useT();
  const all = value.includes("*");
  const areas = useMemo(() => {
    const out: [string, Permission[]][] = [];
    for (const p of catalog) {
      const a = out.find(([k]) => k === p.area);
      if (a) a[1].push(p);
      else out.push([p.area, [p]]);
    }
    return out;
  }, [catalog]);
  return (
    <div className="flex flex-col gap-3">
      {all && <p className="m-0 border-l-2 border-accent pl-3">{t("configurazione.admin.tuttiIPermessiAnche")}</p>}
      {areas.map(([area, perms]) => (
        <fieldset key={area} className="m-0 flex flex-col gap-1.5 border-0 p-0">
          <legend className="label mb-1 p-0 text-muted">{area}</legend>
          {perms.map((p) => {
            const on = all || value.includes(p.key);
            return (
              <label key={p.key} className={`flex items-start gap-2 ${readOnly && !on ? "opacity-60" : ""}`}>
                <input
                  type="checkbox"
                  className="mt-0.5"
                  checked={on}
                  disabled={readOnly}
                  onChange={(e) => onChange?.(e.target.checked ? [...value, p.key] : value.filter((x) => x !== p.key))}
                />
                <span>
                  <span className="font-medium">{p.name}</span>
                  {p.sensitive && <span className="text-flag"> {t("configurazione.admin.riservato")}</span>} <span className="font-mono text-[11px] text-muted">{p.key}</span>
                  <span className="block text-[12px] text-muted">{p.description}</span>
                </span>
              </label>
            );
          })}
        </fieldset>
      ))}
    </div>
  );
}

function RoleForm({ role, copyOf, catalog, onSaved, onCancel }: { role?: AdminRole; copyOf?: AdminRole; catalog: Permission[]; onSaved: (id: string) => void; onCancel: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const base = role ?? copyOf;
  const expand = (p: string[]) => (p.includes("*") ? catalog.map((c) => c.key) : p);
  const [name, setName] = useState(role?.name ?? (copyOf ? tr("configurazione.admin.copySuffix", { name: copyOf.name }) : ""));
  const [description, setDescription] = useState(base?.description ?? "");
  const [perms, setPerms] = useState<string[]>(base ? expand(base.permissions) : []);
  const save = useMutation({
    mutationFn: async () => {
      const body = { name, description, permissions: perms };
      if (role) {
        await adminApi.updateRole(role.id, body);
        return role.id;
      }
      return (await adminApi.createRole({ ...body, copy_of: copyOf?.id })).id;
    },
    onSuccess: (id) => {
      invalidateAll(qc);
      onSaved(id);
    },
  });
  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      {!role && <SectionTitle>{copyOf ? t("configurazione.admin.copyOf", { name: copyOf.name }) : t("configurazione.admin.nuovoRuolo")}</SectionTitle>}
      <label className="flex flex-col gap-1.5">
        <span className="label">{t("configurazione.common.nome")}</span>
        <input className="field" required maxLength={80} value={name} onChange={(e) => setName(e.target.value)} />
      </label>
      <label className="flex flex-col gap-1.5">
        <span className="label">{t("configurazione.common.descrizione")}</span>
        <textarea className="field min-h-[64px]" maxLength={500} value={description} onChange={(e) => setDescription(e.target.value)} />
      </label>
      <div className="flex flex-col gap-1.5">
        <span className="label">{t("configurazione.admin.permessi")}</span>
        <PermissionList catalog={catalog} value={perms} onChange={setPerms} />
      </div>
      <ErrorNote error={save.error} />
      <div className="flex flex-wrap gap-2">
        <button className="btn btn-primary" disabled={save.isPending || !name.trim() || perms.length === 0}>{role ? t("configurazione.common.salva") : t("configurazione.admin.creaIlRuolo")}</button>
        {!role && <button type="button" className="btn" onClick={onCancel}>{t("configurazione.common.annulla")}</button>}
      </div>
    </form>
  );
}

function RolePanel({ role, catalog, onDuplicate, onDeleted }: { role: AdminRole; catalog: Permission[]; onDuplicate: () => void; onDeleted: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const members = useQuery({ queryKey: ["admin-members", ""], queryFn: () => adminApi.members("") });
  const groups = useQuery({ queryKey: ["admin-groups"], queryFn: adminApi.groups });
  const [who, setWho] = useState("");
  const del = useMutation({ mutationFn: () => adminApi.deleteRole(role.id), onSuccess: () => { invalidateAll(qc); onDeleted(); } });
  const unassign = useMutation({ mutationFn: adminApi.unassign, onSuccess: () => invalidateAll(qc) });
  const assign = useMutation({
    mutationFn: () => {
      const [kind, id] = who.split(":");
      return adminApi.assign(role.id, kind === "g" ? { group_id: id } : { member_id: id });
    },
    onSuccess: () => {
      setWho("");
      invalidateAll(qc);
    },
  });
  return (
    <div className="flex flex-col gap-5">
      <div className="flex flex-col gap-1">
        <h2 className="m-0 text-[22px] font-bold">{role.name}</h2>
        <span className="flex flex-wrap gap-1">
          {role.system ? <Tag>{t("configurazione.common.diSistema")}</Tag> : <Tag tone="fg">{t("configurazione.admin.personalizzato")}</Tag>}
          {role.admin && <Tag tone="accent">{t("configurazione.admin.amministrazione2")}</Tag>}
        </span>
      </div>
      {role.system ? (
        <>
          <p className="m-0 text-muted">
            {role.description || t("configurazione.admin.ruoloDiSistemaIl")}
          </p>
          <div>
            <button className="btn" onClick={onDuplicate}>{t("configurazione.admin.duplicaEModificaLa")}</button>
          </div>
          <PermissionList catalog={catalog} value={role.permissions} readOnly />
        </>
      ) : (
        <>
          <RoleForm role={role} catalog={catalog} onSaved={() => undefined} onCancel={() => undefined} />
          <div className="flex flex-wrap gap-2">
            <button className="btn" onClick={onDuplicate}>{t("configurazione.common.duplica")}</button>
            <ConfirmButton label={t("configurazione.admin.eliminaIlRuolo")} confirm={t("configurazione.admin.eliminaConLeAssegnazioni")} danger pending={del.isPending} onConfirm={() => del.mutate()} />
          </div>
          <ErrorNote error={del.error} />
        </>
      )}
      <section className="flex flex-col gap-2">
        <SectionTitle aside={<span className="font-mono text-[11.5px] text-muted tabular">{role.members.length + role.groups.length}</span>}>{t("configurazione.admin.assegnatoA")}</SectionTitle>
        <ul className="m-0 flex list-none flex-col p-0">
          {role.groups.map((h) => (
            <li key={h.assignment_id} className="flex items-center gap-2 border-b border-hair py-1.5">
              <span className="flex-1">
                <span className="text-muted">{t("configurazione.admin.gruppo")}</span> {h.name}
              </span>
              <button type="button" className="text-[12px] text-muted underline hover:text-danger" onClick={() => unassign.mutate(h.assignment_id)}>{t("configurazione.common.togli2")}</button>
            </li>
          ))}
          {role.members.map((h) => (
            <li key={h.assignment_id} className="flex items-center gap-2 border-b border-hair py-1.5">
              <span className="flex-1">
                {h.name}
                {h.status && h.status !== "active" && <span className="text-muted"> · {STATUS_LABELS[h.status] ?? h.status}</span>}
              </span>
              <button type="button" className="text-[12px] text-muted underline hover:text-danger" onClick={() => unassign.mutate(h.assignment_id)}>{t("configurazione.common.togli2")}</button>
            </li>
          ))}
          {role.members.length + role.groups.length === 0 && <li className="py-1.5 text-muted">{t("configurazione.admin.nessuno")}</li>}
        </ul>
        <MutError errors={[unassign.error, assign.error]} />
        <form
          className="flex flex-wrap gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            if (who) assign.mutate();
          }}
        >
          <Select className="field min-w-0 flex-1" aria-label={t("configurazione.admin.personaOGruppo")} value={who} onChange={(e) => setWho(e.target.value)}>
            <option value="">{t("configurazione.admin.assegnaA")}</option>
            <optgroup label={t("configurazione.admin.gruppi")}>
              {(groups.data?.items ?? [])
                .filter((g) => !role.groups.some((h) => h.id === g.id))
                .map((g) => (
                  <option key={g.id} value={`g:${g.id}`}>{g.name}</option>
                ))}
            </optgroup>
            <optgroup label={t("configurazione.admin.persone")}>
              {(members.data?.items ?? [])
                .filter((m) => !role.members.some((h) => h.id === m.id))
                .map((m) => (
                  <option key={m.id} value={`m:${m.id}`}>{m.display_name}</option>
                ))}
            </optgroup>
          </Select>
          <button className="btn py-1.5" disabled={!who || assign.isPending}>{t("configurazione.admin.assegna")}</button>
        </form>
      </section>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Gruppi
// ---------------------------------------------------------------------------

function treeOrder(groups: AdminGroup[]): { g: AdminGroup; depth: number }[] {
  const byParent = new Map<string | null, AdminGroup[]>();
  const ids = new Set(groups.map((g) => g.id));
  for (const g of groups) {
    const p = g.parent_id && ids.has(g.parent_id) ? g.parent_id : null;
    byParent.set(p, [...(byParent.get(p) ?? []), g]);
  }
  const out: { g: AdminGroup; depth: number }[] = [];
  const walk = (p: string | null, depth: number) => {
    for (const g of byParent.get(p) ?? []) {
      out.push({ g, depth });
      if (depth < 20) walk(g.id, depth + 1);
    }
  };
  walk(null, 0);
  return out;
}

function usageText(u: AdminGroup["usage"]): string[] {
  const out: string[] = [];
  if (u.processes.length) out.push(tr("configurazione.admin.usageProcesses", { count: u.processes.length }));
  if (u.review_teams.length) out.push(tr("configurazione.admin.usageReviewTeams"));
  if (u.bodies.length) out.push(tr("configurazione.admin.usageBodies"));
  if (u.elements) out.push(tr("configurazione.admin.usageElements", { count: u.elements }));
  if (u.approvals) out.push(tr("configurazione.admin.usageApprovals", { count: u.approvals }));
  return out;
}

function GroupsTab({ selected, onSelect }: { selected?: string; onSelect: (id?: string) => void }) {
  const t = useT();
  const groups = useQuery({ queryKey: ["admin-groups"], queryFn: adminApi.groups });
  const panel = useScrollOnMobile(selected);
  const items = groups.data?.items ?? [];
  const tree = treeOrder(items);
  const group = items.find((g) => g.id === selected);
  const sec = useQuery({ queryKey: ["admin-security"], queryFn: adminApi.security });
  // Il testo segue l'impostazione reale del tenant (scheda Sicurezza), non una frase fissa.
  const inherits = sec.data?.group_inheritance;
  return (
    <>
      <div className="flex flex-wrap items-center gap-3 border-b border-hair px-4 py-3 md:px-7">
        <p className="m-0 max-w-[620px] flex-1 text-muted">
          {t("configurazione.admin.groupsReceive")}{" "}
          {inherits === undefined
            ? t("configurazione.admin.laGerarchiaOrdinaL")
            : inherits
              ? t("configurazione.admin.laGerarchiaContaI")
              : t("configurazione.admin.laGerarchiaOrdinaSolo")}
        </p>
        <button className="btn btn-primary" onClick={() => onSelect(NEW)}>{t("configurazione.admin.nuovoGruppo")}</button>
      </div>
      <div className="flex flex-wrap">
        <div className="min-w-0 flex-[999_1_520px] px-4 md:px-7">
          <ErrorNote error={groups.error} />
          <ul className="m-0 flex list-none flex-col p-0">
            {tree.map(({ g, depth }) => {
              const used = usageText(g.usage);
              return (
                <li key={g.id} className={`border-b border-hair py-2.5 ${selected === g.id ? "bg-sel" : ""}`} style={{ paddingLeft: `${Math.min(depth, 6) * 20}px` }}>
                  <button className="flex w-full flex-wrap items-baseline gap-x-2 text-left" aria-current={selected === g.id ? "true" : undefined} onClick={() => onSelect(g.id)}>
                    {depth > 0 && <span className="text-muted" aria-hidden="true">└</span>}
                    <span className="font-medium hover:underline">{g.name}</span>
                    {g.system && <Tag>{t("configurazione.common.diSistema")}</Tag>}
                    <span className="ml-auto font-mono text-[11.5px] text-muted tabular">{t("configurazione.admin.memberCount", { count: g.members.length })}</span>
                  </button>
                  {(g.roles.length > 0 || used.length > 0) && (
                    <span className="block text-[12px] text-muted">
                      {g.roles.length > 0 && <>{t("configurazione.admin.rolesList", { list: g.roles.map((r) => r.name).join(", ") })}</>}
                      {g.roles.length > 0 && used.length > 0 && " · "}
                      {used.length > 0 && <>{t("configurazione.admin.usedInList", { list: used.join(", ") })}</>}
                    </span>
                  )}
                </li>
              );
            })}
          </ul>
        </div>
        {selected && groups.data && (
          <div ref={panel} className="flex min-w-0 scroll-mt-14 flex-[1_1_420px]">
            <SidePanel label={t("configurazione.admin.dettaglioDelGruppo")} onClose={() => onSelect(undefined)}>
              {selected === NEW ? (
                <GroupForm groups={items} onSaved={(id) => onSelect(id)} onCancel={() => onSelect(undefined)} />
              ) : group ? (
                <GroupPanel key={group.id} group={group} groups={items} onDeleted={() => onSelect(undefined)} />
              ) : (
                <p className="text-muted">{t("configurazione.admin.gruppoInesistente")}</p>
              )}
            </SidePanel>
          </div>
        )}
      </div>
    </>
  );
}

function GroupForm({ group, groups, onSaved, onCancel }: { group?: AdminGroup; groups: AdminGroup[]; onSaved: (id: string) => void; onCancel?: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const [name, setName] = useState(group?.name ?? "");
  const [parent, setParent] = useState(group?.parent_id ?? "");
  const save = useMutation({
    mutationFn: async () => {
      const body = { name, parent_id: parent || null };
      if (group) {
        await adminApi.updateGroup(group.id, body);
        return group.id;
      }
      return (await adminApi.createGroup(body)).id;
    },
    onSuccess: (id) => {
      invalidateAll(qc);
      onSaved(id);
    },
  });
  return (
    <form
      className="flex flex-col gap-3"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      {!group && <SectionTitle>{t("configurazione.admin.nuovoGruppo")}</SectionTitle>}
      <label className="flex flex-col gap-1.5">
        <span className="label">{t("configurazione.common.nome")}</span>
        <input className="field" required maxLength={120} value={name} disabled={group?.system} onChange={(e) => setName(e.target.value)} />
      </label>
      <label className="flex flex-col gap-1.5">
        <span className="label">{t("configurazione.admin.sottoIlGruppo")}</span>
        <Select className="field" value={parent} disabled={group?.system} onChange={(e) => setParent(e.target.value)}>
          <option value="">{t("configurazione.admin.nessunoPrimoLivello")}</option>
          {groups
            .filter((g) => g.id !== group?.id && !g.system)
            .map((g) => (
              <option key={g.id} value={g.id}>{g.name}</option>
            ))}
        </Select>
      </label>
      <ErrorNote error={save.error} />
      {!group?.system && (
        <div className="flex flex-wrap gap-2">
          <button className="btn btn-primary" disabled={save.isPending || !name.trim()}>{group ? t("configurazione.common.salva") : t("configurazione.admin.creaIlGruppo")}</button>
          {onCancel && <button type="button" className="btn" onClick={onCancel}>{t("configurazione.common.annulla")}</button>}
        </div>
      )}
    </form>
  );
}

/** Ruoli dei gruppi antenati (dal più vicino al più lontano), senza ripetizioni. */
export function ancestorRoles(group: AdminGroup, groups: AdminGroup[]): { id: string; name: string; from: string }[] {
  const out: { id: string; name: string; from: string }[] = [];
  const seen = new Set<string>([group.id]);
  const own = new Set(group.roles.map((r) => r.id));
  let cur = groups.find((g) => g.id === group.parent_id);
  while (cur && !seen.has(cur.id)) {
    seen.add(cur.id);
    for (const r of cur.roles) {
      if (!own.has(r.id) && !out.some((o) => o.id === r.id)) out.push({ id: r.id, name: r.name, from: cur.name });
    }
    const pid: string | null = cur.parent_id;
    cur = groups.find((g) => g.id === pid);
  }
  return out;
}

function GroupPanel({ group, groups, onDeleted }: { group: AdminGroup; groups: AdminGroup[]; onDeleted: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const roles = useQuery({ queryKey: ["admin-roles"], queryFn: adminApi.roles });
  const [who, setWho] = useState("");
  const [roleID, setRoleID] = useState("");
  const add = useMutation({ mutationFn: () => adminApi.addToGroup(group.id, who), onSuccess: () => { setWho(""); invalidateAll(qc); } });
  const remove = useMutation({ mutationFn: (m: string) => adminApi.removeFromGroup(group.id, m), onSuccess: () => invalidateAll(qc) });
  const assign = useMutation({ mutationFn: () => adminApi.assign(roleID, { group_id: group.id }), onSuccess: () => { setRoleID(""); invalidateAll(qc); } });
  const unassign = useMutation({ mutationFn: adminApi.unassign, onSuccess: () => invalidateAll(qc) });
  const del = useMutation({ mutationFn: () => adminApi.deleteGroup(group.id), onSuccess: () => { invalidateAll(qc); onDeleted(); } });
  const u = group.usage;
  const inUse = u.processes.length + u.review_teams.length + u.bodies.length + u.elements + u.approvals > 0;
  // Con l'ereditarietà attiva il gruppo riceve anche i ruoli dei gruppi sopra di lui.
  const sec = useQuery({ queryKey: ["admin-security"], queryFn: adminApi.security });
  const inheritedRoles = sec.data?.group_inheritance ? ancestorRoles(group, groups) : [];
  return (
    <div className="flex flex-col gap-5">
      <div className="flex flex-col gap-1">
        <h2 className="m-0 text-[22px] font-bold">{group.name}</h2>
        {group.system && <span className="text-muted">{t("configurazione.admin.gruppoDiSistemaComprende")}</span>}
      </div>
      {!group.system && <GroupForm key={group.name + group.parent_id} group={group} groups={groups} onSaved={() => undefined} />}
      {u.processes.length > 0 && (
        <p className="m-0 border-l-2 border-flag pl-3 text-[12.5px]">
          {t("configurazione.admin.leRegoleDeiProcessi")}
        </p>
      )}

      <section className="flex flex-col">
        <SectionTitle>{t("configurazione.admin.doveEUsato")}</SectionTitle>
        <dl className="m-0">
          <KV label={t("configurazione.admin.regoleDeiProcessi")}>{u.processes.length ? u.processes.join(", ") : "—"}</KV>
          <KV label={t("configurazione.admin.teamValutatori")}>{u.review_teams.length ? u.review_teams.join(", ") : "—"}</KV>
          <KV label={t("configurazione.admin.organi")}>{u.bodies.length ? u.bodies.join(", ") : "—"}</KV>
          <KV label={t("configurazione.admin.ruoliSugliElementi")}>{u.elements ? t("configurazione.admin.itemCount", { count: u.elements }) : "—"}</KV>
          <KV label={t("configurazione.admin.approvazioniInCorso")}>{u.approvals || "—"}</KV>
        </dl>
      </section>

      <section className="flex flex-col gap-2">
        <SectionTitle>{t("configurazione.admin.ruoliDelGruppo")}</SectionTitle>
        <ul className="m-0 flex list-none flex-col p-0">
          {group.roles.map((r) => (
            <li key={r.assignment_id} className="flex items-center gap-2 border-b border-hair py-1.5">
              <span className="flex-1">{r.name}</span>
              <button type="button" className="text-[12px] text-muted underline hover:text-danger" onClick={() => unassign.mutate(r.assignment_id)}>{t("configurazione.common.togli2")}</button>
            </li>
          ))}
          {group.roles.length === 0 && <li className="py-1.5 text-muted">{inheritedRoles.length ? t("configurazione.admin.nessunoAssegnatoDirettamente") : t("configurazione.admin.nessuno")}</li>}
          {inheritedRoles.map((r) => (
            <li key={`${r.from}-${r.id}`} className="flex items-center gap-2 border-b border-hair py-1.5 text-muted">
              <span className="flex-1">{r.name}</span>
              <span className="text-[12px]">{t("configurazione.admin.inheritedFrom", { from: r.from })}</span>
            </li>
          ))}
        </ul>
        <form
          className="flex flex-wrap gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            if (roleID) assign.mutate();
          }}
        >
          <Select className="field min-w-0 flex-1" aria-label={t("configurazione.common.ruolo")} value={roleID} onChange={(e) => setRoleID(e.target.value)}>
            <option value="">{t("configurazione.admin.daiUnRuoloAl")}</option>
            {(roles.data?.items ?? [])
              .filter((r) => !group.roles.some((x) => x.id === r.id))
              .map((r) => (
                <option key={r.id} value={r.id}>{r.name}</option>
              ))}
          </Select>
          <button className="btn py-1.5" disabled={!roleID || assign.isPending}>{t("configurazione.admin.assegna")}</button>
        </form>
      </section>

      <section className="flex flex-col gap-2">
        <SectionTitle aside={<span className="font-mono text-[11.5px] text-muted tabular">{group.members.length}</span>}>{t("configurazione.admin.membri")}</SectionTitle>
        <ul className="m-0 flex max-h-[360px] list-none flex-col overflow-y-auto p-0">
          {group.members.map((m) => (
            <li key={m.id} className="flex items-center gap-2 border-b border-hair py-1.5">
              <span className="flex-1">
                {m.name}
                {m.status && m.status !== "active" && <span className="text-muted"> · {STATUS_LABELS[m.status] ?? m.status}</span>}
              </span>
              {!group.system && (
                <button type="button" className="text-[12px] text-muted underline hover:text-danger" onClick={() => remove.mutate(m.id)}>{t("configurazione.common.togli2")}</button>
              )}
            </li>
          ))}
        </ul>
        {!group.system && (
          <form
            className="flex flex-wrap gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              if (who) add.mutate();
            }}
          >
            <EntitySelect kind="member" className="field min-w-0 flex-1" aria-label={t("configurazione.common.persona")} noneLabel={t("configurazione.admin.aggiungiUnaPersona")} filter={(o) => !group.members.some((x) => x.id === o.value)} value={who} onChange={setWho} />
            <button className="btn py-1.5" disabled={!who || add.isPending}>{t("configurazione.common.aggiungi")}</button>
          </form>
        )}
      </section>
      <MutError errors={[add.error, remove.error, assign.error, unassign.error, del.error]} />
      {!group.system && (
        <div>
          <ConfirmButton
            label={t("configurazione.admin.eliminaIlGruppo")}
            confirm={t("configurazione.common.elimina")}
            danger
            disabled={inUse}
            title={inUse ? t("configurazione.admin.ilGruppoEIn") : undefined}
            pending={del.isPending}
            onConfirm={() => del.mutate()}
          />
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Ruoli sugli oggetti
// ---------------------------------------------------------------------------

function StakeholderTab() {
  const t = useT();
  const qc = useQueryClient();
  const list = useQuery({ queryKey: ["admin-stakeholder"], queryFn: adminApi.stakeholderRoles });
  const [key, setKey] = useState("");
  const [name, setName] = useState("");
  const [editing, setEditing] = useState<{ id: string; name: string } | null>(null);
  const create = useMutation({
    mutationFn: () => adminApi.createStakeholderRole({ key, name }),
    onSuccess: () => {
      setKey("");
      setName("");
      qc.invalidateQueries({ queryKey: ["admin-stakeholder"] });
    },
  });
  const rename = useMutation({
    mutationFn: (e: { id: string; name: string }) => adminApi.renameStakeholderRole(e.id, e.name),
    onSuccess: () => {
      setEditing(null);
      qc.invalidateQueries({ queryKey: ["admin-stakeholder"] });
    },
  });
  return (
    <div className="flex max-w-[960px] flex-col gap-6 px-4 py-4 md:px-7">
      <p className="m-0 max-w-[680px] text-muted">
        {t("configurazione.admin.sponsorPmoDemandManager")}
      </p>
      <ErrorNote error={list.error} />
      <table className="w-full border-collapse tabular">
        <thead>
          <tr className="label h-9 text-left">
            <th scope="col" className="border-b-2 border-rule pr-3">{t("configurazione.common.ruolo")}</th>
            <th scope="col" className="hidden border-b-2 border-rule px-3 sm:table-cell">{t("configurazione.admin.usatoDa")}</th>
            <th scope="col" className="border-b-2 border-rule pl-3 text-right">{t("configurazione.common.elementi")}</th>
          </tr>
        </thead>
        <tbody>
          {(list.data?.items ?? []).map((r) => (
            <tr key={r.id} className="border-b border-hair align-top">
              <td className="py-2.5 pr-3">
                {editing?.id === r.id ? (
                  <form
                    className="flex flex-wrap gap-2"
                    onSubmit={(e) => {
                      e.preventDefault();
                      rename.mutate(editing);
                    }}
                  >
                    <input className="field min-w-0 flex-1" aria-label={t("configurazione.admin.nuovoNome")} value={editing.name} onChange={(e) => setEditing({ id: r.id, name: e.target.value })} />
                    <button className="btn btn-primary py-1.5" disabled={rename.isPending}>{t("configurazione.common.salva")}</button>
                    <button type="button" className="btn py-1.5" onClick={() => setEditing(null)}>{t("configurazione.common.annulla")}</button>
                  </form>
                ) : (
                  <>
                    <span className="font-medium">{r.name}</span>{" "}
                    <button type="button" className="text-[12px] text-muted underline" onClick={() => setEditing({ id: r.id, name: r.name })}>{t("configurazione.admin.rinomina")}</button>
                    <span className="block font-mono text-[11.5px] text-muted">{r.key}</span>
                    <span className="block text-[12px] text-muted sm:hidden">{r.processes.join(", ")}</span>
                  </>
                )}
              </td>
              <td className="hidden px-3 py-2.5 text-[12.5px] sm:table-cell">{r.processes.length ? r.processes.join(", ") : <span className="text-muted">{t("configurazione.admin.nessunProcesso")}</span>}</td>
              <td className="py-2.5 pl-3 text-right">{r.elements}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <ErrorNote error={rename.error} />
      <form
        className="flex flex-col gap-3 sm:flex-row sm:items-end"
        onSubmit={(e) => {
          e.preventDefault();
          create.mutate();
        }}
      >
        <label className="flex flex-col gap-1.5 sm:w-[220px]">
          <span className="label">{t("configurazione.common.chiave")}</span>
          <input className="field font-mono" required pattern="[a-z][a-z0-9_]{0,62}" placeholder={t("configurazione.admin.esDataOwner")} value={key} onChange={(e) => setKey(e.target.value)} />
        </label>
        <label className="flex min-w-0 flex-1 flex-col gap-1.5">
          <span className="label">{t("configurazione.common.nome")}</span>
          <input className="field" required maxLength={80} placeholder={t("configurazione.admin.esDataOwner2")} value={name} onChange={(e) => setName(e.target.value)} />
        </label>
        <button className="btn btn-primary" disabled={create.isPending || !key || !name.trim()}>{t("configurazione.common.aggiungi")}</button>
      </form>
      <ErrorNote error={create.error} />
    </div>
  );
}

// ---------------------------------------------------------------------------
// Sicurezza
// ---------------------------------------------------------------------------

const POLICIES: { v: Security["mfa_policy"]; label: string; help: ReactNode }[] = [
  { v: "off", get label() { return tr("configurazione.admin.mfa.off"); }, get help() { return tr("configurazione.admin.mfa.offHelp"); } },
  { v: "admins", get label() { return tr("configurazione.admin.mfa.admins"); }, get help() { return tr("configurazione.admin.mfa.adminsHelp"); } },
  { v: "all", get label() { return tr("configurazione.admin.mfa.all"); }, get help() { return tr("configurazione.admin.mfa.allHelp"); } },
];

function SecurityTab() {
  const t = useT();
  const qc = useQueryClient();
  const sec = useQuery({ queryKey: ["admin-security"], queryFn: adminApi.security });
  const [choice, setChoice] = useState<Security["mfa_policy"] | null>(null);
  const save = useMutation({
    mutationFn: (p: Security["mfa_policy"]) => adminApi.setSecurity(p),
    onSuccess: (r) => {
      qc.setQueryData(["admin-security"], r.security);
      setChoice(null);
    },
  });
  const current = choice ?? sec.data?.mfa_policy ?? "off";
  return (
    <div className="flex max-w-[760px] flex-col gap-6 px-4 py-4 md:px-7">
      <section className="flex flex-col gap-3">
        <SectionTitle>{t("configurazione.admin.autenticazioneADueFattori")}</SectionTitle>
        <p className="m-0 text-muted">
          {t("configurazione.admin.codiceASeiCifre")}
        </p>
        <ErrorNote error={sec.error} />
        <fieldset className="m-0 flex flex-col gap-2 border-0 p-0">
          <legend className="sr-only">{t("configurazione.admin.politicaMfa")}</legend>
          {POLICIES.map((p) => (
            <label key={p.v} className="flex items-start gap-2">
              <input type="radio" name="mfa-policy" className="mt-0.5" checked={current === p.v} onChange={() => setChoice(p.v)} />
              <span>
                <span className="font-medium">{p.label}</span>
                <span className="block text-[12.5px] text-muted">{p.help}</span>
              </span>
            </label>
          ))}
        </fieldset>
        {current !== "off" && sec.data && (
          <p className="m-0 border-l-2 border-flag pl-3 text-[12.5px]">
            {choice && choice !== sec.data.mfa_policy
              ? t("configurazione.admin.salvandoLeSessioniCon")
              : t("configurazione.admin.pendingMfa", { count: sec.data.pending_mfa })}
          </p>
        )}
        <ErrorNote error={save.error} />
        {save.data && <p className="m-0 text-ok" role="status">{t("configurazione.admin.policySaved", { count: save.data.sessions_closed })}</p>}
        <div>
          <button className="btn btn-primary" disabled={!choice || choice === sec.data?.mfa_policy || save.isPending} onClick={() => choice && save.mutate(choice)}>
            {t("configurazione.common.salva")}
          </button>
        </div>
      </section>
      <GroupInheritanceSection />
      <section className="flex flex-col gap-2">
        <SectionTitle>{t("configurazione.admin.registro")}</SectionTitle>
        <p className="m-0">
          <Trans k="configurazione.admin.auditNote" slots={{ link: <Link to="/configurazione/audit" className="text-accent">{t("configurazione.admin.registroDiAudit")}</Link> }} />
        </p>
      </section>
    </div>
  );
}
