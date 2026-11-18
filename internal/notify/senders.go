package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/smtp"
	"net/url"
	"strings"
	"time"
)

// httpClient is shared by the webhook-style channels. The per-attempt
// deadline comes from the request context, so no client timeout is set here.
var httpClient = &http.Client{
	Transport: &http.Transport{
		MaxIdleConns:        10,
		IdleConnTimeout:     30 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	},
}

// postJSON sends a JSON body and treats any 2xx as success.
func postJSON(ctx context.Context, endpoint string, payload any, headers map[string]string) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return post(ctx, endpoint, "application/json", bytes.NewReader(body), headers)
}

func post(ctx context.Context, endpoint, contentType string, body io.Reader, headers map[string]string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("User-Agent", "Pulso")
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	// A resposta é lida e descartada para liberar a conexão ao pool.
	snippet, _ := io.ReadAll(io.LimitReader(response.Body, 512))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message := strings.TrimSpace(string(snippet))
		if message == "" {
			return fmt.Errorf("o destino respondeu HTTP %d", response.StatusCode)
		}
		return fmt.Errorf("o destino respondeu HTTP %d: %s", response.StatusCode, message)
	}
	return nil
}

// severityColor maps a severity to a hex colour for the chat providers.
func severityColor(severity string) string {
	switch severity {
	case SeverityError:
		return "#d93025"
	case SeverityWarning:
		return "#f9ab00"
	default:
		return "#1a73e8"
	}
}

// --------------------------------------------------------------- Slack

type slackSender struct{ channel Channel }

func (s *slackSender) Send(ctx context.Context, event Event) error {
	payload := map[string]any{
		"text": event.Title,
		"attachments": []map[string]any{{
			"color":  severityColor(event.Severity),
			"title":  event.Title,
			"text":   event.Message,
			"ts":     event.At.Unix(),
			"fields": slackFields(event),
		}},
	}
	return postJSON(ctx, s.channel.Settings["webhook_url"], payload, nil)
}

func slackFields(event Event) []map[string]any {
	fields := []map[string]any{}
	if event.Application != "" {
		fields = append(fields, map[string]any{"title": "Aplicação", "value": event.Application, "short": true})
	}
	if event.Actor != "" {
		fields = append(fields, map[string]any{"title": "Autor", "value": event.Actor, "short": true})
	}
	return fields
}

// -------------------------------------------------------------- Discord

type discordSender struct{ channel Channel }

func (s *discordSender) Send(ctx context.Context, event Event) error {
	// O Discord recebe a cor como inteiro decimal.
	color := 0x1a73e8
	switch event.Severity {
	case SeverityError:
		color = 0xd93025
	case SeverityWarning:
		color = 0xf9ab00
	}
	embed := map[string]any{
		"title":       event.Title,
		"description": event.Message,
		"color":       color,
		"timestamp":   event.At.UTC().Format(time.RFC3339),
	}
	if event.Application != "" {
		embed["fields"] = []map[string]any{{"name": "Aplicação", "value": event.Application, "inline": true}}
	}
	payload := map[string]any{"embeds": []map[string]any{embed}}
	return postJSON(ctx, s.channel.Settings["webhook_url"], payload, nil)
}

// ------------------------------------------------------------- Telegram

type telegramSender struct{ channel Channel }

func (s *telegramSender) Send(ctx context.Context, event Event) error {
	base := strings.TrimSuffix(s.channel.Settings["api_url"], "/")
	if base == "" {
		base = "https://api.telegram.org"
	}
	endpoint := fmt.Sprintf("%s/bot%s/sendMessage", base, url.PathEscape(s.channel.Settings["bot_token"]))
	payload := map[string]any{
		"chat_id":                  s.channel.Settings["chat_id"],
		"text":                     event.plainText(),
		"disable_web_page_preview": true,
	}
	return postJSON(ctx, endpoint, payload, nil)
}

// --------------------------------------------------------------- Gotify

type gotifySender struct{ channel Channel }

