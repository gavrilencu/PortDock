// Translations for the Versions tab (components/versions.tsx) and version deployments.
export const ro: Record<string, string> = {
  "Deploying {tag}": "Se face deploy la {tag}",
  "It waits for a maintainer's approval.": "Așteaptă aprobarea unui maintainer.",
  "It replaces the live version as soon as it is healthy.": "Înlocuiește versiunea live imediat ce este sănătoasă.",
  "Deploy {tag}?": "Faci deploy la {tag}?",
  "{tag} replaces {live} once it is healthy. The current environment variables are kept and nothing is rebuilt.":
    "{tag} înlocuiește {live} după ce devine sănătoasă. Variabilele de mediu actuale se păstrează și nu se reconstruiește nimic.",
  "{tag} is started with the current environment variables; nothing is rebuilt.": "{tag} pornește cu variabilele de mediu actuale; nu se reconstruiește nimic.",
  "Deploy {tag}": "Deploy {tag}",
  Versions: "Versiuni",
  "Every successful production deployment is saved as a version. The live one and the last {kept} are kept for {days} days. Drag a version onto Live — or press Deploy — to switch; environment variables stay as they are.":
    "Fiecare deploy reușit în producție se salvează ca versiune. Cea live și ultimele {kept} se păstrează {days} zile. Trage o versiune peste Live — sau apasă Deploy — ca s-o schimbi; variabilele de mediu rămân neschimbate.",
  "Roll back to {tag}": "Revino la {tag}",
  "No versions yet": "Încă nu există versiuni",
  "The next successful production deployment is saved as v1.": "Următorul deploy reușit în producție se salvează ca v1.",
  "No saved version is live. Deploy one to start.": "Nicio versiune salvată nu este live. Fă deploy la una ca să începi.",
  "{tag} is waiting for approval": "{tag} așteaptă aprobarea",
  "Switching to {tag}…": "Se trece la {tag}…",
  "A deployment is in progress…": "Un deploy este în curs…",
  "Drop a version here to deploy it": "Trage o versiune aici ca să-i faci deploy",
  "You can't deploy this application": "Nu poți face deploy acestei aplicații",
  "No commit message": "Fără mesaj de commit",
  "created {time}": "creată {time}",
  "kept {n} more day|kept {n} more days": "păstrată încă {n} zi|păstrată încă {n} zile|păstrată încă {n} de zile",
  "Triggered by deploying a saved version": "Pornit prin deploy-ul unei versiuni salvate",
};

export const ru: Record<string, string> = {
  "Deploying {tag}": "Развёртывание {tag}",
  "It waits for a maintainer's approval.": "Ожидает согласования мейнтейнера.",
  "It replaces the live version as soon as it is healthy.": "Заменит текущую версию, как только станет работоспособной.",
  "Deploy {tag}?": "Развернуть {tag}?",
  "{tag} replaces {live} once it is healthy. The current environment variables are kept and nothing is rebuilt.":
    "{tag} заменит {live}, когда станет работоспособной. Текущие переменные окружения сохраняются, ничего не пересобирается.",
  "{tag} is started with the current environment variables; nothing is rebuilt.": "{tag} запускается с текущими переменными окружения; ничего не пересобирается.",
  "Deploy {tag}": "Развернуть {tag}",
  Versions: "Версии",
  "Every successful production deployment is saved as a version. The live one and the last {kept} are kept for {days} days. Drag a version onto Live — or press Deploy — to switch; environment variables stay as they are.":
    "Каждый успешный деплой в продакшен сохраняется как версия. Текущая и последние {kept} хранятся {days} дней. Перетащите версию на Live — или нажмите «Развернуть», чтобы переключиться; переменные окружения не меняются.",
  "Roll back to {tag}": "Откатить на {tag}",
  "No versions yet": "Версий пока нет",
  "The next successful production deployment is saved as v1.": "Следующий успешный деплой в продакшен будет сохранён как v1.",
  "No saved version is live. Deploy one to start.": "Ни одна сохранённая версия не запущена. Разверните любую, чтобы начать.",
  "{tag} is waiting for approval": "{tag} ожидает согласования",
  "Switching to {tag}…": "Переключение на {tag}…",
  "A deployment is in progress…": "Идёт деплой…",
  "Drop a version here to deploy it": "Перетащите сюда версию, чтобы развернуть её",
  "You can't deploy this application": "У вас нет прав на деплой этого приложения",
  "No commit message": "Без сообщения коммита",
  "created {time}": "создана {time}",
  "kept {n} more day|kept {n} more days": "хранится ещё {n} день|хранится ещё {n} дня|хранится ещё {n} дней",
  "Triggered by deploying a saved version": "Запущено развёртыванием сохранённой версии",
};
