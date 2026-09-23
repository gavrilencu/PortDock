# Shipyard

PaaS self-hosted, alternativă la Coolify: deploy din Git cu push-to-deploy, preview-uri pentru PR-uri, rollback instant, baze de date cu backup programat, servicii one-click și management complet de containere pe serverele tale (local sau prin SSH).

## Stack

| Strat | Tehnologii |
|---|---|
| Backend | Go 1.27, `net/http` (routing nativ), SQLite (driver pur Go, WAL), `x/crypto/ssh`, WebSocket (`coder/websocket`), SSE, cron |
| Frontend | React 19, Vite 8, TypeScript 7, Tailwind CSS 4, TanStack Query 5, React Router 8, xterm.js 6 |
| Runtime | Docker + Docker Compose, Traefik v3 (reverse proxy, Let's Encrypt, HTTP/3) |

Un singur binar (~20 MB) care conține API-ul, UI-ul, workerii de deploy, monitorul și scheduler-ul.

## Funcționalități

**CI/CD**
- Deploy din orice repo Git (GitHub, GitLab, Gitea/Forgejo, Bitbucket), public sau privat (token).
- Build packs: **Dockerfile**, **Nixpacks**, **static** (nginx + fallback SPA), **auto-detect** (generează Dockerfile pentru Node/Bun/pnpm/yarn, Python, Go, site static), **Docker Compose**, **imagine Docker**.
- Webhook-uri cu verificare HMAC/token: push → deploy; pull request → **preview deployment** cu domeniu propriu, șters automat la închiderea PR-ului.
- **Watch paths** (glob-uri cu `**` și `!`) pentru monorepo-uri.
- **Deploy hook** generic pentru orice pipeline CI (GitHub Actions, GitLab CI, Jenkins) — rulezi testele, apoi declanșezi deploy-ul.
- Coadă persistentă, build-uri paralele configurabile, un singur deploy activ per aplicație, **anularea automată a build-urilor depășite**.
- **Zero-downtime**: versiunea nouă pornește lângă cea veche, primește trafic doar după health check (HTTP sau `HEALTHCHECK` din imagine), apoi cea veche e oprită. La eșec, versiunea veche rămâne activă.
- Comenzi **pre-deploy** (ex. migrații, într-un container one-off din imaginea nouă) și **post-deploy**.
- **Rollback** instant la orice imagine anterioară (se păstrează ultimele 5 per aplicație), cache de imagine per commit, **force rebuild**.
- Log-uri de build live (SSE), cu secretele mascate.
- Replici multiple load-balanced, limite CPU/memorie, label-uri și opțiuni `docker run` custom.

**Management containere & servere**
- Servere multiple prin SSH (chei Ed25519 generate sau importate, criptate; host key pinning TOFU).
- Setup automat de server: instalează Docker, rețeaua, Traefik, opțional Nixpacks.
- Containere: start/stop/restart/pause/remove, log-uri live, **terminal web** (docker exec cu PTY), inspect, statistici CPU/RAM/rețea.
- Imagini, volume, rețele, prune (build cache, imagini, volume, sistem); curățenie automată zilnică.
- Metrici server (load, RAM, disk, uptime), monitorizare la 30s cu notificări la căderi.
- Terminal SSH pe server (doar admin).

**Resurse**
- Baze de date: PostgreSQL 18, MySQL 8.4, MariaDB 11, MongoDB 8, Redis 8, Valkey 8, ClickHouse — parole generate, URL-uri de conexiune interne/publice, **backup programat (cron)**, retenție, **upload S3** (AWS, R2, B2, MinIO), download și **restore** din UI.
- Servicii one-click: WordPress, n8n, Gitea, MinIO, Umami, Grafana, Vaultwarden, Ghost, Metabase, pgAdmin, NocoDB, Uptime Kuma, plus compose custom.
- Variabile de mediu criptate (AES-256-GCM), build-time/runtime, secrete, override-uri pentru preview, editor `.env` bulk.
- Domenii multiple cu HTTPS automat, redirect HTTP→HTTPS, domenii wildcard per server.

**Platformă**
- Proiecte și medii (production, staging…), echipă cu roluri (owner/admin/member), tokenuri API, audit log.
- Notificări: Discord, Slack, Telegram, email (SMTP), webhook semnat.
- UI în timp real (evenimente SSE), temă dark/light.

## Pornire rapidă (producție)

Pe un server Linux (Ubuntu/Debian recomandat), ca root, cu porturile 80, 443 și 8000 libere:

```bash
git clone <repo> shipyard && cd shipyard
./install.sh https://shipyard.example.com   # sau fără argument: http://<ip>:8000
```

Deschide URL-ul, creează contul de owner. Serverul local e adăugat automat; apasă **Servers → localhost → Run setup** ca să pornești Traefik.

Resetare parolă: `docker exec shipyard shipyard -reset-password admin@example.com`

### Servere remote

1. **Servers → Add server** → generează o cheie SSH.
2. Adaugă cheia publică în `~/.ssh/authorized_keys` pe server (user `root` recomandat).
3. **Run setup** pe noul server — instalează Docker, rețeaua `shipyard` și Traefik.
4. Opțional: setează un *wildcard domain* (`*.apps.example.com` → IP server) pentru domenii automate și preview-uri.

## Dezvoltare

```bash
make dev-api   # backend pe :8000 (date în ./.data)
make dev-ui    # Vite pe :5173, proxy /api → :8000
make test      # go vet + go test + tsc
make build     # binar în bin/shipyard, cu UI încorporat
```

Backend-ul rulează comenzile Docker prin shell; pe Windows folosește Git Bash/WSL și Docker Desktop, sau rulează totul cu `docker compose up --build`.

## Configurare (variabile de mediu)

| Variabilă | Implicit | Descriere |
|---|---|---|
| `SHIPYARD_ADDR` | `:8000` | Adresa HTTP |
| `SHIPYARD_DATA_DIR` | `/data/shipyard` | DB, cheia de criptare, build-uri, backup-uri. **În Docker, montează-l la aceeași cale ca pe host.** |
| `SHIPYARD_PUBLIC_URL` | `http://localhost:8000` | URL-ul extern (pentru webhook-uri); `https://` activează cookie-uri Secure |
| `SHIPYARD_SECRET_KEY` | generat în `secret.key` | Cheia AES pentru secrete (base64 32 bytes sau passphrase). **Fă backup la ea.** |
| `SHIPYARD_MAX_BUILDS` | `4` | Deploy-uri paralele |
| `SHIPYARD_NETWORK` | `shipyard` | Rețeaua Docker partajată cu Traefik |
| `SHIPYARD_DEV` / `SHIPYARD_DEBUG` | – | CORS pentru dev / log-uri debug |

## Domenii

Fiecare aplicație are tab-ul **Domains**, cu un tabel similar celui din Coolify: domeniu, redirect HTTP→HTTPS, redirect spre www sau fără www, port intern per domeniu, indexare în motoarele de căutare (`X-Robots-Tag`), status DNS și acțiuni.

- Modificările se aplică **în câteva secunde, fără redeploy**. Rutarea e generată ca fișiere Traefik (`/data/shipyard/proxy/dynamic`), nu ca label-uri pe containere.
- La deploy, traficul se mută pe versiunea nouă **doar după health check**. Am verificat cu 120 de cereri continue în timpul unui redeploy: 0 erori.
- **Check all DNS** compară A/AAAA cu IP-ul serverului și detectează Cloudflare proxy. **DNS records** arată exact înregistrările de creat.
- Același domeniu nu poate fi folosit de două aplicații. Domeniile wildcard (`*.apps.example.com`) sunt suportate, dar certificatul wildcard cere o provocare DNS-01.

**Settings → General**: URL-ul panoului (Shipyard se servește singur pe acel domeniu, prin Traefik, cu certificat Let's Encrypt), nume, redirect HTTPS, fus orar (folosit de backup-urile programate) și IPv4/IPv6 public (butonul Detect le află automat).

Build prin oglindă, când Docker Hub limitează descărcările: `BASE_REGISTRY=mirror.gcr.io/library docker compose build`.

## Integrarea GitHub (GitHub App)

1. **Settings → Git sources → Connect GitHub.** Shipyard creează prin *manifest flow* o aplicație GitHub privată cu permisiunile minime: Contents / Metadata / Pull requests (read) și Commit statuses (write). Opțional o poți crea sub o organizație sau pe **GitHub Enterprise Server**. Dacă organizația gestionează aplicațiile centralizat, folosește **Use existing app** (App ID + cheie privată + webhook secret).
2. **Instalează aplicația** pe contul sau organizația dorită și alege repo-urile.
3. **New resource → Git repository → GitHub:** cauți repo-ul, alegi branch-ul, iar tipul de deploy se detectează automat (Dockerfile, Compose, Node/Bun/Python/Go, static). Îl poți schimba, inclusiv pe **Shell script**: comenzi proprii de install/build/start pe orice imagine de bază.
4. **Deploy automat:** fiecare push pe branch declanșează un deploy, iar PR-urile primesc preview-uri dacă le activezi. Statusul apare pe commit în GitHub ca `shipyard/deploy`.

Pentru ca GitHub să poată trimite webhook-uri, `SHIPYARD_PUBLIC_URL` trebuie să fie o adresă HTTPS publică. Pe localhost merg navigarea prin repo-uri și deploy-urile manuale.

## CI/CD în practică

**Push-to-deploy**: în aplicație → *CI/CD & Webhooks*, copiază URL-ul GitHub/GitLab/Gitea și secretul în setările webhook ale repo-ului (evenimente: push + pull request).

**Teste înainte de deploy** (GitHub Actions): dezactivează *Deploy on push* și declanșează deploy-ul după teste:

```yaml
- run: npm ci && npm test
- run: curl -fsS -X POST "${{ secrets.SHIPYARD_DEPLOY_HOOK }}&commit=${{ github.sha }}"
```

**API**: `curl -X POST $URL/api/applications/<id>/deploy -H "Authorization: Bearer sy_…"`

## Arhitectură

```
backend/
  cmd/shipyard          entrypoint (server HTTP, workeri, monitor, scheduler)
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

Toate operațiile pe servere trec prin `Executor` (local sau SSH) și CLI-ul Docker, așa că serverele locale și cele remote se comportă identic. Fiecare workload are label-uri `shipyard.*`, iar Traefik descoperă rutele din label-uri.

## Limitări cunoscute / următorii pași

- Build-urile rulează pe serverul țintă; nu există încă push într-un registry pentru deploy multi-server din aceeași imagine.
- Fără Docker Swarm/Kubernetes (un server per resursă).
- GitHub App (autentificare fără token) și repo-uri prin cheie SSH (`git@…`) nu sunt încă suportate — folosește HTTPS + token.
- Rollback-ul nu e disponibil pentru aplicații Docker Compose (redeploy pe un commit anterior în schimb).
- Baze de date/servicii fără backup la nivel de volum (doar dump logic pentru DB-uri).
