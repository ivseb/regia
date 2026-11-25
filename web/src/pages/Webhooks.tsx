// Configurazione → Integrazioni → Webhook (piano, passo 15): URL, eventi, filtro con il
// linguaggio delle condizioni, segreto per la firma, consegne con stato, prova e reinvio.
import { useEffect, useRef, useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError, type FactInfo } from "../api";
import { Select } from "../select";
import { useTypeNames } from "../typesApi";
import { ConditionBuilder } from "./processEditor/ConditionBuilder";
import { webhookApi, type Delivery, type Webhook, type WebhookEvent } from "../adminApi";
import { PageHeader } from "../shell";
import { ErrorNote, SectionTitle, ago } from "../ui";
import { ConfirmButton, CopyBox, MutError, SidePanel, Tag, dateTime } from "./adminUi";
import { useT } from "../i18n/react";
import { tr } from "../i18n/core";
import { Trans } from "../i18n/Trans";

const NEW = "nuovo";

const STATUS: Record<Delivery["status"], { label: string; tone: "ok" | "flag" | "danger" }> = {
  delivered: { get label() { return tr("configurazione.webhooks.status.delivered"); }, tone: "ok" },
  pending: { get label() { return tr("configurazione.webhooks.status.pending"); }, tone: "flag" },
  failed: { get label() { return tr("configurazione.webhooks.status.failed"); }, tone: "danger" },
};

export function Webhooks({ selected }: { selected?: string }) {
  const t = useT();
  const navigate = useNavigate();
  const select = (sel?: string) => navigate({ to: "/configurazione/webhook", search: { sel } });
  const list = useQuery({ queryKey: ["webhooks"], queryFn: webhookApi.list, refetchInterval: 15_000 });
  const [secret, setSecret] = useState<{ name: string; value: string } | null>(null);
  const panel = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!selected || window.innerWidth >= 768) return;
    // Dopo il ripristino dello scroll del router, che riporta la pagina in cima.
    const t = setTimeout(() => panel.current?.scrollIntoView({ block: "start" }), 80);
    return () => clearTimeout(t);
  }, [selected]);

  if (list.error instanceof ApiError && list.error.status === 403) {
    return (
      <>
        <PageHeader path={t("configurazione.path.webhooks")} title={t("configurazione.webhooks.webhook")} />
        <p className="px-4 text-muted md:px-7">{t("configurazione.common.serveIlPermessoAmministrare")}</p>
      </>
    );
  }
  const items = list.data?.items ?? [];
  const hook = items.find((w) => w.id === selected);
  const events = list.data?.events ?? [];
  return (
    <>
      <PageHeader
        path={t("configurazione.path.webhooks")}
        title={t("configurazione.webhooks.webhook")}
        actions={<button className="btn btn-primary" onClick={() => select(NEW)}>{t("configurazione.webhooks.nuovoWebhook")}</button>}
      />
      <div className="flex flex-wrap">
        <div className="flex min-w-0 flex-[999_1_520px] flex-col gap-5 px-4 pb-8 md:px-7">
          <p className="m-0 max-w-[680px] text-muted">
            {t("configurazione.webhooks.regiaInviaUnaRichiesta")}
          </p>
          {secret && (
            <CopyBox
              title={t("configurazione.webhooks.secretOf", { name: secret.name })}
              value={secret.value}
              note={t("configurazione.webhooks.serveAlDestinatarioPer")}
              onDone={() => setSecret(null)}
            />
          )}
          <ErrorNote error={list.error} />
          <ul className="m-0 flex list-none flex-col p-0">
            {items.map((w) => (
              <HookRow key={w.id} w={w} events={events} on={w.id === selected} onSelect={() => select(w.id)} />
            ))}
            {!list.isPending && items.length === 0 && <li className="py-8 text-muted">{t("configurazione.webhooks.nessunWebhookCreaneUno")}</li>}
          </ul>
          <SignatureHelp version={list.data?.version ?? 1} />
        </div>
        {selected && list.data && (
          <div ref={panel} className="flex min-w-0 scroll-mt-14 flex-[1_1_440px]">
            <SidePanel label={t("configurazione.webhooks.dettaglioDelWebhook")} onClose={() => select(undefined)}>
              {selected === NEW ? (
                <HookForm
                  events={events}
                  allowPrivate={list.data.allow_private}
                  onSaved={(id, name, s) => {
                    if (s) setSecret({ name, value: s });
                    select(id);
                  }}
                  onCancel={() => select(undefined)}
                />
              ) : hook ? (
                <HookPanel key={hook.id} w={hook} events={events} allowPrivate={list.data.allow_private} onSecret={(v) => setSecret({ name: hook.name, value: v })} onDeleted={() => select(undefined)} />
              ) : (
                <p className="text-muted">{t("configurazione.webhooks.webhookInesistente")}</p>
              )}
            </SidePanel>
          </div>
        )}
      </div>
    </>
  );
}

