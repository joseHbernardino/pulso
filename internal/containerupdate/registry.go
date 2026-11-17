package containerupdate

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// Optional credentials stay in a read-only file mounted only in the companion/worker.
// The portal never receives the Docker configuration or the registry token.
func registryAuth(ref string) (string, error) {
	path := os.Getenv("PULSO_REGISTRY_CONFIG")
	if path == "" {
		return "", nil
	}
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("abrir credenciais do registro: %w", err)
	}
	defer func() { _ = file.Close() }()
	var config struct {
		Auths map[string]struct {
			Auth          string `json:"auth"`
			IdentityToken string `json:"identitytoken"`
		} `json:"auths"`
	}
	if err = json.NewDecoder(io.LimitReader(file, 1<<20)).Decode(&config); err != nil {
		return "", fmt.Errorf("configuração de credenciais do registro inválida")
	}
	registry := strings.Split(ref, "/")[0]
	if !strings.ContainsAny(registry, ".:") && registry != "localhost" {
		registry = "index.docker.io"
	}
	for address, entry := range config.Auths {
		normalized := strings.TrimPrefix(strings.TrimPrefix(address, "https://"), "http://")
		normalized = strings.TrimSuffix(strings.TrimSuffix(normalized, "/v1/"), "/")
		if normalized != registry {
			continue
		}
		payload := map[string]string{"serveraddress": address}
		if entry.IdentityToken != "" {
			payload["identitytoken"] = entry.IdentityToken
		} else {
			decoded, err := base64.StdEncoding.DecodeString(entry.Auth)
			if err != nil {
				return "", fmt.Errorf("credencial do registro inválida")
			}
			username, password, ok := strings.Cut(string(decoded), ":")
			if !ok {
				return "", fmt.Errorf("o arquivo do registro precisa conter credenciais inline (auth ou identitytoken)")
			}
			payload["username"], payload["password"] = username, password
		}
		data, _ := json.Marshal(payload)
		return base64.URLEncoding.EncodeToString(data), nil
	}
	return "", nil
}
