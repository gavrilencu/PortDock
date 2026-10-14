// Translations for the test restores of database backups (pages/Database.tsx)
// and their progress messages (backend/internal/deploy/verify.go).
export const ro: Record<string, string> = {
  "Test restore": "Test de restaurare",
  "Test restores": "Teste de restaurare",
  "Test restore queued": "Test de restaurare pus în coadă",
  "The backup is restored into a temporary container; the database is not touched.": "Backup-ul se restaurează într-un container temporar; baza de date nu este atinsă.",
  "Test restore pending": "Test de restaurare în așteptare",
  "Testing…": "Se testează…",
  "Restore tested {time}": "Restaurare testată {time}",
  "Test restore failed": "Testul de restaurare a eșuat",
  "Restore into a temporary container and count what it holds": "Restaurează într-un container temporar și numără ce conține",
  "Restores the latest backup into a temporary container (no network, the database untouched), counts its tables and alerts when it fails.":
    "Restaurează ultimul backup într-un container temporar (fără rețea, baza de date rămâne neatinsă), îi numără tabelele și trimite o alertă dacă eșuează.",
  "Weekly, Sunday at 05:00": "Săptămânal, duminica la 05:00",
  "Daily at 05:00": "Zilnic la 05:00",
  "Custom schedule": "Program personalizat",
  "Test restore weekly": "Test săptămânal",
  "Test restore daily": "Test zilnic",
  "Starting a temporary container…": "Se pornește un container temporar…",
  "Restoring the backup into a temporary container…": "Se restaurează backup-ul într-un container temporar…",
  "Checking the restored data…": "Se verifică datele restaurate…",
  "interrupted: PortDock restarted": "întrerupt: PortDock a repornit",
};

export const ru: Record<string, string> = {
  "Test restore": "Тестовое восстановление",
  "Test restores": "Тестовые восстановления",
  "Test restore queued": "Тестовое восстановление в очереди",
  "The backup is restored into a temporary container; the database is not touched.": "Копия восстанавливается во временный контейнер; база данных не затрагивается.",
  "Test restore pending": "Тестовое восстановление ожидает",
  "Testing…": "Проверка…",
  "Restore tested {time}": "Восстановление проверено {time}",
  "Test restore failed": "Тестовое восстановление не удалось",
  "Restore into a temporary container and count what it holds": "Восстановить во временный контейнер и посчитать содержимое",
  "Restores the latest backup into a temporary container (no network, the database untouched), counts its tables and alerts when it fails.":
    "Восстанавливает последнюю копию во временный контейнер (без сети, база данных не затрагивается), считает таблицы и оповещает при ошибке.",
  "Weekly, Sunday at 05:00": "Еженедельно, в воскресенье в 05:00",
  "Daily at 05:00": "Ежедневно в 05:00",
  "Custom schedule": "Своё расписание",
  "Test restore weekly": "Проверка еженедельно",
  "Test restore daily": "Проверка ежедневно",
  "Starting a temporary container…": "Запуск временного контейнера…",
  "Restoring the backup into a temporary container…": "Восстановление копии во временный контейнер…",
  "Checking the restored data…": "Проверка восстановленных данных…",
  "interrupted: PortDock restarted": "прервано: PortDock перезапущен",
};
