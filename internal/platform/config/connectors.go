package config

import "os"

// ConnectorAllowPrivate (REGIA_CONNECTOR_ALLOW_PRIVATE, default false) è il consenso
// dell'operatore alle reti interne per i connettori DevSecOps (GitHub Enterprise Server in
// una rete privata). Senza, l'opzione «Rete interna» di un'integrazione non si può
// attivare e le chiamate vanno solo verso indirizzi pubblici; con, ogni integrazione
// sceglie da sé, e anche così gli indirizzi link-local (metadati del cloud) restano
// bloccati. In modalità sviluppo (--dev) la protezione è spenta del tutto.
func ConnectorAllowPrivate() bool { return envBool("REGIA_CONNECTOR_ALLOW_PRIVATE", false) }

// ConnectorProxy (REGIA_CONNECTOR_PROXY, default vuoto) è il proxy HTTP in uscita dei
// connettori, per esempio http://proxy.azienda.it:3128 (con le credenziali nell'URL, se
// servono). È una scelta esplicita dell'operatore: le variabili standard HTTP_PROXY,
// HTTPS_PROXY e NO_PROXY non si usano mai. Ogni integrazione decide se passare dal proxy
// («Usa il proxy»); senza questa variabile l'opzione non si può attivare.
func ConnectorProxy() string { return os.Getenv("REGIA_CONNECTOR_PROXY") }
