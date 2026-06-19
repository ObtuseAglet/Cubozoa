// Package media implements Cubozoa's library model: scanning directories on
// disk into browsable items, and serving the queries that back the Jellyfin
// Items/Views endpoints.
package media

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/obtuseaglet/cubozoa/internal/store"
)

// scannedFile is a media file discovered on disk during a walk.
type scannedFile struct {
	path      string
	relParts  []string // path segments relative to the library root
	kind      string   // Video | Audio
	container string
	info      fs.FileInfo
}

// Scan walks a library's directory and returns the media items found within it.
// It is a pure read of the filesystem — it does not touch the store — so it is
// easy to test and safe to run off the request path.
//
// Symlinks are not followed, which keeps a scan bounded to the library's own
// subtree and prevents a crafted link from escaping it. The structure produced
// depends on the library type: a "tvshows" library is organized into a
// Series → Season → Episode hierarchy; every other type is a flat list.
func Scan(lib *store.Library) ([]*store.MediaItem, error) {
	files, err := collectFiles(lib.Path)
	if err != nil {
		return nil, err
	}

	lister := newDirLister()
	var items []*store.MediaItem
	switch lib.Type {
	case "tvshows":
		items = buildEpisodes(lib, files, lister)
	case "music":
		items = buildTracks(lib, files, lister)
	default:
		items = buildFlat(lib, files, lister)
	}

	sortItemsStable(items)
	return items, nil
}

