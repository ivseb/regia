# Tipi del lavoro dai dati

I tipi del lavoro non sono più cablati nel modulo `work`: sono i tipi di `element_types` con `module = 'work'`, anche quelli creati dall'editor.

- `workItemTypes(ctx, tx, withEpic)` (`internal/modules/work/tipi.go`) restituisce le chiavi non archiviate, senza `sprint` (e senza `epic` se `withEpic` è falso). Board, backlog, spazi, creazione, operazioni in blocco e validazione della board usano questa funzione.
- `epic` e `sprint` conservano le regole speciali per chiave. Genitori ammessi (`parentTypesOf`): story sotto epic, bug sotto epic o story, task sotto story o bug, ogni altro tipo del lavoro sotto epic.
- Fatti e descrizioni dei fatti: `eng.FactsModule("work", ...)` e `eng.DescribeFactsModule` valgono per i tipi del modulo senza fatti propri (registrati per chiave), quindi anche per quelli creati dopo.
- Board: senza un elenco esplicito di tipi usa i tipi correnti. `CreateType` (modulo work) ammette il tipo negli spazi dei team (`space_types`) e lo aggiunge alle board pubblicate e alle bozze con un elenco esplicito. La risposta della board e del backlog ha `create_types` (chiave, nome, genitori ammessi, se ha `punti`): è ciò che propone «Nuovo elemento».
- Archiviazione: un tipo di sistema si archivia solo senza elementi (errore `validation.type.archive_in_use` con il conteggio); si può riattivare. Un tipo archiviato sparisce dai menu di creazione, dalla board, dalle corsie (epic) e da ⌘K. Con `sprint` archiviato la board è un flusso continuo e la creazione di uno sprint fallisce. `Provision` non riattiva i tipi archiviati.
- Interfaccia: `useWorkTypes()` (`web/src/typesApi.tsx`) dà i tipi del lavoro; sostituisce gli elenchi scritti nel codice in «Il mio lavoro», ⌘K, «+ Crea» e nel backlog.
