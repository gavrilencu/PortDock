// Translations for components/secretstores.tsx (Settings → Secret managers) and
// the secret manager references of the variable editor.
export const ro: Record<string, string> = {
  "Secret managers": "Secret managers",
  "Secret manager": "Secret manager",
  "secret manager": "secret manager",
  "Secret manager…": "Secret manager…",
  "Add a secret manager": "Adaugă un secret manager",
  "Add secret manager": "Adaugă secret manager",
  "Secret manager added": "Secret manager adăugat",
  "Secret manager updated": "Secret manager actualizat",
  "Secret manager removed": "Secret manager șters",
  "No secret managers": "Niciun secret manager",
  "A KV secrets engine of Vault or OpenBao, read with a token or an AppRole.": "Un motor de secrete KV din Vault sau OpenBao, citit cu un token sau un AppRole.",
  "Secrets of a Key Vault, read with an app registration or the managed identity of the VM.":
    "Secretele unui Key Vault, citite cu un app registration sau cu identitatea gestionată (managed identity) a VM-ului.",
  "Variables can read their value from it when a deployment starts; PortDock never stores the value.":
    "Variabilele își pot lua valoarea de aici la pornirea unui deploy; PortDock nu păstrează niciodată valoarea.",
  "Variables can read their value from HashiCorp Vault or Azure Key Vault instead of storing it: PortDock reads it when a deployment or job starts and masks it in logs. A new value is used from the next deployment.":
    "Variabilele își pot lua valoarea din HashiCorp Vault sau Azure Key Vault în loc s-o păstreze în PortDock: valoarea e citită la pornirea unui deploy sau job și e mascată în loguri. O valoare nouă se folosește de la următorul deploy.",
  "Keep the secrets in the vault your security team already runs, and reference them from the variables.":
    "Păstrează secretele în vault-ul pe care echipa de securitate îl administrează deja și referă-le din variabile.",
  "Used in references, e.g. vault-prod: shop/db#password.": "Se folosește în referințe, de exemplu vault-prod: shop/db#password.",
  "Vault URL": "URL-ul vault-ului",
  Token: "Token",
  "App registration (client secret)": "App registration (client secret)",
  "Managed identity": "Identitate gestionată (managed identity)",
  "A token whose policy allows reading the secrets; a periodic token does not expire.":
    "Un token a cărui politică permite citirea secretelor; un token periodic nu expiră.",
  "Role ID": "Role ID",
  "Secret ID": "Secret ID",
  "AppRole mount": "Mount-ul AppRole",
  "Default: approle": "Implicit: approle",
  "Vault Enterprise or HCP only.": "Doar pentru Vault Enterprise sau HCP.",
  "KV secrets engine": "Motorul de secrete KV",
  "Its mount path and version.": "Calea de montare și versiunea lui.",
  "Path prefix": "Prefix de cale",
  "Variables may only read paths under it, e.g. shop. Empty: any path the token can read.":
    "Variabilele pot citi doar căi de sub acest prefix, de exemplu shop. Gol: orice cale pe care o poate citi token-ul.",
  "Tenant ID": "Tenant ID",
  "Authority host": "Authority host",
  "Default: https://login.microsoftonline.com (change it for a sovereign cloud).": "Implicit: https://login.microsoftonline.com (schimbă-l pentru un cloud suveran).",
  "Only for a user-assigned identity; empty uses the VM's system-assigned identity.":
    "Doar pentru o identitate user-assigned; gol folosește identitatea system-assigned a VM-ului.",
  "Give the identity the Key Vault Secrets User role on the vault (or a Get secret access policy). Listing secrets is not needed.":
    "Dă identității rolul Key Vault Secrets User pe vault (sau o politică de acces Get pentru secrete). Listarea secretelor nu e necesară.",
  "CA certificate": "Certificat CA",
  "PEM of an internal CA that signed the server's certificate. Optional.": "PEM-ul unui CA intern care a semnat certificatul serverului. Opțional.",
  "Projects whose variables may use it. None selected: every project and the organization's shared variables.":
    "Proiectele ale căror variabile îl pot folosi. Niciunul selectat: toate proiectele și variabilele partajate ale organizației.",
  "Test a secret (optional)": "Testează un secret (opțional)",
  "A path to read with Test connection, e.g. shop/db: its keys are listed, never the values.":
    "O cale citită la Test connection, de exemplu shop/db: se afișează cheile ei, niciodată valorile.",
  "A secret name to read with Test connection: its keys are listed, never the value.":
    "Numele unui secret citit la Test connection: se afișează cheile lui, niciodată valoarea.",
  "Signed in.": "Autentificare reușită.",
  "Keys: {keys}": "Chei: {keys}",
  "The secret has one value (no keys).": "Secretul are o singură valoare (fără chei).",
  "The test failed": "Testul a eșuat",
  "Last test": "Ultimul test",
  "OK {time}": "OK {time}",
  "Variables that read from it must be removed first.": "Variabilele care citesc din el trebuie șterse mai întâi.",
  "How variables use it": "Cum îl folosesc variabilele",
  "In the Environment tab of an application (or in shared variables), choose Secret manager when adding a variable, then the store, the path and the key. Check lists the keys of the path.":
    "În tab-ul Environment al unei aplicații (sau la variabilele partajate), alege Secret manager când adaugi o variabilă, apoi store-ul, calea și cheia. Check afișează cheile căii.",
  "In portdock.yaml, list them under secret_refs:": "În portdock.yaml, se trec la secret_refs:",
  "(deleted store)": "(store șters)",
  key: "cheie",
  "key (optional)": "cheie (opțional)",
  Check: "Verifică",
  "Found.": "Găsit.",
  "Keys:": "Chei:",
  "Check the reference": "Verifică referința",
  "{key} resolves": "{key} se rezolvă",
  "Read from the secret manager when a deployment starts": "Citită din secret manager la pornirea unui deploy",
  "{n} variable read from a secret manager is not shown here and is kept.|{n} variables read from a secret manager are not shown here and are kept.":
    "{n} variabilă citită dintr-un secret manager nu apare aici și se păstrează.|{n} variabile citite dintr-un secret manager nu apar aici și se păstrează.|{n} de variabile citite dintr-un secret manager nu apar aici și se păstrează.",
};

