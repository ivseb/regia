#!/usr/bin/env bash
# Metriche DORA di esempio nel tenant "demo" (DevSecOps, fase 5a; solo sviluppo e prova).
# Aggiunge ai rilasci del cuore DevSecOps altri rilasci in produzione su sei settimane, con
# l'inizio della modifica (il primo controllo del commit) giorni prima del rilascio, qualche
# hotfix, un deploy annullato e un ritorno alla versione precedente, e tre incidenti collegati
# alle applicazioni (uno con ripristino lento), poi ricalcola i punti.
# Cadenze diverse per servizio, così il confronto tra team mostra più livelli.
# Uso: scripts/dev-demo-dora.sh [url]  — dopo dev-demo-devsecops.sh e dev-demo-risk.sh
# (lo richiama dev-demo-data.sh).
set -euo pipefail
B=${1:-http://127.0.0.1:8080}
PW=${REGIA_DEV_PASSWORD:-'prova-locale-2026!'}
python3 - "$B" "$PW" <<'PY'
import datetime as dt, http.cookiejar, json, sys, urllib.parse, urllib.request

base, pw = sys.argv[1], sys.argv[2]

def session(email):
    jar = http.cookiejar.CookieJar()
    web = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
    def call(method, path, body=None, token=None):
        data = None if body is None else json.dumps(body).encode()
        req = urllib.request.Request(base + path, data=data, method=method)
        if body is not None:
            req.add_header("Content-Type", "application/json")
        opener = web
        if token:
            req.add_header("Authorization", "Bearer " + token)
            opener = urllib.request.build_opener()
        try:
            with opener.open(req) as r:
                raw = r.read()
                return json.loads(raw) if raw else {}
        except urllib.error.HTTPError as e:
            raise SystemExit(f"{method} {path}: {e.code} {e.read().decode(errors='replace')}")
    me = call("POST", "/api/v1/auth/login", {"email": email, "password": pw})
    demo = next(t["tenant_id"] for t in me["tenants"] if t["slug"] == "demo")
    call("POST", "/api/v1/me/tenant", {"tenant_id": demo})
    return call

admin = session("admin@demo.test")
incidenti = session("incidenti@demo.test")
tok = admin("POST", "/api/v1/me/tokens", {"name": "Pipeline demo (DORA)", "expires_in_days": 30, "scopes": ["devsecops.write"]})["token"]

def push(kind, payload):
    return admin("POST", "/api/v1/devsecops/" + kind, dict(payload, format="regia.devsecops/v1"), token=tok)

now = dt.datetime.now(dt.timezone.utc).replace(microsecond=0)
def at(days, hour=10, minute=0):
    return (now - dt.timedelta(days=days)).replace(hour=hour, minute=minute, second=0).isoformat().replace("+00:00", "Z")
def before(iso, hours):
    t = dt.datetime.fromisoformat(iso.replace("Z", "+00:00")) - dt.timedelta(hours=hours)
    return t.isoformat().replace("+00:00", "Z")
def after(iso, hours):
    return before(iso, -hours)

ok = [{"name": "test", "mode": "block", "outcome": "passed"}, {"name": "secrets", "mode": "block", "outcome": "passed"},
      {"name": "cves", "mode": "block", "outcome": "passed"}]

# servizio → (applicazione, repository, [(giorni fa, ore dall'inizio della modifica, titolo)])
plan = {
    "area-clienti-web": ("WEB-AR", "digitale/area-clienti-web", [(39, 14, ""), (36, 20, ""), (34, 9, ""), (31, 30, ""), (29, 11, ""), (26, 16, ""), (24, 8, ""), (22, 22, ""),
                                                                  (18, 10, ""), (15, 14, ""), (13, 9, ""), (10, 18, ""), (8, 12, ""), (6, 15, ""), (4, 8, ""), (3, 20, "")]),
    "clienti-api": ("API-CLI", "digitale/clienti-api", [(36, 30, ""), (31, 50, ""), (27, 26, ""), (24, 40, ""), (19, 22, ""), (16, 60, ""), (13, 28, ""), (10, 36, ""), (7, 24, ""), (3, 70, "")]),
    "clienti-worker": ("API-CLI", "digitale/clienti-worker", [(28, 36, ""), (18, 48, ""), (9, 30, "")]),
    "pagamenti-gateway": ("PAY", "pagamenti/gateway", [(34, 72, ""), (29, 96, ""), (25, 40, ""), (25, 3, "Hotfix autorizzazioni 3DS"), (20, 24, ""), (14, 120, ""), (11, 80, "")]),
    "identity-broker": ("IAM", "piattaforma/identity-broker", [(30, 240, ""), (21, 200, ""), (12, 260, ""), (6, 300, "")]),
    "app-mobile-bff": ("APP-MOB", "mobile/bff", [(37, 48, ""), (32, 40, ""), (26, 52, ""), (21, 36, ""), (17, 60, ""), (12, 44, ""), (8, 48, ""), (5, 40, "")]),
    "dwh-etl": ("DWH", "dati/dwh-etl", [(34, 400, ""), (5, 380, "")]),
    "cruscotti-web": ("BI", "dati/cruscotti-web", [(20, 72, "")]),
    "fornitori-portale": ("FORN", "acquisti/portale-fornitori", [(30, 100, ""), (14, 110, ""), (7, 90, "")]),
}

count, n = 0, 0
for svc, (app, repo, items) in plan.items():
    service = {"name": svc, "repository": repo, "provider": "github", "url": f"https://git.example.test/{repo}", "application": app}
    for days, lead, title in items:
        n += 1
        week = (now - dt.timedelta(days=days)).isocalendar()[1]
        version = f"2026.{week}.{n}" if not title else f"2026.{week}.{n}-hotfix"
        commit = f"{(n * 7919 + 13) % 16**12:012x}{'cd' * 14}"
        hour = 9 + (n % 6) if not title else 16
        rel_at = at(days, hour)
        push("check-runs", {"service": service, "commit": commit, "ref": "main", "run_id": f"d{n}", "verdict": "passed", "checks": ok, "at": before(rel_at, lead)})
        push("releases", {"service": service, "version": version, "commit": commit, "environment": "prod", "change_number": f"CHG004{2000 + n}",
                          "change_title": title or f"Rilascio {version}", "requested_by": "Giulia Neri", "approved_by": "Paolo Conti",
                          "approved_at": before(rel_at, 1), "at": rel_at, "checks": ok})
        count += 1

pay = {"name": "pagamenti-gateway", "repository": "pagamenti/gateway", "provider": "github", "application": "PAY"}
api = {"name": "clienti-api", "repository": "digitale/clienti-api", "provider": "github", "application": "API-CLI"}
# Un deploy annullato sul gateway e un ritorno alla versione precedente sulle API clienti.
push("deployments", {"service": pay, "environment": "prod", "version": "2026.99.0", "status": "rolled_back", "at": at(17, 11), "source": "gitops", "external_ref": "argocd/pagamenti-prod/2026.99.0"})
push("deployments", {"service": api, "environment": "prod", "version": "5.3.1", "status": "succeeded", "at": at(3, 17), "source": "gitops", "external_ref": "argocd/clienti-api-prod/5.3.1-ritorno"})

def app_key(code):
    return next(a["key"] for a in admin("GET", f"/api/v1/architecture/applications?q={code}")["items"] if a["code"] == code)

def incident(title, app, days, hour, hours_to_fix):
    detected = at(days, hour)
    inc = incidenti("POST", "/api/v1/incidents", {"title": title, "detected_at": detected, "impacted": [app_key(app)], "cause": "system"})
    res = incidenti("PATCH", "/api/v1/incidents/" + inc["key"], {"resolved_at": after(detected, hours_to_fix)})
    # Registrare la risoluzione porta l'incidente alla fase di risoluzione se il processo lo
    # consente; dalla fase iniziale no: si passa dalla classificazione, come farebbe una persona.
    if (res.get("phase_move") or {}).get("status") == "choose":
        for fase in ("Classificato", "Risolto"):
            incidenti("POST", "/api/v1/elements/" + res["element"]["id"] + "/transitions", {"to": fase})
    return inc["key"]

keys = [
    incident("Errori 500 sull'area clienti dopo il rilascio", "WEB-AR", 8, 13, 3),
    incident("Pagamenti rifiutati dopo il rilascio", "PAY", 25, 14, 1.5),
    incident("Accessi lenti per l'identity broker", "IAM", 12, 15, 20),
]
r = admin("POST", "/api/v1/devsecops/dora/recompute")
print(f"DORA: {count} rilasci in produzione in più, 2 deploy anomali, incidenti {', '.join(keys)}, {r.get('points', 0)} punti calcolati.")
PY
