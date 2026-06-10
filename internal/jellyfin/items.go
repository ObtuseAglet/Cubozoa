package jellyfin

// BaseItemDto is the universal item shape clients deserialize for everything
// from a library folder to a movie. Only the fields Cubozoa populates today are
// declared; clients tolerate omitted optional fields. The on-disk path is
// deliberately absent — it is never sent to clients.
type BaseItemDto struct {
	Name              string            `json:"Name"`
	ServerID          string            `json:"ServerId"`
	ID                string            `json:"Id"`
	Type              string            `json:"Type"`
	IsFolder          bool              `json:"IsFolder"`
	CollectionType    string            `json:"CollectionType,omitempty"`
	MediaType         string            `json:"MediaType,omitempty"`
	ParentID          string            `json:"ParentId,omitempty"`
	ProductionYear    int               `json:"ProductionYear,omitempty"`
	RunTimeTicks      int64             `json:"RunTimeTicks,omitempty"`
	Container         string            `json:"Container,omitempty"`
	SortName          string            `json:"SortName,omitempty"`
	DateCreated       string            `json:"DateCreated,omitempty"`
	ChildCount        int               `json:"ChildCount,omitempty"`
	LocationType      string            `json:"LocationType,omitempty"`
	ImageTags         map[string]string `json:"ImageTags,omitempty"`
	BackdropImageTags []string          `json:"BackdropImageTags,omitempty"`
	UserData          *UserItemDataDto  `json:"UserData,omitempty"`
}

// UserItemDataDto carries per-user playback state for an item. For now these
// are zero values; playback progress lands in a later milestone.
type UserItemDataDto struct {
	PlaybackPositionTicks int64  `json:"PlaybackPositionTicks"`
	PlayCount             int    `json:"PlayCount"`
	IsFavorite            bool   `json:"IsFavorite"`
	Played                bool   `json:"Played"`
	Key                   string `json:"Key,omitempty"`
}

// VirtualFolderInfo is the admin-facing description of a library, returned from
// GET /Library/VirtualFolders.
type VirtualFolderInfo struct {
	Name           string   `json:"Name"`
	ItemID         string   `json:"ItemId"`
	CollectionType string   `json:"CollectionType"`
	Locations      []string `json:"Locations"`
}
