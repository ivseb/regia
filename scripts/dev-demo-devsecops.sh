#!/usr/bin/env bash
# Dati DevSecOps di esempio nel tenant "demo" (piano, passo 21; solo sviluppo e prova).
# Una pipeline immaginaria invia con un token API (permesso devsecops.write), come farebbe
# uno step curl in CI: servizi delle applicazioni del catalogo demo, esiti dei controlli,
# rilasci con il numero di change (alcuni collegati al portale B2B, uno provvisorio poi
# definitivo), deploy in staging e preprod, SBOM CycloneDX e SPDX piccole, report delle
# vulnerabilità (identificativi realistici ma inventati) e uno storico di sei settimane.
# Due servizi sono solo censiti: non ancora sulla pipeline.
# Uso: scripts/dev-demo-devsecops.sh [url]  — dopo dev-demo-architecture.sh e dev-demo-requirements.sh
# (lo richiama dev-demo-data.sh).
set -euo pipefail
B=${1:-http://127.0.0.1:8080}
PW=${REGIA_DEV_PASSWORD:-'prova-locale-2026!'}
python3 - "$B" "$PW" <<'PY'
import datetime as dt, http.cookiejar, json, sys, urllib.parse, urllib.request

base, pw = sys.argv[1], sys.argv[2]
jar = http.cookiejar.CookieJar()
web = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))

def call(method, path, body=None, token=None, ok=(200, 201, 202, 204)):
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
        detail = e.read().decode(errors="replace")
        if e.code in ok:
            return json.loads(detail) if detail else {}
        raise SystemExit(f"{method} {path}: {e.code} {detail}")

me = call("POST", "/api/v1/auth/login", {"email": "admin@demo.test", "password": pw})
demo = next(t["tenant_id"] for t in me["tenants"] if t["slug"] == "demo")
call("POST", "/api/v1/me/tenant", {"tenant_id": demo})
tok = call("POST", "/api/v1/me/tokens", {"name": "Pipeline demo (DevSecOps)", "expires_in_days": 30, "scopes": ["devsecops.write"]})["token"]

def push(kind, payload):
    payload = dict(payload, format="regia.devsecops/v1")
    return call("POST", "/api/v1/devsecops/" + kind, payload, token=tok).get("result", {})

def q(text):
    return call("GET", "/api/v1/elements?q=" + urllib.parse.quote(text))["items"]

b2b = q("tipo: business_request portale clienti B2B")
b2b = b2b[0]["key"] if b2b else "CHG0041007"
dora = q("tipo: business_request registro delle informazioni")
dora = dora[0]["key"] if dora else "CHG0041011"

now = dt.datetime.now(dt.timezone.utc).replace(microsecond=0)
def at(days, hour=10):
    return (now - dt.timedelta(days=days)).replace(hour=hour, minute=0, second=0).isoformat().replace("+00:00", "Z")

# servizio, applicazione (codice), repository, componenti (nome, versione, ecosistema)
services = {
    "area-clienti-web": ("WEB-AR", "digitale/area-clienti-web", [("react", "18.3.1", "npm"), ("vite", "5.4.8", "npm"), ("nginx", "1.25.4", "deb"), ("node", "18.20.4", "generic")]),
    "clienti-api": ("API-CLI", "digitale/clienti-api", [("spring-boot", "3.2.5", "maven"), ("jackson-databind", "2.17.0", "maven"), ("openjdk", "21.0.3", "generic"), ("libssl3", "3.0.13", "deb")]),
    "clienti-worker": ("API-CLI", "digitale/clienti-worker", [("celery", "5.3.6", "pypi"), ("requests", "2.31.0", "pypi"), ("python", "3.9.19", "generic")]),
    "pagamenti-gateway": ("PAY", "pagamenti/gateway", [("express", "4.19.2", "npm"), ("jsonwebtoken", "9.0.2", "npm"), ("node", "20.15.1", "generic"), ("zlib1g", "1.2.13", "deb")]),
    "identity-broker": ("IAM", "piattaforma/identity-broker", [("keycloak-core", "24.0.5", "maven"), ("openjdk", "17.0.11", "generic")]),
    "app-mobile-bff": ("APP-MOB", "mobile/bff", [("fastify", "4.28.1", "npm"), ("node", "22.4.1", "generic")]),
    "dwh-etl": ("DWH", "dati/dwh-etl", [("pandas", "2.2.2", "pypi"), ("sqlalchemy", "2.0.31", "pypi"), ("python", "3.11.9", "generic")]),
    "cruscotti-web": ("BI", "dati/cruscotti-web", [("vue", "3.4.31", "npm"), ("nginx", "1.27.0", "deb")]),
    "fornitori-portale": ("FORN", "acquisti/portale-fornitori", [("django", "5.0.7", "pypi"), ("python", "3.12.4", "generic")]),
}
purl = lambda n, v, e: f"pkg:{'deb/debian' if e == 'deb' else e}/{n}@{v}"

