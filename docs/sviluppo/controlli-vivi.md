# Controlli GRC vivi (ondata 3, proposta 4)

L'efficacia di un controllo la **propone il sistema**, la **conferma una persona**. Lo stato ufficiale
(`ext_control.effectiveness`) cambia solo con la conferma (o con lo stato scelto da chi non conferma),
sempre con la nota e nell'audit.

## Regola di efficacia

Una regola per controllo (`grc_effectiveness_rules`, migrazione `0058_controlli_vivi.sql`), scritta nel
linguaggio di condizioni del motore con il costruttore unico (`ConditionBuilder`):

- «efficace se» e «a rischio se»; la seconda prevale. Se nessuna è vera la proposta è «non valutato».
- Proposta → stato ufficiale: efficace → `effective`, a rischio → `partial`.
- `sources`: quali dati del grafo diventano evidenze automatiche (`release`, `gate`, `sbom`, `rule`).

Fatti calcolati sul perimetro del controllo (le applicazioni in cui serve per i requisiti che copre; se
non ce n'è nessuna, tutte le applicazioni), in `internal/modules/grc/living.go`:

| Fatto | Da dove |
| --- | --- |
| `vuln_critiche_scadute`, `vuln_scadute` | `findings` aperti con `due_at` passata (stesse fasi di `devsecops/vulnfacts.go`) |
| `gate_superati_90g`, `gate_falliti_90g` | `gate_decisions` degli ultimi 90 giorni |
| `rilasci_approvati_90g`, `rilasci_senza_sbom` | `releases` in produzione degli ultimi 90 giorni, con o senza `sboms` della stessa versione |
| `giorni_riesame_accessi` | ultima evidenza valida del collettore `iam.access_review` (assente se non c'è) |
| `evidenze_valide`, `evidenze_scadute` | evidenze del controllo |

Il riesame degli accessi non ha una sorgente nel grafo: lo alimenta un collettore esterno
(`POST /api/v1/grc/evidence/automatic` con `collector: iam.access_review`).

## Decisioni

`POST /api/v1/grc/controls/{ref}/proposal/decision` (`grc.admin`). La proposta si ricalcola sul server.
`confirm` applica la proposta (nota facoltativa); `not_confirm` vuole la nota
(`validation.grc.proposal_note_required`) e può registrare uno stato diverso. Una proposta non
confermata non si ripropone finché lo stato proposto non cambia. Le ultime decisioni stanno in
`grc_effectiveness_decisions`, con i dati di quel momento.

## Evidenze automatiche

`POST /api/v1/grc/living/sync` (`grc.admin`, facoltativo `control`): per ogni controllo con regola attiva e
sorgenti, registra con `RecordAutomatic` (collettori `graph.release`, `graph.gate`, `graph.sbom`,
`graph.rule`) l'ultimo rilascio approvato, l'ultimo gate superato e l'ultimo SBOM di ogni applicazione del
perimetro, e, se la proposta è «efficace», la verifica della regola. La validità segue la frequenza del
controllo; è idempotente (riferimento esterno per dato e controllo). Un'evidenza oltre la validità diventa
«scaduta» e si rinnova alla sincronizzazione successiva se c'è un dato più recente (la verifica della
regola si rinnova a ogni scadenza). Non c'è ancora un job periodico: la sincronizzazione parte dal
pulsante «Aggiorna le evidenze dai dati» o dall'API (da collegare al ciclo orario del server).

## Rischio ↔ controlli

`GET /api/v1/grc/risks/{ref}/controls`: controlli di trattamento (relazione `mitigates`) con efficacia,
proposta in attesa ed evidenze (solo con `grc.read`), incidenti (`materializes`) e CVE delle eccezioni sulle
vulnerabilità collegate al rischio (`vuln_exceptions.risk_element_id`). La scheda del rischio lo mostra al
posto dell'elenco semplice «Mitigato da».

## Mappatura guidata

Scheda «Mappatura» della conformità. `GET /grc/mapping/workbench` (requisiti senza controllo per
normativa, controlli), `POST /grc/mapping/suggestions` (somiglianza di parole tra titolo e testo, più un
bonus se il controllo copre già requisiti dello stesso capitolo o della stessa normativa) e
`POST /grc/mapping/bulk` (aggiunge senza togliere). Sul telefono una colonna alla volta.

## Già c'era

La copertura pesata per criticità (`coverage.go`, `coverage_weighted_test.go`) e la scheda del controllo
che si apre su «Conformità» (`ElementPage.tsx`). Non fatta: la dichiarazione di applicabilità ISO 27001
come vista della matrice.

Test: `internal/itest/grc_vivi_test.go`, `web/e2e/ondata3-controlli.spec.ts`.
