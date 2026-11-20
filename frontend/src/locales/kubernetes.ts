// Translations for Kubernetes clusters as servers: pages/Servers.tsx and pages/Server.tsx.
export const ro: Record<string, string> = {
  "Compute capacity available to your organization. Connect any Linux host over SSH, or a Kubernetes cluster.":
    "Capacitatea de calcul disponibilă organizației tale. Conectează orice gazdă Linux prin SSH sau un cluster Kubernetes.",
  "PortDock deploys applications to one namespace of the cluster, with the credentials of the kubeconfig.":
    "PortDock rulează aplicațiile într-un namespace al clusterului, cu credențialele din kubeconfig.",
  "Run this where kubectl reaches the cluster: it creates the namespace and a service account limited to it, and prints the kubeconfig to paste below.":
    "Rulează comanda acolo unde kubectl ajunge la cluster: creează namespace-ul și un service account limitat la el și afișează kubeconfig-ul de lipit mai jos.",
  "Linux server": "Server Linux",
  "Kubernetes cluster": "Cluster Kubernetes",
  "Docker, over SSH": "Docker, prin SSH",
  "One namespace, with a kubeconfig": "Un namespace, cu un kubeconfig",
  "Created if it does not exist. Everything PortDock deploys stays in it.": "Este creat dacă nu există. Tot ce rulează PortDock rămâne în el.",
  "With an embedded token or certificate (a service account is best). Files of cloud providers that run a program to sign in are refused. Stored encrypted.":
    "Cu token sau certificat inclus în fișier (cel mai bine, un service account). Fișierele furnizorilor de cloud care rulează un program pentru autentificare sunt refuzate. Se păstrează criptat.",
  "Optional: apps get <name>.<domain> automatically. Point *.domain to the cluster's ingress.":
    "Opțional: aplicațiile primesc automat <name>.<domain>. Direcționează *.domain către ingress-ul clusterului.",
  "Images are built on a Docker server and pushed to the application's registry, where the cluster pulls them. Databases, one-click services and Docker Compose applications stay on Docker servers.":
    "Imaginile sunt construite pe un server Docker și urcate în registry-ul aplicației, de unde le ia clusterul. Bazele de date, serviciile cu un click și aplicațiile Docker Compose rămân pe servere Docker.",
  "Ingress class": "Clasă de ingress",
  "Empty: the cluster's default (nginx, traefik…).": "Gol: cea implicită a clusterului (nginx, traefik…).",
  "Certificate issuer": "Emitent de certificate",
  "A cert-manager ClusterIssuer, for a certificate per HTTPS domain.": "Un ClusterIssuer din cert-manager, pentru câte un certificat pe fiecare domeniu HTTPS.",
  "Storage class": "Clasă de stocare",
  "Empty: the cluster's default.": "Gol: cea implicită a clusterului.",
  "Volume size": "Dimensiunea volumelor",
  "Size of each new application volume (default 5Gi).": "Dimensiunea fiecărui volum nou al unei aplicații (implicit 5Gi).",
  "Kubernetes cluster {host}, namespace {namespace}": "Cluster Kubernetes {host}, namespace {namespace}",
  Pods: "Poduri",
  "Pods ({n})": "Poduri ({n})",
  "running, in the namespace": "pornite, în namespace",
  "cores on the cluster's nodes": "nuclee pe nodurile clusterului",
  "on the cluster's nodes": "pe nodurile clusterului",
  "the credentials cannot list the nodes": "credențialele nu pot lista nodurile",
  Cluster: "Cluster",
  "API server": "Server API",
  Nodes: "Noduri",
  "Domains and storage": "Domenii și stocare",
  "The cluster's ingress controller routes the domains; cert-manager issues their certificates.":
    "Controllerul de ingress al clusterului rutează domeniile; cert-manager le emite certificatele.",
  "the cluster's default": "cea implicită a clusterului",
  "none: the ingress controller's certificate": "niciunul: certificatul controllerului de ingress",
  "Cluster check": "Verificarea clusterului",
  "Checks the connection, the namespace and its permissions, the ingress controller, cert-manager and the metrics API. Nothing is installed on the cluster.":
    "Verifică legătura, namespace-ul și drepturile în el, controllerul de ingress, cert-manager și API-ul de metrici. Nu se instalează nimic pe cluster.",
  "Check the cluster": "Verifică clusterul",
  "Can only change while no application runs on this server.": "Se poate schimba doar cât timp nicio aplicație nu rulează pe acest server.",
  "Stored encrypted and never shown. Paste a new one to replace it.": "Se păstrează criptat și nu este afișat niciodată. Lipește unul nou ca să îl înlocuiești.",
  "PortDock runs in this cluster and uses its service account. Paste a kubeconfig to use other credentials.":
    "PortDock rulează în acest cluster și folosește service account-ul lui. Lipește un kubeconfig ca să folosești alte credențiale.",
  "Remove the cluster from PortDock. What runs in its namespace is not touched.": "Scoate clusterul din PortDock. Ce rulează în namespace-ul lui nu este atins.",
  // The registry of a cluster: pages/Servers.tsx, pages/Server.tsx, pages/Application.tsx.
  "Registry of the cluster": "Registry-ul clusterului",
  "Applications built from Git without a registry of their own push their images here, and the cluster pulls them from it.":
    "Aplicațiile construite din Git care nu au un registry propriu își urcă imaginile aici, iar clusterul le ia de aici.",
  "None: each application chooses one": "Niciunul: fiecare aplicație își alege unul",
  "none: each application chooses one": "niciunul: fiecare aplicație își alege unul",
  "The registry of the cluster ({name})": "Registry-ul clusterului ({name})",
  "Built images are pushed to the registry of the cluster, where the cluster pulls them.":
    "Imaginile construite sunt urcate în registry-ul clusterului, de unde le ia clusterul.",
};

