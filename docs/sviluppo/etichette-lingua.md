# Nomi per lingua di fasi e opzioni dei campi

Fasi dei processi e opzioni dei campi (anche degli insiemi di stati) sono dati del tenant. Il codice, le
condizioni e i processi li citano per **nome**: il nome è canonico e non cambia con la lingua. La traduzione
è solo un'etichetta di visualizzazione, `lingua → testo`, con ripiego sul nome.

## Dove vive

- `options.labels jsonb` (migrazione `0057_etichette_lingua.sql`), p. es. `{"en": "Open"}`.
- `Phase.Labels` dentro la definizione del processo (`internal/engine/workflow.go`); alla pubblicazione le
  etichette delle fasi passano a `options.labels` (`process_admin.go`). Se una fase non ha etichette vale
  quella dell'opzione (`phaseStates`).
- `OptionView`, `OptionInput`, `State`, `BundleOption` portano `labels`; l'export/import dei campi le include
  (all'import si aggiungono a quelle già presenti).
- Validazione in `internal/engine/labels.go`: solo `LabelLangs` (`it`, `en`), testo al più lungo del nome
  (100 per le opzioni, 80 per le fasi), spazi normalizzati, voci vuote tolte (= segue il nome).
- `GET /api/v1/state-labels`: nome della fase → lingua → etichetta, per le liste dove lo stato arriva
  come semplice nome.
- Tenant creati in inglese: `tenant.Localize` riempie `labels.en` di fasi e opzioni di partenza con
  `i18n.NamesEn()`; i nomi restano italiani.

## Interfaccia

- `localName(name, labels)` in `web/src/i18n/localName.ts`: l'etichetta della lingua corrente o il nome.
- `useStateName()` in `web/src/stateLabels.ts`: `stateName(el.state)` per le liste (usa `/state-labels`;
  con `labels` esplicite, quelle hanno la precedenza).
- Usati in: campo di selezione e chip (`fields.tsx`), barra delle fasi `StageGate`, testata dell'elemento,
  richieste, valutazioni, colonna e raggruppamento «Fase» delle liste, colonne e menu della board,
  backlog, «Da fare».
- Non si traducono mai i valori inviati all'API, i fatti delle condizioni (`formFacts`) né i confronti
  per nome.
- Si modificano in Configurazione → Campi (riquadro «Traduzioni» sotto i valori) e nell'editor dei
  processi (riquadro «Traduzioni» della fase). Testi in `web/src/i18n/{it,en}/configurazione-etichette.ts`.

## Aggiungere una lingua

Aggiungerla a `LabelLangs` (Go) e alle cartelle di `web/src/i18n`; il riquadro oggi chiede solo l'inglese.