function HookRow({ w, events, on, onSelect }: { w: Webhook; events: WebhookEvent[]; on: boolean; onSelect: () => void }) {
  const t = useT();
  return (
    <li className={`border-b border-hair py-2.5 ${on ? "bg-sel" : ""}`}>
      <button className="flex w-full flex-wrap items-baseline gap-x-2 text-left" aria-current={on ? "true" : undefined} onClick={onSelect}>
        <span className="font-medium hover:underline">{w.name}</span>
        {!w.active && <Tag>{t("configurazione.webhooks.disattivato")}</Tag>}
        {w.failing > 0 && <Tag tone="danger">{t("configurazione.webhooks.failedIn24", { count: w.failing })}</Tag>}
        {w.pending > 0 && <Tag tone="flag">{t("configurazione.webhooks.pendingCount", { count: w.pending })}</Tag>}
        <span className="ml-auto text-[12px] text-muted">
          {w.last_delivery_at ? `${STATUS[w.last_status ?? "pending"].label} ${ago(w.last_delivery_at)}` : t("configurazione.webhooks.nessunaConsegna")}
        </span>
      </button>
      <span className="block truncate font-mono text-[11.5px] text-muted" title={w.url}>{w.url}</span>
      <span className="block text-[12px] text-muted">
        {w.events.map((e) => events.find((x) => x.key === e)?.name ?? e).join(", ")}
        {w.filter && <Trans k="configurazione.webhooks.filterSuffix" slots={{ filter: <span className="font-mono">{w.filter}</span> }} />}
      </span>
    </li>
  );
}

function HookForm({
  w,
  events,
  allowPrivate,
  onSaved,
  onCancel,
}: {
  w?: Webhook;
  events: WebhookEvent[];
  allowPrivate: boolean;
  onSaved: (id: string, name: string, secret?: string) => void;
  onCancel?: () => void;
}) {
  const t = useT();
  const qc = useQueryClient();
  const [name, setName] = useState(w?.name ?? "");
  const [url, setUrl] = useState(w?.url ?? "https://");
  const [chosen, setChosen] = useState<string[]>(w?.events ?? ["element.phase_changed"]);
  const [filter, setFilter] = useState(w?.filter ?? "");
  const [filterType, setFilterType] = useState("");
  const types = useTypeNames();
  const [active, setActive] = useState(w?.active ?? true);
  const save = useMutation({
    mutationFn: async () => {
      const body = { name, url, events: chosen, filter, active };
      if (w) {
        await webhookApi.update(w.id, body);
        return { id: w.id, secret: undefined };
      }
      return webhookApi.create(body);
    },
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: ["webhooks"] });
      onSaved(r.id, name, r.secret);
    },
  });
  const err = save.error instanceof ApiError ? save.error : null;
  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      {!w && <SectionTitle>{t("configurazione.webhooks.nuovoWebhook")}</SectionTitle>}
      <label className="flex flex-col gap-1.5">
        <span className="label">{t("configurazione.common.nome")}</span>
        <input className="field" required maxLength={80} placeholder={t("configurazione.webhooks.esOrchestratoreDeiRilasci")} value={name} onChange={(e) => setName(e.target.value)} />
      </label>
      <label className="flex flex-col gap-1.5">
        <span className="label">URL</span>
        <input className="field font-mono" required type="url" value={url} aria-invalid={err?.field === "url"} onChange={(e) => setUrl(e.target.value)} />
        <span className="text-[12px] text-muted">
          {allowPrivate
            ? t("configurazione.webhooks.protezioneControLeRichieste")
            : t("configurazione.webhooks.soloHttpsVersoIndirizzi")}
        </span>
      </label>
      <fieldset className="m-0 flex flex-col gap-1.5 border-0 p-0">
        <legend className="label mb-1.5 p-0">{t("configurazione.webhooks.eventi")}</legend>
        {events.map((ev) => (
          <label key={ev.key} className="flex items-start gap-2">
            <input
              type="checkbox"
              className="mt-0.5"
              checked={chosen.includes(ev.key)}
              onChange={(e) => setChosen((c) => (e.target.checked ? [...c, ev.key] : c.filter((x) => x !== ev.key)))}
            />
            <span>
              <span className="font-medium">{ev.name}</span> <span className="font-mono text-[11px] text-muted">{ev.key}</span>
              <span className="block text-[12px] text-muted">{ev.description}</span>
            </span>
          </label>
        ))}
      </fieldset>
      <div className="flex flex-col gap-2">
        <label className="flex flex-col gap-1.5">
          <span className="label">{t("casa.webhooks.filterType")}</span>
          <Select className="field" value={filterType} onChange={(e) => setFilterType(e.target.value)}>
            <option value="">{t("casa.webhooks.allTypes")}</option>
            {types.items.filter((x) => !x.archived).map((x) => (
              <option key={x.key} value={x.key}>
                {x.name}
              </option>
            ))}
          </Select>
        </label>
        <ConditionBuilder
          label={t("configurazione.webhooks.filtroSullElementoFacoltativo")}
          value={filter}
          onChange={setFilter}
          facts={filterType ? undefined : BASE_FACTS()}
          type={filterType}
          empty={t("casa.webhooks.allEvents")}
        />
      </div>
      <label className="flex items-center gap-2">
        <input type="checkbox" checked={active} onChange={(e) => setActive(e.target.checked)} />
        {t("configurazione.webhooks.attivo")}
      </label>
      <ErrorNote error={save.error} />
      <div className="flex flex-wrap gap-2">
        <button className="btn btn-primary" disabled={save.isPending || !name.trim() || chosen.length === 0}>{w ? t("configurazione.common.salva") : t("configurazione.webhooks.creaIlWebhook")}</button>
        {onCancel && <button type="button" className="btn" onClick={onCancel}>{t("configurazione.common.annulla")}</button>}
      </div>
    </form>
  );
}

