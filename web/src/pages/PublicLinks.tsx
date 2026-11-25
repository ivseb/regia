// Pagine pubbliche dei link monouso (piano, passo 15): accettare un invito (/invito/<token>)
// e scegliere una password (/password/<token>). Si aprono anche senza sessione.
import { useState, type FormEvent, type ReactNode } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { linksApi } from "../adminApi";
import { ErrorNote } from "../ui";
import { dateTime } from "./adminUi";
import { useT } from "../i18n/react";
import { Logo } from "../brand";
import { Trans } from "../i18n/Trans";

function Frame({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="flex min-h-screen flex-col">
      <header className="flex items-center gap-2.5 border-b border-rule px-4 py-3 md:px-7">
        <Logo />
        <span className="font-mono text-[12px]">Regia</span>
      </header>
      <main className="mx-auto flex w-full max-w-[480px] flex-col gap-5 px-4 py-10">
        <h1 className="m-0 border-b-2 border-rule pb-2 text-[26px] leading-tight font-bold">{title}</h1>
        {children}
      </main>
    </div>
  );
}

function PasswordFields({ password, setPassword, again, setAgain }: { password: string; setPassword: (v: string) => void; again: string; setAgain: (v: string) => void }) {
  const t = useT();
  return (
    <>
      <label className="flex flex-col gap-1.5">
        <span className="label">{t("auth.links.password")}</span>
        <input className="field" type="password" required minLength={12} autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} />
        <span className="text-[12px] text-muted">{t("auth.links.passwordHint")}</span>
      </label>
      <label className="flex flex-col gap-1.5">
        <span className="label">{t("auth.links.again")}</span>
        <input className="field" type="password" required autoComplete="new-password" value={again} onChange={(e) => setAgain(e.target.value)} />
        {again && again !== password && <span className="text-[12px] text-danger">{t("auth.links.mismatch")}</span>}
      </label>
    </>
  );
}

function Done({ children }: { children: ReactNode }) {
  const t = useT();
  return (
    <>
      <p role="status" className="m-0 border-l-2 border-ok pl-3">{children}</p>
      <a href="/" className="btn btn-primary justify-center py-2.5 no-underline">{t("auth.links.goLogin")}</a>
    </>
  );
}

export function InvitePage({ token }: { token: string }) {
  const t = useT();
  const info = useQuery({ queryKey: ["invitation", token], queryFn: () => linksApi.invitation(token), retry: false });
  const [name, setName] = useState<string | null>(null);
  const [password, setPassword] = useState("");
  const [again, setAgain] = useState("");
  const accept = useMutation({ mutationFn: () => linksApi.accept(token, { display_name: name ?? info.data?.display_name ?? "", password }) });
  const i = info.data;
  function submit(e: FormEvent) {
    e.preventDefault();
    accept.mutate();
  }
  if (accept.isSuccess) {
    return (
      <Frame title={t("auth.invite.acceptedTitle")}>
        <Done>
          {t("auth.invite.joined", { tenant: i?.tenant ?? "" })}{" "}
          {i?.sso_only ? t("auth.invite.nextSso") : i?.needs_password ? t("auth.invite.nextPassword") : t("auth.invite.nextCredentials")}
        </Done>
      </Frame>
    );
  }
  return (
    <Frame title={t("auth.invite.title")}>
      {info.isPending && <p className="m-0 text-muted">{t("auth.links.checking")}</p>}
      {info.error && <p role="alert" className="m-0 border-l-2 border-danger pl-3">{t("auth.invite.invalid")}</p>}
      {i && (
        <form onSubmit={submit} className="flex flex-col gap-4">
          <p className="m-0">
            <Trans
              k="auth.invite.invitedAs"
              slots={{ tenant: <span className="font-semibold">{i.tenant}</span>, email: <span className="font-mono text-[12.5px]">{i.email}</span> }}
            />
          </p>
          {i.needs_password && (
            <>
              <label className="flex flex-col gap-1.5">
                <span className="label">{t("auth.invite.name")}</span>
                <input className="field" required maxLength={120} autoComplete="name" value={name ?? i.display_name} onChange={(e) => setName(e.target.value)} />
              </label>
              <PasswordFields password={password} setPassword={setPassword} again={again} setAgain={setAgain} />
            </>
          )}
          {!i.needs_password && (
            <p className="m-0 text-muted">
              {i.sso_only ? t("auth.invite.afterSso") : t("auth.invite.existing")}
            </p>
          )}
          <span className="text-[12px] text-muted">{t("auth.links.expires", { date: dateTime(i.expires_at) })}</span>
          <ErrorNote error={accept.error} />
          <button className="btn btn-primary justify-center py-2.5" disabled={accept.isPending || (i.needs_password && (password.length < 12 || password !== again))}>
            {accept.isPending ? t("auth.invite.accepting") : t("auth.invite.accept")}
          </button>
        </form>
      )}
    </Frame>
  );
}

export function PasswordPage({ token }: { token: string }) {
  const t = useT();
  const info = useQuery({ queryKey: ["password-link", token], queryFn: () => linksApi.reset(token), retry: false });
  const [password, setPassword] = useState("");
  const [again, setAgain] = useState("");
  const save = useMutation({ mutationFn: () => linksApi.setPassword(token, password) });
  function submit(e: FormEvent) {
    e.preventDefault();
    save.mutate();
  }
  if (save.isSuccess) {
    return (
      <Frame title={t("auth.reset.doneTitle")}>
        <Done>{t("auth.reset.done")}</Done>
      </Frame>
    );
  }
  return (
    <Frame title={t("auth.reset.title")}>
      {info.isPending && <p className="m-0 text-muted">{t("auth.links.checking")}</p>}
      {info.error && <p role="alert" className="m-0 border-l-2 border-danger pl-3">{t("auth.reset.invalid")}</p>}
      {info.data && (
        <form onSubmit={submit} className="flex flex-col gap-4">
          <p className="m-0">
            <Trans
              k="auth.reset.account"
              params={{ date: dateTime(info.data.expires_at) }}
              slots={{ email: <span className="font-mono text-[12.5px]">{info.data.email}</span> }}
            />
          </p>
          <PasswordFields password={password} setPassword={setPassword} again={again} setAgain={setAgain} />
          <ErrorNote error={save.error} />
          <button className="btn btn-primary justify-center py-2.5" disabled={save.isPending || password.length < 12 || password !== again}>
            {save.isPending ? t("auth.reset.saving") : t("auth.reset.save")}
          </button>
        </form>
      )}
    </Frame>
  );
}
