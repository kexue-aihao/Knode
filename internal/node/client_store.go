package node

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
)

type dynamicClientStore struct {
	mu             sync.RWMutex
	staticSecrets  map[string][]byte
	staticUsers    map[string]int64
	dynamicSecrets map[string][]byte
	dynamicUsers   map[string]int64
}

func newDynamicClientStore() *dynamicClientStore {
	return &dynamicClientStore{
		staticSecrets:  make(map[string][]byte),
		staticUsers:    make(map[string]int64),
		dynamicSecrets: make(map[string][]byte),
		dynamicUsers:   make(map[string]int64),
	}
}

func (s *dynamicClientStore) LookupClientSecret(clientID string) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	secret, ok := s.dynamicSecrets[clientID]
	if !ok {
		secret, ok = s.staticSecrets[clientID]
	}
	if !ok {
		return nil, false
	}
	out := make([]byte, len(secret))
	copy(out, secret)
	return out, true
}

func (s *dynamicClientStore) UserID(clientID string) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if userID := s.dynamicUsers[clientID]; userID > 0 {
		return userID
	}
	return s.staticUsers[clientID]
}

func (s *dynamicClientStore) SnapshotSize() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.staticSecrets) + len(s.dynamicSecrets)
}

func (s *dynamicClientStore) SetStatic(entries []ClientCredential) {
	secrets, users := credentialMaps(entries)
	s.mu.Lock()
	s.staticSecrets = secrets
	s.staticUsers = users
	s.mu.Unlock()
}

func (s *dynamicClientStore) AddStatic(entries []ClientCredential) {
	secrets, users := credentialMaps(entries)
	s.mu.Lock()
	for clientID, secret := range secrets {
		s.staticSecrets[clientID] = secret
	}
	for clientID, userID := range users {
		s.staticUsers[clientID] = userID
	}
	s.mu.Unlock()
}

func (s *dynamicClientStore) ReplaceDynamic(entries []ClientCredential) {
	secrets, users := credentialMaps(entries)
	s.mu.Lock()
	s.dynamicSecrets = secrets
	s.dynamicUsers = users
	s.mu.Unlock()
}

func credentialMaps(entries []ClientCredential) (map[string][]byte, map[string]int64) {
	nextSecrets := make(map[string][]byte, len(entries))
	nextUsers := make(map[string]int64, len(entries))
	for _, entry := range entries {
		clientID := strings.TrimSpace(entry.ClientID)
		if clientID == "" || len(entry.Secret) == 0 {
			continue
		}
		secret := make([]byte, len(entry.Secret))
		copy(secret, entry.Secret)
		nextSecrets[clientID] = secret
		nextUsers[clientID] = entry.UserID
	}
	return nextSecrets, nextUsers
}

type ClientCredential struct {
	ClientID string
	Secret   []byte
	UserID   int64
}

func deriveUserClientSecret(nodeSecret, userUUID, token string) []byte {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		"Knode KLESS user secret v1",
		strings.TrimSpace(nodeSecret),
		strings.TrimSpace(userUUID),
		strings.TrimSpace(token),
	}, "\n")))
	return sum[:]
}

func encodeClientSecret(secret []byte) string {
	return base64.RawURLEncoding.EncodeToString(secret)
}

func clientIDForUser(user KboardUser) string {
	if user.KLESSClientID != "" {
		return user.KLESSClientID
	}
	if user.ClientID != "" {
		return user.ClientID
	}
	if user.UUID != "" {
		return user.UUID
	}
	if user.ID > 0 {
		return fmt.Sprintf("%d", user.ID)
	}
	return ""
}
