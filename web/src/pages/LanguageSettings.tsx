import { Select } from "../select";
import { useId } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import type { LanguageInfo } from "../api";
import { languageApi } from "../languageApi";
import { setLang } from "../i18n/core";
import { LANGUAGES, isLang } from "../i18n/languages";
import { useT } from "../i18n/react";
import type { Lang } from "../i18n/types";
import { useMe } from "../shell";
import { ErrorNote, SectionTitle } from "../ui";

// Profilo, sezione «Lingua»: la scelta personale (nulla = segue l'organizzazione) e, per chi
// ha tenant.admin, la lingua predefinita dell'organizzazione (replica/lingua.md).

const nameOf = (l: string | null | undefined) => LANGUAGES.find((x) => x.key === l)?.name ?? "";
const asLang = (v: string): Lang | null => (isLang(v) ? v : null);

/** Dopo un salvataggio: la lingua nuova vale subito, senza ricaricare. */
function useApplyLanguage() {
  const qc = useQueryClient();
  return (r: LanguageInfo) => {
    void setLang(r.effective);
    void qc.invalidateQueries({ queryKey: ["me"] });
    void qc.invalidateQueries({ queryKey: ["type-names"] });
  };
}

/** Scelta personale della lingua. */
export function LanguageSection({ tenantName }: { tenantName: string }) {
  const t = useT();
  const id = useId();
  const me = useMe();
  const apply = useApplyLanguage();
  const info = me.data?.language;
  const save = useMutation({ mutationFn: (l: Lang | null) => languageApi.setMine(l), onSuccess: apply });
  // Se non si sceglie, vale il predefinito dell'organizzazione o, in mancanza, la lingua del browser.
  const followName = nameOf(info?.tenant ?? (info?.source === "browser" || info?.source === "default" ? info.effective : null));
  const followLabel = info?.tenant ? t("profile.language.follow", { name: followName }) : t("profile.language.followBrowser", { name: followName });
  return (
    <section className="flex flex-col gap-4">
      <SectionTitle>{t("profile.language.title")}</SectionTitle>
      <p className="m-0 max-w-[640px] text-muted">{t("profile.language.text", { tenant: tenantName })}</p>
      <div className="flex max-w-[360px] flex-col gap-1.5">
        <label htmlFor={id} className="label">{t("profile.language.title")}</label>
        <Select
          id={id}
          className="field"
          disabled={!info || save.isPending}
          value={info?.member ?? ""}
          onChange={(e) => save.mutate(asLang(e.target.value))}
        >
          <option value="">{followLabel}</option>
          {LANGUAGES.map((l) => (
            <option key={l.key} value={l.key} lang={l.key}>{l.name}</option>
          ))}
        </Select>
        {!info?.member && <span className="text-[12px] text-muted">{t("profile.language.followHint")}</span>}
      </div>
      <ErrorNote error={save.error} />
    </section>
  );
}

/** Lingua predefinita dell'organizzazione, per chi ha tenant.admin. */
export function TenantLanguageSection({ tenantName }: { tenantName: string }) {
  const t = useT();
  const id = useId();
  const me = useMe();
  const apply = useApplyLanguage();
  const info = me.data?.language;
  const save = useMutation({ mutationFn: (l: Lang | null) => languageApi.setTenant(l), onSuccess: apply });
  return (
    <section className="flex flex-col gap-4">
      <SectionTitle>{t("profile.language.tenantTitle")}</SectionTitle>
      <p className="m-0 max-w-[640px] text-muted">{t("profile.language.tenantText", { tenant: tenantName })}</p>
      <div className="flex max-w-[360px] flex-col gap-1.5">
        <label htmlFor={id} className="label">{t("profile.language.tenantLabel")}</label>
        <Select
          id={id}
          className="field"
          disabled={!info || save.isPending}
          value={info?.tenant ?? ""}
          onChange={(e) => save.mutate(asLang(e.target.value))}
        >
          <option value="">{t("profile.language.tenantNone")}</option>
          {LANGUAGES.map((l) => (
            <option key={l.key} value={l.key} lang={l.key}>{l.name}</option>
          ))}
        </Select>
      </div>
      <ErrorNote error={save.error} />
    </section>
  );
}
