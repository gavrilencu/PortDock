// Translations for components/Volumes.tsx (persistent storage of applications).
export const ro: Record<string, string> = {
  Storage: "Stocare",
  "Files written to these paths survive redeploys, restarts and rollbacks.": "Fișierele scrise în aceste căi rămân după redeploy, reporniri și rollback.",
  "No persistent storage": "Nicio stocare persistentă",
  "Without a volume, everything the application writes is lost on the next deployment. Add one for uploads, SQLite databases or caches.":
    "Fără un volum, tot ce scrie aplicația se pierde la următorul deploy. Adaugă unul pentru fișiere încărcate, baze de date SQLite sau cache.",
  "Mount path": "Cale de montare",
  "On servers": "Pe servere",
  "Docker volume": "Volum Docker",
  "Server directory": "Director de pe server",
  "Read-write": "Citire și scriere",
  "Mounted from each server": "Montat de pe fiecare server",
  "Created on the first deployment": "Se creează la primul deploy",
  "Created on the next deployment": "Se creează la următorul deploy",
  "Volume added": "Volum adăugat",
  "Volume updated": "Volum actualizat",
  "Volume removed": "Volum eliminat",
  "Volume and its data deleted": "Volumul și datele lui au fost șterse",
  "It is mounted on the first deployment.": "Va fi montat la primul deploy.",
  "Remove {name} from the application?": "Elimini {name} din aplicație?",
  "The directory stays on the server; it is no longer mounted after the next deployment.": "Directorul rămâne pe server; nu mai este montat după următorul deploy.",
  "The data stays on the servers; the volume is no longer mounted after the next deployment.": "Datele rămân pe servere; volumul nu mai este montat după următorul deploy.",
  "Remove volume": "Elimină volumul",
  "Remove, keep data": "Elimină, păstrează datele",
  "Delete volume {name} and ALL its data?": "Ștergi volumul {name} și TOATE datele lui?",
  "The Docker volume is deleted on every server, including the copies of pull request previews. This cannot be undone.":
    "Volumul Docker se șterge de pe fiecare server, inclusiv copiile preview-urilor pentru pull request-uri. Acțiunea este ireversibilă.",
  "Each server of the application keeps its own copy of a Docker volume; data is not synchronized between servers.":
    "Fiecare server al aplicației are propria copie a unui volum Docker; datele nu se sincronizează între servere.",
  "Replicas on a server, and the old and new version during a deployment, use the same volume at the same time.":
    "Replicile de pe un server, precum și versiunea veche și cea nouă în timpul unui deploy, folosesc același volum simultan.",
  "Pull request previews get their own empty volumes; server directories are not mounted in previews.":
    "Preview-urile pentru pull request-uri primesc volume proprii, goale; directoarele de pe server nu sunt montate în preview-uri.",
  "Leftover data": "Date rămase",
  "Docker volumes of this application that are no longer mounted. Their data is kept until you delete it.":
    "Volume Docker ale acestei aplicații care nu mai sunt montate. Datele lor se păstrează până le ștergi.",
  "Delete the data of {name}?": "Ștergi datele din {name}?",
  "The Docker volume is deleted on every server. This cannot be undone.": "Volumul Docker se șterge de pe fiecare server. Acțiunea este ireversibilă.",
  "Delete data": "Șterge datele",
  "Data deleted": "Datele au fost șterse",
  "Volumes come from the compose file": "Volumele vin din fișierul compose",
  "Docker Compose applications declare their volumes in docker-compose.yml; they are kept across deployments.":
    "Aplicațiile Docker Compose își declară volumele în docker-compose.yml; acestea se păstrează între deploy-uri.",
  "Edit volume": "Editează volumul",
  "Changes apply on the next deployment.": "Modificările se aplică la următorul deploy.",
  "Only administrators can mount directories of the server": "Doar administratorii pot monta directoare de pe server",
  "The data is stored under this name, so it cannot be changed.": "Datele sunt stocate sub acest nume, deci nu poate fi schimbat.",
  "Lowercase letters, digits, '.' and '-', e.g. data or uploads.": "Litere mici, cifre, „.” și „-”, de ex. data sau uploads.",
  "Absolute path on every server of the application. System directories and PortDock's data cannot be mounted.":
    "Cale absolută pe fiecare server al aplicației. Directoarele de sistem și datele PortDock nu pot fi montate.",
  "Absolute path inside the container where the application reads and writes the data.": "Cale absolută în container, unde aplicația citește și scrie datele.",
  "The application can read the files but not change them.": "Aplicația poate citi fișierele, dar nu le poate modifica.",
  // Application danger zone
  "Its persistent volumes can be kept for later or deleted with it.": "Volumele persistente pot fi păstrate pentru mai târziu sau șterse odată cu ea.",
  "Delete application (keep data)?": "Ștergi aplicația (păstrezi datele)?",
  "The Docker volumes stay on the servers.": "Volumele Docker rămân pe servere.",
  "Delete application and ALL data?": "Ștergi aplicația și TOATE datele?",
  "Its persistent volumes are destroyed on every server. This cannot be undone.": "Volumele persistente se distrug pe fiecare server. Acțiunea este ireversibilă.",
};

