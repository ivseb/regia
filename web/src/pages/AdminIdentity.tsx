// Configurazione → Persone e accessi → Identità aziendale: chi entra e in quali gruppi lo
// decide l'IdP. Creazione al primo accesso OIDC, claim dei gruppi, mappatura esplicita dei
// gruppi dell'IdP sui gruppi di Regia e token SCIM. Le regole stanno in
// internal/platform/admin/idp.go; guida in docs/identita-aziendale.md.
import { useEffect, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { adminApi, idpApi, type IdPGroup, type IdPSettings } from "../adminApi";
import { ErrorNote, SectionTitle, ago } from "../ui";
import { Select } from "../select";
import { ConfirmButton, CopyBox, MutError, Tag } from "./adminUi";
import { useT } from "../i18n/react";
import { tr } from "../i18n/core";

const SOURCE: Record<IdPGroup["source"], string> = {
  get scim() { return tr("identita.source.scim"); },
  get oidc() { return tr("identita.source.oidc"); },
  get manual() { return tr("identita.source.manual"); },
};

export function IdentityTab() {
  const t = useT();
  const qc = useQueryClient();
  const ov = useQuery({ queryKey: ["admin-idp"], queryFn: idpApi.overview });
  const refresh = () => {
    for (const k of ["admin-idp", "admin-groups", "admin-members", "admin-member"]) qc.invalidateQueries({ queryKey: [k] });
  };
  if (ov.error) return <div className="px-4 py-4 md:px-7"><ErrorNote error={ov.error} /></div>;
  if (!ov.data) return null;
  const x = ov.data;
  return (
    <div className="flex max-w-[860px] flex-col gap-7 px-4 py-4 md:px-7">
      <p className="m-0 text-muted">{t("identita.intro")}</p>
      {!x.oidc.enabled && <p className="m-0 border-l-2 border-flag pl-3 text-[12.5px]">{t("identita.oidcOff")}</p>}
      {x.all_members_roles.length > 0 && (
        <p className="m-0 border-l-2 border-danger pl-3 text-[12.5px]" role="note">
          {t("identita.allMembersWarning", { roles: x.all_members_roles.join(", ") })}
        </p>
      )}
      {x.only_viewer > 0 && (
        <p className="m-0 border-l-2 border-flag pl-3 text-[12.5px]" role="note">
          {t("identita.onlyViewer", { count: x.only_viewer })}{" "}
          <Link to="/configurazione/persone" search={{ tab: "membri" }} className="text-accent">{t("identita.onlyViewerLink")}</Link>
        </p>
      )}
      <SettingsSection settings={x.settings} onSaved={refresh} />
      <GroupsSection groups={x.groups} scimActive={x.scim_active} claim={x.settings.groups_claim} onChange={refresh} />
      <ScimSection baseURL={x.scim_base_url} tokens={x.scim_tokens} onChange={refresh} />
    </div>
  );
}

function SettingsSection({ settings, onSaved }: { settings: IdPSettings; onSaved: () => void }) {
  const t = useT();
  const [on, setOn] = useState(settings.provisioning);
  const [domains, setDomains] = useState(settings.allowed_domains.join("\n"));
  const [claim, setClaim] = useState(settings.groups_claim);
  useEffect(() => {
    setOn(settings.provisioning);
    setDomains(settings.allowed_domains.join("\n"));
    setClaim(settings.groups_claim);
  }, [settings]);
  const save = useMutation({
    mutationFn: () =>
      idpApi.setSettings({ provisioning: on, groups_claim: claim.trim(), allowed_domains: domains.split(/[\s,;]+/).map((d) => d.trim()).filter(Boolean) }),
    onSuccess: onSaved,
  });
  return (
    <section className="flex flex-col gap-3">
      <SectionTitle>{t("identita.firstLogin")}</SectionTitle>
      <label className="flex items-start gap-2">
        <input type="checkbox" className="mt-0.5" checked={on} onChange={(e) => setOn(e.target.checked)} />
        <span>
          <span className="font-medium">{t("identita.provisioning")}</span>
          <span className="block text-[12.5px] text-muted">{t("identita.provisioningHelp")}</span>
        </span>
      </label>
      <label className="flex flex-col gap-1">
        <span className="label">{t("identita.domains")}</span>
        <textarea className="field min-h-[72px] font-mono text-[12.5px]" value={domains} onChange={(e) => setDomains(e.target.value)} placeholder={t("identita.domainsPlaceholder")} />
        <span className="text-[12px] text-muted">{t("identita.domainsHelp")}</span>
      </label>
      <label className="flex flex-col gap-1">
        <span className="label">{t("identita.claim")}</span>
        <input className="field max-w-xs font-mono text-[12.5px]" value={claim} onChange={(e) => setClaim(e.target.value)} />
        <span className="text-[12px] text-muted">{t("identita.claimHelp")}</span>
      </label>
      <ErrorNote error={save.error} />
      {save.isSuccess && <p className="m-0 text-ok" role="status">{t("identita.saved")}</p>}
      <div>
        <button className="btn btn-primary" disabled={save.isPending} onClick={() => save.mutate()}>{t("configurazione.common.salva")}</button>
      </div>
    </section>
  );
}

function GroupsSection({ groups, scimActive, claim, onChange }: { groups: IdPGroup[]; scimActive: boolean; claim: string; onChange: () => void }) {
  const t = useT();
  const regia = useQuery({ queryKey: ["admin-groups"], queryFn: adminApi.groups });
  const [ext, setExt] = useState("");
  const [name, setName] = useState("");
  const add = useMutation({
    mutationFn: () => idpApi.createGroup(ext.trim(), name.trim()),
    onSuccess: () => {
      setExt("");
      setName("");
      onChange();
    },
  });
  const map = useMutation({ mutationFn: (v: { idp: string; group: string }) => idpApi.map(v.idp, v.group), onSuccess: onChange });
  const unmap = useMutation({ mutationFn: (id: string) => idpApi.unmap(id), onSuccess: onChange });
  const del = useMutation({ mutationFn: (id: string) => idpApi.deleteGroup(id), onSuccess: onChange });
  const options = (regia.data?.items ?? []).filter((g) => !g.system);
  return (
    <section className="flex flex-col gap-3">
      <SectionTitle aside={<span className="font-mono text-[11.5px] text-muted tabular">{groups.length}</span>}>{t("identita.groups")}</SectionTitle>
      <p className="m-0 text-[12.5px] text-muted">{t("identita.groupsHelp")}</p>
      <p className="m-0 text-[12.5px]">{scimActive ? t("identita.sourceScim") : t("identita.sourceClaim", { claim })}</p>
      <ul className="m-0 flex list-none flex-col p-0" aria-label={t("identita.groups")}>
        {groups.map((g) => (
          <li key={g.id} className="flex flex-col gap-1.5 border-b border-hair py-2.5">
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-medium">{g.display_name || g.external_id}</span>
              {g.display_name && g.display_name !== g.external_id && <code className="break-all font-mono text-[11.5px] text-muted">{g.external_id}</code>}
              <Tag>{SOURCE[g.source]}</Tag>
              <span className="ml-auto font-mono text-[11.5px] text-muted tabular">{t("identita.people", { count: g.members })}</span>
            </div>
            <div className="flex flex-wrap items-center gap-2 text-[12.5px]">
              {g.mappings.length === 0 && <span className="text-muted">{t("identita.unmapped")}</span>}
              {g.mappings.map((m) => (
                <span key={m.id} className="inline-flex items-center gap-1.5">
                  <span aria-hidden="true" className="text-muted">→</span>
                  <span className="font-medium">{m.group_name}</span>
                  <button type="button" className="text-[12px] text-muted underline hover:text-danger" disabled={unmap.isPending} onClick={() => unmap.mutate(m.id)}>
                    {t("configurazione.common.togli2")}
                  </button>
                </span>
              ))}
            </div>
            <div className="flex flex-wrap items-center gap-2">
              <Select
                className="field min-w-0 flex-1 py-1 text-[12.5px] sm:max-w-xs"
                aria-label={t("identita.mapTo", { group: g.display_name || g.external_id })}
                value=""
                onChange={(e) => e.target.value && map.mutate({ idp: g.id, group: e.target.value })}
              >
                <option value="">{t("identita.mapToPlaceholder")}</option>
                {options.filter((o) => !g.mappings.some((m) => m.group_id === o.id)).map((o) => (
                  <option key={o.id} value={o.id}>{o.name}</option>
                ))}
              </Select>
              {g.source !== "scim" && (
                <ConfirmButton label={t("identita.forget")} confirm={t("identita.forgetConfirm")} danger pending={del.isPending} onConfirm={() => del.mutate(g.id)} />
              )}
            </div>
          </li>
        ))}
        {groups.length === 0 && <li className="py-1.5 text-muted">{t("identita.noGroups")}</li>}
      </ul>
      <form
        className="flex flex-wrap gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          if (ext.trim()) add.mutate();
        }}
      >
        <input className="field min-w-0 flex-1 font-mono text-[12.5px]" aria-label={t("identita.externalId")} placeholder={t("identita.externalId")} value={ext} onChange={(e) => setExt(e.target.value)} />
        <input className="field min-w-0 flex-1" aria-label={t("identita.displayName")} placeholder={t("identita.displayName")} value={name} onChange={(e) => setName(e.target.value)} />
        <button className="btn py-1.5" disabled={!ext.trim() || add.isPending}>{t("identita.addGroup")}</button>
      </form>
      <MutError errors={[add.error, map.error, unmap.error, del.error]} />
    </section>
  );
}

