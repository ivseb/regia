# Testi e lingue

## Testi e lingue
Piano e decisioni: [replica/lingua.md](../../replica/lingua.md). Glossario inglese e stile: `web/src/i18n/GLOSSARIO.md`. Niente libreria: catalogo e `t()` sono in `web/src/i18n/`.

**Dove stanno i testi**
- `web/src/i18n/it/<area>.ts` è la sorgente, `web/src/i18n/en/<area>.ts` la traduzione. Un file per area, così più persone lavorano senza conflitti. Le aree si registrano da sole: una nuova area è solo la coppia di file `it/<area>.ts` e `en/<area>.ts`, senza toccare indici né elenchi. Lo script `scripts/gen-i18n.mjs` elenca i file di area e scrive i `areas.gen.ts` (in `.gitignore`) che tengono tipizzate le chiavi; parte da solo prima di `pnpm typecheck`, `build`, `dev` e `test` (anche da `make vet`, `make test`, `make web`). Anche `it/lavoro.ts` aggrega in automatico `it/lavoro/*.ts` (stesso schema in `en/`). Il test controlla che ogni file di area abbia il gemello nell'altra lingua. Un'area può stare in più file: `errors-finance.ts` appartiene all'area `errors` (le chiavi iniziano con `errors.`), così chi lavora in parallelo aggiunge un file invece di toccare quello comune.
- Ogni chiave inizia con il nome dell'area (`auth.login.title`), tutta minuscola, con parti separate da punti. Il test `web/test/i18n.test.ts` lo controlla, e controlla che non ci siano chiavi doppie.
- I due cataloghi si caricano con un import dinamico (`it/index.ts`, `en/index.ts`, che usano i file generati): l'inglese solo se la persona lo usa.

**Aggiungere un testo**
1. In `it/<area>.ts` una riga: `"lavoro.board.title": "Board dei team",`. Un segnaposto si scrive `{nome}`: `"lavoro.board.count": "{n} elementi"`.
2. In `en/<area>.ts` la stessa chiave: se manca, `pnpm typecheck` (e `make vet`) fallisce. Segnaposto e forma (testo o plurale) devono coincidere con l'italiano: lo controlla il test.
3. Nel componente: `const t = useT(); … {t("lavoro.board.title")}`. Fuori da React: `tr("…")` da `i18n/core.ts`. Una chiave scritta male non compila.
4. Un plurale è un oggetto con `one` e `other` (e `zero`, `two`, `few`, `many` se servono), parametro `{count}`: `"lavoro.board.items": { one: "{count} elemento", other: "{count} elementi" }`, `t("lavoro.board.items", { count: n })`.
5. Una frase con del JSX dentro (un link, un grassetto): `<Trans k="…" slots={{ nome: <b>{x}</b> }} />` (`i18n/Trans.tsx`). Non spezzare la frase in pezzi.
6. Chiavi composte da una variabile (`` t(`profile.theme.${key}`) ``) compilano solo se tutte le chiavi possibili esistono: per un insieme aperto si usa `lookup()`, che restituisce `undefined`.

**Aggiungere una lingua** (es. francese, `fr`)
1. `web/src/i18n/fr/` con un file per area e un `index.ts` come quello inglese (e il suo `areas.gen.ts`: aggiungere la cartella a `scripts/gen-i18n.mjs`) (`satisfies Translation<typeof itCatalog>`).
2. `languages.ts`: la lingua in `LANGUAGES` (nome nella lingua stessa e locale BCP 47, es. `fr-FR`); `types.ts`: `Lang`; `core.ts`: il caricatore in `loaders`.
3. Backend: `internal/api/language.go` (`supportedLangs`) e il controllo della colonna in una migrazione nuova (`members.locale`, `tenant_language.locale`).
4. Un `GLOSSARIO` per la lingua, se serve, e i test e2e in quella lingua.

