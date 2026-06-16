package media

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// imageExtensions are the artwork formats Cubozoa recognizes on disk.
var imageExtensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".webp": true,
	".gif": true, ".bmp": true,
}

// imageContentType maps an artwork file extension to its MIME type. An unknown
// extension yields "application/octet-stream" so we never mislabel content.
func imageContentType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	case ".bmp":
		return "image/bmp"
	default:
		return "application/octet-stream"
	}
}

// dirInfo is a cached listing of one directory.
type dirInfo struct {
	files      []string // regular-file names
	videoCount int      // how many of them are videos
}

// dirLister caches directory listings for the duration of a scan so artwork
// lookup does not re-read the same directory once per media file.
type dirLister struct {
	cache map[string]*dirInfo
}

func newDirLister() *dirLister {
	return &dirLister{cache: make(map[string]*dirInfo)}
}

// info returns the cached listing for dir, reading it on first access.
func (d *dirLister) info(dir string) *dirInfo {
	if di, ok := d.cache[dir]; ok {
		return di
	}
	di := &dirInfo{}
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			di.files = append(di.files, e.Name())
			if k, _ := mediaKind(e.Name()); k == "Video" {
				di.videoCount++
			}
		}
	}
	d.cache[dir] = di
	return di
}

// findArtwork locates a primary (poster) and backdrop (fanart) image for a media
// file. Name-matched artwork (e.g. "Movie.jpg", "Movie-fanart.jpg") always wins;
// generic names (poster/folder/fanart/backdrop) only apply when the directory
// holds a single video, so a shared folder of loose files is not mislabeled.
func findArtwork(d *dirLister, videoPath string) (primary, backdrop string) {
	dir := filepath.Dir(videoPath)
	base := strings.TrimSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))
	di := d.info(dir)
	sole := di.videoCount == 1

	lower := make(map[string]string, len(di.files)) // lowercased name -> actual name
	for _, n := range di.files {
		if imageExtensions[strings.ToLower(filepath.Ext(n))] {
			lower[strings.ToLower(n)] = n
		}
	}

	primaryNamed := []string{base + "-poster", base + "-thumb", base}
	primaryGeneric := []string{"poster", "folder", "cover", "default", "movie"}
	backdropNamed := []string{base + "-fanart", base + "-backdrop"}
	backdropGeneric := []string{"fanart", "backdrop", "background", "art"}

	primary = pickImage(lower, dir, primaryNamed)
	if primary == "" && sole {
		primary = pickImage(lower, dir, primaryGeneric)
	}
	backdrop = pickImage(lower, dir, backdropNamed)
	if backdrop == "" && sole {
		backdrop = pickImage(lower, dir, backdropGeneric)
	}
	return primary, backdrop
}

// pickImage returns the absolute path of the first stem that exists as an image
// in the directory, trying each recognized extension.
func pickImage(lower map[string]string, dir string, stems []string) string {
	for _, stem := range stems {
		for ext := range imageExtensions {
			if actual, ok := lower[strings.ToLower(stem)+ext]; ok {
				return filepath.Join(dir, actual)
			}
		}
	}
	return ""
}

// imageTag fingerprints an image file by path, size and mod time, so a client's
// cached copy is invalidated only when the underlying file actually changes.
func imageTag(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	h := sha256.New()
	h.Write([]byte(path))
	h.Write([]byte(strconv.FormatInt(info.Size(), 10)))
	h.Write([]byte(strconv.FormatInt(info.ModTime().UnixNano(), 10)))
	return hex.EncodeToString(h.Sum(nil)[:16])
}
