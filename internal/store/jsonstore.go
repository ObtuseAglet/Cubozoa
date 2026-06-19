package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/obtuseaglet/cubozoa/internal/security"
)

// jsonStore is an in-memory store with durable JSON persistence. All reads are
// served from memory under an RWMutex; every mutation is flushed to disk
// atomically (write-temp-then-rename) so a crash cannot leave a half-written
// datastore.
type jsonStore struct {
	mu        sync.RWMutex
	path      string
	serverID  string
	users     map[string]*User // keyed by user ID
	sessions  map[string]*Session
	libraries map[string]*Library      // keyed by library ID
	items     map[string]*MediaItem    // keyed by item ID
	userData  map[string]*UserItemData // keyed by userID + "\x00" + itemID
}

// persisted is the on-disk schema. Versioned so future migrations are possible.
type persisted struct {
	Version   int                      `json:"version"`
	ServerID  string                   `json:"server_id"`
	Users     map[string]*User         `json:"users"`
	Sessions  map[string]*Session      `json:"sessions"`
	Libraries map[string]*Library      `json:"libraries"`
	Items     map[string]*MediaItem    `json:"items"`
	UserData  map[string]*UserItemData `json:"user_data"`
}

// userDataKey composes the map key for a user/item pair.
func userDataKey(userID, itemID string) string {
	return userID + "\x00" + itemID
}

const schemaVersion = 1

// OpenJSON opens (or initializes) a JSON-backed store rooted at dataDir.
func OpenJSON(dataDir string) (Store, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("store: creating data dir: %w", err)
	}

	s := &jsonStore{
		path:      filepath.Join(dataDir, "cubozoa.json"),
		users:     make(map[string]*User),
		sessions:  make(map[string]*Session),
		libraries: make(map[string]*Library),
		items:     make(map[string]*MediaItem),
		userData:  make(map[string]*UserItemData),
	}

	if err := s.load(); err != nil {
		return nil, err
	}

	if s.serverID == "" {
		id, err := security.NewID()
		if err != nil {
			return nil, err
		}
		s.serverID = id
		if err := s.flushLocked(); err != nil {
			return nil, err
		}
	}

	return s, nil
}

func (s *jsonStore) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // fresh install
		}
		return fmt.Errorf("store: reading datastore: %w", err)
	}

	var p persisted
	if err := json.Unmarshal(data, &p); err != nil {
		return fmt.Errorf("store: parsing datastore: %w", err)
	}

	s.serverID = p.ServerID
	if p.Users != nil {
		s.users = p.Users
	}
	if p.Sessions != nil {
		s.sessions = p.Sessions
	}
	if p.Libraries != nil {
		s.libraries = p.Libraries
	}
	if p.Items != nil {
		s.items = p.Items
	}
	if p.UserData != nil {
		s.userData = p.UserData
	}
	return nil
}

// flushLocked writes the current state to disk atomically. Callers must hold
// the write lock.
func (s *jsonStore) flushLocked() error {
	p := persisted{
		Version:   schemaVersion,
		ServerID:  s.serverID,
		Users:     s.users,
		Sessions:  s.sessions,
		Libraries: s.libraries,
		Items:     s.items,
		UserData:  s.userData,
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("store: encoding datastore: %w", err)
	}

	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("store: writing datastore: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("store: committing datastore: %w", err)
	}
	return nil
}

func (s *jsonStore) ServerID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.serverID
}

func (s *jsonStore) CreateUser(u *User) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, existing := range s.users {
		if strings.EqualFold(existing.Name, u.Name) {
			return ErrConflict
		}
	}
	clone := *u
	s.users[u.ID] = &clone
	return s.flushLocked()
}

func (s *jsonStore) GetUserByID(id string) (*User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[id]
	if !ok {
		return nil, ErrNotFound
	}
	clone := *u
	return &clone, nil
}

func (s *jsonStore) GetUserByName(name string) (*User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, u := range s.users {
		if strings.EqualFold(u.Name, name) {
			clone := *u
			return &clone, nil
		}
	}
	return nil, ErrNotFound
}

func (s *jsonStore) ListUsers() ([]*User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*User, 0, len(s.users))
	for _, u := range s.users {
		clone := *u
		out = append(out, &clone)
	}
	return out, nil
}

func (s *jsonStore) UpdateUser(u *User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[u.ID]; !ok {
		return ErrNotFound
	}
	clone := *u
	s.users[u.ID] = &clone
	return s.flushLocked()
}