**Scelta della lingua**
- Precedenza: scelta del membro (`members.locale`, nulla = segue il tenant), poi predefinita del tenant (`tenant_language`), poi `Accept-Language`, poi l'italiano. `GET /api/v1/me` restituisce `language` con `effective` e `source` (member, tenant, browser, default); login, MFA e cambio di organizzazione lo includono.
- API: `PUT /api/v1/me/language` (`{"locale": "en"}`, `""` o `null` = segue il tenant), `GET /api/v1/me/language`, `PUT /api/v1/tenant/language` (`tenant.admin`, nell'audit come `tenant.language.update`). Nel Profilo: sezioni «Lingua» e «Lingua dell'organizzazione» (`pages/LanguageSettings.tsx`).
- Prima di conoscere la persona (accesso, link monouso) vale la lingua ricordata nel browser (`regia.lingua`) o quella del browser; il selettore nella pagina di accesso la cambia.
- `<html lang>` segue la lingua, e il cambio è immediato: al cambio l'albero React si ricostruisce (`main.tsx`), le pagine si ridisegnano senza ricaricare e i nomi dei tipi si chiedono di nuovo con `?lang=`.

**Date, numeri, importi, durate**: mai `toLocaleString("it-IT")` a mano. `loc()` dà il locale della lingua corrente; `fmtDate`, `fmtDateTime`, `fmtTime`, `fmtNumber`, `fmtCurrency`, `fmtRelative`, `fmtDuration` sono in `i18n/format.ts`. Le funzioni già usate dalle pagine (`date`, `ago`, `euro` in `ui.tsx`, `fmtMoney` in `fields.tsx`, `hoursText` e `weekLabel` in `tempoApi.ts`) le usano già. Nel resto del codice sostituire `"it-IT"` con `loc()` (import da `i18n/format`).

**Errori dell'API**: il client traduce il `code` del Problem (`errors.<code>` nel catalogo), con i parametri del campo `params` (`writeProblemParams` in `internal/api/problem.go`); in italiano resta il `detail` del server. Un `code` nuovo nel backend richiede una riga in `it/errors.ts` e in `en/errors.ts`: lo controlla `web/test/i18n.test.ts` leggendo i sorgenti Go.

**Testi del server** (`internal/platform/i18n`, niente libreria): chiave, testo it/en, segnaposto `{nome}`, plurale con `OneIt`/`OneEn` e il parametro `count`.
- Un testo nuovo: `i18n.Register(map[string]i18n.Msg{"area.chiave": {It: "…", En: "…"}})` (vedi `internal/engine/notify/texts.go`) e `i18n.T(lang, "area.chiave", i18n.P{"nome": x})`. Un test controlla che le due lingue abbiano gli stessi segnaposto.
- La lingua di una persona nel tenant: `i18n.MemberLang(ctx, tx, memberID)` (scelta, poi tenant, poi italiano). Nelle richieste HTTP `langCtx(ctx, tx, p)` la lega al contesto e il motore la legge con `i18n.Lang(ctx)`.
- Notifiche ed email si scrivono nella lingua di chi le riceve (`notify.DescribeLang`, `Mailer.compose`), non di chi compie l'azione.
- Nomi di sistema (fatti e azioni dei moduli, controlli di rilascio, tipi, ruoli, relazioni, campi e processi di partenza): la sorgente è l'italiano, l'inglese sta in `internal/platform/i18n/names_en.go` (`i18n.Name(lang, "…")`). Un nome registrato da un modulo senza traduzione fa fallire `TestSystemNamesAreTranslated`.
- Un tenant nuovo in inglese: `regia tenant create --language en` (o `CreateInput.Language`). I nomi di partenza passano da `tenant.Localize`; le fasi e le opzioni dei campi non si traducono (il codice e i processi le citano per nome), come i gruppi.
- Errori di validazione: gli helper di `internal/engine/validation.go` danno un code specifico e i parametri per il client, e il `detail` resta italiano: `engine.Required(campo, …)`, `TooLong(campo, max, …)`, `OutOfRange(campo, min, max, …)`, `DateInvalid`, `NotFound(campo, genere, …)` (il genere si traduce con `errors.kind.<genere>`), `Duplicate`, `InvalidValue`, `InvalidParams(code, campo, params, …)`. Un caso con un testo proprio: `engine.InvalidCode("validation.xxx", campo, "messaggio")`. Ogni code sta in `engine.ValidationCodes` e ha una voce in `it/errors.ts` e `en/errors.ts` (lo controlla `validation_codes_test.go`). `engine.Invalid(campo, …)` senza code resta valido per i casi rari: in inglese il client mostra «Some values are not valid.» seguito da «(detail: …)» con il testo italiano del server. I code con testo generico (`validation`, `required`, `invalid_value`, `duplicate`) aggiungono sempre il dettaglio (`COARSE` in `i18n/core.ts`).

**Controllo dei testi rimasti** (`scripts/check-i18n.mjs`, parte di `pnpm test`): conta i testi scritti fuori da `t()` e li confronta con `scripts/i18n-soglia.json` (oggi 0: il numero può solo scendere; il conteggio per file si stampa sempre, e `--verbose` elenca ogni testo). Cerca in quattro posti: testo nel JSX, attributi `aria-label`, `title`, `placeholder`, `alt`, valori italiani nelle tabelle `Record<…, string>` e nelle proprietà `label`, `title`, `name`, `text`, `help`… degli oggetti, e frasi italiane (uno spazio e una parola tipica, o una lettera accentata) in qualunque stringa del codice. Una tabella di etichette si scrive con i getter: `get low() { return tr("common.criticality.low"); }`, così il testo segue la lingua a ogni lettura. Le eccezioni motivate si segnano con `// i18n-ok: motivo` (sintassi della query e delle condizioni, intestazioni dei file CSV, nomi di ripiego, nomi uguali nelle due lingue). Il percorso sopra il titolo di una pagina («governance / conformità») è `crumbs("governance", "compliance")` (`web/src/crumbs.ts`, voci `common.crumb.*`).
- `node scripts/check-i18n.mjs --verbose` elenca ogni testo con la riga.
- Dopo una conversione: `node scripts/check-i18n.mjs --update` abbassa la soglia; si committa il file. La soglia non si alza mai.
- Un'eccezione motivata (una sigla, un'intestazione HTTP) si segna con un commento `i18n-ok: motivo` sulla riga o su quella prima.
- Non vede le tabelle di etichette nel codice (`Record<string, string>` con testi italiani): vanno portate nel catalogo con una chiave per voce.
