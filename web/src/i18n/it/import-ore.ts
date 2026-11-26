// Import CSV delle ore dal timesheet (target «ore» accanto ai costi), nella pagina Import.
// Chiavi «import.ore.»: vedi web/src/i18n/GLOSSARIO.md.
import type { Source } from "../types";

export default {
  "import.ore.target.label": "Che cosa importi",
  "import.ore.target.costs": "Costi",
  "import.ore.target.hours": "Ore",
  "import.ore.help": "Ogni riga è una registrazione di ore: l'attività o l'elemento si indica con la sua chiave, la persona con la sua email. Mappa una colonna per le ore (anche decimali) oppure una per i minuti. Una riga identica a una già importata viene saltata; per distinguere due registrazioni uguali mappa un riferimento.",
  "import.ore.field.element": "Attività o elemento (chiave)",
  "import.ore.field.member": "Persona (email)",
  "import.ore.field.work_date": "Data",
  "import.ore.field.hours": "Ore (anche decimali)",
  "import.ore.field.minutes": "Minuti",
  "import.ore.field.activity_type": "Tipo di attività",
  "import.ore.field.text": "Testo",
  "import.ore.field.capitalizable": "Capitalizzabile (sì/no)",
  "import.ore.field.source_ref": "Riferimento (numero della riga)",
  "import.ore.defaultElement": "Chiave se la colonna manca",
  "import.ore.defaultMember": "Email se la colonna manca",
  "import.ore.col.activity": "Attività",
  "import.ore.col.person": "Persona",
  "import.ore.col.hours": "Ore",
  "import.ore.col.type": "Tipo",
  "import.ore.alreadyImported": "già importata",
} satisfies Source;
