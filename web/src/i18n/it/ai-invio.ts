// Pannello «Assistente»: invio della domanda (subito nella conversazione) e avanzamento.
// Le chiavi iniziano con «ai.»: vedi docs/sviluppo/testi-e-lingue.md.
import type { Source } from "../types";

export default {
  "ai.panel.step.think": "Capisco che cosa cercare…",
  "ai.panel.step.read": "Ho usato {tools}: preparo la risposta…",
  "ai.panel.step.write": "Scrivo la risposta…",
  "ai.panel.elapsed": "{seconds} s",
  "ai.panel.restored": "La domanda non è partita: è di nuovo nella casella, puoi riprovare.",
} satisfies Source;
