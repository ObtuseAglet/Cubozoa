// Package metadata fetches optional external metadata (overviews, ratings,
// genres and artwork) for media items. It is entirely opt-in: without a
// configured provider, scanning falls back to filename-derived data.
//
// Providers never touch the rest of the application; they return plain results
// that the media layer maps onto items, keeping this package dependency-free
// and easy to test against a mock HTTP server.
package metadata

import (
	"context"
	"errors"
)

// ErrNotFound indicates no match was found for a title.
var ErrNotFound = errors.New("metadata: no match found")

// Result is the normalized metadata for a movie or series.
type Result struct {
	Title            string
	Overview         string
	Year             int
	Rating           float64
	Genres           []string
	PrimaryImageURL  string
	BackdropImageURL string
}

// Provider looks up metadata for movies and series by title (and optional year).
type Provider interface {
	Movie(ctx context.Context, title string, year int) (*Result, error)
	Series(ctx context.Context, title string, year int) (*Result, error)
}
