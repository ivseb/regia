package config

// ConnectorAllowPrivate (REGIA_CONNECTOR_ALLOW_PRIVATE, default false) è il consenso
// dell'operatore alle reti interne per i connettori DevSecOps (GitHub Enterprise Server in
// una rete privata). Senza, l'opzione «Rete interna» di un'integrazione non si può
// attivare e le chiamate vanno solo verso indirizzi pubblici; con, ogni integrazione
// sceglie da sé, e anche così gli indirizzi link-local (metadati del cloud) restano
// bloccati. In modalità sviluppo (--dev) la protezione è spenta del tutto.
func ConnectorAllowPrivate() bool { return envBool("REGIA_CONNECTOR_ALLOW_PRIVATE", false) }