# Vulnerabilità inventate: (id, pacchetto, severità, corretta in, strato)
cves = {
    "clienti-api": [("CVE-2026-41201", "jackson-databind", "high", "2.17.2", "app"), ("CVE-2026-40877", "libssl3", "critical", "3.0.14", "image")],
    "pagamenti-gateway": [("CVE-2026-42118", "jsonwebtoken", "critical", "9.0.3", "app"), ("GHSA-4q7m-9vxc-2hfj", "express", "medium", "", "app"), ("CVE-2026-39940", "zlib1g", "high", "", "image")],
    "area-clienti-web": [("CVE-2026-38812", "nginx", "medium", "1.25.5", "image")],
    "clienti-worker": [("CVE-2026-40311", "requests", "high", "2.32.0", "app")],
    "dwh-etl": [("CVE-2026-37755", "sqlalchemy", "low", "", "app")],
}
# Una critica in più comparsa nell'ultima settimana sull'identity broker (il confronto a 7 giorni la mostra).
new_today = {"identity-broker": [("CVE-2026-43390", "keycloak-core", "critical", "24.0.6", "app")]}

def checks(secrets="passed", cves_out="passed", test="passed", image=None, skip=None):
    out = [{"name": "test", "mode": "block", "outcome": test, "summary": "unit e integrazione"},
           {"name": "secrets", "mode": "block", "outcome": secrets},
           {"name": "cves", "mode": "block", "outcome": cves_out},
           {"name": "lint", "mode": "warn", "outcome": "passed"}]
    if image:
        c = {"name": "image", "mode": "block", "outcome": image}
        if skip:
            c["skip"] = {"by": skip, "reason": "CVE dell'immagine base senza correzione disponibile: deroga in corso"}
        out.append(c)
    return out

def summary(svc):
    vs = cves.get(svc, [])
    return {"critical": sum(v[2] == "critical" for v in vs), "high": sum(v[2] == "high" for v in vs),
            "medium": sum(v[2] == "medium" for v in vs), "low": sum(v[2] == "low" for v in vs),
            "fixable_critical": sum(v[2] == "critical" and bool(v[3]) for v in vs), "fixable_high": sum(v[2] == "high" and bool(v[3]) for v in vs)}

def vulns(svc, comps, extra=()):
    ver = {n: v for n, v, _ in comps}
    eco = {n: e for n, _, e in comps}
    out = []
    for vid, pkg, sev, fix, layer in list(cves.get(svc, [])) + list(extra):
        out.append({"id": vid, "package": pkg, "version": ver.get(pkg, ""), "purl": purl(pkg, ver.get(pkg, ""), eco.get(pkg, "generic")),
                    "fixed_in": fix, "severity": sev, "layer": layer, "url": f"https://vulnerabilita.example.test/{vid}"})
    return out

people = [("Giulia Neri", "Paolo Conti"), ("Marco Ferri", "Laura Gatti"), ("Sara Riva", "Paolo Conti"), ("Luca Moretti", "Elena Sala")]

# Storico: rilasci in produzione (giorni fa, versione, change), più staging e preprod avanti.
history = {
    "area-clienti-web": [(41, "3.7.0", b2b), (27, "3.8.0", b2b), (12, "3.9.0", b2b)],
    "clienti-api": [(38, "5.2.0", "CHG0041002"), (20, "5.3.0", b2b), (6, "5.3.1", b2b)],
    "clienti-worker": [(35, "1.12.0", "CHG0041002"), (15, "1.13.0", "CHG0041019")],
    "pagamenti-gateway": [(40, "2.4.0", "CHG0040977"), (22, "2.5.0", "TMP-7731"), (9, "2.5.1", "CHG0041044")],
    "identity-broker": [(33, "24.0.4-1", "CHG0041012"), (17, "24.0.5-1", dora)],
    "app-mobile-bff": [(30, "0.18.0", "CHG0041015"), (4, "0.19.0", "CHG0041051")],
    "dwh-etl": [(128, "7.1.0", "CHG0039120")],
    "cruscotti-web": [(25, "1.4.2", "CHG0041021")],
    "fornitori-portale": [(37, "2.0.0", dora), (11, "2.1.0", dora)],
}
ahead = {"area-clienti-web": [("staging", "3.10.0-rc.2", 2, "succeeded"), ("preprod", "3.10.0-rc.1", 3, "succeeded")],
         "clienti-api": [("staging", "5.4.0-rc.1", 1, "running")],
         "pagamenti-gateway": [("staging", "2.6.0-rc.1", 2, "failed"), ("preprod", "2.5.1", 9, "succeeded")],
         "app-mobile-bff": [("staging", "0.20.0-rc.1", 1, "succeeded")]}

