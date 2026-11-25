# Scorecard e catalogo dei requisiti: editor visuali

Le pagine `web/src/pages/ScorecardConfig.tsx` (Graduatoria, vista Scorecard) e `RequirementsConfig.tsx` (Configurazione, Requisiti, Cataloghi) non fanno più scrivere JSON. Gli editor stanno in `web/src/pages/configEditors/`:

- `ScorecardEditor.tsx`: criteri come schede (nome, direzione, scala a righe valore/etichetta, peso con cursore). I pesi sono percentuali del totale; il totale è sempre visibile e, se non fa 100, compare l'avviso con «Porta il totale a 100» (il backend accetta pesi qualsiasi maggiori di zero e li normalizza). I ruoli dei valutatori sono un menu a scelta multipla (`ROLE_NAMES`). Le regole `suggest[].when` usano `ConditionBuilder` sul tipo della scorecard.
- `CatalogEditor.tsx`: voci (chiave, titolo, categoria, testo, verifica manuale o da evidenza con controllo e criterio, verificatore team o ruolo), baseline (ambito, `when` con `ConditionBuilder` e conteggio dal vivo, voci da menu multiplo con ricerca), ricerca tra le voci da 8 in su, riordino, importazione CSV.
- `csvVoci.ts`: colonne `key,title,text,category,method`, separatore riconosciuto (virgola, punto e virgola, tab), intestazione facoltativa. Una chiave già presente aggiorna titolo, testo, categoria e metodo; le nuove prendono il verificatore dell'ultima voce che ne ha uno (il server vuole un team o un ruolo). Nulla cambia finché non si applica alla bozza.
- `common.tsx`: problemi del server accanto alla riga (`path` come `criteria[2].weight`, `items[0].key`, `baselines[1].when`), riquadro «Avanzato» con il JSON (chiuso di default; «Applica al modulo» riporta il testo nel modulo, poi si salva la bozza come prima).

Salva bozza, pubblica e scarta sono invariati; «Pubblica» resta spento finché ci sono modifiche non salvate. I testi stanno in `web/src/i18n/{it,en}/configurazione-scorecard.ts`. Test: `web/e2e/scorecard-catalogo.spec.ts`, `web/test/csv-voci.test.ts`.
