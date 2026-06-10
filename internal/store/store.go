// Package store defines Cubozoa's persistence layer.
//
// The interface is deliberately storage-agnostic. Milestone 1 ships a small
// JSON-backed implementation that needs no external database — keeping the
// dependency surface (and therefore the attack surface) minimal — while leaving
// a clean seam to drop in SQLite or Postgres as the data model grows.
package store

import "errors"

var (
	// ErrNotFound is returned when a requested record does not exist.
	ErrNotFound = errors.New("store: not found")
	// ErrConflict is returned when a record violates a uniqueness constraint.
	ErrConflict = errors.New("store: conflict")
)

// Store is the persistence contract used by the rest of the application.
type Store interface {
	// Meta
	ServerID() string

	// Users
	CreateUser(u *User) error
	GetUserByID(id string) (*User, error)
	GetUserByName(name string) (*User, error)
	ListUsers() ([]*User, error)
	UpdateUser(u *User) error
	CountUsers() (int, error)

	// Sessions
	CreateSession(s *Session) error
	GetSessionByTokenHash(tokenHash string) (*Session, error)
	DeleteSession(id string) error
	TouchSession(id string) error

	// Close flushes and releases any resources held by the store.
	Close() error
}
