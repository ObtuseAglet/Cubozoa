// Package userdata manages per-user playback state: resume position, play
// count, watched status and favorites. It is intentionally separate from the
// shared media catalog so a library re-scan never disturbs a user's progress.
package userdata

import (
	"errors"
	"time"

	"github.com/obtuseaglet/cubozoa/internal/store"
)

// Service provides per-user item-data operations over a Store.
type Service struct {
	store store.Store
}

// New constructs a userdata Service.
func New(st store.Store) *Service {
	return &Service{store: st}
}

// Get returns the user's data for an item, or a zeroed record (not an error)
// when none exists yet. Callers can always rely on a non-nil result.
func (s *Service) Get(userID, itemID string) *store.UserItemData {
	d, err := s.store.GetUserItemData(userID, itemID)
	if err != nil {
		return &store.UserItemData{UserID: userID, ItemID: itemID}
	}
	return d
}

// Map returns all of a user's item data keyed by item ID, for efficiently
// decorating a page of browse results without a lookup per item.
func (s *Service) Map(userID string) map[string]*store.UserItemData {
	list, err := s.store.ListUserItemData(userID)
	if err != nil {
		return nil
	}
	m := make(map[string]*store.UserItemData, len(list))
	for _, d := range list {
		m[d.ItemID] = d
	}
	return m
}

// ReportPosition records a resume position from a playback progress report. A
// non-positive position is treated as "start over" and clears any resume mark.
func (s *Service) ReportPosition(userID, itemID string, ticks int64) error {
	if itemID == "" {
		return errors.New("userdata: empty item id")
	}
	d := s.Get(userID, itemID)
	if ticks < 0 {
		ticks = 0
	}
	d.PlaybackPositionTicks = ticks
	d.LastPlayedAt = time.Now().UTC()
	return s.store.UpsertUserItemData(d)
}

// MarkPlayed marks an item watched: it sets Played, increments the play count,
// and clears the resume position (matching Jellyfin's behavior).
func (s *Service) MarkPlayed(userID, itemID string) (*store.UserItemData, error) {
	d := s.Get(userID, itemID)
	d.Played = true
	d.PlayCount++
	d.PlaybackPositionTicks = 0
	d.LastPlayedAt = time.Now().UTC()
	return d, s.store.UpsertUserItemData(d)
}

// MarkUnplayed clears watched status and resume position.
func (s *Service) MarkUnplayed(userID, itemID string) (*store.UserItemData, error) {
	d := s.Get(userID, itemID)
	d.Played = false
	d.PlaybackPositionTicks = 0
	return d, s.store.UpsertUserItemData(d)
}

// SetFavorite toggles the favorite flag.
func (s *Service) SetFavorite(userID, itemID string, fav bool) (*store.UserItemData, error) {
	d := s.Get(userID, itemID)
	d.IsFavorite = fav
	return d, s.store.UpsertUserItemData(d)
}

// Resumable returns the user's in-progress item IDs (a resume position set and
// not yet watched), most recently played first.
func (s *Service) Resumable(userID string) []string {
	list, err := s.store.ListUserItemData(userID)
	if err != nil {
		return nil
	}
	resumable := make([]*store.UserItemData, 0)
	for _, d := range list {
		if d.PlaybackPositionTicks > 0 && !d.Played {
			resumable = append(resumable, d)
		}
	}
	// Most recent first.
	sortByLastPlayedDesc(resumable)
	ids := make([]string, len(resumable))
	for i, d := range resumable {
		ids[i] = d.ItemID
	}
	return ids
}

func sortByLastPlayedDesc(d []*store.UserItemData) {
	for i := 1; i < len(d); i++ {
		for j := i; j > 0 && d[j].LastPlayedAt.After(d[j-1].LastPlayedAt); j-- {
			d[j], d[j-1] = d[j-1], d[j]
		}
	}
}
