package notify

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Channel kinds.
const (
	KindSlack    = "slack"
	KindDiscord  = "discord"
	KindTelegram = "telegram"
	KindGotify   = "gotify"
	KindNtfy     = "ntfy"
	KindWebhook  = "webhook"
	KindEmail    = "email"
)

// KnownKinds lists every supported channel kind.
func KnownKinds() []string {
	return []string{KindSlack, KindDiscord, KindTelegram, KindGotify, KindNtfy, KindWebhook, KindEmail}
}

// Redacted is what the API returns in place of a stored secret. It is a fixed
// marker, not a prefix of the real value, so nothing can be inferred from it.
const Redacted = "__stored__"

// secretSettings names the settings that hold a credential, per kind. These
// are never returned by the API and never written to a log.
var secretSettings = map[string]map[string]bool{
	KindSlack:    {"webhook_url": true},
	KindDiscord:  {"webhook_url": true},
	KindTelegram: {"bot_token": true},
	KindGotify:   {"token": true},
	KindNtfy:     {"token": true},
	KindWebhook:  {"url": true, "authorization": true},
	KindEmail:    {"password": true},
}

// IsSecretSetting reports whether a setting holds a credential.
func IsSecretSetting(kind, key string) bool {
	return secretSettings[kind][key]
}

// Channel is one configured destination.
type Channel struct {
	ID       string            `json:"id"`
	Kind     string            `json:"kind"`
	Name     string            `json:"name"`
	Enabled  bool              `json:"enabled"`
	Events   []string          `json:"events"`
	Settings map[string]string `json:"settings"`
}

var channelName = regexp.MustCompile(`^[\p{L}\p{N} ._-]{1,60}$`)

// Subscribes reports whether this channel wants the given event kind. An
// empty list means every kind. A test event always goes through, so the
// dashboard's test button works regardless of the subscriptions.
func (c Channel) Subscribes(kind string) bool {
	if kind == EventTest {
		return true
	}
	if len(c.Events) == 0 {
		return true
	}
	for _, subscribed := range c.Events {
		if subscribed == kind {
			return true
		}
	}
	return false
}

// Public returns a copy safe to send to a browser: every credential is
// replaced by Redacted, and the presence of a value is preserved so the
// interface can show the field as already configured.
func (c Channel) Public() Channel {
	copied := c
	copied.Settings = make(map[string]string, len(c.Settings))
	for key, value := range c.Settings {
		if IsSecretSetting(c.Kind, key) && value != "" {
			copied.Settings[key] = Redacted
			continue
		}
		copied.Settings[key] = value
	}
	return copied
}

// MergeSecrets keeps the stored credential whenever the incoming value is the
// redaction marker, so an edit that does not retype a secret preserves it.
func (c *Channel) MergeSecrets(previous Channel) {
	if c.Settings == nil {
		c.Settings = map[string]string{}
	}
	for key, value := range c.Settings {
		if value == Redacted {
			if stored, ok := previous.Settings[key]; ok {
				c.Settings[key] = stored
			} else {
				delete(c.Settings, key)
			}
		}
	}
}

// Validate checks the configuration, including the settings each kind needs.
func (c *Channel) Validate() error {
	c.Kind = strings.TrimSpace(strings.ToLower(c.Kind))
	c.Name = strings.TrimSpace(c.Name)
	if !channelName.MatchString(c.Name) {
		return fmt.Errorf("informe um nome de até 60 caracteres para o canal")
	}
	known := false
	for _, kind := range KnownKinds() {
		if c.Kind == kind {
			known = true
			break
		}
	}
	if !known {
		return fmt.Errorf("tipo de canal inválido: %q", c.Kind)
	}
	if c.Settings == nil {
		c.Settings = map[string]string{}
	}
	for key, value := range c.Settings {
		c.Settings[key] = strings.TrimSpace(value)
		if strings.ContainsAny(key, " \t\n") {
			return fmt.Errorf("nome de configuração inválido: %q", key)
		}
	}
	c.Events = sortedEventList(c.Events)
	for _, event := range c.Events {
		valid := false
		for _, known := range KnownEvents() {
			if event == known {
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("evento desconhecido: %q", event)
		}
	}

	switch c.Kind {
	case KindSlack, KindDiscord:
		return c.requireHTTPS("webhook_url")
	case KindWebhook:
		return c.requireHTTPURL("url")
	case KindTelegram:
		if c.Settings["bot_token"] == "" {
			return fmt.Errorf("informe o bot_token do Telegram")
		}
		if c.Settings["chat_id"] == "" {
			return fmt.Errorf("informe o chat_id do Telegram")
		}
		return nil
	case KindGotify:
		if err := c.requireHTTPURL("url"); err != nil {
			return err
		}
		if c.Settings["token"] == "" {
			return fmt.Errorf("informe o token do Gotify")
		}
		return nil
	case KindNtfy:
		if err := c.requireHTTPURL("url"); err != nil {
			return err
		}
		if c.Settings["topic"] == "" {
			return fmt.Errorf("informe o tópico do ntfy")
		}
		return nil
	case KindEmail:
		return c.validateEmail()
	}
	return nil
}

// requireHTTPURL accepts http and https. Plain http is allowed because an
// internal Gotify, ntfy or webhook often has no certificate.
func (c *Channel) requireHTTPURL(key string) error {
	raw := c.Settings[key]
	if raw == "" {
		return fmt.Errorf("informe a URL (%s)", key)
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("URL inválida em %s; use http:// ou https://", key)
	}
	return nil
}

// requireHTTPS is used for the hosted providers, whose endpoints are always
// HTTPS; accepting http there would only leak the webhook secret.
func (c *Channel) requireHTTPS(key string) error {
	raw := c.Settings[key]
	if raw == "" {
		return fmt.Errorf("informe a URL do webhook (%s)", key)
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.Scheme != "https" {
		return fmt.Errorf("URL inválida em %s; o webhook deve usar https://", key)
	}
	return nil
}

func (c *Channel) validateEmail() error {
	if c.Settings["host"] == "" {
		return fmt.Errorf("informe o servidor SMTP (host)")
	}
	port := c.Settings["port"]
	if port == "" {
		port = "587"
		c.Settings["port"] = port
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return fmt.Errorf("porta SMTP inválida: %q", port)
	}
	if c.Settings["from"] == "" {
		return fmt.Errorf("informe o remetente (from)")
	}
	recipients := c.Recipients()
	if len(recipients) == 0 {
		return fmt.Errorf("informe ao menos um destinatário (to)")
	}
	for _, address := range recipients {
		if !strings.Contains(address, "@") || strings.ContainsAny(address, " \t\r\n") {
			return fmt.Errorf("destinatário inválido: %q", address)
		}
	}
	if strings.ContainsAny(c.Settings["from"], " \t\r\n") || !strings.Contains(c.Settings["from"], "@") {
		return fmt.Errorf("remetente inválido: %q", c.Settings["from"])
	}
	return nil
}

// Recipients splits the e-mail "to" setting on commas.
func (c Channel) Recipients() []string {
	result := []string{}
	for _, part := range strings.Split(c.Settings["to"], ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