export const ru: Record<string, string> = {
  Storage: "Хранилище",
  "Files written to these paths survive redeploys, restarts and rollbacks.": "Файлы, записанные по этим путям, сохраняются при повторных деплоях, перезапусках и откатах.",
  "No persistent storage": "Нет постоянного хранилища",
  "Without a volume, everything the application writes is lost on the next deployment. Add one for uploads, SQLite databases or caches.":
    "Без тома всё, что записывает приложение, теряется при следующем деплое. Добавьте том для загрузок, баз SQLite или кэша.",
  "Mount path": "Путь монтирования",
  "On servers": "На серверах",
  "Docker volume": "Том Docker",
  "Server directory": "Каталог сервера",
  "Read-write": "Чтение и запись",
  "Mounted from each server": "Монтируется с каждого сервера",
  "Created on the first deployment": "Создаётся при первом деплое",
  "Created on the next deployment": "Создаётся при следующем деплое",
  "Volume added": "Том добавлен",
  "Volume updated": "Том обновлён",
  "Volume removed": "Том отключён",
  "Volume and its data deleted": "Том и его данные удалены",
  "It is mounted on the first deployment.": "Он будет смонтирован при первом деплое.",
  "Remove {name} from the application?": "Отключить {name} от приложения?",
  "The directory stays on the server; it is no longer mounted after the next deployment.": "Каталог остаётся на сервере; после следующего деплоя он больше не монтируется.",
  "The data stays on the servers; the volume is no longer mounted after the next deployment.": "Данные остаются на серверах; после следующего деплоя том больше не монтируется.",
  "Remove volume": "Отключить том",
  "Remove, keep data": "Отключить, сохранить данные",
  "Delete volume {name} and ALL its data?": "Удалить том {name} и ВСЕ его данные?",
  "The Docker volume is deleted on every server, including the copies of pull request previews. This cannot be undone.":
    "Том Docker удаляется на каждом сервере, включая копии превью pull request. Это действие необратимо.",
  "Each server of the application keeps its own copy of a Docker volume; data is not synchronized between servers.":
    "Каждый сервер приложения хранит свою копию тома Docker; данные между серверами не синхронизируются.",
  "Replicas on a server, and the old and new version during a deployment, use the same volume at the same time.":
    "Реплики на сервере, а также старая и новая версии во время деплоя используют один и тот же том одновременно.",
  "Pull request previews get their own empty volumes; server directories are not mounted in previews.":
    "Превью pull request получают собственные пустые тома; каталоги сервера в превью не монтируются.",
  "Leftover data": "Оставшиеся данные",
  "Docker volumes of this application that are no longer mounted. Their data is kept until you delete it.":
    "Тома Docker этого приложения, которые больше не монтируются. Их данные хранятся, пока вы их не удалите.",
  "Delete the data of {name}?": "Удалить данные {name}?",
  "The Docker volume is deleted on every server. This cannot be undone.": "Том Docker удаляется на каждом сервере. Это действие необратимо.",
  "Delete data": "Удалить данные",
  "Data deleted": "Данные удалены",
  "Volumes come from the compose file": "Тома задаются в compose-файле",
  "Docker Compose applications declare their volumes in docker-compose.yml; they are kept across deployments.":
    "Приложения Docker Compose объявляют тома в docker-compose.yml; они сохраняются между деплоями.",
  "Edit volume": "Изменить том",
  "Changes apply on the next deployment.": "Изменения применяются при следующем деплое.",
  "Only administrators can mount directories of the server": "Только администраторы могут монтировать каталоги сервера",
  "The data is stored under this name, so it cannot be changed.": "Данные хранятся под этим именем, поэтому его нельзя изменить.",
  "Lowercase letters, digits, '.' and '-', e.g. data or uploads.": "Строчные буквы, цифры, «.» и «-», например data или uploads.",
  "Absolute path on every server of the application. System directories and PortDock's data cannot be mounted.":
    "Абсолютный путь на каждом сервере приложения. Системные каталоги и данные PortDock монтировать нельзя.",
  "Absolute path inside the container where the application reads and writes the data.": "Абсолютный путь внутри контейнера, где приложение читает и записывает данные.",
  "The application can read the files but not change them.": "Приложение может читать файлы, но не изменять их.",
  // Application danger zone
  "Its persistent volumes can be kept for later or deleted with it.": "Его постоянные тома можно сохранить или удалить вместе с ним.",
  "Delete application (keep data)?": "Удалить приложение (сохранить данные)?",
  "The Docker volumes stay on the servers.": "Тома Docker остаются на серверах.",
  "Delete application and ALL data?": "Удалить приложение и ВСЕ данные?",
  "Its persistent volumes are destroyed on every server. This cannot be undone.": "Его постоянные тома уничтожаются на каждом сервере. Это действие необратимо.",
};
