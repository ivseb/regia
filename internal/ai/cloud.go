package ai

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Credenziali dei cloud (replica/ai.md §3: Anthropic via AWS Bedrock e Google Vertex per la
// regione UE). Come le chiavi, si cifrano nel database con la chiave applicativa legata al
// fornitore e non tornano mai indietro: l'API dice solo che ci sono (has_key). Regione e
// progetto non sono segreti e si vedono.

// AWSCredentials è il segreto di un fornitore Bedrock: la chiave API di Bedrock oppure una
// coppia di chiavi d'accesso IAM (con il token di sessione, se temporanee).
type AWSCredentials struct {
	APIKey          string `json:"api_key,omitempty"`
	AccessKeyID     string `json:"access_key_id,omitempty"`
	SecretAccessKey string `json:"secret_access_key,omitempty"`
	SessionToken    string `json:"session_token,omitempty"`
}

// ParseAWSCredentials legge il segreto di Bedrock salvato da Regia.
func ParseAWSCredentials(secret string) (AWSCredentials, bool) {
	var c AWSCredentials
	if err := json.Unmarshal([]byte(secret), &c); err != nil {
		return c, false
	}
	return c, c.APIKey != "" || (c.AccessKeyID != "" && c.SecretAccessKey != "")
}

// ServiceAccount sono i campi della chiave JSON di un service account Google Cloud che Regia
// controlla al salvataggio; l'adattatore Vertex legge la chiave intera.
type ServiceAccount struct {
	Type        string `json:"type"`
	ProjectID   string `json:"project_id"`
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	TokenURI    string `json:"token_uri"`
}

// GoogleTokenURI è l'indirizzo dei token OAuth di Google: una chiave che ne indica un altro si
// rifiuta (Regia non manda la firma del service account altrove).
const GoogleTokenURI = "https://oauth2.googleapis.com/token"

var (
	awsRegionRe    = regexp.MustCompile(`^[a-z]{2}(-gov|-iso[a-z]*)?-[a-z]+-[0-9]{1,2}$`)
	gcpRegionRe    = regexp.MustCompile(`^[a-z]+-[a-z]+[0-9]{1,2}$`)
	gcpProjectRe   = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)
	awsAccessKeyRe = regexp.MustCompile(`^[A-Z0-9]{16,128}$`)
)

// ValidVertexRegion: «global», le multiregioni «eu» e «us», o una regione (europe-west8).
func ValidVertexRegion(r string) bool {
	return r == "global" || r == "eu" || r == "us" || gcpRegionRe.MatchString(r)
}

func noSpace(s string, max int) bool {
	return s != "" && len(s) <= max && !strings.ContainsAny(s, " \n\r\t")
}

// cleanCloud controlla regione, progetto e credenziali dei fornitori cloud e prepara il
// segreto da cifrare (in.secret). Credenziali vuote in modifica = invariate.
func cleanCloud(k Kind, in *ProviderInput, prev *ProviderRow) error {
	in.Region, in.ProjectID = strings.TrimSpace(in.Region), strings.TrimSpace(in.ProjectID)
	// In modifica regione e progetto non indicati restano quelli salvati.
	if prev != nil && in.Region == "" {
		in.Region = prev.Region
	}
	switch k.Cloud {
	case "aws":
		in.ProjectID, in.RemoveKey = "", false
		if !awsRegionRe.MatchString(in.Region) {
			return invalid("region", "ai.region_invalid", "regione AWS non valida (es. eu-central-1)")
		}
		key := strings.TrimSpace(in.APIKey)
		id, sec, tok := strings.TrimSpace(in.AWSAccessKeyID), strings.TrimSpace(in.AWSSecretAccessKey), strings.TrimSpace(in.AWSSessionToken)
		var c AWSCredentials
		switch {
		case key != "":
			if !noSpace(key, 4000) {
				return invalid("api_key", "ai.key_invalid", "chiave non valida")
			}
			c.APIKey = key
		case id != "" || sec != "":
			if !awsAccessKeyRe.MatchString(id) {
				return invalid("aws_access_key_id", "ai.key_invalid", "ID della chiave d'accesso non valido")
			}
			if !noSpace(sec, 512) {
				return invalid("aws_secret_access_key", "ai.key_invalid", "chiave d'accesso segreta non valida")
			}
			if tok != "" && !noSpace(tok, 4000) {
				return invalid("aws_session_token", "ai.key_invalid", "token di sessione non valido")
			}
			c = AWSCredentials{AccessKeyID: id, SecretAccessKey: sec, SessionToken: tok}
		case prev != nil && prev.HasKey:
			return nil
		default:
			return invalid("api_key", "ai.key_required", "servono la chiave API di Bedrock o le chiavi d'accesso AWS")
		}
		b, _ := json.Marshal(c)
		in.secret = string(b)
	case "gcp":
		in.RemoveKey = false
		if !ValidVertexRegion(in.Region) {
			return invalid("region", "ai.region_invalid", "regione Google Cloud non valida (es. europe-west8, eu o global)")
		}
		raw := strings.TrimSpace(in.ServiceAccountJSON)
		if raw != "" {
			if len(raw) > 16000 {
				return invalid("service_account_json", "ai.key_invalid", "chiave del service account troppo lunga")
			}
			var sa ServiceAccount
			if err := json.Unmarshal([]byte(raw), &sa); err != nil || sa.Type != "service_account" || sa.ClientEmail == "" ||
				!strings.Contains(sa.PrivateKey, "PRIVATE KEY") {
				return invalid("service_account_json", "ai.key_invalid", "serve la chiave JSON di un service account (type: service_account)")
			}
			if sa.TokenURI != "" && sa.TokenURI != GoogleTokenURI {
				return invalid("service_account_json", "ai.key_invalid", "la chiave indica un indirizzo dei token diverso da quello di Google")
			}
			if in.ProjectID == "" {
				in.ProjectID = sa.ProjectID
			}
			var compact map[string]any
			_ = json.Unmarshal([]byte(raw), &compact)
			b, _ := json.Marshal(compact)
			in.secret = string(b)
		} else if prev == nil || !prev.HasKey {
			return invalid("service_account_json", "ai.key_required", "serve la chiave JSON del service account")
		}
		if in.ProjectID == "" && prev != nil {
			in.ProjectID = prev.ProjectID
		}
		if !gcpProjectRe.MatchString(in.ProjectID) {
			return invalid("project_id", "ai.project_invalid", "ID del progetto Google Cloud non valido")
		}
	}
	return nil
}
