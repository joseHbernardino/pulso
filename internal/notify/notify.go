// Package notify delivers events to outbound channels such as Slack, Discord,
// Telegram, Gotify, ntfy, a generic webhook, or e-mail.
//
// Every channel is outbound-only: nothing has to be reachable from outside,
// which is what makes this work on a closed network with no inbound access.
//
// Delivery never blocks the caller. Dispatch hands the event to a bounded
// queue and returns; a worker sends it with a timeout and a few retries.
package notify

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// Event kinds. Channels subscribe to these by name; an empty subscription
// list means every kind.
const (
	EventDeploySucceeded    = "deploy.succeeded"
	EventDeployFailed       = "deploy.failed"
	EventApplicationStopped = "application.stopped"
	EventApplicationFailed  = "application.unhealthy"
	EventSystemUpdate       = "system.update"
	EventResourceThreshold  = "resource.threshold"
	EventTest               = "test"
)

// KnownEvents lists every kind a channel may subscribe to.
func KnownEvents() []string {
	return []string{
		EventDeploySucceeded,
		EventDeployFailed,
		EventApplicationStopped,
		EventApplicationFailed,
		EventSystemUpdate,
		EventResourceThreshold,
	}
}

// Severity levels, used by channels that render colours or priorities.
const (
	SeverityInfo    = "info"
	SeverityWarning = "warning"
	SeverityError   = "error"
)

// Event is one notifiable occurrence.
type Event struct {
	Kind        string    `json:"kind"`
	Severity    string    `json:"severity"`
	Title       string    `json:"title"`
	Message     string    `json:"message"`
	Application string    `json:"application,omitempty"`
	Actor       string    `json:"actor,omitempty"`
	At          time.Time `json:"at"`
}

// normalize fills the defaults a sender relies on.
func (e Event) normalize() Event {
	if e.Severity == "" {
		e.Severity = SeverityInfo
	}
	if e.At.IsZero() {
		e.At = time.Now()
	}
	if e.Title == "" {
		e.Title = e.Kind
	}
	return e
}

// plainText renders the event for channels without rich formatting.
func (e Event) plainText() string {
	var builder strings.Builder
	builder.WriteString(e.Title)
	if e.Application != "" {
		builder.WriteString(" — ")
		builder.WriteString(e.Application)
	}
	if e.Message != "" {
		builder.WriteString("\n")
		builder.WriteString(e.Message)
	}
	if e.Actor != "" {
		builder.WriteString("\npor ")
		builder.WriteString(e.Actor)
	}
	builder.WriteString("\n")
	builder.WriteString(e.At.Format(time.RFC3339))
	return builder.String()
}

// Sender delivers one event over one transport.
type Sender interface {
	// Send delivers the event. The context carries the per-attempt timeout.
	Send(ctx context.Context, event Event) error
}

// senderFor builds the transport for a channel configuration.
func senderFor(channel Channel) (Sender, error) {
	if err := channel.Validate(); err != nil {
		return nil, err
	}
	switch channel.Kind {
	case KindSlack:
		return &slackSender{channel: channel}, nil
	case KindDiscord:
		return &discordSender{channel: channel}, nil
	case KindTelegram:
		return &telegramSender{channel: channel}, nil
	case KindGotify:
		return &gotifySender{channel: channel}, nil
	case KindNtfy:
		return &ntfySender{channel: channel}, nil
	case KindWebhook:
		return &webhookSender{channel: channel}, nil
	case KindEmail:
		return &emailSender{channel: channel}, nil
	}
	return nil, fmt.Errorf("tipo de canal desconhecido: %s", channel.Kind)
}

// Delivery settings. A notification is a courtesy: it is given a short
// timeout and a few tries, and then dropped rather than held.
const (
	attemptTimeout = 10 * time.Second
	maxAttempts    = 3
	retryBackoff   = 2 * time.Second
	queueCapacity  = 256
	// shutdownGrace bounds how long Close waits for the queued events.
	shutdownGrace = 5 * time.Second
	// testTimeout bounds the synchronous channel test. Someone is waiting on
	// the answer, so a single attempt with a clear error beats three silent
	// retries.
	testTimeout = 8 * time.Second
	// dropLogEvery throttles the "queue full" warning, so a burst cannot
	// flood the log with thousands of identical lines.
	dropLogEvery = 100
)

// Dispatcher fans events out to the configured channels.
type Dispatcher struct {
	store *Store

	// mu guards closed, the send on queue and the drop counter. Holding it
	// while sending is what makes Dispatch safe against a concurrent Close:
	// the queue is only closed while the lock is held, so no send can race
	// with the close.
	mu      sync.Mutex
	closed  bool
	dropped int64

	queue chan Event
	wait  sync.WaitGroup

	// ctx is cancelled when the grace period expires, aborting the attempts
	// still in flight so that Close cannot hang.
	ctx    context.Context
	cancel context.CancelFunc

	// newSender is swapped in tests.
	newSender func(Channel) (Sender, error)
}

