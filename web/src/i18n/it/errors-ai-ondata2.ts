// Errori dell'AI, ondata 2 (server MCP, streaming dell'assistente).
import type { Source } from "../types";

export default {
  "errors.ai.mcp_disabled": "Il server MCP non è acceso in questa organizzazione: lo accende chi la amministra, in Configurazione → Intelligenza artificiale.",
  "errors.ai.stream_interrupted": "La risposta si è interrotta prima della fine: riprova.",
  "errors.auth.token_required": "Serve un token API: Authorization: Bearer seguito dal token.",
} satisfies Source;