export const ru: Record<string, string> = {
  "Secret managers": "Менеджеры секретов",
  "Secret manager": "Менеджер секретов",
  "secret manager": "менеджер секретов",
  "Secret manager…": "Менеджер секретов…",
  "Add a secret manager": "Добавить менеджер секретов",
  "Add secret manager": "Добавить менеджер секретов",
  "Secret manager added": "Менеджер секретов добавлен",
  "Secret manager updated": "Менеджер секретов обновлён",
  "Secret manager removed": "Менеджер секретов удалён",
  "No secret managers": "Нет менеджеров секретов",
  "A KV secrets engine of Vault or OpenBao, read with a token or an AppRole.": "Движок секретов KV в Vault или OpenBao, чтение по токену или AppRole.",
  "Secrets of a Key Vault, read with an app registration or the managed identity of the VM.":
    "Секреты Key Vault, чтение через регистрацию приложения или управляемое удостоверение ВМ.",
  "Variables can read their value from it when a deployment starts; PortDock never stores the value.":
    "Переменные могут брать из него значение при запуске деплоя; PortDock никогда не хранит значение.",
  "Variables can read their value from HashiCorp Vault or Azure Key Vault instead of storing it: PortDock reads it when a deployment or job starts and masks it in logs. A new value is used from the next deployment.":
    "Переменные могут брать значение из HashiCorp Vault или Azure Key Vault вместо хранения: PortDock читает его при запуске деплоя или задачи и скрывает в логах. Новое значение используется со следующего деплоя.",
  "Keep the secrets in the vault your security team already runs, and reference them from the variables.":
    "Храните секреты в хранилище, которое уже ведёт служба безопасности, и ссылайтесь на них из переменных.",
  "Used in references, e.g. vault-prod: shop/db#password.": "Используется в ссылках, например vault-prod: shop/db#password.",
  "Vault URL": "URL хранилища",
  Token: "Токен",
  "App registration (client secret)": "Регистрация приложения (секрет клиента)",
  "Managed identity": "Управляемое удостоверение",
  "A token whose policy allows reading the secrets; a periodic token does not expire.":
    "Токен, политика которого разрешает чтение секретов; периодический токен не истекает.",
  "Role ID": "Role ID",
  "Secret ID": "Secret ID",
  "AppRole mount": "Путь AppRole",
  "Default: approle": "По умолчанию: approle",
  "Vault Enterprise or HCP only.": "Только для Vault Enterprise или HCP.",
  "KV secrets engine": "Движок секретов KV",
  "Its mount path and version.": "Путь монтирования и версия.",
  "Path prefix": "Префикс пути",
  "Variables may only read paths under it, e.g. shop. Empty: any path the token can read.":
    "Переменные могут читать только пути под ним, например shop. Пусто: любой путь, доступный токену.",
  "Tenant ID": "ID клиента (tenant)",
  "Authority host": "Хост авторизации",
  "Default: https://login.microsoftonline.com (change it for a sovereign cloud).": "По умолчанию: https://login.microsoftonline.com (измените для суверенного облака).",
  "Only for a user-assigned identity; empty uses the VM's system-assigned identity.":
    "Только для назначенного пользователем удостоверения; пусто — системное удостоверение ВМ.",
  "Give the identity the Key Vault Secrets User role on the vault (or a Get secret access policy). Listing secrets is not needed.":
    "Выдайте удостоверению роль Key Vault Secrets User для хранилища (или политику доступа Get для секретов). Право на список секретов не нужно.",
  "CA certificate": "Сертификат ЦС",
  "PEM of an internal CA that signed the server's certificate. Optional.": "PEM внутреннего ЦС, подписавшего сертификат сервера. Необязательно.",
  "Projects whose variables may use it. None selected: every project and the organization's shared variables.":
    "Проекты, переменные которых могут его использовать. Ничего не выбрано: все проекты и общие переменные организации.",
  "Test a secret (optional)": "Проверить секрет (необязательно)",
  "A path to read with Test connection, e.g. shop/db: its keys are listed, never the values.":
    "Путь для чтения при проверке подключения, например shop/db: показываются ключи, но не значения.",
  "A secret name to read with Test connection: its keys are listed, never the value.":
    "Имя секрета для чтения при проверке подключения: показываются ключи, но не значение.",
  "Signed in.": "Вход выполнен.",
  "Keys: {keys}": "Ключи: {keys}",
  "The secret has one value (no keys).": "У секрета одно значение (без ключей).",
  "The test failed": "Проверка не удалась",
  "Last test": "Последняя проверка",
  "OK {time}": "OK {time}",
  "Variables that read from it must be removed first.": "Сначала удалите переменные, которые из него читают.",
  "How variables use it": "Как его используют переменные",
  "In the Environment tab of an application (or in shared variables), choose Secret manager when adding a variable, then the store, the path and the key. Check lists the keys of the path.":
    "На вкладке Environment приложения (или в общих переменных) при добавлении переменной выберите «Менеджер секретов», затем хранилище, путь и ключ. «Проверить» показывает ключи пути.",
  "In portdock.yaml, list them under secret_refs:": "В portdock.yaml перечислите их в secret_refs:",
  "(deleted store)": "(удалённое хранилище)",
  key: "ключ",
  "key (optional)": "ключ (необязательно)",
  Check: "Проверить",
  "Found.": "Найдено.",
  "Keys:": "Ключи:",
  "Check the reference": "Проверить ссылку",
  "{key} resolves": "{key} разрешается",
  "Read from the secret manager when a deployment starts": "Читается из менеджера секретов при запуске деплоя",
  "{n} variable read from a secret manager is not shown here and is kept.|{n} variables read from a secret manager are not shown here and are kept.":
    "{n} переменная из менеджера секретов здесь не показана и сохраняется.|{n} переменные из менеджера секретов здесь не показаны и сохраняются.|{n} переменных из менеджера секретов здесь не показаны и сохраняются.",
};
