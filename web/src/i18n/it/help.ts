// Spiegazione dei termini (web/src/help.tsx): una frase semplice per ogni parola del lessico del prodotto.
// Le chiavi iniziano con «help.»: vedi web/src/i18n/GLOSSARIO.md.
import type { Source } from "../types";

export default {
  "help.hint": "Spiegazione del termine «{term}»",
  "help.close": "Chiudi la spiegazione",

  "help.term.richiesta": "Una richiesta è ciò che qualcuno chiede all'organizzazione: un progetto, un cambiamento, una spesa. Segue un processo fino alla decisione.",
  "help.term.fase": "Una fase è un tratto del processo: la richiesta è sempre in una sola fase, e per passare alla successiva servono alcune condizioni.",
  "help.term.valutazione": "Una valutazione (o parere) è un questionario che un team competente compila o esamina per dire se la richiesta va bene dal suo punto di vista: sicurezza, architettura, privacy.",
  "help.term.impatto": "Un impatto è l'effetto della richiesta su un'applicazione e sul team che la gestisce. Ogni team coinvolto ne riceve uno.",
  "help.term.epic": "Un epic è il lavoro che un team apre nel proprio spazio quando accetta un impatto. Raccoglie le story che lo realizzano.",
  "help.term.story": "Una story è un pezzo di lavoro piccolo e concreto dentro un epic. Quando si chiudono tutte, l'epic è finito.",
  "help.term.approvazione": "Un'approvazione è la decisione di una persona o di un organo (sì, no, con condizioni) richiesta per entrare in una fase.",
  "help.term.regola": "Una regola fa succedere qualcosa da sola quando la richiesta entra in una fase, per esempio aprire un parere o assegnare un ruolo. Si configura nel processo.",
  "help.term.processo": "Il processo è il percorso che una richiesta segue: le fasi, i passaggi consentiti, chi li può fare e le regole che scattano.",
  "help.term.versione": "Ogni modifica al processo crea una nuova versione. Le richieste già avviate restano sulla versione con cui sono partite.",
} satisfies Source;
