// Translations for domains per Docker Compose service (Domains.tsx, AppForm.tsx).
export const ro: Record<string, string> = {
  "The service of docker-compose.yml that receives this domain's traffic, e.g. the frontend or the backend.": "Serviciul din docker-compose.yml care primește traficul acestui domeniu, de exemplu frontend-ul sau backend-ul.",
  "Main service ({name})": "Serviciul principal ({name})",
  "Main service": "Serviciul principal",
  "Container port that receives the traffic. Required: the service declares no single port.": "Portul containerului care primește traficul. Obligatoriu: serviciul nu declară un singur port.",
  "Domains per service": "Domenii pe servicii",
  "Give each public service of docker-compose.yml its own domain, e.g. the frontend on app.example.com and the backend on api.example.com. Leave databases and workers empty.": "Dă fiecărui serviciu public din docker-compose.yml propriul domeniu, de exemplu frontend-ul pe app.example.com și backend-ul pe api.example.com. Lasă goale bazele de date și workerii.",
  "Domain of {service}": "Domeniul pentru {service}",
  "Port of {service}": "Portul pentru {service}",
};

export const ru: Record<string, string> = {
  "The service of docker-compose.yml that receives this domain's traffic, e.g. the frontend or the backend.": "Сервис из docker-compose.yml, который получает трафик этого домена, например фронтенд или бэкенд.",
  "Main service ({name})": "Основной сервис ({name})",
  "Main service": "Основной сервис",
  "Container port that receives the traffic. Required: the service declares no single port.": "Порт контейнера, который получает трафик. Обязателен: сервис не объявляет единственный порт.",
  "Domains per service": "Домены по сервисам",
  "Give each public service of docker-compose.yml its own domain, e.g. the frontend on app.example.com and the backend on api.example.com. Leave databases and workers empty.": "Назначьте каждому публичному сервису из docker-compose.yml свой домен, например фронтенд на app.example.com, а бэкенд на api.example.com. Для баз данных и воркеров оставьте поле пустым.",
  "Domain of {service}": "Домен для {service}",
  "Port of {service}": "Порт для {service}",
};