/** Dati comuni a ogni elemento, per il filtro quando non si sceglie un tipo. */
const BASE_FACTS = (): FactInfo[] => [
  { key: "tipo", name: tr("casa.webhooks.factType"), kind: "text", source: "base" },
  { key: "stato", name: tr("casa.webhooks.factState"), kind: "text", source: "base" },
  { key: "categoria", name: tr("casa.webhooks.factCategory"), kind: "text", source: "base" },
  { key: "spazio", name: tr("casa.webhooks.factSpace"), kind: "text", source: "base" },
];

function HookPanel({
  w,
  events,
  allowPrivate,
  onSecret,
  onDeleted,
}: {
  w: Webhook;
  events: WebhookEvent[];
  allowPrivate: boolean;
  onSecret: (v: string) => void;
  onDeleted: () => void;
}) {
  const t = useT();
  const qc = useQueryClient();
  const deliveries = useQuery({ queryKey: ["webhook-deliveries", w.id], queryFn: () => webhookApi.deliveries(w.id), refetchInterval: 5_000 });
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["webhooks"] });
    qc.invalidateQueries({ queryKey: ["webhook-deliveries", w.id] });
  };
  const ping = useMutation({ mutationFn: () => webhookApi.ping(w.id), onSuccess: refresh });
  const rotate = useMutation({ mutationFn: () => webhookApi.rotate(w.id), onSuccess: (r) => { onSecret(r.secret); refresh(); } });
  const del = useMutation({ mutationFn: () => webhookApi.remove(w.id), onSuccess: () => { qc.invalidateQueries({ queryKey: ["webhooks"] }); onDeleted(); } });
  const redeliver = useMutation({ mutationFn: webhookApi.redeliver, onSuccess: refresh });
  return (
    <div className="flex flex-col gap-5">
      <div className="flex flex-col gap-1">
        <h2 className="m-0 text-[22px] font-bold">{w.name}</h2>
        <span className="text-[12px] text-muted">
          <Trans k="configurazione.webhooks.secretCreated" slots={{ hint: <span className="font-mono">{w.secret_hint}…</span>, date: dateTime(w.created_at) }} />
        </span>
      </div>
      <HookForm key={w.updated_at} w={w} events={events} allowPrivate={allowPrivate} onSaved={() => undefined} />
      <div className="flex flex-wrap gap-2">
        <button className="btn py-1.5" disabled={ping.isPending || !w.active} title={w.active ? undefined : t("configurazione.webhooks.ilWebhookEDisattivato")} onClick={() => ping.mutate()}>
          {t("configurazione.webhooks.inviaUnaProva")}
        </button>
        <ConfirmButton label={t("configurazione.webhooks.nuovoSegreto")} confirm={t("configurazione.webhooks.sostituisciIlSegreto")} pending={rotate.isPending} onConfirm={() => rotate.mutate()} />
        <ConfirmButton label={t("configurazione.common.elimina")} confirm={t("configurazione.webhooks.eliminaConLeConsegne")} danger pending={del.isPending} onConfirm={() => del.mutate()} />
      </div>
      <MutError errors={[ping.error, rotate.error, del.error, redeliver.error]} />
      <section className="flex flex-col gap-2">
        <SectionTitle aside={<span className="font-mono text-[11.5px] text-muted tabular">{deliveries.data?.items.length ?? 0}</span>}>{t("configurazione.webhooks.ultimeConsegne")}</SectionTitle>
        <ErrorNote error={deliveries.error} />
        <ul className="m-0 flex list-none flex-col p-0">
          {(deliveries.data?.items ?? []).map((d) => (
            <DeliveryRow key={d.id} d={d} events={events} onRedeliver={() => redeliver.mutate(d.id)} pending={redeliver.isPending} />
          ))}
          {deliveries.data?.items.length === 0 && <li className="py-3 text-muted">{t("configurazione.webhooks.ancoraNessunaConsegnaProva")}</li>}
        </ul>
      </section>
    </div>
  );
}

