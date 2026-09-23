// Translations for the database "Public access" card (pages/Database.tsx) and the
// related fixed API errors (backend/internal/api/databases.go).
export const ro: Record<string, string> = {
  "Public access saved — the container was recreated": "Accesul public a fost salvat — containerul a fost recreat",
  "Public access saved — applied on the next start": "Accesul public a fost salvat — se aplică la următoarea pornire",
  "Public access saved": "Accesul public a fost salvat",
  "Connect to the database from outside the server, e.g. from your computer or another cloud.": "Conectați-vă la baza de date din afara serverului, de exemplu de pe calculatorul dvs. sau din alt cloud.",
  "Accessible from the internet": "Accesibilă din internet",
  "Publishes the database port on the server. Off: only applications on the internal network can connect.": "Publică portul bazei de date pe server. Dezactivat: doar aplicațiile din rețeaua internă se pot conecta.",
  "Default: {port}": "Implicit: {port}",
  "Domain (optional)": "Domeniu (opțional)",
  "Create a DNS A record pointing to the server; connections use this domain.": "Creați o înregistrare DNS A care să indice spre server; conexiunile folosesc acest domeniu.",
  "Checking DNS…": "Se verifică DNS…",
  "Database connections are not HTTP: turn off the Cloudflare proxy (grey cloud) for this record.": "Conexiunile la baza de date nu sunt HTTP: dezactivați proxy-ul Cloudflare (norul gri) pentru această înregistrare.",
  "Connect from a terminal": "Conectare din terminal",
  "The server's public address is unknown. Set a domain above or the public IP in Settings.": "Adresa publică a serverului nu este cunoscută. Setați un domeniu mai sus sau IP-ul public în Setări.",
  "Anyone on the internet can try to connect to this port. Use a strong password and restrict access with a firewall (allow only trusted IP addresses).":
    "Oricine din internet poate încerca să se conecteze la acest port. Folosiți o parolă puternică și restricționați accesul cu un firewall (permiteți doar adresele IP de încredere).",
  "Recreate the container now to apply image and resource changes? Data is kept in the volume.": "Recreați containerul acum pentru a aplica modificările de imagine și resurse? Datele sunt păstrate în volum.",
  // api errors
  "invalid public host: use a domain such as db.example.com": "host public invalid: folosiți un domeniu precum db.example.com",
};

export const ru: Record<string, string> = {
  "Public access saved — the container was recreated": "Публичный доступ сохранён — контейнер пересоздан",
  "Public access saved — applied on the next start": "Публичный доступ сохранён — изменения вступят в силу при следующем запуске",
  "Public access saved": "Публичный доступ сохранён",
  "Connect to the database from outside the server, e.g. from your computer or another cloud.": "Подключайтесь к базе данных извне сервера, например с вашего компьютера или из другого облака.",
  "Accessible from the internet": "Доступна из интернета",
  "Publishes the database port on the server. Off: only applications on the internal network can connect.": "Публикует порт базы данных на сервере. Если выключено, подключаться могут только приложения из внутренней сети.",
  "Default: {port}": "По умолчанию: {port}",
  "Domain (optional)": "Домен (необязательно)",
  "Create a DNS A record pointing to the server; connections use this domain.": "Создайте DNS-запись A, указывающую на сервер; подключения будут использовать этот домен.",
  "Checking DNS…": "Проверка DNS…",
  "Database connections are not HTTP: turn off the Cloudflare proxy (grey cloud) for this record.": "Подключения к базе данных не используют HTTP: отключите прокси Cloudflare (серое облако) для этой записи.",
  "Connect from a terminal": "Подключение из терминала",
  "The server's public address is unknown. Set a domain above or the public IP in Settings.": "Публичный адрес сервера неизвестен. Укажите домен выше или публичный IP в настройках.",
  "Anyone on the internet can try to connect to this port. Use a strong password and restrict access with a firewall (allow only trusted IP addresses).":
    "Любой пользователь интернета может попытаться подключиться к этому порту. Используйте надёжный пароль и ограничьте доступ с помощью файрвола (разрешите только доверенные IP-адреса).",
  "Recreate the container now to apply image and resource changes? Data is kept in the volume.": "Пересоздать контейнер сейчас, чтобы применить изменения образа и ресурсов? Данные сохраняются в томе.",
  // api errors
  "invalid public host: use a domain such as db.example.com": "недопустимый публичный хост: укажите домен, например db.example.com",
};
