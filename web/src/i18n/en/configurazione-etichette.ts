import type { Translation } from "../types";
import type it from "../it/configurazione-etichette";

export default {
  "configurazione.etichette.titolo": "Translations",
  "configurazione.etichette.riassunto": "{n} of {total} in English",
  "configurazione.etichette.opzioniGuida": "The English name of each value, shown to people who use the interface in English. Left empty it follows the Italian name, which stays the one conditions and processes use.",
  "configurazione.etichette.faseGuida": "The English name of the phase, shown to people who use the interface in English. Left empty it follows the Italian name, which stays the one transitions, conditions and code use.",
  "configurazione.etichette.opzioneInglese": "English name of \"{name}\"",
  "configurazione.etichette.faseInglese": "English name",
  "configurazione.etichette.nomeItaliano": "Name",
  "configurazione.etichette.inglese": "English",
} satisfies Translation<typeof it>;
