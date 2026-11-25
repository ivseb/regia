# Vista DPO

Ondata 3, `replica/usabilita/governo.md` (C8). Pagina `/privacy` (voce di menu «Privacy (DPO)») per chi ha `privacy.read` o `privacy.write`. I riferimenti al GDPR (artt. 6, 30, 33, 35) sono marcati «da verificare» nell'interfaccia: nessun testo normativo è incluso.

**Permessi e ruolo** (`internal/platform/rbac/privacy.go`): `privacy.read` (consulta) e `privacy.write` (tiene registro, DPIA, segni e notifiche). Ruolo di sistema `dpo` (con `element.read` e `comment.write`); l'amministratore li ha con `*`. Il seed di sviluppo assegna `dpo` a `dpo@demo.test`. I tenant esistenti ricevono il ruolo al prossimo provisioning.

**Dati** (migrazione `0064_vista_dpo.sql`, tutte con RLS): `privacy_treatments` (registro, art. 30), `privacy_treatment_apps` (applicazioni del catalogo), `privacy_dpias` (una per trattamento; stati bozza, in revisione, approvata), `privacy_dpia_risks` (elementi `risk` del registro rischi), `privacy_dpia_controls` (elementi `control` della conformità), `privacy_incident_marks` e `privacy_incident_treatments` (segno del DPO, notifica al Garante e trattamenti coinvolti). Le violazioni non hanno una tabella: sono gli incidenti esistenti.

**Regole**
- La DPIA serve se il trattamento ha categorie particolari, larga scala, profilazione o è segnato ad alto rischio dal DPO. Segnalazioni: `no_basis`, `no_purpose`, `no_application`, `dpia_missing`.
- DPIA: bozza → in revisione (servono descrizione e valutazione dei rischi) → approvata (serve il parere del DPO); da approvata si riapre per modificarla. Il questionario «DPIA preliminare» delle valutazioni si collega (`review_id`) invece di ripeterlo.
- Violazione: un incidente è una violazione se il DPO l'ha segnato, se è collegato a un trattamento o se colpisce un'applicazione con dati personali (`data_classification`: personali, particolari, sanitari). La scadenza è la rilevazione più 72 ore (`BreachHours`); stati `open`, `overdue`, `notified`, `notified_late`, `not_required` (con motivo).
- Dati dichiarati dall'applicazione (campo «Dati»): personali, credenziali, finanziari (`dataKinds`). Flussi: per trattamento, le applicazioni collegate e le dipendenze EA tra applicazioni (con i «dati scambiati» dell'arco); le applicazioni con dati personali senza trattamento attivo sono segnalate a parte.

**API** (`internal/api/privacy.go`, prefisso `privacy`, spento insieme al modulo «Conformità, rischi e incidenti»): `GET privacy/home`, `GET|POST privacy/treatments`, `GET privacy/treatments.csv`, `GET|PUT|DELETE privacy/treatments/{id}`, `GET|PUT privacy/treatments/{id}/dpia`, `POST …/dpia/transition`, `GET privacy/dpias`, `GET privacy/breaches[?all=1]`, `GET|PUT privacy/breaches/{id}`, `POST privacy/breaches/{id}/notification`, `GET privacy/flows`.

**Interfaccia**: `web/src/pages/Privacy.tsx` (pagina, home, violazioni, flussi), `PrivacyRegister.tsx` (registro, scheda, DPIA), `privacy-ui.tsx`; testi in `i18n/{it,en}/governance-privacy.ts`, `shell-privacy.ts`, `errors-privacy.ts`. Telefono prima: una colonna, bersagli da 44 px.

**Test**: `internal/itest/ondata3_dpo_test.go` (trattamento, DPIA, violazioni, flussi, CSV, RLS tra tenant) e `web/e2e/ondata3-dpo.spec.ts`.
