# Architettura enterprise

## Architettura enterprise (piano, passo 20)

Disegno e scelte in [replica/architettura-ea.md](../../replica/architettura-ea.md). Codice in `internal/modules/architecture` (`ea.go`, `capability.go`, `dependency.go`, `graph.go`, `lifecycle.go`, `diagram.go`, `ea_import.go`, `ea_facts.go`), migrazione `0030_architettura.sql`, API in `internal/api/architecture_ea.go`, pagine in `web/src/pages/ea/`.

- **API** (tutte sotto `/api/v1/architecture`):
  - capability: `GET/POST /capabilities`, `PATCH /capabilities/{rif}`, collegamenti `POST /capabilities/{rif}/links` con `element_id` e `DELETE /capabilities/{rif}/links/{elemento}`;
  - tipi di relazione: `GET/POST /relation-types`, `PATCH /relation-types/{chiave}`;
  - relazioni: `GET /relations?category=dependency&element=…`, `POST /relations` con `type`, `source`, `target`, `attrs`, `PATCH /relations/{id}` (attributi), `DELETE /relations/{id}`;
  - grafo e impatto: `GET /graph`, `GET /impact/{rif}?direction=dependents|dependencies&depth=N&format=csv`, `GET /profile/{rif}`;
  - ciclo di vita: `PUT /applications/{rif}/time`, `PUT /applications/{rif}/technologies`, `GET/POST /technologies`, `PATCH/DELETE /technologies/{id}`, `GET /rationalization`, `GET/PUT /settings`;
  - diagrammi: `GET/POST /diagrams`, `GET/PUT/DELETE /diagrams/{id}` (`PUT` con `version`: 409 se è cambiata);
  - catalogo: `GET /catalog/export?format=json` oppure `?format=csv&what=capabilities|dependencies`, `POST /catalog/import?dry_run=true` con `{"kind":"capabilities"|"dependencies","csv":…}` o `{"json":…}`.
  I riferimenti `{rif}` accettano id, chiave (`ARC-12`), codice o nome esatto.
- **Interfaccia:** Governo → Architettura, schede Mappa (`/architettura/mappa?nodo=ARC-12`), Capability, Dipendenze, Razionalizzazione, Diagrammi; sezione "Architettura" nel dettaglio di applicazioni, asset e capability.
- **Fatti:** `capability`, `dipendenti`, `dipendenti_diretti`, `dipende_da`, `time`, `fine_supporto_giorni` sull'applicazione; `dipendenti`, `capability`, `time`, `fine_supporto_giorni` sull'impatto; `dipendenti_max` sulla richiesta.
- **Test:** `internal/itest/architettura_ea_test.go`; modello del grafo e layout in `web/test/eaGraph.test.ts` (`cd web && pnpm test`).
- **Dati di esempio:** `scripts/dev-demo-ea.sh [url]` (lo richiama `dev-demo-data.sh`): capability di una banca, dipendenze tra le 8 applicazioni demo, tecnologie, valutazioni TIME e il diagramma "Canali digitali e pagamenti".
- **Dopo l'aggiornamento:** `regia tenant provision --slug <tenant>` aggiunge tipo capability, tipi di relazione e impostazioni ai tenant esistenti.