count = 0
for i, (svc, (app, repo, comps)) in enumerate(services.items()):
    service = {"name": svc, "repository": repo, "provider": "github", "url": f"https://git.example.test/{repo}", "application": app}
    for j, (days, version, change) in enumerate(history[svc]):
        last = j == len(history[svc]) - 1
        commit = f"{(i * 7919 + j * 104729) % 16**12:012x}{'ab' * 14}"
        req_by, appr_by = people[(i + j) % len(people)]
        if svc == "pagamenti-gateway" and last:
            chk = checks(image="failed", skip="Laura Gatti")
        elif svc == "clienti-worker" and last:
            chk = checks(cves_out="failed")
        else:
            chk = checks()
        push("check-runs", {"service": service, "commit": commit, "ref": "main", "run_id": f"{9000 + i * 10 + j}", "verdict": "passed",
                            "checks": chk, "at": at(days, 8)})
        comps_v = [(n, v, e) for n, v, e in comps]
        doc = {"bomFormat": "CycloneDX", "specVersion": "1.5", "components": [
            {"type": "application" if e == "generic" else "library", "name": n, "version": v, "purl": purl(n, v, e)} for n, v, e in comps_v]}
        if svc in ("fornitori-portale", "dwh-etl"):
            # Queste pipeline producono SPDX.
            push("sboms", {"service": service, "version": version, "document": {"spdxVersion": "SPDX-2.3", "packages": [
                {"name": n, "versionInfo": v, "licenseConcluded": "NOASSERTION",
                 "externalRefs": [{"referenceCategory": "PACKAGE-MANAGER", "referenceType": "purl", "referenceLocator": purl(n, v, e)}]} for n, v, e in comps_v]}})
        else:
            push("sboms", {"service": service, "version": version, "digest": f"sha256:{commit}{commit[:4]}", "document": doc})
        push("vuln-reports", {"service": service, "version": version, "scanner": "grype", "scanned_at": at(days, 8), "vulnerabilities": vulns(svc, comps)})
        aliases = ["TMP-7731"] if change == "CHG0041044" else []
        rel = push("releases", {"service": service, "version": version, "commit": commit, "image": f"registry.example.test/{repo}:{version}",
                                "digest": f"sha256:{commit}{commit[:4]}", "environment": "prod", "change_number": change, "change_aliases": aliases,
                                "change_title": "Rilascio " + version, "requested_by": req_by, "approved_by": appr_by, "approved_at": at(days, 9),
                                "release_url": f"https://git.example.test/{repo}/releases/{version}", "at": at(days, 10),
                                "cves_at_release": summary(svc), "checks": chk})
        count += 1
        if last:
            # Scansione giornaliera della versione in produzione: oggi.
            push("vuln-reports", {"service": service, "version": version, "scanner": "trivy", "scanned_at": at(0, 4),
                                  "vulnerabilities": vulns(svc, comps, new_today.get(svc, ()))})
    for env, version, days, status in ahead.get(svc, []):
        push("deployments", {"service": service, "environment": env, "version": version, "status": status, "at": at(days, 15),
                             "source": "gitops", "external_ref": f"argocd/{svc}-{env}/{version}"})

# Servizi censiti ma non ancora sulla pipeline (dall'interfaccia, sessione di un architetto o dell'amministratore).
for name, app in (("pagamenti-riconciliazione", "PAY"), ("dwh-legacy-batch", "DWH")):
    call("POST", "/api/v1/devsecops/services", {"format": "regia.devsecops/v1", "name": name, "application": app, "repository": "legacy/" + name})

print(f"DevSecOps: {len(services)} servizi, {count} rilasci in produzione, 2 servizi non ancora sulla pipeline.")
PY