func (s *jsonStore) CountUsers() (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.users), nil
}

func (s *jsonStore) CreateSession(sess *Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	clone := *sess
	s.sessions[sess.ID] = &clone
	return s.flushLocked()
}

func (s *jsonStore) GetSessionByTokenHash(tokenHash string) (*Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, sess := range s.sessions {
		if sess.TokenHash == tokenHash {
			clone := *sess
			return &clone, nil
		}
	}
	return nil, ErrNotFound
}

func (s *jsonStore) DeleteSession(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[id]; !ok {
		return ErrNotFound
	}
	delete(s.sessions, id)
	return s.flushLocked()
}

func (s *jsonStore) TouchSession(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return ErrNotFound
	}
	sess.LastActivity = time.Now().UTC()
	return s.flushLocked()
}

func (s *jsonStore) CreateLibrary(l *Library) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.libraries {
		if existing.Path == l.Path {
			return ErrConflict
		}
	}
	clone := *l
	s.libraries[l.ID] = &clone
	return s.flushLocked()
}

func (s *jsonStore) GetLibrary(id string) (*Library, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	l, ok := s.libraries[id]
	if !ok {
		return nil, ErrNotFound
	}
	clone := *l
	return &clone, nil
}

func (s *jsonStore) GetLibraryByPath(path string) (*Library, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, l := range s.libraries {
		if l.Path == path {
			clone := *l
			return &clone, nil
		}
	}
	return nil, ErrNotFound
}

func (s *jsonStore) ListLibraries() ([]*Library, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Library, 0, len(s.libraries))
	for _, l := range s.libraries {
		clone := *l
		out = append(out, &clone)
	}
	return out, nil
}

func (s *jsonStore) DeleteLibrary(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.libraries[id]; !ok {
		return ErrNotFound
	}
	delete(s.libraries, id)
	for itemID, it := range s.items {
		if it.LibraryID == id {
			delete(s.items, itemID)
		}
	}
	return s.flushLocked()
}

func (s *jsonStore) GetItem(id string) (*MediaItem, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	it, ok := s.items[id]
	if !ok {
		return nil, ErrNotFound
	}
	clone := *it
	return &clone, nil
}

func (s *jsonStore) ListItemsByLibrary(libraryID string) ([]*MediaItem, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*MediaItem, 0)
	for _, it := range s.items {
		if it.LibraryID == libraryID {
			clone := *it
			out = append(out, &clone)
		}
	}
	return out, nil
}

func (s *jsonStore) ListItemsByParent(parentID string) ([]*MediaItem, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*MediaItem, 0)
	for _, it := range s.items {
		if it.ParentID == parentID {
			clone := *it
			out = append(out, &clone)
		}
	}
	return out, nil
}

func (s *jsonStore) AllItems() ([]*MediaItem, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*MediaItem, 0, len(s.items))
	for _, it := range s.items {
		clone := *it
		out = append(out, &clone)
	}
	return out, nil
}

func (s *jsonStore) ReplaceLibraryItems(libraryID string, items []*MediaItem) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	lib, ok := s.libraries[libraryID]
	if !ok {
		return ErrNotFound
	}

	// Drop the library's existing items, then insert the fresh set.
	for itemID, it := range s.items {
		if it.LibraryID == libraryID {
			delete(s.items, itemID)
		}
	}
	for _, it := range items {
		clone := *it
		clone.LibraryID = libraryID
		s.items[it.ID] = &clone
	}

	lib.ItemCount = len(items)
	lib.ScannedAt = time.Now().UTC()
	return s.flushLocked()
}

func (s *jsonStore) GetUserItemData(userID, itemID string) (*UserItemData, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.userData[userDataKey(userID, itemID)]
	if !ok {
		return nil, ErrNotFound
	}
	clone := *d
	return &clone, nil
}

func (s *jsonStore) UpsertUserItemData(d *UserItemData) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	clone := *d
	s.userData[userDataKey(d.UserID, d.ItemID)] = &clone
	return s.flushLocked()
}

func (s *jsonStore) ListUserItemData(userID string) ([]*UserItemData, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*UserItemData, 0)
	for _, d := range s.userData {
		if d.UserID == userID {
			clone := *d
			out = append(out, &clone)
		}
	}
	return out, nil
}

func (s *jsonStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.flushLocked()
}
