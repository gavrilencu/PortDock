// Translations for database backups (pages/Database.tsx Backups tab, Settings → Backup storage)
// and the fixed messages of the backup jobs (backend/internal/deploy/backups.go, api/databases.go).
export const ro: Record<string, string> = {
  Scheduled: "Programat",
  "Before restore": "Înainte de restaurare",
  Pending: "În așteptare",
  Success: "Reușit",
  "Backup queued": "Backup pus în coadă",
  "It starts as soon as other backups of this database have finished.": "Pornește imediat ce se termină celelalte backup-uri ale acestei baze de date.",
  "Restore queued": "Restaurare pusă în coadă",
  "A safety backup of the current data is taken first.": "Mai întâi se face un backup de siguranță al datelor actuale.",
  "Backup deleted": "Backup șters",
  "Stored on {server} in {directory}. Backups and restores of this database run one at a time; new requests wait as pending.":
    "Stocate pe {server} în {directory}. Backup-urile și restaurările acestei baze de date rulează pe rând; cererile noi așteaptă în coadă.",
  "Back up now": "Fă backup acum",
  "Backups are not available for this database engine.": "Backup-urile nu sunt disponibile pentru acest tip de bază de date.",
  "No backups yet": "Încă nu există backup-uri",
  "Back up now, or add a schedule below.": "Fă un backup acum sau adaugă mai jos o programare.",
  "File on the server": "Fișierul pe server",
  "Restore pending": "Restaurare în așteptare",
  "Restoring…": "Se restaurează…",
  "Restored {time}": "Restaurat {time}",
  "Restore failed": "Restaurare eșuată",
  "The data in {name} is replaced by this backup. A safety backup of the current data is taken first, so you can go back.":
    "Datele din {name} sunt înlocuite cu acest backup. Mai întâi se face un backup de siguranță al datelor actuale, ca să poți reveni.",
  "Delete backup": "Șterge backup-ul",
  "Delete this backup?": "Ștergi acest backup?",
  "The file is removed from the server.": "Fișierul este șters de pe server.",
  "This engine's backups can be downloaded but not restored from PortDock: restore them with the engine's own tools.":
    "Backup-urile acestui tip de bază de date pot fi descărcate, dar nu restaurate din PortDock: restaurează-le cu instrumentele proprii ale bazei de date.",
  "Backups on a schedule, each keeping its last N files, with optional S3 upload.": "Backup-uri programate; fiecare programare păstrează ultimele N fișiere, cu încărcare opțională în S3.",
  "Add a schedule to back up this database automatically.": "Adaugă o programare ca această bază de date să aibă backup automat.",
  "{n} database backup queued|{n} database backups queued": "{n} backup de bază de date pus în coadă|{n} backup-uri de baze de date puse în coadă|{n} de backup-uri de baze de date puse în coadă",
  "Follow them in the Backups tab of each database.": "Urmărește-le în tab-ul Backups al fiecărei baze de date.",
  "Back up all databases": "Backup pentru toate bazele de date",
  "Queues a backup of every database now. Each is stored on its own server and can be downloaded from the database's Backups tab.":
    "Pune acum în coadă câte un backup pentru fiecare bază de date. Fiecare se stochează pe serverul ei și poate fi descărcat din tab-ul Backups al bazei de date.",
  "Back up all now": "Backup pentru toate acum",
  // Job progress and fixed server messages
  "Taking a safety backup of the current data…": "Se face backup-ul de siguranță al datelor actuale…",
  "the database is not running; start it to back it up": "baza de date nu rulează; pornește-o ca să-i faci backup",
  "this backup is already being restored": "acest backup este deja în curs de restaurare",
  "this backup is in use; wait until it finishes": "acest backup este în uz; așteaptă să se termine",
  "the backup is empty": "backup-ul este gol",
  "the SQL Server database name may only contain letters, digits and underscores": "numele bazei de date SQL Server poate conține doar litere, cifre și liniuțe de subliniere",
  "SQL Server needs a password of at least 8 characters with three of: uppercase, lowercase, digits, symbols":
    "SQL Server cere o parolă de cel puțin 8 caractere, cu trei dintre: majuscule, minuscule, cifre, simboluri",
  "interrupted: PortDock restarted": "întrerupt: PortDock a repornit",
  "interrupted: PortDock restarted; check the database and restore again": "întrerupt: PortDock a repornit; verifică baza de date și restaurează din nou",
};

