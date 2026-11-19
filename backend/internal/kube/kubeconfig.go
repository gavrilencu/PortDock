package kube

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/store"
	"go.yaml.in/yaml/v3"
)

type kubeconfig struct {
	CurrentContext string `yaml:"current-context"`
	Clusters       []struct {
		Name    string `yaml:"name"`
		Cluster struct {
			Server string `yaml:"server"`
			CAFile string `yaml:"certificate-authority"`
		} `yaml:"cluster"`
	} `yaml:"clusters"`
	Contexts []struct {
		Name    string `yaml:"name"`
		Context struct {
			Cluster string `yaml:"cluster"`
			User    string `yaml:"user"`
		} `yaml:"context"`
	} `yaml:"contexts"`
	Users []struct {
		Name string `yaml:"name"`
		User struct {
			Exec         any    `yaml:"exec"`
			AuthProvider any    `yaml:"auth-provider"`
			TokenFile    string `yaml:"tokenFile"`
			CertFile     string `yaml:"client-certificate"`
			KeyFile      string `yaml:"client-key"`
		} `yaml:"user"`
	} `yaml:"users"`
}

// CheckKubeconfig validates a kubeconfig before it is stored and returns the
// address of its cluster.
//
// The file must stand on its own: a kubeconfig can name a program to run for
// credentials (`exec`, as cloud providers' files do) or files to read, and
// both would act on the machine PortDock runs on. Only embedded credentials
// are accepted: a service account token, or a client certificate as *-data.
func CheckKubeconfig(raw string) (server string, err error) {
	var doc any
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		return "", fmt.Errorf("the kubeconfig is not valid YAML: %w", err)
	}
	// Refused wherever they appear and however they are spelled: the check
	// must not depend on reading the file exactly as kubectl does (which
	// context it picks, how it matches the case of a key).
	switch findKey(doc, "exec", "auth-provider", "tokenfile", "client-certificate", "client-key", "certificate-authority") {
	case "exec", "auth-provider":
		return "", errors.New("the kubeconfig runs a program to sign in (exec or auth-provider), as the files of cloud providers do; use the token of a service account instead")
	case "tokenfile", "client-certificate", "client-key":
		return "", errors.New("the kubeconfig points to credential files (tokenFile, client-certificate, client-key): embed them as token, client-certificate-data and client-key-data")
	case "certificate-authority":
		return "", errors.New("the kubeconfig points to a certificate file (certificate-authority): embed it as certificate-authority-data")
	}
	var cfg kubeconfig
	if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
		return "", fmt.Errorf("the kubeconfig is not valid: %w", err)
	}
	if len(cfg.Clusters) == 0 || len(cfg.Users) == 0 || len(cfg.Contexts) == 0 {
		return "", errors.New("the kubeconfig needs a cluster, a user and a context")
	}
	ctxName := cfg.CurrentContext
	if ctxName == "" {
		if len(cfg.Contexts) != 1 {
			return "", errors.New("the kubeconfig has several contexts and no current-context")
		}
		ctxName = cfg.Contexts[0].Name
	}
	var clusterName, userName string
	found := false
	for _, c := range cfg.Contexts {
		if c.Name == ctxName {
			clusterName, userName, found = c.Context.Cluster, c.Context.User, true
		}
	}
	if !found {
		return "", fmt.Errorf("the kubeconfig has no context named %q", ctxName)
	}
	for _, c := range cfg.Clusters {
		if c.Name != clusterName {
			continue
		}
		if c.Cluster.CAFile != "" {
			return "", errors.New("the kubeconfig points to a certificate file (certificate-authority): embed it as certificate-authority-data")
		}
		server = c.Cluster.Server
	}
	if !strings.HasPrefix(server, "https://") && !strings.HasPrefix(server, "http://") {
		return "", fmt.Errorf("the cluster %q of the kubeconfig has no server address", clusterName)
	}
	for _, u := range cfg.Users {
		if u.Name != userName {
			continue
		}
		switch {
		case u.User.Exec != nil, u.User.AuthProvider != nil:
			return "", errors.New("the kubeconfig runs a program to sign in (exec or auth-provider), as the files of cloud providers do; use the token of a service account instead")
		case u.User.TokenFile != "", u.User.CertFile != "", u.User.KeyFile != "":
			return "", errors.New("the kubeconfig points to credential files (tokenFile, client-certificate, client-key): embed them as token, client-certificate-data and client-key-data")
		}
		return server, nil
	}
	return "", fmt.Errorf("the kubeconfig has no user named %q", userName)
}

