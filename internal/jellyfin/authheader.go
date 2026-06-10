package jellyfin

import (
	"net/http"
	"strings"
)

// ClientAuth holds the client/device metadata a Jellyfin client supplies on
// every request, plus the access token if one is present.
//
// Jellyfin carries this in one of three places, in priority order:
//   - the Authorization header, scheme "MediaBrowser" (or legacy "Emby")
//   - the legacy X-Emby-Authorization header (same grammar)
//   - the X-Emby-Token / X-MediaBrowser-Token header, or ?api_key= (token only)
type ClientAuth struct {
	Token    string
	Client   string
	Device   string
	DeviceID string
	Version  string
}

// ParseClientAuth extracts client metadata and any access token from a request.
func ParseClientAuth(r *http.Request) ClientAuth {
	var ca ClientAuth

	header := r.Header.Get("Authorization")
	if header == "" {
		header = r.Header.Get("X-Emby-Authorization")
	}
	if header != "" {
		ca.parseAuthGrammar(header)
	}

	// A bare token may arrive via dedicated headers or query param. These never
	// override a token already supplied in the structured header.
	if ca.Token == "" {
		if t := r.Header.Get("X-Emby-Token"); t != "" {
			ca.Token = t
		} else if t := r.Header.Get("X-MediaBrowser-Token"); t != "" {
			ca.Token = t
		} else if t := r.URL.Query().Get("api_key"); t != "" {
			ca.Token = t
		}
	}

	return ca
}

// parseAuthGrammar parses the comma-separated key="value" grammar that follows
// the scheme name, e.g.:
//
//	MediaBrowser Client="Jellyfin Web", Device="Firefox", DeviceId="abc",
//	             Version="10.10.0", Token="..."
func (ca *ClientAuth) parseAuthGrammar(header string) {
	// Strip the scheme prefix ("MediaBrowser" or "Emby") if present.
	if i := strings.IndexByte(header, ' '); i >= 0 {
		scheme := strings.ToLower(strings.TrimSpace(header[:i]))
		if scheme == "mediabrowser" || scheme == "emby" {
			header = header[i+1:]
		}
	}

	for _, part := range splitParams(header) {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"`)
		switch strings.ToLower(key) {
		case "token":
			ca.Token = value
		case "client":
			ca.Client = value
		case "device":
			ca.Device = value
		case "deviceid":
			ca.DeviceID = value
		case "version":
			ca.Version = value
		}
	}
}

// splitParams splits on commas that are not inside a quoted value, so that a
// Device name like "Living Room, TV" is not torn apart.
func splitParams(s string) []string {
	var parts []string
	var b strings.Builder
	inQuotes := false
	for _, r := range s {
		switch {
		case r == '"':
			inQuotes = !inQuotes
			b.WriteRune(r)
		case r == ',' && !inQuotes:
			parts = append(parts, b.String())
			b.Reset()
		default:
			b.WriteRune(r)
		}
	}
	if b.Len() > 0 {
		parts = append(parts, b.String())
	}
	return parts
}
