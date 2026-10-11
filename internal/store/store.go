package store

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
)

type ChatStore struct {
	mu   sync.Mutex
	path string
}

func NewChatStore(path string) *ChatStore {
	return &ChatStore{path: path}
}

func (s *ChatStore) Read() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("[chats] не удалось прочитать %s: %v", s.path, err)
		}
		return nil
	}
	var chats []string
	if err := json.Unmarshal(raw, &chats); err != nil {
		log.Printf("[chats] битый JSON в %s: %v", s.path, err)
		return nil
	}
	return chats
}

func (s *ChatStore) Add(jid string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := os.ReadFile(s.path)
	var chats []string
	if err != nil && !os.IsNotExist(err) {
		log.Printf("[chats] не удалось прочитать %s: %v", s.path, err)
	} else if err == nil {
		if uerr := json.Unmarshal(raw, &chats); uerr != nil {
			log.Printf("[chats] битый JSON в %s: %v", s.path, uerr)
			chats = nil
		}
	}
	for _, c := range chats {
		if c == jid {
			return
		}
	}
	chats = append(chats, jid)
	if dir := filepath.Dir(s.path); dir != "" {
		if mkerr := os.MkdirAll(dir, 0o755); mkerr != nil {
			log.Printf("[chats] не удалось создать папку %s: %v", dir, mkerr)
			return
		}
	}
	out, merr := json.MarshalIndent(chats, "", "  ")
	if merr != nil {
		log.Printf("[chats] не удалось сериализовать список: %v", merr)
		return
	}
	if werr := os.WriteFile(s.path, out, 0o644); werr != nil {
		log.Printf("[chats] не удалось записать %s: %v", s.path, werr)
	}
}
