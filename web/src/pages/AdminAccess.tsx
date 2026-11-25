// Persone e accessi: da dove arrivano i permessi di un membro (catena gruppo -> ruolo ->
// permesso) e l'opzione del tenant che li propaga lungo la gerarchia dei gruppi.
import { Fragment } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { adminApi, type AccessGrant } from "../adminApi";
import { ErrorNote, SectionTitle } from "../ui";
import { Tag } from "./adminUi";
import { useT } from "../i18n/react";
import { tr } from "../i18n/core";

const SOURCE: Record<AccessGrant["source"], string> = {
  get direct() { return tr("configurazione.adminAccess.source.direct"); },
  get group() { return tr("configurazione.adminAccess.source.group"); },
  get inherited() { return tr("configurazione.adminAccess.source.inherited"); },
};

/** Catena "Squadra → Dipartimento → ruolo", leggibile anche a 375 px (va a capo). */
function Chain({ g }: { g: AccessGrant }) {
  const t = useT();
  const steps = g.source === "direct" ? [tr("configurazione.adminAccess.personCap")] : g.chain;
  return (
    <span className="flex flex-wrap items-center gap-x-1.5 gap-y-0.5 text-[12.5px]">
      {steps.map((s, i) => (
        <Fragment key={s + i}>
          {i > 0 && (
            <span className="text-muted" aria-hidden="true">
              →
            </span>
          )}
          <span className={i === 0 ? "text-muted" : i === steps.length - 1 ? "font-medium" : "text-muted"}>{s}</span>
        </Fragment>
      ))}
      <span className="text-muted" aria-hidden="true">
        →
      </span>
      <span className="font-medium">{g.role}</span>
      {g.space && <Tag tone="muted">{t("configurazione.adminAccess.space", { space: g.space })}</Tag>}
    </span>
  );
}

export function AccessExplainSection({ id }: { id: string }) {
  const t = useT();
  const q = useQuery({ queryKey: ["admin-member-access", id], queryFn: () => adminApi.memberAccess(id) });
  const roles = useQuery({ queryKey: ["admin-roles"], queryFn: adminApi.roles });
  const names = new Map((roles.data?.permissions ?? []).map((p) => [p.key, p.name]));
  if (q.error) return <ErrorNote error={q.error} />;
  if (!q.data) return null;
  const x = q.data;
  return (
    <section className="flex flex-col gap-2">
      <SectionTitle>{t("configurazione.adminAccess.daDoveArrivanoI")}</SectionTitle>
      <p className="m-0 text-[12.5px] text-muted">
        {x.inheritance
          ? t("configurazione.adminAccess.iRuoliDiUn")
          : t("configurazione.adminAccess.iRuoliDiUn2")}
      </p>
      <ul className="m-0 flex list-none flex-col p-0">
        {x.grants.map((g, i) => (
          <li key={i} className="flex flex-col gap-0.5 border-b border-hair py-1.5">
            <Chain g={g} />
            <span className="text-[12px] text-muted">
              {SOURCE[g.source]}
              {g.source !== "direct" && g.chain.length > 0 ? ` ${g.chain[g.chain.length - 1]}` : ""}
            </span>
          </li>
        ))}
        {x.grants.length === 0 && <li className="py-1.5 text-muted">{t("configurazione.adminAccess.nessunRuolo")}</li>}
      </ul>
      {x.permissions.length > 0 && (
        <details className="text-[12.5px]">
          <summary className="cursor-pointer text-accent">{t("configurazione.adminAccess.effectivePermissions", { count: x.permissions.length })}</summary>
          <ul className="m-0 mt-1 flex list-none flex-col p-0">
            {x.permissions.map((p) => (
              <li key={p.permission + (p.space ?? "")} className="flex flex-col border-b border-hair py-1">
                <span>
                  <span className="font-medium">{p.permission === "*" ? t("configurazione.adminAccess.tutto") : (names.get(p.permission) ?? p.permission)}</span>
                  {p.space && <span className="text-muted">{t("configurazione.adminAccess.onlyInSpace", { space: p.space })}</span>}
                </span>
                <span className="text-[12px] text-muted">
                  {p.from.map((i) => x.grants[i]).map((g) => `${g.source === "direct" ? t("configurazione.adminAccess.person") : g.chain.join(" → ")} → ${g.role}`).join(" · ")}
                </span>
              </li>
            ))}
          </ul>
        </details>
      )}
    </section>
  );
}

export function GroupInheritanceSection() {
  const t = useT();
  const qc = useQueryClient();
  const sec = useQuery({ queryKey: ["admin-security"], queryFn: adminApi.security });
  const set = useMutation({
    mutationFn: (on: boolean) => adminApi.setGroupInheritance(on),
    onSuccess: (r) => {
      qc.setQueryData(["admin-security"], r.security);
      qc.invalidateQueries({ queryKey: ["admin-member-access"] });
    },
  });
  const on = sec.data?.group_inheritance ?? true;
  return (
    <section className="flex flex-col gap-2">
      <SectionTitle>{t("configurazione.adminAccess.permessiLungoIGruppi")}</SectionTitle>
      <label className="flex items-start gap-2">
        <input type="checkbox" className="mt-0.5" checked={on} disabled={!sec.data || set.isPending} onChange={(e) => set.mutate(e.target.checked)} />
        <span>
          <span className="font-medium">{t("configurazione.adminAccess.iRuoliDiUn3")}</span>
          <span className="block text-[12.5px] text-muted">
            {t("configurazione.adminAccess.unRuoloDatoAl")}
          </span>
        </span>
      </label>
      <ErrorNote error={sec.error ?? set.error} />
    </section>
  );
}
