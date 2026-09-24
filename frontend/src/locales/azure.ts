// Translations for Azure DevOps (components/azure.tsx, the new application form, the CI/CD tab)
// and the fixed messages of backend/internal/azuredevops and api/azure.go.
export const ro: Record<string, string> = {
  "Connect Azure DevOps": "Conectează Azure DevOps",
  "connect Azure DevOps": "conectează Azure DevOps",
  Connect: "Conectează",
  "Connect an Azure DevOps organization with a personal access token to deploy its Git repositories and deploy on every push.":
    "Conectează o organizație Azure DevOps cu un personal access token ca să lansezi repozitoriile ei Git, cu deploy la fiecare push.",
  "The public URL of this instance is a localhost address, so Azure DevOps cannot deliver push events to it. Repository browsing and manual deploys work; set a public HTTPS address to deploy on push.":
    "URL-ul public al acestei instanțe este o adresă localhost, așa că Azure DevOps nu îi poate trimite evenimentele de push. Navigarea prin repozitorii și deploy-ul manual funcționează; setează o adresă HTTPS publică pentru deploy la push.",
  "No Azure DevOps organization connected": "Nicio organizație Azure DevOps conectată",
  "PortDock clones the repositories with the token and adds a service hook to each application's repository, so every push deploys.":
    "PortDock clonează repozitoriile cu tokenul și adaugă un service hook în repozitoriul fiecărei aplicații, astfel încât fiecare push face deploy.",
  "{n} application|{n} applications": "{n} aplicație|{n} aplicații|{n} de aplicații",
  "connected {time}": "conectat {time}",
  "Replace token": "Înlocuiește tokenul",
  "Open in Azure DevOps": "Deschide în Azure DevOps",
  "PortDock forgets the token. Revoke it in Azure DevOps if no longer needed.": "PortDock uită tokenul. Revocă-l în Azure DevOps dacă nu mai este necesar.",
  "PortDock checks the token by listing the organization's repositories.": "PortDock verifică tokenul listând repozitoriile organizației.",
  "The name or URL, e.g. contoso or https://dev.azure.com/contoso. For Azure DevOps Server, the collection URL.":
    "Numele sau URL-ul, de ex. contoso sau https://dev.azure.com/contoso. Pentru Azure DevOps Server, URL-ul colecției.",
  "Personal access token": "Personal access token",
  "Defaults to the organization name.": "Implicit, numele organizației.",
  "{n} repository available|{n} repositories available": "{n} repozitoriu disponibil|{n} repozitorii disponibile|{n} de repozitorii disponibile",
  "Token replaced": "Token înlocuit",
  "Replace the token of {name}": "Înlocuiește tokenul pentru {name}",
  "Personal access tokens expire. Paste a new one; applications keep deploying with it.": "Tokenurile personale expiră. Lipește unul nou; aplicațiile continuă să facă deploy cu el.",
  "Create the token": "Creează tokenul",
  "In Azure DevOps open {path}, choose the organization and at least these scopes:": "În Azure DevOps deschide {path}, alege organizația și cel puțin aceste scope-uri:",
  "User settings → Personal access tokens": "User settings → Personal access tokens",
  "To add service hooks, the token's user must be allowed to edit subscriptions in the projects (project administrators are).":
    "Ca să adauge service hook-uri, utilizatorul tokenului trebuie să aibă dreptul de a edita abonamentele în proiecte (administratorii de proiect îl au).",
  "Refresh from Azure DevOps": "Reîncarcă din Azure DevOps",
  "This organization has no Git repositories the token can read.": "Această organizație nu are repozitorii Git pe care tokenul să le poată citi.",
  "Pick a repository from your connected Azure DevOps organizations.": "Alege un repozitoriu din organizațiile Azure DevOps conectate.",
  "Tip: {github} or {azure} to browse repositories and deploy automatically on every push.":
    "Sfat: {github} sau {azure} ca să navighezi prin repozitorii și să faci deploy automat la fiecare push.",
  "Connected through Azure DevOps. New commits on the branch deploy automatically.": "Conectat prin Azure DevOps. Commit-urile noi pe branch fac deploy automat.",
  // CI/CD tab
  "Service hooks in the repository's project send pushes and pull requests to PortDock, which reports each deployment on the commit and the pull request.":
    "Service hook-urile din proiectul repozitoriului trimit push-urile și pull request-urile către PortDock, care raportează fiecare deploy pe commit și pe pull request.",
  "Create service hooks": "Creează service hook-urile",
  "Recreate service hooks": "Recreează service hook-urile",
  "Service hook created": "Service hook creat",
  "Service hooks active": "Service hook-uri active",
  "{n} of {total} service hooks active": "{n} din {total} service hook-uri active",
  "No service hook": "Fără service hook",
  "The service hooks could not be created": "Service hook-urile nu au putut fi create",
  "You can also add them by hand: in {path}, add a {type} subscription on this repository for each of {events}, with this URL and HTTP header.":
    "Le poți adăuga și manual: în {path}, adaugă pe acest repozitoriu câte un abonament {type} pentru fiecare dintre {events}, cu acest URL și header HTTP.",
  "Project settings → Service hooks": "Project settings → Service hooks",
  "HTTP headers": "Headere HTTP",
  "Pushes to {branch} deploy when {auto} is on; pull requests targeting it get a preview when previews are enabled. Statuses show as {status} (the token needs the Code: Status scope).":
    "Push-urile pe {branch} fac deploy când {auto} este activ; pull request-urile către el primesc un preview dacă preview-urile sunt activate. Statusurile apar ca {status} (tokenul are nevoie de scope-ul Code: Status).",
  "In the Teams channel: ⋯ → Workflows → “Post to a channel when a webhook request is received”, then copy the URL. Incoming webhook URLs work too.":
    "În canalul Teams: ⋯ → Workflows → „Post to a channel when a webhook request is received”, apoi copiază URL-ul. Merg și URL-urile de incoming webhook.",
  // Fixed server messages
  "organization and personal access token are required": "organizația și personal access token-ul sunt obligatorii",
  "enter the organization, e.g. contoso or https://dev.azure.com/contoso": "introdu organizația, de ex. contoso sau https://dev.azure.com/contoso",
  "invalid organization name": "nume de organizație invalid",
  "invalid organization URL": "URL de organizație invalid",
  "Azure DevOps rejected the personal access token (check that it is valid, not expired and has the needed scopes)":
    "Azure DevOps a respins personal access token-ul (verifică dacă este valid, neexpirat și are scope-urile necesare)",
  "Azure DevOps connection not found": "Conexiunea Azure DevOps nu a fost găsită",
  "select an Azure DevOps repository": "alege un repozitoriu Azure DevOps",
  "invalid Azure DevOps repository URL": "URL de repozitoriu Azure DevOps invalid",
  "this application doesn't deploy from Azure DevOps": "această aplicație nu face deploy din Azure DevOps",
};