function ScimSection({ baseURL, tokens, onChange }: { baseURL: string; tokens: { id: string; name: string; hint: string; created_at: string; last_used_at: string | null }[]; onChange: () => void }) {
  const t = useT();
  const [name, setName] = useState("");
  const [secret, setSecret] = useState<string | null>(null);
  const create = useMutation({
    mutationFn: () => idpApi.createToken(name.trim()),
    onSuccess: (r) => {
      setSecret(r.token);
      setName("");
      onChange();
    },
  });
  const revoke = useMutation({ mutationFn: (id: string) => idpApi.revokeToken(id), onSuccess: onChange });
  return (
    <section className="flex flex-col gap-3">
      <SectionTitle>{t("identita.scim")}</SectionTitle>
      <p className="m-0 text-[12.5px] text-muted">{t("identita.scimHelp")}</p>
      <div className="flex flex-col gap-1">
        <span className="label">{t("identita.scimURL")}</span>
        <code className="block select-all break-all font-mono text-[12.5px]">{baseURL}</code>
      </div>
      {secret && <CopyBox title={t("identita.tokenOnce")} value={secret} note={t("identita.tokenOnceNote")} onDone={() => setSecret(null)} />}
      <ul className="m-0 flex list-none flex-col p-0" aria-label={t("identita.tokens")}>
        {tokens.map((k) => (
          <li key={k.id} className="flex flex-wrap items-center gap-2 border-b border-hair py-2">
            <span className="font-medium">{k.name}</span>
            <code className="font-mono text-[11.5px] text-muted">{k.hint}…</code>
            <span className="text-[12px] text-muted">{k.last_used_at ? t("identita.lastUsed", { when: ago(k.last_used_at) }) : t("identita.neverUsed")}</span>
            <span className="ml-auto">
              <ConfirmButton label={t("identita.revoke")} confirm={t("identita.revokeConfirm")} danger pending={revoke.isPending} onConfirm={() => revoke.mutate(k.id)} />
            </span>
          </li>
        ))}
        {tokens.length === 0 && <li className="py-1.5 text-muted">{t("identita.noTokens")}</li>}
      </ul>
      <form
        className="flex flex-wrap gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          create.mutate();
        }}
      >
        <input className="field min-w-0 flex-1 sm:max-w-xs" aria-label={t("identita.tokenName")} placeholder={t("identita.tokenName")} value={name} onChange={(e) => setName(e.target.value)} />
        <button className="btn py-1.5" disabled={create.isPending}>{t("identita.newToken")}</button>
      </form>
      <MutError errors={[create.error, revoke.error]} />
    </section>
  );
}
