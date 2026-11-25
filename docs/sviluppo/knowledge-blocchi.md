# Knowledge: editor a blocchi e pannello «evidenza»

Ondata 3. Per chi non scrive Markdown: l'editor delle pagine ha due modi sullo stesso testo, «Blocchi» (predefinito) e «Markdown».

## Il formato resta Markdown

Il testo salvato è sempre Markdown: versioni, confronto, ancore dei commenti in linea, importazione, esportazione e ricerca non cambiano. Non c'è migrazione né modifica del server.

- `web/src/kbBlocks.ts` (senza DOM né React) legge il Markdown in una lista piatta di blocchi e lo riscrive. Ogni blocco ricorda il proprio testo originale (`src`) e la distanza dal precedente (`pre`): finché non lo si tocca si riscrive identico, quindi un giro lettura → scrittura non cambia nemmeno uno spazio; un blocco modificato si riscrive in forma canonica. Le voci di un elenco numerato si rinumerano solo quando cambia la numerazione.
- Quello che l'editor non sa modificare (citazioni o avvisi che contengono elenchi, titoli o altro) diventa un blocco «grezzo», scritto in Markdown dentro il blocco e riscritto tale e quale.
- `web/test/kbBlocks.test.ts` prova il giro con i modelli di pagina di `templates.go` e con pagine scritte apposta (CRLF, righe vuote in più, codice non chiuso, numerazione `1. 1. 1.`…), la stabilità della forma canonica, spostamento, duplicazione, conversione e rinumerazione.
- `web/src/kbInline.ts` converte il Markdown in linea di un blocco (grassetto, corsivo, codice, barrato, link) nel DOM modificabile e ritorna. Si creano solo nodi con `createElement`: nessun HTML grezzo.

## Blocchi

Paragrafo, titoli 1-3, elenco puntato, numerato e di controllo (con livelli, Tab e Maiusc+Tab), citazione, avviso (nota, suggerimento, importante, attenzione, pericolo, i tipi di `CALLOUT_KINDS`), codice con linguaggio, tabella semplice (riga e colonna da aggiungere o togliere), immagine e allegato, riferimento a un elemento (chiave, con titolo e stato), separatore.

Uso: «/» all'inizio di un paragrafo apre il menu di inserimento, filtrabile da tastiera (frecce, Invio, Esc); le scorciatoie `## `, `- `, `1. `, `> `, `[] ` convertono il blocco; Invio crea un blocco (su una voce vuota esce dall'elenco), Maiusc+Invio va a capo nel blocco, Backspace all'inizio converte in paragrafo un titolo o una voce e poi unisce al blocco precedente; Alt+↑ e Alt+↓ spostano; le frecce passano al blocco vicino. La maniglia a sinistra (trascinabile, con Alt+↑/↓ anche da lei) apre il menu del blocco: cambia in, sposta su e giù, duplica, elimina. La barra sulla selezione dà grassetto, corsivo, codice, link e menzione. Incollare testo su più righe lo legge come Markdown e crea blocchi; incollare o trascinare file li carica come allegati.

Accessibilità: ogni blocco di testo è un `textbox` con il nome del suo tipo; il menu «/» è un `listbox` con `option`; il menu del blocco è un `menu` con `menuitem`; la barra è un `toolbar`.

Telefono: blocchi a tutta larghezza; la maniglia è sostituita da un pulsante «Menu del blocco» da 44 px che compare sul blocco in cui si scrive (il menu si apre dal basso); menu e pulsanti a 44 px; nessuno scorrimento orizzontale a 375 px. Il trascinamento non c'è sul telefono: si sposta dal menu.

## Modo Markdown

Interruttore «Blocchi / Markdown» (la scelta resta nel browser). Il modo Markdown è la casella di testo di prima con anteprima affiancata (a schede sul telefono); la barra ha parole al posto delle sigle: Titolo, Sottotitolo, Grassetto, Corsivo, Codice, Elenco puntato, Elenco numerato, Elenco di controllo, Citazione, Tabella, Blocco di codice, Link, Menzione, Elemento, Immagine o allegato, Separatore, Inserisci un avviso.

Restano in entrambi i modi: menzioni con @, chiavi degli elementi, allegati per trascinamento e incolla, bozza locale, blocco di modifica rinnovato ogni minuto, nota di versione, Ctrl/Cmd+S.

## Pannello «Questo documento è evidenza di…»

`web/src/pages/KnowledgeEvidence.tsx`, nella lettura della pagina al posto del vecchio «Documenta». Legge i collegamenti già nel grafo (`links.go`, relazione «documenta», `PageDetail.links`) e li raggruppa in controlli, applicazioni, rischi e altri elementi. «Collega» apre un selettore con ricerca (`EntitySelect`, come ovunque); «Scollega» toglie il legame. Non scrive nei moduli GRC e rischi: quei moduli vedono il documento tra i «Documenti collegati» dell'elemento. Il sommario («In questa pagina») sta accanto al testo da 1280 px in su (prima da 1536 px).

## Cosa non c'è

- Annulla/ripeti tra blocchi: vale quello del browser dentro un blocco, non le operazioni sui blocchi (spostare, eliminare…); si ritorna indietro con la versione precedente.
- Elenchi, titoli e codice dentro citazioni o avvisi: si modificano come blocco «grezzo» in Markdown.
- Immagini in linea nel testo e HTML: restano Markdown nel paragrafo; le immagini a sé sono blocchi.
- Modifica contemporanea di più persone: resta il blocco di modifica di prima.
- Trascinamento sul telefono.

## Nota sul modello d'uso (clean-room)

L'utente ha chiesto un editor che si usi come quello a blocchi di YouTrack. Si è preso come modello **solo il comportamento**: blocchi a cui si arriva con «/», maniglia per spostare e per il menu, barra contestuale sulla selezione, Invio che crea un blocco e Backspace che unisce. Non si è guardato, copiato né adattato nessun codice, testo, icona, stile o struttura di dati di YouTrack né di altri editor; tutto è scritto da zero con parole e schemi nostri (modello dei blocchi su Markdown, resa a elementi React, menu, catalogo dei testi). Nessuna dipendenza nuova: il peso è tutto nel pezzo caricato solo quando si modifica una pagina.
