package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"time"

	sdk "github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"regia/internal/ai"
)

// KindVertex è Anthropic via Google Vertex AI.
const KindVertex = "anthropic_vertex"

// RegisterVertex descrive Anthropic via Google Vertex: regione, progetto e chiave JSON del
// service account nelle impostazioni del tenant. Su Vertex i modelli hanno il nome di Anthropic.
func RegisterVertex() ai.Kind {
	return ai.Kind{Key: KindVertex, New: NewVertex, NeedsKey: true, External: true, Tools: true, Cloud: "gcp",
		Proposed: Register().Proposed}
}

// vertexScope è l'ambito OAuth chiesto per il token del service account.
const vertexScope = "https://www.googleapis.com/auth/cloud-platform"

// VertexBaseURL è l'indirizzo di Vertex AI per la regione: «global», le multiregioni «eu» e
// «us» o una regione (europe-west8 → https://europe-west8-aiplatform.googleapis.com/).
func VertexBaseURL(region string) string {
	switch region {
	case "global":
		return "https://aiplatform.googleapis.com/"
	case "eu", "us":
		return "https://aiplatform." + region + ".rep.googleapis.com/"
	}
	return "https://" + region + "-aiplatform.googleapis.com/"
}

var vertexModelRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._@-]{0,199}$`)

// NewVertex crea l'adattatore con il client dell'SDK ufficiale e il token del service account.
//
// Non usa il pacchetto vertex dell'SDK: porta con sé google.golang.org/api e gRPC (circa 22 MB
// in più nel binario, misurati il 2026-11-27) e accetta qualunque credenziale di Google. Qui
// le richieste sono quelle dell'SDK, riscritte come fa quel pacchetto secondo la guida di
// Vertex (rawPredict e streamRawPredict, versione dell'API nel corpo), e il token viene solo da
// una chiave di service account (golang.org/x/oauth2/google), mai dalle credenziali predefinite
// del server né da configurazioni che leggono file o eseguono comandi.
func NewVertex(cfg ai.ProviderConfig) (ai.Provider, error) {
	if cfg.Region == "" || cfg.ProjectID == "" {
		return nil, &ai.ProviderError{Code: "config", Msg: "mancano regione o progetto di Google Cloud"}
	}
	if !ai.ValidVertexRegion(cfg.Region) {
		return nil, &ai.ProviderError{Code: "config", Msg: "regione di Google Cloud non valida"}
	}
	var sa ai.ServiceAccount
	if err := json.Unmarshal([]byte(cfg.APIKey), &sa); err != nil || sa.Type != "service_account" {
		return nil, &ai.ProviderError{Code: "auth", Msg: "manca la chiave del service account"}
	}
	jwtCfg, err := google.JWTConfigFromJSON([]byte(cfg.APIKey), vertexScope)
	if err != nil {
		return nil, &ai.ProviderError{Code: "auth", Msg: "chiave del service account non valida"}
	}
	// Un indirizzo dei token diverso da quello di Google vale solo nei test (indirizzo base di prova).
	if jwtCfg.TokenURL != ai.GoogleTokenURI && cfg.BaseURL == "" {
		return nil, &ai.ProviderError{Code: "auth", Msg: "la chiave indica un indirizzo dei token diverso da quello di Google"}
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 5 * time.Minute}
	}
	tokenCtx := context.WithValue(context.Background(), oauth2.HTTPClient, hc)
	authed := *hc
	authed.Transport = &oauth2.Transport{Source: oauth2.ReuseTokenSource(nil, jwtCfg.TokenSource(tokenCtx)), Base: hc.Transport}
	base := cfg.BaseURL
	if base == "" {
		base = VertexBaseURL(cfg.Region)
	}
	c := sdk.NewClient(
		option.WithoutEnvironmentDefaults(),
		option.WithBaseURL(base),
		option.WithHTTPClient(&authed),
		option.WithMaxRetries(2),
		option.WithMiddleware(vertexMiddleware(cfg.Region, cfg.ProjectID)),
	)
	return &provider{msgs: c.Messages, proposed: RegisterVertex().Proposed, who: "Vertex"}, nil
}

// vertexMiddleware riscrive le richieste Messages nella forma di Vertex: il modello passa dal
// corpo al percorso (…/publishers/anthropic/models/<modello>:rawPredict, o streamRawPredict
// in streaming; il conteggio dei token è …/models/count-tokens:rawPredict) e la versione
// dell'API passa dall'intestazione anthropic-version al campo anthropic_version del corpo.
func vertexMiddleware(region, project string) option.Middleware {
	prefix := fmt.Sprintf("/v1/projects/%s/locations/%s/publishers/anthropic/models/", url.PathEscape(project), url.PathEscape(region))
	return func(r *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		if r.Method != http.MethodPost || r.Body == nil {
			return nil, &ai.ProviderError{Code: "config", Msg: "richiesta non supportata da Vertex"}
		}
		version := r.Header.Get("anthropic-version")
		r.Header.Del("anthropic-version")
		raw, err := io.ReadAll(r.Body)
		r.Body.Close()
		if err != nil {
			return nil, err
		}
		var body map[string]json.RawMessage
		if err := json.Unmarshal(raw, &body); err != nil {
			return nil, &ai.ProviderError{Code: "config", Msg: "richiesta non valida"}
		}
		if version != "" {
			if _, ok := body["anthropic_version"]; !ok {
				v, _ := json.Marshal(version)
				body["anthropic_version"] = v
			}
		}
		var model string
		_ = json.Unmarshal(body["model"], &model)
		var stream bool
		_ = json.Unmarshal(body["stream"], &stream)
		switch r.URL.Path {
		case "/v1/messages":
			if !vertexModelRe.MatchString(model) {
				return nil, &ai.ProviderError{Code: "model_not_found", Msg: "nome del modello non valido"}
			}
			delete(body, "model")
			spec := "rawPredict"
			if stream {
				spec = "streamRawPredict"
			}
			r.URL.Path = prefix + model + ":" + spec
		case "/v1/messages/count_tokens":
			r.URL.Path = prefix + "count-tokens:rawPredict"
		default:
			return nil, &ai.ProviderError{Code: "config", Msg: "richiesta non supportata da Vertex"}
		}
		r.URL.RawPath = ""
		out, _ := json.Marshal(body)
		r.Body = io.NopCloser(bytes.NewReader(out))
		r.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(out)), nil }
		r.ContentLength = int64(len(out))
		return next(r)
	}
}
