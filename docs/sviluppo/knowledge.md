# Knowledge

## Knowledge: documenti (piano, passo 19)

Disegno, decisioni e API: [replica/knowledge.md](../../replica/knowledge.md).

- **Modulo:** `internal/modules/knowledge` (spazi, pagine, versioni e diff, blocchi, template, documenti controllati, ricerca, collegamenti); API in `internal/api/knowledge.go` (`/api/v1/kb/...`); migrazione `0026_knowledge.sql`.
- **Accesso per spazio** nel motore (`internal/engine/space_access.go`): spazi aperti o riservati (`spaces.settings.access`), ruoli assegnati sullo spazio con `role_assignments.space_id` (persone o gruppi). Vale per lettura, query e palette, collegamenti, notifiche e scrittura.
- **Pacchetto di partenza** (`knowledge.Provision`, anche con `regia tenant provision`): tipo `page`, ruoli `doc_reviewer` e `doc_approver`, relazione `documents`, politica di approvazione della fase «In revisione» (revisori, poi approvatori), processo «Documento controllato», template runbook, ADR, policy, verbale e guida.
- **Job:** promemoria delle revisioni periodiche ogni ora (`RunReminders`), notifica `document` 14 giorni prima della scadenza.
- **Query:** `tipo: page spazio: DOC testo`, `fase: Approvata`, `revisione: +30g`.
- **Interfaccia:** Governo → Documenti (`/documenti`), spazio `/documenti/spazi/DOC?q=`, impostazioni `/documenti/spazi/DOC/impostazioni`, pagina `/documenti/DOC-12` con `?vista=modifica`, `?vista=storia&da=2&a=3` e `?approvata=true`; `/e/DOC-12` porta alla pagina. Nel dettaglio di ogni elemento c'è «Documenti collegati» (`web/src/pages/KnowledgeSections.tsx`). Markdown esteso in `web/src/kbMarkdown.tsx`, editor in `web/src/pages/KnowledgeEditor.tsx` (caricato solo quando si modifica), bozza locale in `regia.kb.bozza.<id>`.
- **Test:** `internal/itest/knowledge_test.go` (`TestKnowledge`), diff e testo piano in `internal/modules/knowledge/diff_test.go`.
- **Dati di esempio:** `scripts/dev-demo-knowledge.sh [url]`, richiamato da `dev-demo-data.sh`: spazio «Documentazione IT» (DOC) con il runbook del Portale B2B collegato alle applicazioni, l'ADR collegato alla richiesta B2B, la policy di sicurezza controllata e approvata (revisione di Giulia Sala, approvazione di Andrea Fontana) con la versione successiva in lavorazione, un verbale con una menzione; spazio riservato «Sicurezza operativa» (SOC) per il gruppo Sicurezza.
