package webhooks

import (
	"net/http"
	"time"
)

// Le stesse protezioni SSRF dei webhook servono ai connettori dell'import (Jira e YouTrack
// via API, internal/modules/importer): l'URL lo scrive l'amministratore, la connessione la
// apre il server.

// SafeClient è un client HTTP che non si collega a indirizzi interni (salvo allowPrivate),
// non segue i redirect e non usa i proxy dell'ambiente.
func SafeClient(allowPrivate bool, timeout time.Duration) *http.Client {
	return newClient(allowPrivate, timeout)
}

// CheckURL valida un URL di destinazione (https, niente credenziali, niente nomi o IP
// interni salvo allowPrivate) e lo restituisce normalizzato.
func CheckURL(raw string, allowPrivate bool) (string, error) { return checkURL(raw, allowPrivate) }
