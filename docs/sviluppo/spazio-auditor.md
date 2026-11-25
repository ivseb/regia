# Spazio dell'auditor (ondata 3, C6)

Home dedicata di chi ha il profilo auditor (`menu_profile = "auditor"`): `/audit-spazio`. La radice `/` rimanda lì il revisore (se il modulo GRC è acceso); la voce di menu compare a chi ha `grc.audit`.

## Permessi
`grc.audit` (nuovo, `internal/platform/rbac/grc.go`) è dato al ruolo `auditor` (ruoli nuovi dal codice, tenant esistenti dalla migrazione `0063`). L'auditor continua a non avere `element.write` né `grc.admin`: scrive solo le tabelle `grc_audit_*`. Rispondono a richieste, scrivono commenti e propongono piani il responsabile del controllo (`ext_control.owner_id`), il destinatario della richiesta e chi ha `grc.admin`. Lettura: `grc.read`.

## Modello (migrazione `0063_spazio_auditor.sql`, RLS come le tabelle vicine)
- `grc_audit_engagements`: incarico con norma facoltativa, periodo, stato `open|closed`; `grc_audit_scope` ne dice i controlli e le applicazioni (senza controlli: tutti quelli della norma, o tutti).
- `grc_audit_reviews`: esame di un controllo nell'incarico (`reviewed` o `to_clarify`). Avanzamento = esaminati / controlli del perimetro.
- `grc_audit_samples` + `grc_audit_sample_items`: campione di N evidenze del controllo valide nel periodo (`valid`/`revoked`, ordinate per id), estratte con un PCG a seme registrato: stesso seme e stessa popolazione, stesso campione. Esito per elemento: `compliant | non_compliant | to_clarify` (nota obbligatoria se non conforme o da chiarire).
- `grc_audit_requests`: richiesta dell'auditor al responsabile (scadenza, evidenza di riferimento); risposta con testo, link http(s) e/o evidenza già caricata; chiude l'auditor.
- `grc_audit_comments`: commenti in linea sul controllo o su una sua evidenza.
- `grc_audit_findings`: rilievo (gravità `low|medium|high|critical`), piano d'azione del responsabile (`open → plan_proposed`), chiusura dell'auditor con nota (`closed`).

## API (`internal/api/grc_audit.go`, logica in `internal/modules/grc/audit*.go`)
`/api/v1/grc/audit/` `home`, `engagements` (GET, POST), `engagements/{id}` (+ `/close`, `/pack`, `/samples`, `/requests`, `/findings`, `PUT /controls/{ref}/review`), `samples/{id}` (+ `PUT items/{pos}`), `requests/mine`, `requests/{id}/answer|close`, `findings/{id}/plan|close`; `GET /grc/controls/{ref}/audit` e `POST …/audit/comments` per la scheda del controllo.

Il pacchetto dell'incarico (`/engagements/{id}/pack`) è l'auditpack ristretto ai controlli e al periodo dell'incarico, con in più `campioni.csv`, `richieste.csv` e `rilievi.csv`.

## Audit e notifiche
Ogni scrittura va nel registro (`grc.audit.engagement.*`, `grc.audit.sample.*`, `grc.audit.request.*`, `grc.audit.finding.*`, `grc.audit.comment`, `grc.audit.control.review`). Le notifiche usano il tipo nuovo `audit` (`internal/engine/notify/audit.go`), sull'elemento controllo: richiesta aperta, risposta, richiesta chiusa, commento, rilievo aperto, piano proposto, rilievo chiuso.

## Interfaccia
`web/src/pages/AuditSpace.tsx` (home: incarico, avanzamento, controlli con «Lavora su questo controllo») e `AuditPanels.tsx` (richieste, rilievi, campioni, commenti; `AuditBox` è il riquadro «Audit su questo controllo» nella scheda del controllo). Testi: `governance-auditor`, `shell-auditor`, `errors-auditor`. Test: `internal/itest/grc_auditor_test.go`, `web/e2e/ondata3-auditor.spec.ts`.