function DeliveryRow({ d, events, onRedeliver, pending }: { d: Delivery; events: WebhookEvent[]; onRedeliver: () => void; pending: boolean }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const st = STATUS[d.status];
  let body = d.body ?? "";
  try {
    body = JSON.stringify(JSON.parse(body), null, 2);
  } catch {
    /* corpo non JSON: si mostra com'è */
  }
  return (
    <li className="flex flex-col gap-0.5 border-b border-hair py-2">
      <div className="flex flex-wrap items-baseline gap-x-2">
        <Tag tone={st.tone}>{st.label}</Tag>
        <span className="font-medium">{d.event === "ping" ? t("configurazione.common.prova") : events.find((e) => e.key === d.event)?.name ?? d.event}</span>
        {d.redelivery_of && <span className="text-[12px] text-muted">{t("configurazione.webhooks.reinvio")}</span>}
        <span className="ml-auto font-mono text-[11.5px] text-muted tabular">{dateTime(d.last_attempt_at ?? d.created_at)}</span>
      </div>
      <span className="text-[12px] text-muted tabular">
        {d.response_status ? `HTTP ${d.response_status}` : t("configurazione.webhooks.nessunaRisposta")} · {t("configurazione.webhooks.attempts", { count: d.attempts })}
        {/* i18n-ok: unità di misura, uguale in ogni lingua */}
        {d.duration_ms != null && <> · {d.duration_ms} ms</>}
        {d.next_attempt_at && <>{t("configurazione.webhooks.nextAttempt", { date: dateTime(d.next_attempt_at) })}</>}
      </span>
      {d.last_error && <span className="break-words text-[12px] text-danger">{d.last_error}</span>}
      <div className="flex flex-wrap gap-3">
        <button type="button" className="text-[12px] text-muted underline" aria-expanded={open} onClick={() => setOpen((o) => !o)}>
          {open ? t("configurazione.webhooks.nascondiIlPayload") : "payload"}
        </button>
        {d.status !== "pending" && (
          <button type="button" className="text-[12px] text-accent underline" disabled={pending} onClick={onRedeliver}>{t("configurazione.webhooks.reinvia")}</button>
        )}
      </div>
      {open && <pre className="m-0 max-h-[320px] overflow-auto border-l-2 border-hair pl-3 font-mono text-[11.5px] whitespace-pre-wrap break-all">{body}</pre>}
    </li>
  );
}

function SignatureHelp({ version }: { version: number }) {
  const t = useT();
  return (
    <section className="flex max-w-[680px] flex-col gap-2">
      <SectionTitle>{t("configurazione.webhooks.verificaDellaFirma")}</SectionTitle>
      <p className="m-0">
        <Trans
          k="configurazione.webhooks.signatureHelp"
          slots={{
            header: <span className="font-mono text-[12px]">{t("configurazione.webhooks.sigHeader")}</span>,
            signed: <span className="font-mono text-[12px]">{t("configurazione.webhooks.sigSigned")}</span>,
            t: <span className="font-mono text-[12px]">t</span>,
          }}
        />
      </p>
      <p className="m-0 text-muted">
        <Trans
          k="configurazione.webhooks.payloadHelp"
          params={{ version }}
          slots={{
            fields: <span className="font-mono text-[12px]">{t("configurazione.webhooks.versionIdTypeOccurred")}</span>,
            header: <span className="font-mono text-[12px]">{t("configurazione.webhooks.xRegiaEventId")}</span>,
          }}
        />
      </p>
    </section>
  );
}
