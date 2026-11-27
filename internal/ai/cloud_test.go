package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

// Validazione di regione, progetto e credenziali dei cloud; il segreto si compone solo con le
// credenziali nuove (in modifica, vuote = invariate).
func TestCleanCloud(t *testing.T) {
	aws := Kind{Key: "anthropic_bedrock", Cloud: "aws", NeedsKey: true}
	gcp := Kind{Key: "anthropic_vertex", Cloud: "gcp", NeedsKey: true}
	code := func(err error) string {
		if e, ok := err.(*Error); ok {
			return e.Field + ":" + e.Code
		}
		return ""
	}
	in := ProviderInput{Region: "eu-central-1", AWSAccessKeyID: "AKIAPROVAREGIA0001", AWSSecretAccessKey: "segreto"}
	if err := cleanCloud(aws, &in, nil); err != nil {
		t.Fatal(err)
	}
	var c AWSCredentials
	_ = json.Unmarshal([]byte(in.secret), &c)
	if c.AccessKeyID != "AKIAPROVAREGIA0001" || c.SecretAccessKey != "segreto" {
		t.Fatalf("segreto: %q", in.secret)
	}
	if _, ok := ParseAWSCredentials(in.secret); !ok {
		t.Fatal("credenziali non rilette")
	}
	for _, bad := range []ProviderInput{
		{Region: "Europa", APIKey: "k"},
		{Region: "eu-central-1"},
		{Region: "eu-central-1", AWSAccessKeyID: "chiave con spazi", AWSSecretAccessKey: "s"},
		{Region: "eu-central-1", AWSAccessKeyID: "AKIAPROVAREGIA0001"},
	} {
		if err := cleanCloud(aws, &bad, nil); err == nil {
			t.Fatalf("accettato: %+v", bad)
		}
	}
	keep := ProviderInput{Region: "eu-south-1"}
	if err := cleanCloud(aws, &keep, &ProviderRow{HasKey: true}); err != nil || keep.secret != "" {
		t.Fatalf("credenziali invariate: %v %q", err, keep.secret)
	}

	sa := `{"type":"service_account","project_id":"progetto-prova","client_email":"a@b.iam.gserviceaccount.com",
		"private_key":"-----BEGIN PRIVATE KEY-----\nfinta\n-----END PRIVATE KEY-----\n","token_uri":"https://oauth2.googleapis.com/token"}`
	g := ProviderInput{Region: "europe-west8", ServiceAccountJSON: sa}
	if err := cleanCloud(gcp, &g, nil); err != nil {
		t.Fatal(err)
	}
	if g.ProjectID != "progetto-prova" || !strings.Contains(g.secret, "service_account") || strings.Contains(g.secret, "\n\t") {
		t.Fatalf("vertex: %+v", g)
	}
	for name, bad := range map[string]ProviderInput{
		"region:ai.region_invalid":             {Region: "Milano", ServiceAccountJSON: sa},
		"service_account_json:ai.key_required": {Region: "eu", ProjectID: "progetto-prova"},
		"service_account_json:ai.key_invalid":  {Region: "eu", ServiceAccountJSON: `{"type":"external_account","credential_source":{"file":"/etc/passwd"}}`},
		"project_id:ai.project_invalid":        {Region: "eu", ProjectID: "X", ServiceAccountJSON: strings.Replace(sa, "progetto-prova", "", 1)},
	} {
		if got := code(cleanCloud(gcp, &bad, nil)); got != name {
			t.Fatalf("%s: %s", name, got)
		}
	}
	other := ProviderInput{Region: "eu", ServiceAccountJSON: strings.Replace(sa, "https://oauth2.googleapis.com/token", "https://esempio.invalid/token", 1)}
	if got := code(cleanCloud(gcp, &other, nil)); got != "service_account_json:ai.key_invalid" {
		t.Fatalf("indirizzo dei token estraneo: %s", got)
	}
	for _, r := range []string{"global", "eu", "us", "europe-west8", "us-central1"} {
		if !ValidVertexRegion(r) {
			t.Fatal(r)
		}
	}
}

// Il costo usa il listino del modello anche con i prefissi dei cloud e il prezzo dell'input in
// cache proprio del modello (OpenAI, Gemini).
func TestListPriceCloud(t *testing.T) {
	if c := Cost(nil, "anthropic.claude-opus-5-5", 1_000_000, 0); c != 4 {
		t.Fatalf("Bedrock: %v", c)
	}
	if c := Cost(nil, "eu.anthropic.claude-opus-5-5", 0, 1_000_000); c != 20 {
		t.Fatalf("profilo UE: %v", c)
	}
	if c := CostCached(nil, "gpt-6.2-astra", 1_000_000, 1_000_000, 0, 0); c != 0.40 {
		t.Fatalf("cache OpenAI: %v", c)
	}
	if c := CostCached(nil, "claude-opus-5-5", 1_000_000, 1_000_000, 0, 0); c < 0.399 || c > 0.401 {
		t.Fatalf("cache Anthropic al 10%%: %v", c)
	}
	in := 1.0
	if c := CostCached(&ProviderRow{PriceInput: &in}, "gpt-6.2-astra", 1_000_000, 1_000_000, 0, 0); c != 0.1 {
		t.Fatalf("prezzo del fornitore: %v", c)
	}
}