export const ru: Record<string, string> = {
  Scheduled: "По расписанию",
  "Before restore": "Перед восстановлением",
  Pending: "В очереди",
  Success: "Успешно",
  "Backup queued": "Резервная копия поставлена в очередь",
  "It starts as soon as other backups of this database have finished.": "Начнётся, как только завершатся другие резервные копии этой базы данных.",
  "Restore queued": "Восстановление поставлено в очередь",
  "A safety backup of the current data is taken first.": "Сначала создаётся страховочная копия текущих данных.",
  "Backup deleted": "Резервная копия удалена",
  "Stored on {server} in {directory}. Backups and restores of this database run one at a time; new requests wait as pending.":
    "Хранятся на {server} в {directory}. Резервные копии и восстановления этой базы данных выполняются по очереди; новые запросы ждут в очереди.",
  "Back up now": "Создать копию сейчас",
  "Backups are not available for this database engine.": "Резервные копии недоступны для этого типа базы данных.",
  "No backups yet": "Резервных копий пока нет",
  "Back up now, or add a schedule below.": "Создайте копию сейчас или добавьте расписание ниже.",
  "File on the server": "Файл на сервере",
  "Restore pending": "Восстановление в очереди",
  "Restoring…": "Восстановление…",
  "Restored {time}": "Восстановлено {time}",
  "Restore failed": "Ошибка восстановления",
  "The data in {name} is replaced by this backup. A safety backup of the current data is taken first, so you can go back.":
    "Данные в {name} будут заменены этой копией. Сначала создаётся страховочная копия текущих данных, чтобы можно было вернуться.",
  "Delete backup": "Удалить копию",
  "Delete this backup?": "Удалить эту резервную копию?",
  "The file is removed from the server.": "Файл будет удалён с сервера.",
  "This engine's backups can be downloaded but not restored from PortDock: restore them with the engine's own tools.":
    "Копии этого типа базы данных можно скачать, но не восстановить из PortDock: восстановите их средствами самой базы данных.",
  "Backups on a schedule, each keeping its last N files, with optional S3 upload.": "Резервные копии по расписанию: каждое расписание хранит последние N файлов, с загрузкой в S3 по желанию.",
  "Add a schedule to back up this database automatically.": "Добавьте расписание, чтобы копировать эту базу данных автоматически.",
  "{n} database backup queued|{n} database backups queued": "{n} резервная копия поставлена в очередь|{n} резервные копии поставлены в очередь|{n} резервных копий поставлено в очередь",
  "Follow them in the Backups tab of each database.": "Следите за ними на вкладке «Резервные копии» каждой базы данных.",
  "Back up all databases": "Копии всех баз данных",
  "Queues a backup of every database now. Each is stored on its own server and can be downloaded from the database's Backups tab.":
    "Ставит в очередь копию каждой базы данных. Каждая хранится на своём сервере и скачивается на вкладке «Резервные копии» базы данных.",
  "Back up all now": "Копировать все сейчас",
  // Job progress and fixed server messages
  "Taking a safety backup of the current data…": "Создаётся страховочная копия текущих данных…",
  "the database is not running; start it to back it up": "база данных не запущена; запустите её, чтобы создать копию",
  "this backup is already being restored": "эта копия уже восстанавливается",
  "this backup is in use; wait until it finishes": "эта копия используется; дождитесь завершения",
  "the backup is empty": "резервная копия пуста",
  "the SQL Server database name may only contain letters, digits and underscores": "имя базы данных SQL Server может содержать только буквы, цифры и подчёркивания",
  "SQL Server needs a password of at least 8 characters with three of: uppercase, lowercase, digits, symbols":
    "SQL Server требует пароль не короче 8 символов, содержащий три типа из: заглавные, строчные, цифры, символы",
  "interrupted: PortDock restarted": "прервано: PortDock перезапущен",
  "interrupted: PortDock restarted; check the database and restore again": "прервано: PortDock перезапущен; проверьте базу данных и повторите восстановление",
};
