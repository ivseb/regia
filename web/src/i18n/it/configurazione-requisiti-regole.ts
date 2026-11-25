// Requisiti veri dalle regole dei questionari (kind «requirement»). Vedi docs/sviluppo/requisiti.md.
import type { Source } from "../types";

export default {
  "configurazione.requisitiQ.regola.guidaRequisito": "Crea un requisito vero: alla chiusura della valutazione (esito favorevole o con prescrizioni) entra nel modulo Requisiti, di applicazione se la valutazione è di un impatto, di progetto se è della richiesta; lo verifica il team della valutazione.",
  "configurazione.requisitiQ.regola.guidaPrescrizione": "Propone una prescrizione al team, che la accetta o la scarta; una prescrizione accettata diventa poi un requisito.",
  "configurazione.requisitiQ.regola.titolo": "Titolo del requisito (facoltativo)",
  "configurazione.requisitiQ.regola.titoloPlaceholder": "Se manca si ricava dal testo",
  "configurazione.requisitiQ.regola.metodo": "Verifica",
  "configurazione.requisitiQ.regola.manuale": "Manuale, con firma",
  "configurazione.requisitiQ.regola.evidenza": "Automatica da evidenza",
  "configurazione.requisitiQ.regola.controllo": "Chiave del controllo",
} satisfies Source;
