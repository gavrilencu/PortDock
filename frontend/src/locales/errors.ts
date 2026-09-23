// Translations of fixed API error messages (backend/internal/...); shown via tMaybe() in lib/api.ts.
export const ro: Record<string, string> = {
  // api: generic
  "not found": "nu a fost găsit",
  "no such endpoint": "endpoint inexistent",
  "internal error": "eroare internă",
  "invalid payload": "date invalide",
  "could not read body": "corpul cererii nu a putut fi citit",
  "unknown action": "acțiune necunoscută",
  "unknown resource type": "tip de resursă necunoscut",
  "unsupported type": "tip neacceptat",
  "unsupported language": "limbă neacceptată",
  "invalid path": "cale invalidă",
  "invalid host": "host invalid",
  "invalid port": "port invalid",
  "invalid public port": "port public invalid",
  "invalid pr": "pull request invalid",
  "invalid user_id": "user_id invalid",
  "invalid session id": "ID de sesiune invalid",
  "invalid variable name": "nume de variabilă invalid",
  "invalid email address": "adresă de e-mail invalidă",
  "invalid DNS provider code": "cod de furnizor DNS invalid",
  "name is required": "numele este obligatoriu",
  "name and type are required": "numele și tipul sunt obligatorii",
  "host is required": "hostul este obligatoriu",
  "domain is required": "domeniul este obligatoriu",
  "client ID is required": "client ID este obligatoriu",
  "an SSH key is required": "este necesară o cheie SSH",
  "compose content is required": "conținutul compose este obligatoriu",
  "git_repository is required": "git_repository este obligatoriu",
  "docker_image is required": "docker_image este obligatoriu",
  "App ID and private key are required": "App ID și cheia privată sunt obligatorii",
  "app, installation and repo (owner/name) are required": "app, installation și repo (owner/name) sunt obligatorii",
  "target not found": "ținta nu a fost găsită",
  "environment not found": "mediul nu a fost găsit",
  "server not found": "serverul nu a fost găsit",
  "build server not found": "serverul de build nu a fost găsit",
  "registry not found": "registry-ul nu a fost găsit",
  "GitHub App not found": "GitHub App nu a fost găsită",
  "directory not found in this branch": "directorul nu a fost găsit în acest branch",
  "an update is already in progress": "o actualizare este deja în curs",
  "no release available": "nicio versiune disponibilă",

  // api: authentication and accounts
  "invalid username or password": "nume de utilizator sau parolă incorectă",
  "invalid token": "token invalid",
  "invalid signature": "semnătură invalidă",
  "invalid signature or unknown GitHub App": "semnătură invalidă sau GitHub App necunoscută",
  "invalid authentication code": "cod de autentificare invalid",
  "the sign-in session expired; enter your password again": "sesiunea de autentificare a expirat; introdu din nou parola",
  "too many incorrect codes; enter your password again": "prea multe coduri incorecte; introdu din nou parola",
  "this account signs in with single sign-on": "acest cont se autentifică prin single sign-on",
  "password sign-in is disabled for your organization; use single sign-on":
    "autentificarea cu parolă este dezactivată pentru organizația ta; folosește single sign-on",
  "registration is closed; ask an administrator to invite you": "înregistrarea este închisă; cere unui administrator să te invite",
  "this account has been disabled; contact an administrator": "acest cont a fost dezactivat; contactează un administrator",
  "two-factor authentication must be enabled for your account": "autentificarea în doi pași trebuie activată pentru contul tău",
  "your organization requires two-factor authentication": "organizația ta impune autentificarea în doi pași",
  "two-factor authentication is already enabled": "autentificarea în doi pași este deja activată",
  "two-factor authentication is not enabled": "autentificarea în doi pași nu este activată",
  "only the owner can reset the owner's two-factor authentication":
    "doar proprietarul poate reseta autentificarea în doi pași a proprietarului",
  "the code is incorrect — check that your device's clock is correct":
    "codul este incorect — verifică dacă ceasul dispozitivului este corect",
  "enter a current code from your authenticator app": "introdu un cod actual din aplicația de autentificare",
  "start the setup again": "începe din nou configurarea",
  "current password is incorrect": "parola actuală este incorectă",
  "password must be at least 8 characters": "parola trebuie să aibă cel puțin 8 caractere",
  "a user with this email already exists": "există deja un utilizator cu această adresă de e-mail",
  "you cannot disable this account": "nu poți dezactiva acest cont",
  "the owner account cannot be deleted": "contul proprietarului nu poate fi șters",
  "password_login must be true or false": "password_login trebuie să fie true sau false",
  "require_2fa must be off, admins or all": "require_2fa trebuie să fie off, admins sau all",

  // api: roles and permissions
  "admin role required": "este necesar rolul de administrator",
  "project maintainer role required": "este necesar rolul de maintainer al proiectului",
  "your project role does not allow changes": "rolul tău în proiect nu permite modificări",
  "your project role does not allow deployments": "rolul tău în proiect nu permite deploy-uri",
  "you requested this deployment; another maintainer must approve it":
    "ai solicitat acest deploy; un alt maintainer trebuie să îl aprobe",
  "only administrators can manage this container": "doar administratorii pot gestiona acest container",
  "container does not belong to this application": "containerul nu aparține acestei aplicații",
  "role must be viewer, developer or maintainer": "rolul trebuie să fie viewer, developer sau maintainer",
  "role must be admin or member": "rolul trebuie să fie admin sau member",
  "default role must be none, viewer, developer or maintainer":
    "rolul implicit trebuie să fie none, viewer, developer sau maintainer",
  "you would remove your own maintainer access; keep yourself as a maintainer":
    "ți-ai elimina propriul acces de maintainer; păstrează-te ca maintainer",
  "scope must be server, application, database or service": "domeniul de aplicare trebuie să fie server, application, database sau service",

  // api: projects, applications, deployments
  "slug may contain lowercase letters, digits and dashes": "slug-ul poate conține litere mici, cifre și cratime",
  "repository must look like owner/name": "repository-ul trebuie să aibă forma owner/name",
  "select a repository from the GitHub App": "selectează un repository din GitHub App",
  "this GitHub App is already registered": "această GitHub App este deja înregistrată",
  "GitHub Enterprise URL must look like https://github.example.com":
    "URL-ul GitHub Enterprise trebuie să aibă forma https://github.example.com",
  "replicas must be between 1 and 20": "numărul de replici trebuie să fie între 1 și 20",
  "stop the application before moving it to another server": "oprește aplicația înainte de a o muta pe alt server",
  "no containers; deploy the application first": "niciun container; fă mai întâi deploy aplicației",
  "backup run has no file": "rularea backup-ului nu are niciun fișier",
  "image name may contain lowercase letters, digits, '.', '_', '-' and '/' (e.g. team/api)":
    "numele imaginii poate conține litere mici, cifre, '.', '_', '-' și '/' (de ex. team/api)",
  "namespace may contain lowercase letters, digits, '.', '_', '-' and '/'":
    "namespace-ul poate conține litere mici, cifre, '.', '_', '-' și '/'",
  "image must be a repository without tag, e.g. ghcr.io/acme/portdock":
    "imaginea trebuie să fie un repository fără tag, de ex. ghcr.io/acme/portdock",
  "channel must be stable or prerelease": "canalul trebuie să fie stable sau prerelease",

  // api: domains and certificates
  "cert_mode must be auto, dns or custom": "cert_mode trebuie să fie auto, dns sau custom",
  "www_redirect must be none, to-www or to-apex": "www_redirect trebuie să fie none, to-www sau to-apex",
  "www redirects are not available for wildcard domains": "redirecționările www nu sunt disponibile pentru domeniile wildcard",
  "HTTPS is disabled for this domain": "HTTPS este dezactivat pentru acest domeniu",
  "this domain uses an uploaded certificate; upload a renewed one in Settings → Certificates":
    "acest domeniu folosește un certificat încărcat; încarcă unul reînnoit în Setări → Certificate",
  "the ACME directory must be an https:// URL": "directorul ACME trebuie să fie un URL https://",
  "renewal threshold must be between 3 and 60 days": "pragul de reînnoire trebuie să fie între 3 și 60 de zile",
  "instance URL must look like https://panel.example.com": "URL-ul instanței trebuie să aibă forma https://panel.example.com",

  // api: monitoring, notifications, audit
  "threshold must be positive": "pragul trebuie să fie pozitiv",
  "threshold must be a percentage between 0 and 100": "pragul trebuie să fie un procent între 0 și 100",
  "duration must be between 1 and 1440 minutes": "durata trebuie să fie între 1 și 1440 de minute",
  "webhook URL must be an http(s) URL": "URL-ul webhook trebuie să fie un URL http(s)",
  "configure a webhook URL or syslog target first": "configurează mai întâi un URL webhook sau o destinație syslog",

  // api: LDAP and SSO settings
  "the user filter must be an LDAP filter containing {username}": "filtrul de utilizatori trebuie să fie un filtru LDAP care conține {username}",
  "security must be ldaps, starttls or none": "securitatea trebuie să fie ldaps, starttls sau none",
  "host must be a hostname or IP address, e.g. dc01.corp.example": "hostul trebuie să fie un nume de host sau o adresă IP, de ex. dc01.corp.example",
  "base DN is required, e.g. DC=corp,DC=example": "base DN este obligatoriu, de ex. DC=corp,DC=example",
  "issuer must be a URL such as https://login.microsoftonline.com/<tenant-id>/v2.0":
    "issuer trebuie să fie un URL precum https://login.microsoftonline.com/<tenant-id>/v2.0",

  // api: license
  "a license is already installed": "o licență este deja instalată",
  "the trial has already been used on this instance": "perioada de probă a fost deja folosită pe această instanță",

  // certificates (upload)
  "the private key is not PEM encoded": "cheia privată nu este codificată PEM",
  "the private key is password protected; export it without a passphrase":
    "cheia privată este protejată cu parolă; export-o fără parolă",
  "unsupported private key format": "format de cheie privată neacceptat",
  "the private key does not belong to the first certificate": "cheia privată nu aparține primului certificat",
  "the certificate has no DNS names": "certificatul nu conține nume DNS",

  // deploy
  'nixpacks is not installed on the server; run Server setup with "Also install Nixpacks" enabled':
    'nixpacks nu este instalat pe server; rulează configurarea serverului cu opțiunea „Instalează și Nixpacks” activată',
  "could not detect the project type; add a Dockerfile or choose a build pack":
    "tipul proiectului nu a putut fi detectat; adaugă un Dockerfile sau alege un build pack",
  "the shell build pack requires a start command": "build pack-ul shell necesită o comandă de pornire",
  "compose file defines no services": "fișierul compose nu definește niciun serviciu",
  "rollback is not supported for docker compose applications; redeploy a previous commit instead":
    "rollback-ul nu este acceptat pentru aplicațiile docker compose; fă din nou deploy unui commit anterior",
  "rollback target not found": "ținta rollback-ului nu a fost găsită",
  "rollback target has no image": "ținta rollback-ului nu are imagine",
  "deployment is not waiting for approval": "deploy-ul nu așteaptă aprobare",
  "deployment is not queued or running": "deploy-ul nu este în coadă sau în desfășurare",
  "no docker image configured": "nicio imagine docker configurată",
  "no git repository configured": "niciun repository git configurat",
  "no containers found; deploy the application first": "nu s-a găsit niciun container; fă mai întâi deploy aplicației",
  "S3 storage is not configured (Settings → Backups)": "stocarea S3 nu este configurată (Setări → Backup-uri)",
  "invalid backup file": "fișier de backup invalid",
  "PortDock is not running in Docker; route the dashboard domain to it with your own reverse proxy":
    "PortDock nu rulează în Docker; direcționează domeniul panoului către el cu propriul reverse proxy",

  // executor
  "unterminated quote or escape in command": "ghilimele sau secvență escape neînchisă în comandă",
  "interactive terminal on the local server requires Linux; run PortDock in Docker":
    "terminalul interactiv pe serverul local necesită Linux; rulează PortDock în Docker",
  "server has no SSH key configured": "serverul nu are nicio cheie SSH configurată",
  "ssh key not found": "cheia SSH nu a fost găsită",

  // github
  "invalid private key: not PEM encoded": "cheie privată invalidă: nu este codificată PEM",
  "private key must be RSA": "cheia privată trebuie să fie RSA",

  // ldap
  "user not found in the directory": "utilizatorul nu a fost găsit în director",
  "the directory account is disabled": "contul din director este dezactivat",
  "the CA certificate is not valid PEM": "certificatul CA nu este un PEM valid",
  "password expired": "parola a expirat",
  "account expired": "contul a expirat",
  "account locked out": "contul este blocat",
  "the base DN does not exist": "base DN nu există",
  "the directory requires an encrypted connection (use LDAPS or StartTLS)":
    "directorul necesită o conexiune criptată (folosește LDAPS sau StartTLS)",

  // license
  "this is not a PortDock license key": "aceasta nu este o cheie de licență PortDock",
  "the license key signature is invalid": "semnătura cheii de licență este invalidă",
  "license has no valid expiry date": "licența nu are o dată de expirare validă",

  // registry
  "the registry rejected these credentials": "registry-ul a respins aceste credențiale",
  "the registry did not issue a token": "registry-ul nu a emis un token",

  // sso
  "discovery document is missing required endpoints": "documentului discovery îi lipsesc endpoint-uri obligatorii",
  "ID token audience does not include this client ID": "audiența ID token-ului nu include acest client ID",
  "ID token has expired — check the server clock": "ID token-ul a expirat — verifică ceasul serverului",

  // updater
  "PortDock is not running in a Docker container with the Docker socket mounted; update it by pulling the new image manually":
    "PortDock nu rulează într-un container Docker cu socket-ul Docker montat; actualizează-l descărcând manual noua imagine",
  "the new version is unhealthy": "noua versiune nu este funcțională",
};

