// Translations for the image signature check (components/updates.tsx).
export const ro: Record<string, string> = {
  "Image signature": "Semnătura imaginii",
  "Before installing, cosign checks that the image was signed by the release workflow of the source repository; an unsigned or foreign image is not installed.":
    "Înainte de instalare, cosign verifică dacă imaginea a fost semnată de workflow-ul de release al repository-ului sursă; o imagine nesemnată sau străină nu se instalează.",
  "The signature is checked, but an update proceeds when it cannot be verified (for networks without access to Sigstore). The update log says so.":
    "Semnătura se verifică, dar actualizarea continuă când nu poate fi verificată (pentru rețele fără acces la Sigstore). Jurnalul actualizării o menționează.",
  "The signature is not checked. Use only with a trusted private mirror.": "Semnătura nu se verifică. Folosește doar cu un mirror privat de încredere.",
  "Required (recommended)": "Obligatorie (recomandat)",
  "Check, but install anyway": "Verifică, dar instalează oricum",
  "verify must be required, warn or off": "verificarea trebuie să fie required, warn sau off",
};

export const ru: Record<string, string> = {
  "Image signature": "Подпись образа",
  "Before installing, cosign checks that the image was signed by the release workflow of the source repository; an unsigned or foreign image is not installed.":
    "Перед установкой cosign проверяет, что образ подписан release-процессом исходного репозитория; неподписанный или чужой образ не устанавливается.",
  "The signature is checked, but an update proceeds when it cannot be verified (for networks without access to Sigstore). The update log says so.":
    "Подпись проверяется, но обновление продолжается, если её нельзя проверить (для сетей без доступа к Sigstore). Журнал обновления это отмечает.",
  "The signature is not checked. Use only with a trusted private mirror.": "Подпись не проверяется. Используйте только с доверенным частным зеркалом.",
  "Required (recommended)": "Обязательна (рекомендуется)",
  "Check, but install anyway": "Проверять, но устанавливать в любом случае",
  "verify must be required, warn or off": "проверка должна быть required, warn или off",
};
