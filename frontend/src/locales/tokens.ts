// Translations for components/tokens.tsx (scoped API tokens and service accounts).
export const ro: Record<string, string> = {
  Read: "Citire",
  "View resources, logs and deployments.": "Vede resursele, logurile și deploy-urile.",
  "Read, plus deploy, redeploy and roll back. The usual choice for CI.": "Citire, plus deploy, redeploy și rollback. Alegerea obișnuită pentru CI.",
  Write: "Scriere",
  "Deploy, plus change configuration, variables and domains. No deletions.": "Deploy, plus modificarea configurației, variabilelor și domeniilor. Fără ștergeri.",
  Full: "Complet",
  "Everything the account may do, including deleting resources and, for administrators, the organization.":
    "Tot ce poate face contul, inclusiv ștergerea resurselor și, pentru administratori, organizația.",
  "90 days": "90 de zile",
  "1 year": "1 an",
  "Every project the account can access, now and later.": "Toate proiectele la care contul are acces, acum și pe viitor.",
  "Other projects stay invisible to this token.": "Celelalte proiecte rămân invizibile pentru acest token.",
  "Only selected projects": "Doar proiectele selectate",
  "Create token": "Creează token",
  "Scripts using this token stop working immediately.": "Scripturile care folosesc acest token nu mai funcționează imediat.",
  "Service accounts": "Conturi de serviciu",
  "Accounts for CI pipelines and scripts. They cannot sign in; they act only through the API tokens you create for them, and their actions appear under their name in the audit log.":
    "Conturi pentru pipeline-uri CI și scripturi. Nu se pot autentifica; acționează doar prin token-urile API pe care le creezi pentru ele, iar acțiunile lor apar sub numele lor în jurnalul de audit.",
  "Add service account": "Adaugă cont de serviciu",
  "No service accounts": "Niciun cont de serviciu",
  "Create one per pipeline (e.g. github-actions) instead of using a person's token.": "Creează câte unul pentru fiecare pipeline (de ex. github-actions) în loc să folosești token-ul unei persoane.",
  Tokens: "Token-uri",
  "{n} token|{n} tokens": "{n} token|{n} token-uri|{n} de token-uri",
  Enable: "Activează",
  Disable: "Dezactivează",
  "Its tokens stop working immediately.": "Token-urile lui nu mai funcționează imediat.",
  "Members get access to projects like people do: the project's default role or an explicit membership.":
    "Membrii primesc acces la proiecte la fel ca oamenii: rolul implicit al proiectului sau o apartenență explicită.",
  "Tokens of {name}": "Token-urile lui {name}",
  "Use with Authorization: Bearer <token> to automate PortDock from scripts or CI. A token acts with your rights, capped by its scope and projects; for pipelines, prefer a service account (Team).":
    "Se folosește cu Authorization: Bearer <token> pentru a automatiza PortDock din scripturi sau CI. Un token acționează cu drepturile tale, limitate de scope și proiecte; pentru pipeline-uri, preferă un cont de serviciu (Team).",
  // server messages
  "this API token has expired": "acest token API a expirat",
  "API tokens cannot manage accounts": "token-urile API nu pot gestiona conturi",
  "API tokens cannot manage API tokens or service accounts": "token-urile API nu pot gestiona token-uri API sau conturi de serviciu",
  "a service account cannot sign in; it acts through API tokens": "un cont de serviciu nu se poate autentifica; acționează prin token-uri API",
  "scope must be read, deploy, write or full": "scope-ul trebuie să fie read, deploy, write sau full",
};

export const ru: Record<string, string> = {
  Read: "Чтение",
  "View resources, logs and deployments.": "Просмотр ресурсов, логов и деплоев.",
  "Read, plus deploy, redeploy and roll back. The usual choice for CI.": "Чтение, а также деплой, повторный деплой и откат. Обычный выбор для CI.",
  Write: "Запись",
  "Deploy, plus change configuration, variables and domains. No deletions.": "Деплой, а также изменение конфигурации, переменных и доменов. Без удаления.",
  Full: "Полный",
  "Everything the account may do, including deleting resources and, for administrators, the organization.":
    "Всё, что может учётная запись, включая удаление ресурсов и, для администраторов, управление организацией.",
  "90 days": "90 дней",
  "1 year": "1 год",
  "Every project the account can access, now and later.": "Все проекты, к которым у учётной записи есть доступ, сейчас и в будущем.",
  "Other projects stay invisible to this token.": "Остальные проекты остаются невидимыми для этого токена.",
  "Only selected projects": "Только выбранные проекты",
  "Create token": "Создать токен",
  "Scripts using this token stop working immediately.": "Скрипты с этим токеном сразу перестанут работать.",
  "Service accounts": "Сервисные учётные записи",
  "Accounts for CI pipelines and scripts. They cannot sign in; they act only through the API tokens you create for them, and their actions appear under their name in the audit log.":
    "Учётные записи для конвейеров CI и скриптов. Они не могут входить в систему; они действуют только через созданные для них токены API, а их действия отображаются под их именем в журнале аудита.",
  "Add service account": "Добавить сервисную учётную запись",
  "No service accounts": "Сервисных учётных записей нет",
  "Create one per pipeline (e.g. github-actions) instead of using a person's token.": "Создайте отдельную запись для каждого конвейера (например, github-actions) вместо токена сотрудника.",
  Tokens: "Токены",
  "{n} token|{n} tokens": "{n} токен|{n} токена|{n} токенов",
  Enable: "Включить",
  Disable: "Отключить",
  "Its tokens stop working immediately.": "Её токены сразу перестанут работать.",
  "Members get access to projects like people do: the project's default role or an explicit membership.":
    "Участники получают доступ к проектам так же, как люди: роль проекта по умолчанию или явное членство.",
  "Tokens of {name}": "Токены {name}",
  "Use with Authorization: Bearer <token> to automate PortDock from scripts or CI. A token acts with your rights, capped by its scope and projects; for pipelines, prefer a service account (Team).":
    "Используйте с Authorization: Bearer <token> для автоматизации PortDock из скриптов или CI. Токен действует с вашими правами, ограниченными областью и проектами; для конвейеров лучше использовать сервисную учётную запись (Команда).",
  // server messages
  "this API token has expired": "срок действия этого токена API истёк",
  "API tokens cannot manage accounts": "токены API не могут управлять учётными записями",
  "API tokens cannot manage API tokens or service accounts": "токены API не могут управлять токенами API и сервисными учётными записями",
  "a service account cannot sign in; it acts through API tokens": "сервисная учётная запись не может входить в систему; она действует через токены API",
  "scope must be read, deploy, write or full": "область должна быть read, deploy, write или full",
};
