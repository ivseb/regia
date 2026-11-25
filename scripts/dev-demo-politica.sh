#!/usr/bin/env bash
# Politica della pipeline e adozione guidata di esempio nel tenant "demo" (DevSecOps, fase 3;
# solo sviluppo e prova). Salva una politica in due versioni (la seconda rende il SAST bloccante
# per le applicazioni di criticità alta), con un profilo di pipeline che ha la sezione di
# adozione: due workflow riusabili inventati, nessun contenuto di template aziendali.
# Se è indicato un finto GitHub (FINTO_GH_URL, avviato con `regia dev fake-github --write`),
# crea anche l'integrazione, scopre i repository e associa «strumenti-interni» al Portale
# fornitori, che non ha ancora una pipeline: da lì si prova «Proponi la pipeline».
# Uso: scripts/dev-demo-politica.sh [url]  — dopo dev-demo-devsecops.sh (lo richiama dev-demo-data.sh).
#      FINTO_GH_URL=http://127.0.0.1:8122 FINTO_GH_TOKEN=ghp_finto_sviluppo scripts/dev-demo-politica.sh [url]
set -euo pipefail
B=${1:-http://127.0.0.1:8080}
PW=${REGIA_DEV_PASSWORD:-'prova-locale-2026!'}
python3 - "$B" "$PW" "${FINTO_GH_URL:-}" "${FINTO_GH_TOKEN:-ghp_finto_sviluppo}" <<'PY'
import http.cookiejar, json, sys, urllib.error, urllib.request

base, pw, fake, fake_token = sys.argv[1:5]
jar = http.cookiejar.CookieJar()
web = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))

def call(method, path, body=None):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(base + path, data=data, method=method)
    if body is not None:
        req.add_header("Content-Type", "application/json")
    try:
        with web.open(req) as r:
            raw = r.read()
            return json.loads(raw) if raw else {}
    except urllib.error.HTTPError as e:
        raise SystemExit(f"{method} {path}: {e.code} {e.read().decode(errors='replace')}")

me = call("POST", "/api/v1/auth/login", {"email": "admin@demo.test", "password": pw})
demo = next(t["tenant_id"] for t in me["tenants"] if t["slug"] == "demo")
call("POST", "/api/v1/me/tenant", {"tenant_id": demo})

# --- Politica: due versioni.
st = call("GET", "/api/v1/devsecops/policy")
if not st["configured"]:
    d = st["definition"]
    call("PUT", "/api/v1/devsecops/policy", {"definition": d, "note": "Politica iniziale di esempio"})
    d["classes"][1]["modes"]["sast"] = "block"
    call("PUT", "/api/v1/devsecops/policy", {"definition": d, "note": "Il SAST diventa bloccante per le applicazioni di criticità alta"})
    print("Politica della pipeline: 2 versioni salvate.")

# --- Profilo con la sezione di adozione.
adoption = {
    "params": [{"key": "stack", "label": "Stack dell'applicazione", "default": "node", "options": ["node", "java", "python", "dotnet"]}],
    "templates": [
        {"key": "ci", "name": "Build, test e qualità", "description": "Compila, esegue i test e i controlli di qualità.",
         "uses": "esempio/pipeline-standard/.github/workflows/ci.yml@v1", "default": True, "inherit_secrets": True,
         "with": {"stack": "{stack}", "application": "{application}"}, "controls": ["test", "lint", "secrets"]},
        {"key": "sicurezza", "name": "Scansioni di sicurezza", "description": "Dipendenze, analisi statica del codice e immagine del container.",
         "uses": "esempio/pipeline-standard/.github/workflows/security.yml@v1", "default": True, "needs": ["ci"],
         "with": {"stack": "{stack}"}, "controls": ["dependencies", "sast", "image"]},
    ],
}
profiles = call("GET", "/api/v1/devsecops/pipeline-profiles")["items"]
prof = next((p for p in profiles if "adoption" in p["definition"]), None)
if prof is None:
    pid = call("POST", "/api/v1/devsecops/pipeline-profiles", {"key": "standard-esempio", "name": "Template standard (esempio)",
        "description": "Profilo di esempio con la sezione di adozione.", "definition": {"format": "regia.pipeline-profile/v1", "adoption": adoption}})["id"]
    print("Profilo di pipeline con l'adozione: creato.")
else:
    pid = prof["id"]

# --- Finto GitHub (facoltativo).
if fake:
    integrations = call("GET", "/api/v1/devsecops/github/integrations")["items"]
    ig = next((i for i in integrations if i["name"] == "GitHub di prova"), None)
    iid = ig["id"] if ig else call("POST", "/api/v1/devsecops/github/integrations", {"name": "GitHub di prova", "base_url": fake, "owners": ["esempio"],
        "auth_mode": "token", "token": fake_token, "profile_id": pid})["id"]
    call("POST", f"/api/v1/devsecops/github/integrations/{iid}/discover")
    repos = call("GET", f"/api/v1/devsecops/github/integrations/{iid}/repositories")["items"]
    app = next((a for a in call("GET", "/api/v1/architecture/applications")["items"] if a.get("code") == "FORN"), None)
    repo = next((r for r in repos if r["full_name"] == "esempio/strumenti-interni"), None)
    if app and repo and repo["status"] != "confirmed":
        call("PUT", f"/api/v1/devsecops/github/integrations/{iid}/repositories/{repo['id']}", {"status": "confirmed", "application_id": app["id"]})
        print("Integrazione GitHub di prova: «strumenti-interni» associato al Portale fornitori.")
print("Politica della pipeline di esempio: fatto.")
PY