// collectFiles walks the library root and returns the media files within it.
func collectFiles(root string) ([]scannedFile, error) {
	var files []scannedFile
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if d.IsDir() {
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
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		files = append(files, scannedFile{
			path:      path,
			relParts:  strings.Split(filepath.ToSlash(rel), "/"),
			kind:      kind,
			container: container,
			info:      info,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

// buildFlat produces one item per media file (Movie/Audio/Video), parented
// directly to the library.
func buildFlat(lib *store.Library, files []scannedFile, lister *dirLister) []*store.MediaItem {
	items := make([]*store.MediaItem, 0, len(files))
	for _, f := range files {
		title, year := parseName(filepath.Base(f.path))
		item := &store.MediaItem{
			ID:             itemID(f.path),
			LibraryID:      lib.ID,
			ParentID:       lib.ID,
			Name:           title,
			SortName:       strings.ToLower(title),
			Type:           itemTypeFor(lib.Type, f.kind),
			MediaType:      f.kind,
			Path:           f.path,
			Container:      f.container,
			ProductionYear: year,
			SizeBytes:      f.info.Size(),
			DateCreated:    f.info.ModTime().UTC(),
		}
		applyArtwork(item, lister, f.path)
		item.Subtitles = findSubtitles(lister, f.path)
		items = append(items, item)
	}
	return items
}

// buildEpisodes organizes a tvshows library into Series → Season → Episode.
// Series and Season items are synthetic folders (no media file); each Episode
// links back to its season and series and carries its numbering.
func buildEpisodes(lib *store.Library, files []scannedFile, lister *dirLister) []*store.MediaItem {
	series := map[string]*store.MediaItem{}  // seriesID -> Series
	seasons := map[string]*store.MediaItem{} // seasonID -> Season
	var episodes []*store.MediaItem

	for _, f := range files {
		seriesName, seasonNum, epNum, epTitle := parseEpisode(f.relParts)

		sID := seriesID(lib.ID, seriesName)
		if _, ok := series[sID]; !ok {
			s := &store.MediaItem{
				ID:          sID,
				LibraryID:   lib.ID,
				ParentID:    lib.ID,
				Name:        seriesName,
				SortName:    strings.ToLower(seriesName),
				Type:        "Series",
				DateCreated: f.info.ModTime().UTC(),
				SeriesID:    sID,
				SeriesName:  seriesName,
			}
			// Series artwork lives in the series folder (first relative segment).
			applyFolderArtwork(s, lister, filepath.Join(lib.Path, f.relParts[0]))
			series[sID] = s
		}

		seID := seasonID(sID, seasonNum)
		if _, ok := seasons[seID]; !ok {
			seasons[seID] = &store.MediaItem{
				ID:          seID,
				LibraryID:   lib.ID,
				ParentID:    sID,
				Name:        fmt.Sprintf("Season %d", seasonNum),
				SortName:    fmt.Sprintf("%05d", seasonNum),
				Type:        "Season",
				DateCreated: f.info.ModTime().UTC(),
				SeriesID:    sID,
				SeriesName:  seriesName,
				IndexNumber: seasonNum,
			}
		}

		ep := &store.MediaItem{
			ID:                itemID(f.path),
			LibraryID:         lib.ID,
			ParentID:          seID,
			Name:              epTitle,
			SortName:          fmt.Sprintf("%05d", epNum),
			Type:              "Episode",
			MediaType:         f.kind,
			Path:              f.path,
			Container:         f.container,
			SizeBytes:         f.info.Size(),
			DateCreated:       f.info.ModTime().UTC(),
			SeriesID:          sID,
			SeriesName:        seriesName,
			SeasonID:          seID,
			IndexNumber:       epNum,
			ParentIndexNumber: seasonNum,
		}
		applyArtwork(ep, lister, f.path)
		ep.Subtitles = findSubtitles(lister, f.path)
		episodes = append(episodes, ep)
		seasons[seID].ChildCount++
	}

	// Count seasons per series.
	for _, se := range seasons {
		if s, ok := series[se.SeriesID]; ok {
			s.ChildCount++
		}
	}

	out := make([]*store.MediaItem, 0, len(series)+len(seasons)+len(episodes))
	for _, s := range series {
		out = append(out, s)
	}
	for _, se := range seasons {
		out = append(out, se)
	}
	out = append(out, episodes...)
	return out
}

// buildTracks organizes a music library into MusicArtist → MusicAlbum → Audio.
// Artist and Album are synthetic folder items; each track links back to them
// and carries its track number.
func buildTracks(lib *store.Library, files []scannedFile, lister *dirLister) []*store.MediaItem {
	artists := map[string]*store.MediaItem{}
	albums := map[string]*store.MediaItem{}
	var tracks []*store.MediaItem

	for _, f := range files {
		artistName, albumName, trackNum, title := parseTrack(f.relParts)

		aID := artistID(lib.ID, artistName)
		if _, ok := artists[aID]; !ok {
			art := &store.MediaItem{
				ID:          aID,
				LibraryID:   lib.ID,
				ParentID:    lib.ID,
				Name:        artistName,
				SortName:    strings.ToLower(artistName),
				Type:        "MusicArtist",
				DateCreated: f.info.ModTime().UTC(),
				ArtistID:    aID,
			}
			applyFolderArtwork(art, lister, filepath.Join(lib.Path, f.relParts[0]))
			artists[aID] = art
		}

		alID := albumID(aID, albumName)
		if _, ok := albums[alID]; !ok {
			al := &store.MediaItem{
				ID:          alID,
				LibraryID:   lib.ID,
				ParentID:    aID,
				Name:        albumName,
				SortName:    strings.ToLower(albumName),
				Type:        "MusicAlbum",
				DateCreated: f.info.ModTime().UTC(),
				ArtistID:    aID,
				AlbumID:     alID,
				Album:       albumName,
				AlbumArtist: artistName,
				Artists:     []string{artistName},
			}
			if len(f.relParts) >= 3 {
				applyFolderArtwork(al, lister, filepath.Join(lib.Path, f.relParts[0], f.relParts[1]))
			}
			albums[alID] = al
		}

		track := &store.MediaItem{
			ID:          itemID(f.path),
			LibraryID:   lib.ID,
			ParentID:    alID,
			Name:        title,
			SortName:    fmt.Sprintf("%04d", trackNum),
			Type:        "Audio",
			MediaType:   "Audio",
			Path:        f.path,
			Container:   f.container,
			SizeBytes:   f.info.Size(),
			DateCreated: f.info.ModTime().UTC(),
			ArtistID:    aID,
			AlbumID:     alID,
			Album:       albumName,
			AlbumArtist: artistName,
			Artists:     []string{artistName},
			IndexNumber: trackNum,
		}
		applyArtwork(track, lister, f.path)
		tracks = append(tracks, track)
		albums[alID].ChildCount++
	}

	for _, al := range albums {
		if art, ok := artists[al.ArtistID]; ok {
			art.ChildCount++
		}
	}

	out := make([]*store.MediaItem, 0, len(artists)+len(albums)+len(tracks))
	for _, a := range artists {
		out = append(out, a)
	}
	for _, al := range albums {
		out = append(out, al)
	}
	out = append(out, tracks...)
	return out
}

// applyArtwork attaches name-matched poster/backdrop images for a media file.
func applyArtwork(item *store.MediaItem, lister *dirLister, path string) {
	if primary, backdrop := findArtwork(lister, path); primary != "" || backdrop != "" {
		item.PrimaryImagePath = primary
		item.PrimaryImageTag = imageTag(primary)
		item.BackdropImagePath = backdrop
		item.BackdropImageTag = imageTag(backdrop)
	}
}

// applyFolderArtwork attaches a poster/backdrop found directly in a folder
// (used for Series, which have no single backing file).
func applyFolderArtwork(item *store.MediaItem, lister *dirLister, dir string) {
	di := lister.info(dir)
	lower := map[string]string{}
	for _, n := range di.files {
		if imageExtensions[strings.ToLower(filepath.Ext(n))] {
			lower[strings.ToLower(n)] = n
		}
	}
	if p := pickImage(lower, dir, []string{"poster", "folder", "cover", "default", "show"}); p != "" {
		item.PrimaryImagePath = p
		item.PrimaryImageTag = imageTag(p)
	}
	if b := pickImage(lower, dir, []string{"fanart", "backdrop", "background", "art"}); b != "" {
		item.BackdropImagePath = b
		item.BackdropImageTag = imageTag(b)
	}
}

func sortItemsStable(items []*store.MediaItem) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].SortName != items[j].SortName {
			return items[i].SortName < items[j].SortName
		}
		return items[i].ID < items[j].ID
	})
}

// itemID derives a stable identifier from a file's path, so re-scanning the
// same library does not create duplicate items or churn IDs that clients have
// already cached. The 16-byte hex form matches the shape clients expect.
func itemID(path string) string {
	return hashID("item:" + path)
}

func seriesID(libraryID, name string) string {
	return hashID("series:" + libraryID + ":" + strings.ToLower(name))
}

func seasonID(sID string, season int) string {
	return hashID(fmt.Sprintf("season:%s:%d", sID, season))
}

func artistID(libraryID, name string) string {
	return hashID("artist:" + libraryID + ":" + strings.ToLower(name))
}

func albumID(aID, name string) string {
	return hashID("album:" + aID + ":" + strings.ToLower(name))
}

// libraryID derives a stable identifier for a library from its path, for the
// same idempotency reasons as itemID.
func libraryID(path string) string {
	return hashID("library:" + path)
}

func hashID(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:16])
}
