package server

import (
	"time"

	"github.com/obtuseaglet/cubozoa/internal/jellyfin"
	"github.com/obtuseaglet/cubozoa/internal/store"
)

// libraryToDto maps a library to the "CollectionFolder" item clients render as
// a top-level view on the home screen.
func (s *Server) libraryToDto(lib *store.Library) jellyfin.BaseItemDto {
	return jellyfin.BaseItemDto{
		Name:           lib.Name,
		ServerID:       s.store.ServerID(),
		ID:             lib.ID,
		Type:           "CollectionFolder",
		CollectionType: lib.Type,
		IsFolder:       true,
		ChildCount:     lib.ItemCount,
		LocationType:   "FileSystem",
	}
}

// itemToDto maps a media item to its client representation, decorated with the
// requesting user's playback state (ud may be nil). The filesystem path is
// intentionally never copied into the DTO.
func (s *Server) itemToDto(it *store.MediaItem, ud *store.UserItemData) jellyfin.BaseItemDto {
	dto := jellyfin.BaseItemDto{
		Name:              it.Name,
		ServerID:          s.store.ServerID(),
		ID:                it.ID,
		Type:              it.Type,
		MediaType:         it.MediaType,
		IsFolder:          isFolderType(it.Type),
		ParentID:          it.ParentID,
		ProductionYear:    it.ProductionYear,
		Container:         it.Container,
		SortName:          it.SortName,
		RunTimeTicks:      it.RunTimeTicks,
		ChildCount:        it.ChildCount,
		SeriesID:          it.SeriesID,
		SeriesName:        it.SeriesName,
		SeasonID:          it.SeasonID,
		IndexNumber:       it.IndexNumber,
		ParentIndexNumber: it.ParentIndexNumber,
		LocationType:      "FileSystem",
		UserData:          userDataDto(it.ID, ud),
	}
	if !it.DateCreated.IsZero() {
		dto.DateCreated = it.DateCreated.Format(time.RFC3339Nano)
	}
	// Advertise available artwork by tag so clients know to request it (and can
	// cache-bust). The image bytes are served from /Items/{id}/Images/{type}.
	if it.PrimaryImageTag != "" {
		dto.ImageTags = map[string]string{"Primary": it.PrimaryImageTag}
	}
	if it.BackdropImageTag != "" {
		dto.BackdropImageTags = []string{it.BackdropImageTag}
	}
	return dto
}

// isFolderType reports whether an item type is a browsable container.
func isFolderType(t string) bool {
	switch t {
	case "Series", "Season", "Folder":
		return true
	default:
		return false
	}
}

// userDataDto builds the per-user playback DTO. A nil ud yields a clean,
// never-played record so the field is always present for clients.
func userDataDto(itemID string, ud *store.UserItemData) *jellyfin.UserItemDataDto {
	dto := &jellyfin.UserItemDataDto{Key: itemID}
	if ud != nil {
		dto.PlaybackPositionTicks = ud.PlaybackPositionTicks
		dto.PlayCount = ud.PlayCount
		dto.Played = ud.Played
		dto.IsFavorite = ud.IsFavorite
	}
	return dto
}

// itemsToDtos maps items, decorating each with the given user's data. Pass the
// map from userdata.Service.Map to avoid a store lookup per item.
func (s *Server) itemsToDtos(items []*store.MediaItem, udByItem map[string]*store.UserItemData) []jellyfin.BaseItemDto {
	out := make([]jellyfin.BaseItemDto, 0, len(items))
	for _, it := range items {
		out = append(out, s.itemToDto(it, udByItem[it.ID]))
	}
	return out
}
