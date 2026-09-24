// Translations for the SQL console (components/sqlconsole.tsx) and the fixed
// messages of internal/sqlconsole, deploy/sqlconsole.go and api/dbconsole.go.
export const ro: Record<string, string> = {
  "SQL console": "Consolă SQL",
  Tables: "Tabele",
  Refresh: "Reîncarcă",
  "Filter…": "Filtrează…",
  "No tables match.": "Niciun tabel nu corespunde.",
  "No tables yet.": "Încă nu există tabele.",
  "Show the first 100 rows": "Arată primele 100 de rânduri",
  "Read-only": "Doar citire",
  "read-only": "doar citire",
  "Writes allowed": "Scriere permisă",
  "Allow writes": "Permite scrierea",
  "Ctrl + Enter runs the script, or only the selected text.": "Ctrl + Enter rulează scriptul sau doar textul selectat.",
  Run: "Rulează",
  "Statements change the data directly, without a transaction to undo them. Every query is recorded in the audit log.":
    "Instrucțiunile modifică datele direct, fără o tranzacție care să le anuleze. Fiecare interogare este înregistrată în audit log.",
  "You have read-only access: {kinds} statements run in a session that cannot change data. Organization admins can allow writes.":
    "Ai acces doar de citire: instrucțiunile {kinds} rulează într-o sesiune care nu poate modifica datele. Administratorii organizației pot permite scrierea.",
  "{n} statement ran in {ms} ms|{n} statements ran in {ms} ms": "{n} instrucțiune a rulat în {ms} ms|{n} instrucțiuni au rulat în {ms} ms|{n} de instrucțiuni au rulat în {ms} ms",
  "Statement {n} failed; the next ones did not run": "Instrucțiunea {n} a eșuat; următoarele nu au rulat",
  "The query failed": "Interogarea a eșuat",
  "The query was not run": "Interogarea nu a fost rulată",
  "The output was too large; later results are missing. Add a LIMIT.": "Rezultatul a fost prea mare; ultimele rezultate lipsesc. Adaugă un LIMIT.",
  "{n} row affected|{n} rows affected": "{n} rând modificat|{n} rânduri modificate|{n} de rânduri modificate",
  "{n} row|{n} rows": "{n} rând|{n} rânduri|{n} de rânduri",
  "first {n} shown": "se afișează primele {n}",
  "Show all {n} rows": "Arată toate cele {n} rânduri",
  "The SQL console is not available for this engine": "Consola SQL nu este disponibilă pentru acest tip de bază de date",
  "Use the Terminal tab to open the database's own client.": "Folosește tab-ul Terminal pentru a deschide clientul propriu al bazei de date.",
  "Start it to run queries.": "Pornește-o ca să rulezi interogări.",
  // Fixed server messages
  "client commands (backslash commands, system, source…) are not allowed; send SQL only":
    "comenzile clientului (comenzi cu backslash, system, source…) nu sunt permise; trimite doar SQL",
  "enter a statement to run": "introdu o instrucțiune de rulat",
  "read-only access: only reading statements (SELECT, SHOW, EXPLAIN…) can run": "acces doar de citire: pot rula doar instrucțiuni de citire (SELECT, SHOW, EXPLAIN…)",
  "read-only access: the statement uses a function or clause that can write or read outside the database":
    "acces doar de citire: instrucțiunea folosește o funcție sau o clauză care poate scrie sau citi în afara bazei de date",
  "the SQL console is not available for this database engine": "consola SQL nu este disponibilă pentru acest tip de bază de date",
  "run at most 100 statements at a time": "rulează cel mult 100 de instrucțiuni odată",
  "the database is not running; start it to run queries": "baza de date nu rulează; pornește-o ca să rulezi interogări",
  "the script is too long": "scriptul este prea lung",
  "only organization admins with write access to the database can run statements that write":
    "doar administratorii organizației cu drept de scriere pe baza de date pot rula instrucțiuni care scriu",
};

export const ru: Record<string, string> = {
  "SQL console": "SQL-консоль",
  Tables: "Таблицы",
  Refresh: "Обновить",
  "Filter…": "Фильтр…",
  "No tables match.": "Таблицы не найдены.",
  "No tables yet.": "Таблиц пока нет.",
  "Show the first 100 rows": "Показать первые 100 строк",
  "Read-only": "Только чтение",
  "read-only": "только чтение",
  "Writes allowed": "Запись разрешена",
  "Allow writes": "Разрешить запись",
  "Ctrl + Enter runs the script, or only the selected text.": "Ctrl + Enter выполняет скрипт или только выделенный текст.",
  Run: "Выполнить",
  "Statements change the data directly, without a transaction to undo them. Every query is recorded in the audit log.":
    "Запросы меняют данные напрямую, без транзакции для отката. Каждый запрос записывается в журнал аудита.",
  "You have read-only access: {kinds} statements run in a session that cannot change data. Organization admins can allow writes.":
    "У вас доступ только на чтение: запросы {kinds} выполняются в сеансе, который не может менять данные. Администраторы организации могут разрешить запись.",
  "{n} statement ran in {ms} ms|{n} statements ran in {ms} ms": "{n} запрос выполнен за {ms} мс|{n} запроса выполнено за {ms} мс|{n} запросов выполнено за {ms} мс",
  "Statement {n} failed; the next ones did not run": "Запрос {n} завершился ошибкой; следующие не выполнялись",
  "The query failed": "Запрос завершился ошибкой",
  "The query was not run": "Запрос не был выполнен",
  "The output was too large; later results are missing. Add a LIMIT.": "Результат слишком большой; последние результаты отсутствуют. Добавьте LIMIT.",
  "{n} row affected|{n} rows affected": "Изменена {n} строка|Изменено {n} строки|Изменено {n} строк",
  "{n} row|{n} rows": "{n} строка|{n} строки|{n} строк",
  "first {n} shown": "показаны первые {n}",
  "Show all {n} rows": "Показать все строки ({n})",
  "The SQL console is not available for this engine": "SQL-консоль недоступна для этого типа базы данных",
  "Use the Terminal tab to open the database's own client.": "Откройте собственный клиент базы данных на вкладке «Терминал».",
  "Start it to run queries.": "Запустите её, чтобы выполнять запросы.",
  // Fixed server messages
  "client commands (backslash commands, system, source…) are not allowed; send SQL only":
    "команды клиента (с обратной косой чертой, system, source…) запрещены; отправляйте только SQL",
  "enter a statement to run": "введите запрос",
  "read-only access: only reading statements (SELECT, SHOW, EXPLAIN…) can run": "доступ только на чтение: разрешены только запросы на чтение (SELECT, SHOW, EXPLAIN…)",
  "read-only access: the statement uses a function or clause that can write or read outside the database":
    "доступ только на чтение: запрос использует функцию или конструкцию, которая может писать или читать за пределами базы данных",
  "the SQL console is not available for this database engine": "SQL-консоль недоступна для этого типа базы данных",
  "run at most 100 statements at a time": "за раз можно выполнить не более 100 запросов",
  "the database is not running; start it to run queries": "база данных не запущена; запустите её, чтобы выполнять запросы",
  "the script is too long": "скрипт слишком длинный",
  "only organization admins with write access to the database can run statements that write":
    "запросы на запись могут выполнять только администраторы организации с правом записи в эту базу данных",
};
