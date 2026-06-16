// Package media implements Cubozoa's library model: scanning directories on
// disk into browsable items, and serving the queries that back the Jellyfin
// Items/Views endpoints.
package media

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/obtuseaglet/cubozoa/internal/store"
)

// Scan walks a library's directory and returns the media items found within it.
// It is a pure read of the filesystem — it does not touch the store — so it is
// easy to test and safe to run off the request path.
//
// Symlinks are not followed, which keeps a scan bounded to the library's own
// subtree and prevents a crafted link from escaping it.
func Scan(lib *store.Library) ([]*store.MediaItem, error) {
	root := lib.Path
	var items []*store.MediaItem
	lister := newDirLister()

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Skip unreadable entries rather than aborting the whole scan.
			return nil
		}
		if d.IsDir() {
			// Ignore hidden directories (e.g. .git, .Trash, @eaDir).
			if path != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 || strings.HasPrefix(d.Name(), ".") {
			return nil
		}

		kind, container := mediaKind(path)
		if kind == "" {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}

		title, year := parseName(d.Name())
		item := &store.MediaItem{
			ID:             itemID(path),
			LibraryID:      lib.ID,
			ParentID:       lib.ID,
			Name:           title,
			SortName:       strings.ToLower(title),
			Type:           itemTypeFor(lib.Type, kind),
			MediaType:      kind,
			Path:           path,
			Container:      container,
			ProductionYear: year,
			SizeBytes:      info.Size(),
			DateCreated:    info.ModTime().UTC(),
		}

		if primary, backdrop := findArtwork(lister, path); primary != "" || backdrop != "" {
			item.PrimaryImagePath = primary
			item.PrimaryImageTag = imageTag(primary)
			item.BackdropImagePath = backdrop
			item.BackdropImageTag = imageTag(backdrop)
		}

		items = append(items, item)
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Deterministic order keeps re-scans and tests stable.
	sort.Slice(items, func(i, j int) bool {
		if items[i].SortName != items[j].SortName {
			return items[i].SortName < items[j].SortName
		}
		return items[i].ID < items[j].ID
	})
	return items, nil
}

// itemID derives a stable identifier from a file's path, so re-scanning the
// same library does not create duplicate items or churn IDs that clients have
// already cached. The 16-byte hex form matches the shape clients expect.
func itemID(path string) string {
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:16])
}

// libraryID derives a stable identifier for a library from its path, for the
// same idempotency reasons as itemID.
func libraryID(path string) string {
	sum := sha256.Sum256([]byte("library:" + path))
	return hex.EncodeToString(sum[:16])
}
