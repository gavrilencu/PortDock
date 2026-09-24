# PortDock

> 🇬🇧 [English version](../README.md)

PaaS self-hosted, alternativă la Coolify: deploy din Git cu push-to-deploy, preview-uri pentru PR-uri, rollback instant, baze de date cu backup programat, servicii one-click și management complet de containere pe serverele tale (local sau prin SSH).

## Stack

| Strat | Tehnologii |
|---|---|
| Backend | Go 1.27, `net/http` (routing nativ), SQLite (driver pur Go, WAL), `x/crypto/ssh`, WebSocket (`coder/websocket`), SSE, cron |
| Frontend | React 19, Vite 8, TypeScript 7, Tailwind CSS 4, TanStack Query 5, React Router 8, xterm.js 6 |
| Runtime | Docker + Docker Compose, Traefik v3 (reverse proxy, Let's Encrypt, HTTP/3) |

Un singur binar care conține API-ul, UI-ul, workerii de deploy, monitorul și scheduler-ul.

## Funcționalități

**CI/CD**
- Deploy din orice repo Git (GitHub, **Azure DevOps**, GitLab, Gitea/Forgejo, Bitbucket), public sau privat (token).
- **Azure DevOps**: conectezi organizația din *Settings → Git sources* cu un personal access token (scope-uri *Code: Read, Status* și *Service Hooks: Read, query & manage*), alegi repozitoriul la creare, iar PortDock adaugă singur service hook-uri: fiecare push face deploy, pull request-urile primesc preview-uri, iar statusul deploy-ului apare pe commit (`portdock/deploy`) și pe pull request (`portdock/preview`).
- Build packs: **Dockerfile**, **Nixpacks**, **static** (nginx + fallback SPA), **auto-detect** (generează Dockerfile pentru Node/Bun/pnpm/yarn, Python, Go, site static), **Docker Compose**, **imagine Docker**.
- Webhook-uri cu verificare HMAC/token: push → deploy; pull request → **preview deployment** cu domeniu propriu, șters automat la închiderea PR-ului.
- **Watch paths** (glob-uri cu `**` și `!`) pentru monorepo-uri.
- **Deploy hook** generic pentru orice pipeline CI (GitHub Actions, GitLab CI, Jenkins) — rulezi testele, apoi declanșezi deploy-ul.
- Coadă persistentă, build-uri paralele configurabile, un singur deploy activ per aplicație, **anularea automată a build-urilor depășite**.
- **Zero-downtime**: versiunea nouă pornește lângă cea veche, primește trafic doar după health check (HTTP sau `HEALTHCHECK` din imagine), apoi cea veche e oprită. La eșec, versiunea veche rămâne activă.
- Comenzi **pre-deploy** (ex. migrații, într-un container one-off din imaginea nouă) și **post-deploy**.
- **Versiuni**: fiecare deploy reușit în producție devine o versiune (v1, v2…); versiunea live și ultimele 5 se păstrează 15 zile. Revenirea se face prin drag and drop în tab-ul **Versions**, fără rebuild și cu variabilele actuale, inclusiv pentru aplicațiile Docker Compose (fiecare serviciu își păstrează imaginile). Cache de imagine per commit, **force rebuild**.
- Log-uri de build live (SSE), cu secretele mascate.
- Replici multiple load-balanced, limite CPU/memorie, label-uri și opțiuni `docker run` custom.

**Registry-uri & deploy pe mai multe servere**
- **Registry-uri de containere**: Docker Hub, GitHub Container Registry, GitLab, **Amazon ECR** (token reînnoit automat la fiecare deploy, semnătură SigV4), Harbor, Quay sau orice registry OCI (Azure ACR, Google Artifact Registry, Nexus). Credențialele se verifică din UI (*Test connection*) și ajung la `docker login` doar prin stdin.
- Imaginea se construiește **o singură dată**, se publică `registry/namespace/app:<commit>` și `:latest`, iar fiecare server o descarcă din registry. Același registry se folosește și pentru imaginile private (sursă *Docker image* sau Compose).
- **Aceeași aplicație pe mai multe servere**: deploy de tip rolling, server cu server. Dacă un server pică la health check, deploy-ul se oprește, iar mesajul arată pe câte servere rulează deja versiunea nouă.
- **Fără registry**, imaginea e transmisă direct, prin SSH, de pe serverul de build pe celelalte (`docker save | docker load`, comprimat).
- **Server de build dedicat**, ca build-urile să nu consume resursele serverelor de producție.
- Start/stop/restart, log-uri, terminal, rute Traefik, status și ștergerea funcționează pe toate serverele aplicației. Un server scos din listă e curățat automat de aplicație.

**Monitorizare & alerte**
- **Metrici istorice** pentru servere (CPU, memorie, disc, rețea, load) și pentru fiecare aplicație, bază de date și serviciu (CPU, memorie față de limită, rețea, disponibilitate, restarturi). Sunt colectate la fiecare minut, printr-o singură conexiune SSH pe server.
- Grafice pentru 1h, 6h, 24h, 7 zile și 30 de zile. Pentru intervalele lungi se afișează media și vârful pe fiecare oră. Datele la minut se păstrează 48 de ore, cele pe oră 90 de zile. Pentru aplicațiile pe mai multe servere sau cu mai multe replici, valorile se adună.
- **Reguli de alertă**: CPU, memorie, disc sau load peste un prag pe o durată dată, crash loop (restarturi într-un interval) și resursă căzută. Se aplică pe toate resursele de un tip sau pe una anume, cu severitate warning sau critical.
- Alerta pornește doar dacă pragul e depășit la fiecare măsurare de pe toată durata (fără alarme false din vârfuri scurte) și se rezolvă automat când valoarea revine. Resursele oprite manual nu generează alerte „down”.
- Pagina **Alerts** are alertele active (cu *Acknowledge*), istoricul, regulile și alegerea canalelor de notificare (Slack, Microsoft Teams, Discord, Telegram, email, webhook). În meniu apare un badge cu numărul de alerte active.
- Reguli implicite pe o instalare nouă: CPU server peste 90% timp de 10 minute, memorie peste 90% timp de 5 minute, disc peste 85%, crash loop de aplicație, aplicație căzută, bază de date căzută.

**Management containere & servere**
- Servere multiple prin SSH (chei Ed25519 generate sau importate, criptate; host key pinning TOFU).
- Setup automat de server: instalează Docker, rețeaua, Traefik, opțional Nixpacks.
- Containere: start/stop/restart/pause/remove, log-uri live, **terminal web** (docker exec cu PTY), inspect, statistici CPU/RAM/rețea.
- Imagini, volume, rețele, prune (build cache, imagini, volume, sistem); curățenie automată zilnică.
- Metrici server (load, RAM, disk, uptime), monitorizare la 30s cu notificări la căderi.
- Terminal SSH pe server (doar admin).

**Resurse**
- Baze de date: PostgreSQL 18, MySQL 8.4, MariaDB 11, Microsoft SQL Server 2022, MongoDB 8, Redis 8, Valkey 8, ClickHouse — parole generate, URL-uri de conexiune interne/publice, **backup programat (cron)**, retenție, **upload S3** (AWS, R2, B2, MinIO), download și **restore** din UI.
- Servicii one-click: WordPress, n8n, Gitea, MinIO, Umami, Grafana, Vaultwarden, Ghost, Metabase, pgAdmin, NocoDB, Uptime Kuma, plus compose custom.
- Variabile de mediu criptate (AES-256-GCM), build-time/runtime, secrete, override-uri pentru preview, editor `.env` bulk.
- Domenii multiple cu HTTPS automat, redirect HTTP→HTTPS, domenii wildcard per server.

**Securitate & autentificare**
- **Autentificare cu doi factori (TOTP)**: aplicații tip Microsoft/Google Authenticator, 1Password, Bitwarden; 10 coduri de recuperare de unică folosință; codurile folosite nu pot fi refolosite (replay).
- **Politică 2FA** la nivel de organizație: opțională, obligatorie pentru admini sau pentru toți; cei care nu au 2FA sunt obligați să-l configureze la următoarea autentificare.
- **Single sign-on (OpenID Connect)**: Microsoft Entra ID, Okta, Google Workspace, Keycloak, Authentik, GitLab sau orice furnizor OIDC. Folosește PKCE, verifică semnătura ID token-ului (JWKS), plus nonce, audience și expirarea. Conturile pot fi create automat (JIT), există restricție pe domenii de email, iar rolul de admin se mapează din grupuri la fiecare login.
- Autentificarea cu parolă poate fi dezactivată (SSO obligatoriu); owner-ul păstrează un cont de urgență (break-glass).
- Conturile existente se asociază după email doar pentru membri, cu email verificat; owner-ul și administratorii își conectează singuri furnizorul din **Setări → Profil**, iar administratorii asociază conturile locale cu un director LDAP din **Setări → Echipă**.
- **Sesiuni active** per utilizator (dispozitiv, IP, metodă de login), deconectare individuală sau de pe toate celelalte sesiuni. Adminii pot dezactiva conturi, reseta 2FA și închide sesiunile unui utilizator.
- Protecție brute-force: blocare după 10 încercări eșuate în 15 minute (per IP și per cont). Toate evenimentele ajung în audit log.
- Recuperare din linia de comandă: `docker exec portdock portdock -disable-2fa user@example.com`.

**Control acces & conformitate**
- **Roluri pe proiect**: *viewer* (doar citire, fără secrete), *developer* (deploy și configurare pe mediile neprotejate), *maintainer* (acces total în proiect, membri, aprobări). Fiecare proiect are și un rol implicit pentru restul membrilor. Cu rolul implicit *none*, proiectul e privat: cine nu e membru nici nu-l vede. Owner-ii și adminii au acces total.
- **Medii protejate** (de exemplu production): doar maintainerii schimbă configurația, variabilele și domeniile. Deploy-urile făcute de developeri devin cereri de aprobare.
- **Aprobare obligatorie**: orice deploy dintr-un mediu (inclusiv git push și API) așteaptă aprobarea unui maintainer diferit de cel care l-a cerut (principiul celor patru ochi). Aprobarea și respingerea pot avea comentariu, apar pe pagina deploy-ului, trimit notificare (`deployment.approval_required`) și ajung în audit log.
- Toate rutele API sunt verificate central după rol, inclusiv containerele de pe servere (log-uri, terminal, acțiuni). Listele, dashboard-ul și alertele arată doar resursele accesibile. Viewerii nu văd parolele bazelor de date.
- **Audit log**: fiecare acțiune are IP-ul utilizatorului. Poți filtra după text, tip de acțiune și interval, și exporta în **CSV/JSON** (protejat împotriva injecției de formule în Excel). **Streaming către SIEM** (Splunk, Elastic, Sentinel, Datadog): webhook JSON semnat HMAC-SHA256 sau syslog RFC 5424 prin UDP/TCP.

**Gratuit și open source**
- Toate funcțiile sunt disponibile, fără planuri, chei de licență sau limite de servere și utilizatori. Licență [Apache-2.0](../LICENSE).

**Platformă**
- Proiecte și medii (production, staging…), echipă cu roluri (owner/admin/member), tokenuri API, audit log.
- Notificări: Discord, Slack, Microsoft Teams (Workflows sau incoming webhook, Adaptive Card), Telegram, email (SMTP), webhook semnat.
- **Uptime**: monitoare pentru site-uri și API-uri (cod de status, cuvânt-cheie, timp de răspuns, expirarea certificatului TLS), incidente, alerte `monitor.down` / `monitor.up` și o **pagină de status publică** la `/status`, cu uptime-ul pe 90 de zile.
- **Scanare de vulnerabilități cu Trivy**: la fiecare deploy sau la cerere, din tab-ul *Security* al aplicației; deploy-ul poate fi blocat la vulnerabilități critice sau ridicate.
- **SAST cu Semgrep**: codul sursă este analizat imediat după clone, înainte de build (injecții, deserializare nesigură, criptografie slabă, secrete…); deploy-ul poate fi blocat la probleme de nivel ridicat. Regulile se aleg din *Setări → Rețea și proxy → Scanere de securitate*.
- **DAST cu OWASP ZAP**: după fiecare deploy sau la cerere, aplicația pornită este scanată din rețeaua PortDock (header-e de securitate, cookie-uri, scurgeri de informații); URL-ul se poate schimba per aplicație.
- **Rapoarte descărcabile** pentru fiecare scanare (HTML, CSV, JSON, SARIF, raportul ZAP) și un ZIP cu toate rapoartele unui deploy, direct din pagina deploy-ului.
- **Containere de pe toate serverele** într-o singură pagină (*Containere*): containere, imagini și volume ale fiecărui server conectat, cu serverul, resursa PortDock, CPU și memorie, loguri, terminal și acțiuni; pagina proiectului arată pe ce server rulează fiecare resursă.
- **Proxy corporativ și Nexus** (*Setări → Rețea și proxy*): proxy HTTP(S) cu utilizator/parolă și NO_PROXY, folosit pentru PortDock, build-uri și containere (cu excepție per aplicație); mirror pentru Docker Hub (Nexus, Artifactory, Harbor) aplicat în `daemon.json` pe fiecare server cu reload fără oprirea containerelor; mirror-uri npm / PyPI / Go / Maven pentru build-uri; buton de test al conexiunii.
- **Consolă SQL** în browser pentru PostgreSQL, MySQL, MariaDB, SQL Server și ClickHouse: tabele, rezultate, export CSV; membrii fără rol de admin au acces doar de citire (sesiune care nu poate scrie), iar fiecare interogare ajunge în audit log.
- UI în timp real (evenimente SSE), temă dark/light.

## Pornire rapidă (producție)

Pe un server Linux (Ubuntu/Debian recomandat), ca root, cu porturile 80, 443 și 8000 libere:

```bash
git clone https://github.com/gavrilencu/PortDock.git portdock && cd portdock
sudo bash install.sh https://portdock.example.com   # sau fără argument: http://<ip>:8000 (alt port: PORTDOCK_PORT=9000)
```

Deschide URL-ul, creează contul de owner. Serverul local e adăugat automat; apasă **Servers → localhost → Run setup** ca să pornești Traefik.

Resetare parolă: `docker exec portdock portdock -reset-password admin@example.com`

### Servere remote

1. **Servers → Add server** → generează o cheie SSH.
2. Adaugă cheia publică în `~/.ssh/authorized_keys` pe server (user `root` recomandat).
3. **Run setup** pe noul server — instalează Docker, rețeaua `portdock` și Traefik.
4. Opțional: setează un *wildcard domain* (`*.apps.example.com` → IP server) pentru domenii automate și preview-uri.

## Dezvoltare

```bash
make dev-api   # backend pe :8000 (date în ./.data)
make dev-ui    # Vite pe :5173, proxy /api → :8000
make test      # go vet + go test + tsc
make build     # binar în bin/portdock, cu UI încorporat
```

Backend-ul rulează comenzile Docker prin shell; pe Windows folosește Git Bash/WSL și Docker Desktop, sau rulează totul cu `docker compose up --build`.

## Configurare (variabile de mediu)

| Variabilă | Implicit | Descriere |
|---|---|---|
| `PORTDOCK_ADDR` | `:8000` | Adresa HTTP |
| `PORTDOCK_DATA_DIR` | `/data/portdock` | DB, cheia de criptare, build-uri, backup-uri. **În Docker, montează-l la aceeași cale ca pe host.** |
| `PORTDOCK_PUBLIC_URL` | `http://localhost:8000` | URL-ul extern (pentru webhook-uri); `https://` activează cookie-uri Secure |
| `PORTDOCK_SECRET_KEY` | generat în `secret.key` | Cheia AES pentru secrete (base64 32 bytes sau passphrase). **Fă backup la ea.** |
| `PORTDOCK_MAX_BUILDS` | `4` | Deploy-uri paralele |
| `PORTDOCK_NETWORK` | `portdock` | Rețeaua Docker partajată cu Traefik |
| `PORTDOCK_TRUSTED_PROXIES` | rețelele private | Proxy-urile al căror `X-Forwarded-For` e crezut pentru adresa clientului (limitele de autentificare, audit log): IP-uri sau intervale CIDR, ori `none` |
| `PORTDOCK_DEV` / `PORTDOCK_DEBUG` | – | CORS pentru dev / log-uri debug |
| `PORTDOCK_OPENROUTER_URL` | `https://openrouter.ai/api/v1` | API-ul folosit de asistentul AI (un gateway compatibil OpenRouter, de exemplu în rețele fără acces direct) |

## Asistent AI

**Settings → AI assistant** activează un asistent de chat în colțul din dreapta jos al aplicației:

- **Model:** orice model de pe [OpenRouter](https://openrouter.ai), ales din catalogul complet (cu căutare, context și preț per 1M de tokeni, filtru pentru modelele gratuite) sau scris manual (de exemplu `anthropic/claude-sonnet-4.5`).
- **Cheia API** OpenRouter se salvează criptat; **Test connection** verifică cheia și modelul.
- **Acces:** toți utilizatorii, doar administratorii sau doar utilizatorii bifați.
- **Instrucțiuni** opționale adăugate la prompt-ul de sistem (convențiile echipei, tonul).
- Fiecare utilizator are **istoricul** conversațiilor lui (vizibil doar lui). Răspunsurile apar pe măsură ce sunt generate, cu Stop, copiere și formatare Markdown; fereastra se poate minimiza și mări.

Mesajele sunt trimise la OpenRouter și la furnizorul modelului ales: nu lipi secrete în chat.

## Domenii

Fiecare aplicație are tab-ul **Domains**, cu un tabel similar celui din Coolify: domeniu, redirect HTTP→HTTPS, redirect spre www sau fără www, port intern per domeniu, indexare în motoarele de căutare (`X-Robots-Tag`), status DNS și acțiuni.

- Modificările se aplică **în câteva secunde, fără redeploy**. Rutarea e generată ca fișiere Traefik (`/data/portdock/proxy/dynamic`), nu ca label-uri pe containere.
- La deploy, traficul se mută pe versiunea nouă **doar după health check**. Am verificat cu 120 de cereri continue în timpul unui redeploy: 0 erori.
- **Check all DNS** compară A/AAAA cu IP-ul serverului și detectează Cloudflare proxy. **DNS records** arată exact înregistrările de creat.
- Același domeniu nu poate fi folosit de două aplicații. Domeniile wildcard (`*.apps.example.com`) sunt suportate, dar certificatul wildcard cere o provocare DNS-01.

**Settings → General**: URL-ul panoului (PortDock se servește singur pe acel domeniu, prin Traefik, cu certificat Let's Encrypt), nume, redirect HTTPS, fus orar (folosit de backup-urile programate) și IPv4/IPv6 public (butonul Detect le află automat).

Build prin oglindă, când Docker Hub limitează descărcările: `BASE_REGISTRY=mirror.gcr.io/library docker compose build`.

## Certificate SSL

- Certificatele Let's Encrypt se emit și se reînnoiesc automat, prin Traefik (reînnoirea are loc cu 30 de zile înainte de expirare). **Settings → Certificates** afișează fiecare domeniu cu starea certificatului, emitentul și zilele rămase.
- **Supraveghere automată** la fiecare 6 ore. Dacă un certificat are sub 14 zile rămase (pragul se poate configura), Traefik n-a reușit reînnoirea, iar PortDock forțează o emitere nouă: oprește proxy-ul câteva secunde, șterge certificatul vechi, pornește proxy-ul și verifică din nou după 3 minute. Rezultatul vine ca notificare (`certificate.expiring`, `certificate.renewed`, `certificate.failed`). Reînnoirea forțată se face cel mult o dată pe zi per domeniu, din cauza limitelor Let's Encrypt. Pentru fiecare domeniu există și butonul *Renew now*.
- **DNS-01** pentru domenii wildcard și interne: Cloudflare, Route 53, Azure DNS, Google Cloud DNS, DigitalOcean, Hetzner, OVH, GoDaddy, RFC 2136 (Windows DNS/BIND/PowerDNS) sau orice provider suportat de lego. Credențialele sunt criptate și ajung pe server într-un fișier cu permisiuni 0600.
- **Server ACME propriu** (step-ca, PKI internă) sau Let's Encrypt staging pentru teste.
- **Certificate încărcate manual** (CA corporativ, EV/OV): lanțul și cheia sunt validate (cheia trebuie să corespundă certificatului, iar certificatul să nu fie expirat). Se distribuie pe toate proxy-urile, iar tu primești notificări la 30, 14, 7 și 3 zile înainte de expirare.
- La orice schimbare de configurare (email, CA, DNS), proxy-ul se recreează singur, pe baza unei amprente a configurației.

## LDAP / Active Directory

**Settings → Authentication → LDAP & Active Directory**. Există presetări pentru Active Directory, OpenLDAP și FreeIPA:
- conexiune LDAPS sau StartTLS, cu CA propriu opțional;
- un cont de serviciu caută utilizatorul, iar parola lui se verifică printr-un bind cu propriul DN;
- conturile dezactivate în AD sunt refuzate;
- grupurile imbricate din AD sunt rezolvate;
- poți cere apartenența la anumite grupuri pentru acces și poți mapa grupurile de admin;
- contul PortDock se creează automat la prima logare.

Utilizatorii se loghează cu username, UPN sau email. Politicile de 2FA se aplică și lor, iar conturile locale rămân disponibile ca rezervă. Butonul *Test* verifică conexiunea și arată pentru un utilizator emailul, grupurile și rolul rezultat.

## Actualizări PortDock

Împingi un tag `vX.Y.Z` pe GitHub, iar workflow-ul `.github/workflows/release.yml` rulează testele, publică imaginea `ghcr.io/<owner>/<repo>:vX.Y.Z` și creează release-ul. Workflow-ul include în imagine și numele repository-ului, așa că **orice instalare dintr-un release știe singură de unde vin update-urile, fără nicio configurare**. Repository-ul fiind public, nu e nevoie de token nici pentru release-uri, nici pentru imagine. PortDock verifică la fiecare deschidere a aplicației și afișează în meniu *Update available*, cu versiunea instalată, versiunea nouă și notele de release. Sursa se poate schimba doar din *Settings → Updates → Advanced* (pentru fork-uri sau mirror-uri).

La *Update now*:
1. Update-ul e refuzat dacă rulează deploy-uri.
2. Se descarcă imaginea, iar baza de date și cheia de criptare se salvează în `/data/portdock/backups/portdock/`.
3. Un container *updater* pornește din **imaginea curentă, verificată**, apoi:
   - oprește PortDock;
   - creează containerul nou cu exact aceeași configurare (volume, porturi, rețele, env, restart policy);
   - așteaptă health check-ul.
4. Dacă versiunea nouă nu pornește sau e nesănătoasă, **revine automat** la versiunea veche.

Aplicațiile rulează în continuare pe tot parcursul update-ului. Opțional poți activa update-uri automate între 02:00 și 05:00, doar pentru versiunile stabile.

## Topology

Pagina **Topology** (și tab-ul *Topology* din fiecare aplicație) e o hartă live, în stilul ArgoCD: *domeniu → resursă → server → containere*. Culorile arată starea (healthy, running, starting, unhealthy, restarting, **CrashLoop**, failed, exited), iar muchiile animate arată containerele care cad în buclă. Se reîmprospătează la fiecare 5 secunde.

Click pe un container deschide un panou cu:
- rezumatul: imagine, exit code, OOM, restarturi, porturi, rețele cu IP;
- **log-uri live**;
- **variabilele de mediu**: valorile sunt vizibile doar celor cu drept de modificare, iar secretele sunt ascunse până le afișezi;
- **terminal**;
- butoane pentru restart, stop și start.

## Single sign-on

1. **Settings → Authentication → Add provider** și alegi furnizorul.
2. Copiezi **Redirect URI** (`<URL panou>/api/auth/sso/<slug>/callback`) în aplicația creată la furnizor.
3. Completezi Issuer URL-ul, apeși **Test** (verifică documentul de discovery), apoi Client ID și Client secret.
   - Entra ID: `https://login.microsoftonline.com/<tenant-id>/v2.0`. Pentru maparea adminilor, adaugă claim-ul opțional `groups` și pune ID-ul grupului la *Admin groups*.
   - Okta: `https://<org>.okta.com`, scope-ul `groups`.
   - Keycloak: `https://<host>/realms/<realm>`.
4. Opțional, în **Sign-in policy**: 2FA obligatoriu și dezactivarea autentificării cu parolă.

URL-ul panoului (Settings → General → URL) trebuie să fie cel pe care îl folosesc utilizatorii, pentru că din el se construiește redirect URI-ul.

## Integrarea GitHub (GitHub App)

1. **Settings → Git sources → Connect GitHub.** PortDock creează prin *manifest flow* o aplicație GitHub privată cu permisiunile minime: Contents / Metadata / Pull requests (read) și Commit statuses (write). Opțional o poți crea sub o organizație sau pe **GitHub Enterprise Server**. Dacă organizația gestionează aplicațiile centralizat, folosește **Use existing app** (App ID + cheie privată + webhook secret).
2. **Instalează aplicația** pe contul sau organizația dorită și alege repo-urile.
3. **New resource → Git repository → GitHub:** cauți repo-ul, alegi branch-ul, iar tipul de deploy se detectează automat (Dockerfile, Compose, Node/Bun/Python/Go, static). Îl poți schimba, inclusiv pe **Shell script**: comenzi proprii de install/build/start pe orice imagine de bază.
4. **Deploy automat:** fiecare push pe branch declanșează un deploy, iar PR-urile primesc preview-uri dacă le activezi. Statusul apare pe commit în GitHub ca `portdock/deploy`.

Pentru ca GitHub să poată trimite webhook-uri, `PORTDOCK_PUBLIC_URL` trebuie să fie o adresă HTTPS publică. Pe localhost merg navigarea prin repo-uri și deploy-urile manuale.

## CI/CD în practică

**Push-to-deploy**: în aplicație → *CI/CD & Webhooks*, copiază URL-ul GitHub/GitLab/Gitea și secretul în setările webhook ale repo-ului (evenimente: push + pull request).

**Teste înainte de deploy** (GitHub Actions): dezactivează *Deploy on push* și declanșează deploy-ul după teste:

```yaml
- run: npm ci && npm test
- run: curl -fsS -X POST "${{ secrets.PORTDOCK_DEPLOY_HOOK }}&commit=${{ github.sha }}"
```

**API**: `curl -X POST $URL/api/applications/<id>/deploy -H "Authorization: Bearer pd_…"`

## Arhitectură

```
backend/
  cmd/portdock          entrypoint (server HTTP, workeri, monitor, scheduler)
  internal/api          REST, SSE (log-uri/evenimente), WebSocket (terminal), webhook-uri, UI embed
  internal/deploy       coada de deploy, pipeline (clone → build → health check → rollout),
                        builderi, compose, baze de date, backup, servicii, monitor
  internal/executor     rulare comenzi local / SSH (pool, PTY, host key pinning)
  internal/docker       wrapper Docker CLI (containere, stats, imagini, volume, rețele, metrici)
  internal/proxy        Traefik: pornire + label-uri de rutare/TLS
  internal/templates    motoare DB și șabloane de servicii
  internal/store        SQLite, schema, mapper generic
  internal/{auth,secrets,notify,scheduler,stream}
frontend/src
  pages/                Dashboard, Projects, Application, Deployment, Database, Service, Servers, Settings
  components/           UI kit, LogViewer, Terminal (xterm), EnvEditor, ContainerTable
  lib/                  client API, tipuri, hook-uri SSE
```

Toate operațiile pe servere trec prin `Executor` (local sau SSH) și CLI-ul Docker, așa că serverele locale și cele remote se comportă identic. Rutarea e scrisă ca fișiere Traefik (file provider), de aceea schimbările de domenii se aplică imediat.

## Limitări cunoscute / următorii pași

- Bazele de date și serviciile one-click rulează pe un singur server. Aplicațiile pot rula pe mai multe servere, dar echilibrarea traficului între ele se face prin DNS (mai multe înregistrări A) sau printr-un load balancer extern.
- Preview-urile pentru PR-uri rulează doar pe serverul principal.
- Fără Docker Swarm/Kubernetes.
- Repo-urile prin cheie SSH (`git@…`) nu sunt suportate — folosește HTTPS + token sau GitHub App.
- Baze de date/servicii fără backup la nivel de volum (doar dump logic pentru DB-uri).

## Licență

PortDock este gratuit și open source, sub [Apache License 2.0](../LICENSE). Vezi [NOTICE](../NOTICE).

Dezvoltat de **Gavrilencu Grigore**.
