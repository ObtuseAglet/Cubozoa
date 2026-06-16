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

// itemToDto maps a media item to its client representation. The filesystem path
// is intentionally never copied into the DTO.
func (s *Server) itemToDto(it *store.MediaItem) jellyfin.BaseItemDto {
	dto := jellyfin.BaseItemDto{
		Name:           it.Name,
		ServerID:       s.store.ServerID(),
		ID:             it.ID,
		Type:           it.Type,
		MediaType:      it.MediaType,
		IsFolder:       it.Type == "Folder",
		ParentID:       it.ParentID,
		ProductionYear: it.ProductionYear,
		Container:      it.Container,
		SortName:       it.SortName,
		LocationType:   "FileSystem",
		UserData:       &jellyfin.UserItemDataDto{Key: it.ID},
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

func (s *Server) itemsToDtos(items []*store.MediaItem) []jellyfin.BaseItemDto {
	out := make([]jellyfin.BaseItemDto, 0, len(items))
	for _, it := range items {
		out = append(out, s.itemToDto(it))
	}
	return out
}