export const ru: Record<string, string> = {
  "Connect Azure DevOps": "Подключить Azure DevOps",
  "connect Azure DevOps": "подключите Azure DevOps",
  Connect: "Подключить",
  "Connect an Azure DevOps organization with a personal access token to deploy its Git repositories and deploy on every push.":
    "Подключите организацию Azure DevOps с помощью персонального токена доступа, чтобы развёртывать её Git-репозитории с деплоем при каждом push.",
  "The public URL of this instance is a localhost address, so Azure DevOps cannot deliver push events to it. Repository browsing and manual deploys work; set a public HTTPS address to deploy on push.":
    "Публичный адрес этого экземпляра — localhost, поэтому Azure DevOps не может доставлять ему события push. Просмотр репозиториев и ручной деплой работают; задайте публичный HTTPS-адрес для деплоя при push.",
  "No Azure DevOps organization connected": "Организации Azure DevOps не подключены",
  "PortDock clones the repositories with the token and adds a service hook to each application's repository, so every push deploys.":
    "PortDock клонирует репозитории с этим токеном и добавляет service hook в репозиторий каждого приложения, чтобы каждый push запускал деплой.",
  "{n} application|{n} applications": "{n} приложение|{n} приложения|{n} приложений",
  "connected {time}": "подключено {time}",
  "Replace token": "Заменить токен",
  "Open in Azure DevOps": "Открыть в Azure DevOps",
  "PortDock forgets the token. Revoke it in Azure DevOps if no longer needed.": "PortDock забудет токен. Отзовите его в Azure DevOps, если он больше не нужен.",
  "PortDock checks the token by listing the organization's repositories.": "PortDock проверяет токен, запрашивая список репозиториев организации.",
  "The name or URL, e.g. contoso or https://dev.azure.com/contoso. For Azure DevOps Server, the collection URL.":
    "Имя или URL, например contoso или https://dev.azure.com/contoso. Для Azure DevOps Server — URL коллекции.",
  "Personal access token": "Персональный токен доступа",
  "Defaults to the organization name.": "По умолчанию — имя организации.",
  "{n} repository available|{n} repositories available": "Доступен {n} репозиторий|Доступно {n} репозитория|Доступно {n} репозиториев",
  "Token replaced": "Токен заменён",
  "Replace the token of {name}": "Заменить токен для {name}",
  "Personal access tokens expire. Paste a new one; applications keep deploying with it.": "Срок действия персональных токенов истекает. Вставьте новый — приложения продолжат деплоиться с ним.",
  "Create the token": "Создайте токен",
  "In Azure DevOps open {path}, choose the organization and at least these scopes:": "В Azure DevOps откройте {path}, выберите организацию и как минимум эти области доступа:",
  "User settings → Personal access tokens": "User settings → Personal access tokens",
  "To add service hooks, the token's user must be allowed to edit subscriptions in the projects (project administrators are).":
    "Чтобы добавлять service hooks, пользователь токена должен иметь право изменять подписки в проектах (у администраторов проекта оно есть).",
  "Refresh from Azure DevOps": "Обновить из Azure DevOps",
  "This organization has no Git repositories the token can read.": "В этой организации нет Git-репозиториев, доступных токену.",
  "Pick a repository from your connected Azure DevOps organizations.": "Выберите репозиторий из подключённых организаций Azure DevOps.",
  "Tip: {github} or {azure} to browse repositories and deploy automatically on every push.":
    "Совет: {github} или {azure}, чтобы выбирать репозитории и деплоить автоматически при каждом push.",
  "Connected through Azure DevOps. New commits on the branch deploy automatically.": "Подключено через Azure DevOps. Новые коммиты в ветке деплоятся автоматически.",
  // CI/CD tab
  "Service hooks in the repository's project send pushes and pull requests to PortDock, which reports each deployment on the commit and the pull request.":
    "Service hooks в проекте репозитория отправляют push и pull request в PortDock, а PortDock сообщает о каждом деплое в коммите и в pull request.",
  "Create service hooks": "Создать service hooks",
  "Recreate service hooks": "Пересоздать service hooks",
  "Service hook created": "Service hook создан",
  "Service hooks active": "Service hooks активны",
  "{n} of {total} service hooks active": "Активно service hooks: {n} из {total}",
  "No service hook": "Нет service hook",
  "The service hooks could not be created": "Не удалось создать service hooks",
  "You can also add them by hand: in {path}, add a {type} subscription on this repository for each of {events}, with this URL and HTTP header.":
    "Их можно добавить и вручную: в {path} добавьте для этого репозитория подписку {type} на каждое из событий {events}, с этим URL и HTTP-заголовком.",
  "Project settings → Service hooks": "Project settings → Service hooks",
  "HTTP headers": "HTTP-заголовки",
  "Pushes to {branch} deploy when {auto} is on; pull requests targeting it get a preview when previews are enabled. Statuses show as {status} (the token needs the Code: Status scope).":
    "Push в {branch} запускает деплой, когда включено «{auto}»; pull request в эту ветку получают превью, если превью включены. Статусы отображаются как {status} (токену нужна область Code: Status).",
  "In the Teams channel: ⋯ → Workflows → “Post to a channel when a webhook request is received”, then copy the URL. Incoming webhook URLs work too.":
    "В канале Teams: ⋯ → Workflows → «Post to a channel when a webhook request is received», затем скопируйте URL. URL входящих вебхуков тоже подходят.",
  // Fixed server messages
  "organization and personal access token are required": "укажите организацию и персональный токен доступа",
  "enter the organization, e.g. contoso or https://dev.azure.com/contoso": "укажите организацию, например contoso или https://dev.azure.com/contoso",
  "invalid organization name": "неверное имя организации",
  "invalid organization URL": "неверный URL организации",
  "Azure DevOps rejected the personal access token (check that it is valid, not expired and has the needed scopes)":
    "Azure DevOps отклонил персональный токен доступа (проверьте, что он действителен, не истёк и имеет нужные области доступа)",
  "Azure DevOps connection not found": "Подключение Azure DevOps не найдено",
  "select an Azure DevOps repository": "выберите репозиторий Azure DevOps",
  "invalid Azure DevOps repository URL": "неверный URL репозитория Azure DevOps",
  "this application doesn't deploy from Azure DevOps": "это приложение не деплоится из Azure DevOps",
};
