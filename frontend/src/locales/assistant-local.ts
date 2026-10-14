// Translations for the self-hosted model provider of the AI assistant (Settings → AI assistant).
export const ro: Record<string, string> = {
  "Answers questions about PortDock, deployments, Docker and configuration with a model of your choice, on OpenRouter or on a server of your own. It only sees what users write in the chat.":
    "Răspunde la întrebări despre PortDock, deploy-uri, Docker și configurare cu un model ales de tine, pe OpenRouter sau pe un server propriu. Vede doar ce scriu utilizatorii în chat.",
  "Model provider": "Furnizorul modelului",
  "Your own server (OpenAI-compatible)": "Server propriu (compatibil OpenAI)",
  "Any OpenAI-compatible server: Ollama, vLLM, LiteLLM, LocalAI or llama.cpp, running in your network.":
    "Orice server compatibil OpenAI: Ollama, vLLM, LiteLLM, LocalAI sau llama.cpp, care rulează în rețeaua voastră.",
  "Server address": "Adresa serverului",
  "The OpenAI-compatible API base, reachable from PortDock: Ollama http://ollama:11434/v1, vLLM http://vllm:8000/v1, LiteLLM http://litellm:4000/v1.":
    "Baza API-ului compatibil OpenAI, accesibilă din PortDock: Ollama http://ollama:11434/v1, vLLM http://vllm:8000/v1, LiteLLM http://litellm:4000/v1.",
  "API key (optional)": "Cheie API (opțională)",
  "Only if your server asks for one (vLLM --api-key, LiteLLM master key).": "Doar dacă serverul cere una (vLLM --api-key, cheia master LiteLLM).",
  "Enter the address of your AI server below.": "Introdu mai jos adresa serverului AI.",
  "Messages go only to your AI server: nothing leaves your network unless that server forwards it.":
    "Mesajele ajung doar la serverul vostru AI: nimic nu iese din rețea dacă serverul nu le trimite mai departe.",
  "Connected — the model replied: {reply}": "Conectat — modelul a răspuns: {reply}",
  "Remove the API key?": "Ștergi cheia API?",
};

export const ru: Record<string, string> = {
  "Answers questions about PortDock, deployments, Docker and configuration with a model of your choice, on OpenRouter or on a server of your own. It only sees what users write in the chat.":
    "Отвечает на вопросы о PortDock, деплоях, Docker и настройке с помощью выбранной модели — в OpenRouter или на собственном сервере. Видит только то, что пользователи пишут в чате.",
  "Model provider": "Поставщик модели",
  "Your own server (OpenAI-compatible)": "Собственный сервер (совместимый с OpenAI)",
  "Any OpenAI-compatible server: Ollama, vLLM, LiteLLM, LocalAI or llama.cpp, running in your network.":
    "Любой сервер, совместимый с OpenAI: Ollama, vLLM, LiteLLM, LocalAI или llama.cpp в вашей сети.",
  "Server address": "Адрес сервера",
  "The OpenAI-compatible API base, reachable from PortDock: Ollama http://ollama:11434/v1, vLLM http://vllm:8000/v1, LiteLLM http://litellm:4000/v1.":
    "Базовый адрес API, совместимого с OpenAI, доступный из PortDock: Ollama http://ollama:11434/v1, vLLM http://vllm:8000/v1, LiteLLM http://litellm:4000/v1.",
  "API key (optional)": "API-ключ (необязательно)",
  "Only if your server asks for one (vLLM --api-key, LiteLLM master key).": "Только если сервер его требует (vLLM --api-key, мастер-ключ LiteLLM).",
  "Enter the address of your AI server below.": "Укажите ниже адрес вашего AI-сервера.",
  "Messages go only to your AI server: nothing leaves your network unless that server forwards it.":
    "Сообщения идут только на ваш AI-сервер: ничего не покидает сеть, если сервер сам их не пересылает.",
  "Connected — the model replied: {reply}": "Подключено — модель ответила: {reply}",
  "Remove the API key?": "Удалить API-ключ?",
};
