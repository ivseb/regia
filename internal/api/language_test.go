package api

import "testing"

func TestParseAcceptLanguage(t *testing.T) {
	for in, want := range map[string]string{
		"":                             "",
		"it-IT":                        "it",
		"en-US,en;q=0.9":               "en",
		"de-DE, en-GB;q=0.8, it;q=0.5": "en",
		"fr, de;q=0.9":                 "",
		"*":                            "",
		"en;q=0, it":                   "it",
		"EN-gb":                        "en",
	} {
		if got := parseAcceptLanguage(in); got != want {
			t.Errorf("%q: %q, atteso %q", in, got, want)
		}
	}
}

func TestResolveLanguage(t *testing.T) {
	en, it := "en", "it"
	if v := resolveLanguage(&en, &it, "it"); v.Effective != "en" || v.Source != "member" {
		t.Errorf("membro: %+v", v)
	}
	if v := resolveLanguage(nil, &en, "it"); v.Effective != "en" || v.Source != "tenant" {
		t.Errorf("tenant: %+v", v)
	}
	if v := resolveLanguage(nil, nil, "en-US"); v.Effective != "en" || v.Source != "browser" {
		t.Errorf("browser: %+v", v)
	}
	if v := resolveLanguage(nil, nil, ""); v.Effective != "it" || v.Source != "default" {
		t.Errorf("default: %+v", v)
	}
}