func (s *gotifySender) Send(ctx context.Context, event Event) error {
	endpoint := strings.TrimSuffix(s.channel.Settings["url"], "/") + "/message"
	priority := 5
	switch event.Severity {
	case SeverityError:
		priority = 8
	case SeverityWarning:
		priority = 6
	}
	payload := map[string]any{
		"title":    event.Title,
		"message":  event.plainText(),
		"priority": priority,
	}
	// O token do Gotify vai em cabeçalho, não na URL, para não aparecer em
	// logs de servidor intermediários.
	return postJSON(ctx, endpoint, payload, map[string]string{"X-Gotify-Key": s.channel.Settings["token"]})
}

// ----------------------------------------------------------------- ntfy

type ntfySender struct{ channel Channel }

func (s *ntfySender) Send(ctx context.Context, event Event) error {
	endpoint := strings.TrimSuffix(s.channel.Settings["url"], "/") + "/" + url.PathEscape(s.channel.Settings["topic"])
	headers := map[string]string{
		"Title": sanitizeHeader(event.Title),
		"Tags":  ntfyTag(event.Severity),
	}
	switch event.Severity {
	case SeverityError:
		headers["Priority"] = "high"
	case SeverityWarning:
		headers["Priority"] = "default"
	default:
		headers["Priority"] = "low"
	}
	if token := s.channel.Settings["token"]; token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	return post(ctx, endpoint, "text/plain; charset=utf-8", strings.NewReader(event.plainText()), headers)
}

func ntfyTag(severity string) string {
	switch severity {
	case SeverityError:
		return "rotating_light"
	case SeverityWarning:
		return "warning"
	default:
		return "information_source"
	}
}

// sanitizeHeader removes the characters that would break a header line.
func sanitizeHeader(value string) string {
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\r", " ")
	if len(value) > 200 {
		value = value[:200]
	}
	return value
}

// -------------------------------------------------------------- webhook

type webhookSender struct{ channel Channel }

func (s *webhookSender) Send(ctx context.Context, event Event) error {
	headers := map[string]string{}
	if authorization := s.channel.Settings["authorization"]; authorization != "" {
		headers["Authorization"] = authorization
	}
	// O webhook genérico recebe o evento inteiro, para integrar com qualquer
	// sistema interno sem um formato imposto.
	return postJSON(ctx, s.channel.Settings["url"], event, headers)
}

// ---------------------------------------------------------------- e-mail

type emailSender struct{ channel Channel }

func (s *emailSender) Send(ctx context.Context, event Event) error {
	settings := s.channel.Settings
	address := settings["host"] + ":" + settings["port"]
	recipients := s.channel.Recipients()

	var auth smtp.Auth
	if username := settings["username"]; username != "" {
		auth = smtp.PlainAuth("", username, settings["password"], settings["host"])
	}

	message := buildEmail(settings["from"], recipients, event)

	// net/smtp não aceita contexto; a chamada roda em uma goroutine para que
	// o prazo da tentativa seja respeitado pelo chamador.
	done := make(chan error, 1)
	go func() {
		done <- smtp.SendMail(address, auth, settings["from"], recipients, message)
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("tempo esgotado ao enviar e-mail por %s: %w", address, ctx.Err())
	}
}

// buildEmail monta a mensagem. Os campos do evento são limpos de quebras de
// linha antes de entrar em cabeçalhos, para impedir injeção de cabeçalho.
func buildEmail(from string, recipients []string, event Event) []byte {
	subject := sanitizeHeader("[Pulso] " + event.Title)
	var builder strings.Builder
	builder.WriteString("From: " + sanitizeHeader(from) + "\r\n")
	builder.WriteString("To: " + sanitizeHeader(strings.Join(recipients, ", ")) + "\r\n")
	builder.WriteString("Subject: " + subject + "\r\n")
	builder.WriteString("Date: " + event.At.Format(time.RFC1123Z) + "\r\n")
	builder.WriteString("MIME-Version: 1.0\r\n")
	builder.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	builder.WriteString("\r\n")
	builder.WriteString(event.plainText())
	builder.WriteString("\r\n")
	return []byte(builder.String())
}
