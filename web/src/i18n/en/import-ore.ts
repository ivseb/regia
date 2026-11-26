import type { Translation } from "../types";
import type it from "../it/import-ore";

export default {
  "import.ore.target.label": "What you import",
  "import.ore.target.costs": "Costs",
  "import.ore.target.hours": "Hours",
  "import.ore.help": "Each row is a time entry: the activity or item is its key, the person is their email. Map one column to hours (decimals are fine) or one to minutes. A row identical to one already imported is skipped; to tell two equal entries apart, map a reference.",
  "import.ore.field.element": "Activity or item (key)",
  "import.ore.field.member": "Person (email)",
  "import.ore.field.work_date": "Date",
  "import.ore.field.hours": "Hours (decimals allowed)",
  "import.ore.field.minutes": "Minutes",
  "import.ore.field.activity_type": "Activity type",
  "import.ore.field.text": "Text",
  "import.ore.field.capitalizable": "Capitalizable (yes/no)",
  "import.ore.field.source_ref": "Reference (row number)",
  "import.ore.defaultElement": "Key if the column is missing",
  "import.ore.defaultMember": "Email if the column is missing",
  "import.ore.col.activity": "Activity",
  "import.ore.col.person": "Person",
  "import.ore.col.hours": "Hours",
  "import.ore.col.type": "Type",
  "import.ore.alreadyImported": "already imported",
} satisfies Translation<typeof it>;