// NewDispatcher starts the delivery worker. The caller closes it on shutdown.
func NewDispatcher(store *Store) *Dispatcher {
	ctx, cancel := context.WithCancel(context.Background())
	d := &Dispatcher{
		store:     store,
		queue:     make(chan Event, queueCapacity),
		ctx:       ctx,
		cancel:    cancel,
		newSender: senderFor,
	}
	d.wait.Add(1)
	go d.run()
	return d
}

// Dispatch queues an event. It never blocks and never returns an error: a
// notification must not be able to hold up a deployment. When the queue is
// full the event is dropped and counted.
func (d *Dispatcher) Dispatch(event Event) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return
	}
	select {
	case d.queue <- event.normalize():
	default:
		d.dropped++
		if d.dropped == 1 || d.dropped%dropLogEvery == 0 {
			log.Warn().Str("kind", event.Kind).Int64("dropped_total", d.dropped).
				Msg("notification queue full; events dropped")
		}
	}
}

// Dropped reports how many events were discarded for lack of queue space.
func (d *Dispatcher) Dropped() int64 {
	if d == nil {
		return 0
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dropped
}

// Close stops accepting events, gives the queued ones a short window to be
// delivered, and then aborts whatever is still in flight. A pending
// notification must never delay a shutdown.
func (d *Dispatcher) Close() {
	if d == nil {
		return
	}
	d.mu.Lock()
	alreadyClosed := d.closed
	if !alreadyClosed {
		d.closed = true
		close(d.queue)
	}
	d.mu.Unlock()
	if alreadyClosed {
		d.wait.Wait()
		return
	}

	drained := make(chan struct{})
	go func() {
		d.wait.Wait()
		close(drained)
	}()
	select {
	case <-drained:
	case <-time.After(shutdownGrace):
		// Cancelar o contexto faz cada tentativa restante falhar de imediato
		// e encurta as esperas entre tentativas.
		d.cancel()
		<-drained
	}
	d.cancel()
}

func (d *Dispatcher) run() {
	defer d.wait.Done()
	for event := range d.queue {
		d.deliver(event)
	}
}

// deliver sends one event to every channel subscribed to its kind.
func (d *Dispatcher) deliver(event Event) {
	channels, err := d.store.List()
	if err != nil {
		log.Error().Err(err).Msg("read notification channels")
		return
	}
	for _, channel := range channels {
		if !channel.Enabled || !channel.Subscribes(event.Kind) {
			continue
		}
		if err := d.send(channel, event); err != nil {
			// Uma notificação que falha é registrada e descartada; ela não
			// pode afetar a operação que a originou.
			log.Error().Err(err).Str("channel", channel.Name).
				Str("kind", event.Kind).Msg("notification delivery failed")
		}
	}
}

// send delivers one event to one channel, retrying a few times.
func (d *Dispatcher) send(channel Channel, event Event) error {
	sender, err := d.newSender(channel)
	if err != nil {
		return err
	}
	parent := d.attemptContext()
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		ctx, cancel := context.WithTimeout(parent, attemptTimeout)
		err := sender.Send(ctx, event)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		if attempt < maxAttempts {
			select {
			case <-time.After(retryBackoff * time.Duration(attempt)):
			case <-parent.Done():
				return fmt.Errorf("encerrando durante o envio: %w", lastErr)
			}
		}
	}
	return fmt.Errorf("após %d tentativas: %w", maxAttempts, lastErr)
}

// attemptContext is the parent for each delivery attempt. A zero-value
// Dispatcher built in a test without NewDispatcher still works.
func (d *Dispatcher) attemptContext() context.Context {
	if d.ctx == nil {
		return context.Background()
	}
	return d.ctx
}

// TestChannel delivers a test event to one stored channel and reports the
// outcome synchronously, so the dashboard can show the real error.
func (d *Dispatcher) TestChannel(id string) error {
	if d == nil {
		return errors.New("notificações indisponíveis")
	}
	channel, err := d.store.Get(id)
	if err != nil {
		return err
	}
	event := Event{
		Kind:     EventTest,
		Severity: SeverityInfo,
		Title:    "Teste de notificação do Pulso",
		Message:  "Se você recebeu esta mensagem, o canal está configurado corretamente.",
	}.normalize()

	sender, err := d.newSender(channel)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(d.attemptContext(), testTimeout)
	defer cancel()
	return sender.Send(ctx, event)
}

// sortedEventList returns a stable, de-duplicated subscription list.
func sortedEventList(events []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(events))
	for _, event := range events {
		event = strings.TrimSpace(event)
		if event == "" || seen[event] {
			continue
		}
		seen[event] = true
		result = append(result, event)
	}
	sort.Strings(result)
	return result
}
