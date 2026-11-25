// Requisiti veri dalle regole dei questionari (kind «requirement»). Vedi docs/sviluppo/requisiti.md.
import type { Source } from "../types";

export default {
  "lavoro.requisitiQ.proposti.titolo": "Requisiti proposti dalle risposte",
  "lavoro.requisitiQ.proposti.guida": "Diventano requisiti veri quando la valutazione si chiude con esito favorevole o con prescrizioni; il team della valutazione li verifica.",
  "lavoro.requisitiQ.proposti.nessuno": "Per ora nessuno: dipendono dalle risposte.",
  "lavoro.requisitiQ.proposti.manuale": "Verifica manuale con firma",
  "lavoro.requisitiQ.proposti.daEvidenza": "Verifica da evidenza ({check})",
} satisfies Source;
