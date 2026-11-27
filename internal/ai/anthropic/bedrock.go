package anthropic

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2/bedrock"
	"github.com/anthropics/anthropic-sdk-go/v2/option"

	"regia/internal/ai"
)

// KindBedrock è Anthropic via AWS Bedrock.
const KindBedrock = "anthropic_bedrock"

// Modelli proposti su Bedrock: gli stessi di Anthropic con il prefisso «anthropic.» che
// l'endpoint Messages di Bedrock (Mantle) usa.
const (
	BedrockModelFast = "anthropic." + ModelFast
	BedrockModelDeep = "anthropic." + ModelDeep
)

// RegisterBedrock descrive Anthropic via AWS Bedrock: regione e credenziali AWS (chiave API
// di Bedrock oppure chiavi d'accesso IAM) nelle impostazioni del tenant.
func RegisterBedrock() ai.Kind {
	return ai.Kind{Key: KindBedrock, New: NewBedrock, NeedsKey: true, External: true, Tools: true, Cloud: "aws",
		Proposed: []ai.ModelInfo{
			{ID: BedrockModelFast, Name: "Claude Haiku 5.5", Tier: ai.TierFast},
			{ID: BedrockModelDeep, Name: "Claude Opus 5.5", Tier: ai.TierDeep},
		}}
}

// BedrockBaseURL è l'endpoint Messages di Bedrock (Mantle) nella regione.
func BedrockBaseURL(region string) string {
	return fmt.Sprintf("https://bedrock-mantle.%s.api.aws/anthropic", region)
}

// NewBedrock crea l'adattatore con il client Mantle dell'SDK ufficiale (pacchetto bedrock).
// Regione, indirizzo e credenziali sono sempre espliciti: così l'SDK non ricade sulle
// variabili d'ambiente né sulla catena di credenziali AWS del server.
func NewBedrock(cfg ai.ProviderConfig) (ai.Provider, error) {
	creds, ok := ai.ParseAWSCredentials(cfg.APIKey)
	if !ok {
		return nil, &ai.ProviderError{Code: "auth", Msg: "mancano le credenziali di Bedrock"}
	}
	if cfg.Region == "" {
		return nil, &ai.ProviderError{Code: "config", Msg: "manca la regione AWS"}
	}
	base := cfg.BaseURL
	if base == "" {
		base = BedrockBaseURL(cfg.Region)
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 5 * time.Minute}
	}
	mc, err := bedrock.NewMantleClient(context.Background(), bedrock.MantleClientConfig{
		APIKey:             creds.APIKey,
		AWSAccessKey:       creds.AccessKeyID,
		AWSSecretAccessKey: creds.SecretAccessKey,
		AWSSessionToken:    creds.SessionToken,
		AWSRegion:          cfg.Region,
		BaseURL:            base,
	}, option.WithoutEnvironmentDefaults(), option.WithHTTPClient(hc), option.WithMaxRetries(2))
	if err != nil {
		return nil, &ai.ProviderError{Code: "config", Msg: "configurazione di Bedrock non valida"}
	}
	return &provider{msgs: mc.Messages, proposed: RegisterBedrock().Proposed, who: "Bedrock"}, nil
}