// findKey returns the first of keys that some mapping of the document uses,
// compared without regard to case ("" when none does).
func findKey(v any, keys ...string) string {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			for _, want := range keys {
				if strings.EqualFold(strings.TrimSpace(k), want) {
					return want
				}
			}
			if found := findKey(child, keys...); found != "" {
				return found
			}
		}
	case map[any]any:
		for k, child := range t {
			if ks, ok := k.(string); ok {
				for _, want := range keys {
					if strings.EqualFold(strings.TrimSpace(ks), want) {
						return want
					}
				}
			}
			if found := findKey(child, keys...); found != "" {
				return found
			}
		}
	case []any:
		for _, child := range t {
			if found := findKey(child, keys...); found != "" {
				return found
			}
		}
	}
	return ""
}

var namespaceRe = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

// CheckNamespace validates a namespace name.
func CheckNamespace(ns string) error {
	if !namespaceRe.MatchString(ns) {
		return fmt.Errorf("invalid namespace %q: lowercase letters, digits and '-', at most 63 characters", ns)
	}
	switch ns {
	case "kube-system", "kube-public", "kube-node-lease":
		return fmt.Errorf("the namespace %q belongs to Kubernetes itself", ns)
	}
	return nil
}

// ConfigPath is where the kubeconfig of a server is written for kubectl.
func ConfigPath(dataDir string, serverID int64) string {
	return filepath.Join(dataDir, "kube", "server-"+strconv.FormatInt(serverID, 10)+".kubeconfig")
}

// WriteConfig stores the kubeconfig of a server, readable by PortDock only.
func WriteConfig(dataDir string, serverID int64, content string) (string, error) {
	file := ConfigPath(dataDir, serverID)
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return "", err
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
		return "", err
	}
	return file, os.Rename(tmp, file)
}

// RemoveConfig deletes the kubeconfig file of a deleted server.
func RemoveConfig(dataDir string, serverID int64) {
	_ = os.Remove(ConfigPath(dataDir, serverID))
}

// InCluster reports whether PortDock runs in a pod, where kubectl can use the
// pod's service account without a kubeconfig.
func InCluster() bool {
	return os.Getenv("KUBERNETES_SERVICE_HOST") != ""
}

// serviceAccountDir is where Kubernetes mounts the credentials of a pod.
var serviceAccountDir = "/var/run/secrets/kubernetes.io/serviceaccount"

// inClusterConfig is a kubeconfig for the service account of the pod PortDock
// runs in. kubectl can find these credentials by itself, but not every command
// does (`kubectl version` and `kubectl get --raw` look for a cluster on
// localhost:8080 instead), so it is always given this file. It points to the
// token file rather than holding the token, which Kubernetes rotates.
func inClusterConfig() (string, error) {
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return "", errors.New("PortDock does not run in a Kubernetes pod")
	}
	token, ca := serviceAccountDir+"/token", serviceAccountDir+"/ca.crt"
	if _, err := os.Stat(token); err != nil {
		return "", errors.New("the pod has no service account token mounted")
	}
	cfg := map[string]any{
		"apiVersion": "v1", "kind": "Config", "current-context": "in-cluster",
		"clusters": []any{map[string]any{"name": "in-cluster", "cluster": map[string]any{
			"server": "https://" + net.JoinHostPort(host, port), "certificate-authority": ca}}},
		"users":    []any{map[string]any{"name": "service-account", "user": map[string]any{"tokenFile": token}}},
		"contexts": []any{map[string]any{"name": "in-cluster", "context": map[string]any{"cluster": "in-cluster", "user": "service-account"}}},
	}
	b, err := yaml.Marshal(cfg)
	return string(b), err
}

func writeInClusterConfig(dataDir string) (string, error) {
	cfg, err := inClusterConfig()
	if err != nil {
		return "", err
	}
	file := filepath.Join(dataDir, "kube", "in-cluster.kubeconfig")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return "", err
	}
	return file, os.WriteFile(file, []byte(cfg), 0o600)
}

// Factory returns what executor.Pool calls to reach a Kubernetes server: the
// decrypted kubeconfig is written under dataDir for kubectl to read.
func Factory(dataDir string) func(srv store.Server, kubeconfig string) (executor.Executor, error) {
	return func(srv store.Server, kubeconfig string) (executor.Executor, error) {
		if err := CheckNamespace(srv.KubeNamespace); err != nil {
			return nil, err
		}
		if kubeconfig == "" {
			if !InCluster() {
				return nil, errors.New("the server has no kubeconfig")
			}
			file, err := writeInClusterConfig(dataDir)
			if err != nil {
				return nil, fmt.Errorf("use the pod's service account: %w", err)
			}
			return New(file, srv.KubeNamespace), nil
		}
		file, err := WriteConfig(dataDir, srv.ID, kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("write the kubeconfig: %w", err)
		}
		return New(file, srv.KubeNamespace), nil
	}
}
