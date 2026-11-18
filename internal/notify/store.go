package notify

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// maxChannels bounds the configuration file.
const maxChannels = 50

// Store keeps the channel configuration in a single JSON file with
// owner-only permissions.
//
// A file is used instead of a table because the configuration is a handful of
// records read at delivery time: it needs no query engine, and it keeps the
// install free of another schema to migrate.
type Store struct {
	path string
	mu   sync.RWMutex
}

// OpenStore prepares the configuration file.
func OpenStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	store := &Store{path: path}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := store.write(nil); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	return store, nil
}

// List returns every configured channel, including the stored credentials.
// Callers that send data to a browser must use Channel.Public.
func (s *Store) List() ([]Channel, error) {
	if s == nil {
		return []Channel{}, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.readLocked()
}

// PublicList returns every channel with the credentials redacted.
func (s *Store) PublicList() ([]Channel, error) {
	channels, err := s.List()
	if err != nil {
		return nil, err
	}
	result := make([]Channel, 0, len(channels))
	for _, channel := range channels {
		result = append(result, channel.Public())
	}
	return result, nil
}

// Get returns one channel by identifier.
func (s *Store) Get(id string) (Channel, error) {
	channels, err := s.List()
	if err != nil {
		return Channel{}, err
	}
	for _, channel := range channels {
		if channel.ID == id {
			return channel, nil
		}
	}
	return Channel{}, fmt.Errorf("canal de notificação não encontrado")
}

// Put creates or replaces a channel. An incoming credential equal to
// Redacted keeps the stored value, so editing a channel without retyping the
// secret does not erase it.
func (s *Store) Put(channel Channel) (Channel, error) {
	if s == nil {
		return Channel{}, fmt.Errorf("notificações indisponíveis")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	channels, err := s.readLocked()
	if err != nil {
		return Channel{}, err
	}

	index := -1
	for i := range channels {
		if channels[i].ID == channel.ID && channel.ID != "" {
			index = i
			break
		}
	}
	if channel.ID == "" {
		if len(channels) >= maxChannels {
			return Channel{}, fmt.Errorf("limite de %d canais atingido", maxChannels)
		}
		id, err := newID()
		if err != nil {
			return Channel{}, err
		}
		channel.ID = id
	} else if index < 0 {
		return Channel{}, fmt.Errorf("canal de notificação não encontrado")
	}
	if index >= 0 {
		channel.MergeSecrets(channels[index])
	} else {
		// Um canal novo não tem segredo guardado para preservar.
		channel.MergeSecrets(Channel{})
	}
	if err := channel.Validate(); err != nil {
		return Channel{}, err
	}

	if index >= 0 {
		channels[index] = channel
	} else {
		channels = append(channels, channel)
	}
	if err := s.writeLocked(channels); err != nil {
		return Channel{}, err
	}
	return channel, nil
}

// Delete removes a channel.
func (s *Store) Delete(id string) error {
	if s == nil {
		return fmt.Errorf("notificações indisponíveis")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	channels, err := s.readLocked()
	if err != nil {
		return err
	}
	remaining := make([]Channel, 0, len(channels))
	for _, channel := range channels {
		if channel.ID != id {
			remaining = append(remaining, channel)
		}
	}
	if len(remaining) == len(channels) {
		return fmt.Errorf("canal de notificação não encontrado")
	}
	return s.writeLocked(remaining)
}

func (s *Store) readLocked() ([]Channel, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return []Channel{}, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return []Channel{}, nil
	}
	var channels []Channel
	if err := json.Unmarshal(data, &channels); err != nil {
		return nil, fmt.Errorf("configuração de notificações ilegível em %s: %w", s.path, err)
	}
	if channels == nil {
		channels = []Channel{}
	}
	sort.Slice(channels, func(i, j int) bool {
		if channels[i].Name != channels[j].Name {
			return channels[i].Name < channels[j].Name
		}
		return channels[i].ID < channels[j].ID
	})
	return channels, nil
}

func (s *Store) write(channels []Channel) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeLocked(channels)
}

// writeLocked persists atomically: a crash mid-write leaves the previous
// configuration intact rather than a truncated file.
func (s *Store) writeLocked(channels []Channel) error {
	if channels == nil {
		channels = []Channel{}
	}
	data, err := json.MarshalIndent(channels, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(s.path), ".notifications-*.tmp")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer func() { _ = os.Remove(name) }()

	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(name, s.path)
}

func newID() (string, error) {
	buffer := make([]byte, 12)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}
