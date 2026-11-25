package config

// WebhookAllowPrivate (REGIA_WEBHOOK_ALLOW_PRIVATE, default false) permette ai webhook
// di consegnare verso indirizzi interni, di loopback o di reti private, e via http.
// Serve solo quando il destinatario è davvero nella rete interna (per esempio un
// orchestratore on-premise): attivarla toglie la protezione SSRF. In modalità sviluppo
// (--dev) è implicita.
func WebhookAllowPrivate() bool { return envBool("REGIA_WEBHOOK_ALLOW_PRIVATE", false) }
