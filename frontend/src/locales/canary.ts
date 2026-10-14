// Translations for components/canary.tsx (canary deployments).
export const ro: Record<string, string> = {
  "Deployment strategy": "Strategia de deploy",
  "How a new version takes over the traffic.": "Cum preia o versiune nouă traficul.",
  Strategy: "Strategie",
  "Canary: the new version first gets part of the traffic and is rolled back automatically if it answers with more server errors than allowed.":
    "Canary: versiunea nouă primește întâi o parte din trafic și e retrasă automat dacă răspunde cu mai multe erori de server decât e permis.",
  "All at once (after the health check)": "Tot odată (după health check)",
  "Canary: {n}% of the traffic first": "Canary: întâi {n}% din trafic",
  "Observation (minutes)": "Observare (minute)",
  "Then all the traffic moves to the new version.": "Apoi tot traficul trece pe versiunea nouă.",
  "Error threshold (%)": "Prag de erori (%)",
  "Share of 5xx responses of the new version that rolls it back (when also clearly above the current version's).":
    "Procentul de răspunsuri 5xx ale versiunii noi la care e retrasă (când e și clar peste cel al versiunii curente).",
  "Canary: {n}% of the traffic": "Canary: {n}% din trafic",
  "Rolled back": "Retrasă",
  Promoted: "Promovată",
  Observing: "În observare",
  "{time} left": "mai sunt {time}",
  "New version: {requests} responses, {rate}% server errors · current version: {stable}% · threshold {max}%":
    "Versiunea nouă: {requests} răspunsuri, {rate}% erori de server · versiunea curentă: {stable}% · prag {max}%",
  "Promote now": "Promovează acum",
  "Promoting the new version": "Se promovează versiunea nouă",
  "Rolling the new version back": "Se retrage versiunea nouă",
};

export const ru: Record<string, string> = {
  "Deployment strategy": "Стратегия деплоя",
  "How a new version takes over the traffic.": "Как новая версия принимает трафик.",
  Strategy: "Стратегия",
  "Canary: the new version first gets part of the traffic and is rolled back automatically if it answers with more server errors than allowed.":
    "Canary: новая версия сначала получает часть трафика и автоматически откатывается, если отвечает с большим числом ошибок сервера, чем допустимо.",
  "All at once (after the health check)": "Сразу весь трафик (после проверки)",
  "Canary: {n}% of the traffic first": "Canary: сначала {n}% трафика",
  "Observation (minutes)": "Наблюдение (минуты)",
  "Then all the traffic moves to the new version.": "Затем весь трафик переходит на новую версию.",
  "Error threshold (%)": "Порог ошибок (%)",
  "Share of 5xx responses of the new version that rolls it back (when also clearly above the current version's).":
    "Доля ответов 5xx новой версии, при которой она откатывается (если она также заметно выше, чем у текущей версии).",
  "Canary: {n}% of the traffic": "Canary: {n}% трафика",
  "Rolled back": "Откачена",
  Promoted: "Повышена",
  Observing: "Наблюдение",
  "{time} left": "осталось {time}",
  "New version: {requests} responses, {rate}% server errors · current version: {stable}% · threshold {max}%":
    "Новая версия: {requests} ответов, {rate}% ошибок сервера · текущая версия: {stable}% · порог {max}%",
  "Promote now": "Повысить сейчас",
  "Promoting the new version": "Повышение новой версии",
  "Rolling the new version back": "Откат новой версии",
};