export const ru: Record<string, string> = {
  // api: generic
  "not found": "не найдено",
  "no such endpoint": "такого эндпоинта нет",
  "internal error": "внутренняя ошибка",
  "invalid payload": "некорректные данные",
  "could not read body": "не удалось прочитать тело запроса",
  "unknown action": "неизвестное действие",
  "unknown resource type": "неизвестный тип ресурса",
  "unsupported type": "неподдерживаемый тип",
  "unsupported language": "неподдерживаемый язык",
  "invalid path": "некорректный путь",
  "invalid host": "некорректный хост",
  "invalid port": "некорректный порт",
  "invalid public port": "некорректный публичный порт",
  "invalid pr": "некорректный pull request",
  "invalid user_id": "некорректный user_id",
  "invalid session id": "некорректный ID сессии",
  "invalid variable name": "некорректное имя переменной",
  "invalid email address": "некорректный адрес электронной почты",
  "invalid DNS provider code": "некорректный код DNS-провайдера",
  "name is required": "укажите имя",
  "name and type are required": "укажите имя и тип",
  "host is required": "укажите хост",
  "domain is required": "укажите домен",
  "client ID is required": "укажите client ID",
  "an SSH key is required": "требуется SSH-ключ",
  "compose content is required": "укажите содержимое compose",
  "git_repository is required": "укажите git_repository",
  "docker_image is required": "укажите docker_image",
  "App ID and private key are required": "укажите App ID и закрытый ключ",
  "app, installation and repo (owner/name) are required": "укажите app, installation и repo (owner/name)",
  "target not found": "цель не найдена",
  "environment not found": "окружение не найдено",
  "server not found": "сервер не найден",
  "build server not found": "сервер сборки не найден",
  "registry not found": "реестр не найден",
  "GitHub App not found": "GitHub App не найдено",
  "directory not found in this branch": "каталог не найден в этой ветке",
  "an update is already in progress": "обновление уже выполняется",
  "no release available": "нет доступных релизов",

  // api: authentication and accounts
  "invalid username or password": "неверное имя пользователя или пароль",
  "invalid token": "недействительный токен",
  "invalid signature": "недействительная подпись",
  "invalid signature or unknown GitHub App": "недействительная подпись или неизвестное GitHub App",
  "invalid authentication code": "неверный код аутентификации",
  "the sign-in session expired; enter your password again": "сеанс входа истёк; введите пароль ещё раз",
  "too many incorrect codes; enter your password again": "слишком много неверных кодов; введите пароль ещё раз",
  "this account signs in with single sign-on": "эта учётная запись входит через единый вход (SSO)",
  "password sign-in is disabled for your organization; use single sign-on":
    "вход по паролю отключён для вашей организации; используйте единый вход (SSO)",
  "registration is closed; ask an administrator to invite you": "регистрация закрыта; попросите администратора пригласить вас",
  "this account has been disabled; contact an administrator": "эта учётная запись отключена; обратитесь к администратору",
  "two-factor authentication must be enabled for your account": "для вашей учётной записи необходимо включить двухфакторную аутентификацию",
  "your organization requires two-factor authentication": "ваша организация требует двухфакторную аутентификацию",
  "two-factor authentication is already enabled": "двухфакторная аутентификация уже включена",
  "two-factor authentication is not enabled": "двухфакторная аутентификация не включена",
  "only the owner can reset the owner's two-factor authentication":
    "сбросить двухфакторную аутентификацию владельца может только сам владелец",
  "the code is incorrect — check that your device's clock is correct":
    "неверный код — проверьте, правильно ли установлено время на вашем устройстве",
  "enter a current code from your authenticator app": "введите текущий код из приложения-аутентификатора",
  "start the setup again": "начните настройку заново",
  "current password is incorrect": "текущий пароль указан неверно",
  "password must be at least 8 characters": "пароль должен содержать не менее 8 символов",
  "a user with this email already exists": "пользователь с таким адресом электронной почты уже существует",
  "you cannot disable this account": "вы не можете отключить эту учётную запись",
  "the owner account cannot be deleted": "учётную запись владельца нельзя удалить",
  "password_login must be true or false": "password_login должен быть true или false",
  "require_2fa must be off, admins or all": "require_2fa должен быть off, admins или all",

  // api: roles and permissions
  "admin role required": "требуется роль администратора",
  "project maintainer role required": "требуется роль maintainer в проекте",
  "your project role does not allow changes": "ваша роль в проекте не позволяет вносить изменения",
  "your project role does not allow deployments": "ваша роль в проекте не позволяет выполнять деплой",
  "you requested this deployment; another maintainer must approve it":
    "вы запросили этот деплой; согласовать его должен другой maintainer",
  "only administrators can manage this container": "управлять этим контейнером могут только администраторы",
  "container does not belong to this application": "контейнер не принадлежит этому приложению",
  "role must be viewer, developer or maintainer": "роль должна быть viewer, developer или maintainer",
  "role must be admin or member": "роль должна быть admin или member",
  "default role must be none, viewer, developer or maintainer":
    "роль по умолчанию должна быть none, viewer, developer или maintainer",
  "you would remove your own maintainer access; keep yourself as a maintainer":
    "вы лишите себя доступа maintainer; оставьте себя в числе maintainer",
  "scope must be server, application, database or service": "область должна быть server, application, database или service",

  // api: projects, applications, deployments
  "slug may contain lowercase letters, digits and dashes": "slug может содержать строчные буквы, цифры и дефисы",
  "repository must look like owner/name": "репозиторий должен быть в формате owner/name",
  "select a repository from the GitHub App": "выберите репозиторий из GitHub App",
  "this GitHub App is already registered": "это GitHub App уже зарегистрировано",
  "GitHub Enterprise URL must look like https://github.example.com":
    "URL GitHub Enterprise должен быть в формате https://github.example.com",
  "replicas must be between 1 and 20": "число реплик должно быть от 1 до 20",
  "stop the application before moving it to another server": "остановите приложение перед переносом на другой сервер",
  "no containers; deploy the application first": "нет контейнеров; сначала выполните деплой приложения",
  "backup run has no file": "у запуска резервного копирования нет файла",
  "image name may contain lowercase letters, digits, '.', '_', '-' and '/' (e.g. team/api)":
    "имя образа может содержать строчные буквы, цифры, '.', '_', '-' и '/' (например, team/api)",
  "namespace may contain lowercase letters, digits, '.', '_', '-' and '/'":
    "пространство имён может содержать строчные буквы, цифры, '.', '_', '-' и '/'",
  "image must be a repository without tag, e.g. ghcr.io/acme/portdock":
    "образ должен быть репозиторием без тега, например ghcr.io/acme/portdock",
  "channel must be stable or prerelease": "канал должен быть stable или prerelease",

  // api: domains and certificates
  "cert_mode must be auto, dns or custom": "cert_mode должен быть auto, dns или custom",
  "www_redirect must be none, to-www or to-apex": "www_redirect должен быть none, to-www или to-apex",
  "www redirects are not available for wildcard domains": "перенаправления www недоступны для wildcard-доменов",
  "HTTPS is disabled for this domain": "HTTPS отключён для этого домена",
  "this domain uses an uploaded certificate; upload a renewed one in Settings → Certificates":
    "этот домен использует загруженный сертификат; загрузите обновлённый в разделе Настройки → Сертификаты",
  "the ACME directory must be an https:// URL": "каталог ACME должен быть URL-адресом https://",
  "renewal threshold must be between 3 and 60 days": "порог продления должен быть от 3 до 60 дней",
  "instance URL must look like https://panel.example.com": "URL экземпляра должен быть в формате https://panel.example.com",

  // api: monitoring, notifications, audit
  "threshold must be positive": "порог должен быть положительным",
  "threshold must be a percentage between 0 and 100": "порог должен быть в процентах от 0 до 100",
  "duration must be between 1 and 1440 minutes": "длительность должна быть от 1 до 1440 минут",
  "webhook URL must be an http(s) URL": "URL вебхука должен быть адресом http(s)",
  "configure a webhook URL or syslog target first": "сначала настройте URL вебхука или получателя syslog",

  // api: LDAP and SSO settings
  "the user filter must be an LDAP filter containing {username}": "фильтр пользователей должен быть LDAP-фильтром, содержащим {username}",
  "security must be ldaps, starttls or none": "защита должна быть ldaps, starttls или none",
  "host must be a hostname or IP address, e.g. dc01.corp.example": "хост должен быть именем хоста или IP-адресом, например dc01.corp.example",
  "base DN is required, e.g. DC=corp,DC=example": "укажите base DN, например DC=corp,DC=example",
  "issuer must be a URL such as https://login.microsoftonline.com/<tenant-id>/v2.0":
    "issuer должен быть URL-адресом, например https://login.microsoftonline.com/<tenant-id>/v2.0",

  // api: license
  "a license is already installed": "лицензия уже установлена",
  "the trial has already been used on this instance": "пробный период на этом экземпляре уже использован",

  // certificates (upload)
  "the private key is not PEM encoded": "закрытый ключ не в формате PEM",
  "the private key is password protected; export it without a passphrase":
    "закрытый ключ защищён паролем; экспортируйте его без парольной фразы",
  "unsupported private key format": "неподдерживаемый формат закрытого ключа",
  "the private key does not belong to the first certificate": "закрытый ключ не соответствует первому сертификату",
  "the certificate has no DNS names": "в сертификате нет DNS-имён",

  // deploy
  'nixpacks is not installed on the server; run Server setup with "Also install Nixpacks" enabled':
    "nixpacks не установлен на сервере; запустите настройку сервера с включённым параметром «Также установить Nixpacks»",
  "could not detect the project type; add a Dockerfile or choose a build pack":
    "не удалось определить тип проекта; добавьте Dockerfile или выберите build pack",
  "the shell build pack requires a start command": "для build pack shell требуется команда запуска",
  "compose file defines no services": "в compose-файле не определено ни одного сервиса",
  "rollback is not supported for docker compose applications; redeploy a previous commit instead":
    "откат не поддерживается для приложений docker compose; выполните повторный деплой предыдущего коммита",
  "rollback target not found": "цель отката не найдена",
  "rollback target has no image": "у цели отката нет образа",
  "deployment is not waiting for approval": "деплой не ожидает согласования",
  "deployment is not queued or running": "деплой не находится в очереди и не выполняется",
  "no docker image configured": "docker-образ не настроен",
  "no git repository configured": "git-репозиторий не настроен",
  "no containers found; deploy the application first": "контейнеры не найдены; сначала выполните деплой приложения",
  "S3 storage is not configured (Settings → Backups)": "хранилище S3 не настроено (Настройки → Резервные копии)",
  "invalid backup file": "некорректный файл резервной копии",
  "PortDock is not running in Docker; route the dashboard domain to it with your own reverse proxy":
    "PortDock запущен не в Docker; направьте домен панели на него через собственный обратный прокси",

  // executor
  "unterminated quote or escape in command": "незакрытая кавычка или escape-последовательность в команде",
  "interactive terminal on the local server requires Linux; run PortDock in Docker":
    "интерактивный терминал на локальном сервере требует Linux; запустите PortDock в Docker",
  "server has no SSH key configured": "для сервера не настроен SSH-ключ",
  "ssh key not found": "SSH-ключ не найден",

  // github
  "invalid private key: not PEM encoded": "некорректный закрытый ключ: не в формате PEM",
  "private key must be RSA": "закрытый ключ должен быть RSA",

  // ldap
  "user not found in the directory": "пользователь не найден в каталоге",
  "the directory account is disabled": "учётная запись в каталоге отключена",
  "the CA certificate is not valid PEM": "сертификат CA не является корректным PEM",
  "password expired": "срок действия пароля истёк",
  "account expired": "срок действия учётной записи истёк",
  "account locked out": "учётная запись заблокирована",
  "the base DN does not exist": "base DN не существует",
  "the directory requires an encrypted connection (use LDAPS or StartTLS)":
    "каталог требует зашифрованного соединения (используйте LDAPS или StartTLS)",

  // license
  "this is not a PortDock license key": "это не ключ лицензии PortDock",
  "the license key signature is invalid": "подпись ключа лицензии недействительна",
  "license has no valid expiry date": "у лицензии нет корректной даты окончания",

  // registry
  "the registry rejected these credentials": "реестр отклонил эти учётные данные",
  "the registry did not issue a token": "реестр не выдал токен",

  // sso
  "discovery document is missing required endpoints": "в документе discovery отсутствуют обязательные эндпоинты",
  "ID token audience does not include this client ID": "аудитория ID-токена не включает этот client ID",
  "ID token has expired — check the server clock": "срок действия ID-токена истёк — проверьте время на сервере",

  // updater
  "PortDock is not running in a Docker container with the Docker socket mounted; update it by pulling the new image manually":
    "PortDock запущен не в Docker-контейнере с подключённым Docker-сокетом; обновите его, загрузив новый образ вручную",
  "the new version is unhealthy": "новая версия неработоспособна",
};