export const ru: Record<string, string> = {
  "Compute capacity available to your organization. Connect any Linux host over SSH, or a Kubernetes cluster.":
    "Вычислительные мощности вашей организации. Подключите любой Linux-сервер по SSH или кластер Kubernetes.",
  "PortDock deploys applications to one namespace of the cluster, with the credentials of the kubeconfig.":
    "PortDock развёртывает приложения в одном пространстве имён кластера, с учётными данными из kubeconfig.",
  "Run this where kubectl reaches the cluster: it creates the namespace and a service account limited to it, and prints the kubeconfig to paste below.":
    "Выполните команду там, где kubectl подключён к кластеру: она создаст пространство имён и сервисный аккаунт, ограниченный им, и выведет kubeconfig, который нужно вставить ниже.",
  "Linux server": "Сервер Linux",
  "Kubernetes cluster": "Кластер Kubernetes",
  "Docker, over SSH": "Docker, по SSH",
  "One namespace, with a kubeconfig": "Одно пространство имён, с kubeconfig",
  "Created if it does not exist. Everything PortDock deploys stays in it.": "Создаётся, если его нет. Всё, что развёртывает PortDock, остаётся в нём.",
  "With an embedded token or certificate (a service account is best). Files of cloud providers that run a program to sign in are refused. Stored encrypted.":
    "Со встроенным токеном или сертификатом (лучше всего — сервисный аккаунт). Файлы облачных провайдеров, запускающие программу для входа, отклоняются. Хранится в зашифрованном виде.",
  "Optional: apps get <name>.<domain> automatically. Point *.domain to the cluster's ingress.":
    "Необязательно: приложения автоматически получают <name>.<domain>. Направьте *.domain на ingress кластера.",
  "Images are built on a Docker server and pushed to the application's registry, where the cluster pulls them. Databases, one-click services and Docker Compose applications stay on Docker servers.":
    "Образы собираются на сервере Docker и отправляются в реестр приложения, откуда их загружает кластер. Базы данных, сервисы в один клик и приложения Docker Compose остаются на серверах Docker.",
  "Ingress class": "Класс ingress",
  "Empty: the cluster's default (nginx, traefik…).": "Пусто: класс кластера по умолчанию (nginx, traefik…).",
  "Certificate issuer": "Издатель сертификатов",
  "A cert-manager ClusterIssuer, for a certificate per HTTPS domain.": "ClusterIssuer из cert-manager: сертификат для каждого HTTPS-домена.",
  "Storage class": "Класс хранилища",
  "Empty: the cluster's default.": "Пусто: класс кластера по умолчанию.",
  "Volume size": "Размер томов",
  "Size of each new application volume (default 5Gi).": "Размер каждого нового тома приложения (по умолчанию 5Gi).",
  "Kubernetes cluster {host}, namespace {namespace}": "Кластер Kubernetes {host}, пространство имён {namespace}",
  Pods: "Поды",
  "Pods ({n})": "Поды ({n})",
  "running, in the namespace": "запущено, в пространстве имён",
  "cores on the cluster's nodes": "ядер на узлах кластера",
  "on the cluster's nodes": "на узлах кластера",
  "the credentials cannot list the nodes": "учётные данные не позволяют получить список узлов",
  Cluster: "Кластер",
  "API server": "API-сервер",
  Nodes: "Узлы",
  "Domains and storage": "Домены и хранилище",
  "The cluster's ingress controller routes the domains; cert-manager issues their certificates.":
    "Ingress-контроллер кластера маршрутизирует домены; cert-manager выпускает для них сертификаты.",
  "the cluster's default": "по умолчанию для кластера",
  "none: the ingress controller's certificate": "нет: сертификат ingress-контроллера",
  "Cluster check": "Проверка кластера",
  "Checks the connection, the namespace and its permissions, the ingress controller, cert-manager and the metrics API. Nothing is installed on the cluster.":
    "Проверяет подключение, пространство имён и права в нём, ingress-контроллер, cert-manager и API метрик. В кластер ничего не устанавливается.",
  "Check the cluster": "Проверить кластер",
  "Can only change while no application runs on this server.": "Можно изменить, только пока на этом сервере не работает ни одно приложение.",
  "Stored encrypted and never shown. Paste a new one to replace it.": "Хранится в зашифрованном виде и никогда не показывается. Вставьте новый, чтобы заменить.",
  "PortDock runs in this cluster and uses its service account. Paste a kubeconfig to use other credentials.":
    "PortDock работает в этом кластере и использует его сервисный аккаунт. Вставьте kubeconfig, чтобы использовать другие учётные данные.",
  "Remove the cluster from PortDock. What runs in its namespace is not touched.": "Удалить кластер из PortDock. То, что работает в его пространстве имён, не затрагивается.",
  "Registry of the cluster": "Реестр кластера",
  "Applications built from Git without a registry of their own push their images here, and the cluster pulls them from it.":
    "Приложения, собираемые из Git без собственного реестра, загружают сюда свои образы, а кластер берёт их отсюда.",
  "None: each application chooses one": "Нет: каждое приложение выбирает свой",
  "none: each application chooses one": "нет: каждое приложение выбирает свой",
  "The registry of the cluster ({name})": "Реестр кластера ({name})",
  "Built images are pushed to the registry of the cluster, where the cluster pulls them.":
    "Собранные образы загружаются в реестр кластера, откуда кластер их берёт.",
};
