package server

import "net/http"

// routes registers the Jellyfin-compatible endpoints implemented in milestone 1
// (server discovery, branding, and the full authentication/login flow), plus
// the handful of bootstrap endpoints a client touches on its way to the home
// screen.
//
// Go's ServeMux (1.22+) selects the most specific pattern, so static routes like
// "/Users/Me" take precedence over the "/Users/{id}" wildcard automatically.
func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()

	// --- Discovery & health (anonymous) ---
	mux.HandleFunc("GET /System/Info/Public", s.handleSystemInfoPublic)
	mux.HandleFunc("GET /System/Ping", s.handlePing)
	mux.HandleFunc("POST /System/Ping", s.handlePing)
	mux.HandleFunc("GET /health", s.handleHealth)

	// --- Branding (anonymous; shown before login) ---
	mux.HandleFunc("GET /Branding/Configuration", s.handleBrandingConfiguration)
	mux.HandleFunc("GET /Branding/Css", s.handleBrandingCss)
	mux.HandleFunc("GET /Branding/Css.css", s.handleBrandingCss)

	// --- Login flow ---
	mux.HandleFunc("GET /Users/Public", s.handleUsersPublic)
	mux.HandleFunc("POST /Users/AuthenticateByName", s.handleAuthenticateByName)
	mux.HandleFunc("GET /QuickConnect/Enabled", s.handleQuickConnectEnabled)

	// --- Authenticated system info ---
	mux.HandleFunc("GET /System/Info", s.requireAuth(s.handleSystemInfo))

	// --- Users (authenticated) ---
	mux.HandleFunc("GET /Users/Me", s.requireAuth(s.handleUsersMe))
	mux.HandleFunc("GET /Users", s.requireAdmin(s.handleUsersList))
	mux.HandleFunc("GET /Users/{id}", s.requireAuth(s.handleUserByID))
	mux.HandleFunc("GET /Users/{id}/Views", s.requireAuth(s.handleUserViews))
	mux.HandleFunc("GET /UserViews", s.requireAuth(s.handleUserViews))

	// --- Library browse (authenticated) ---
	mux.HandleFunc("GET /Items", s.requireAuth(s.handleItems))
	mux.HandleFunc("GET /Items/{itemId}", s.requireAuth(s.handleItemDetail))
	mux.HandleFunc("GET /Items/Latest", s.requireAuth(s.handleItemsLatest))
	mux.HandleFunc("GET /Users/{userId}/Items", s.requireAuth(s.handleItems))
	mux.HandleFunc("GET /Users/{userId}/Items/Latest", s.requireAuth(s.handleItemsLatest))
	mux.HandleFunc("GET /Users/{userId}/Items/{itemId}", s.requireAuth(s.handleItemDetail))
	mux.HandleFunc("GET /Shows/NextUp", s.requireAuth(s.handleNextUp))
	mux.HandleFunc("GET /Shows/{seriesId}/Seasons", s.requireAuth(s.handleSeasons))
	mux.HandleFunc("GET /Shows/{seriesId}/Episodes", s.requireAuth(s.handleEpisodes))
	mux.HandleFunc("GET /Users/{userId}/Items/Resume", s.requireAuth(s.handleResumeItems))

	// --- Watched & favorite state (authenticated) ---
	mux.HandleFunc("POST /Users/{userId}/PlayedItems/{itemId}", s.requireAuth(s.handleMarkPlayed))
	mux.HandleFunc("DELETE /Users/{userId}/PlayedItems/{itemId}", s.requireAuth(s.handleMarkUnplayed))
	mux.HandleFunc("POST /Users/{userId}/FavoriteItems/{itemId}", s.requireAuth(s.handleAddFavorite))
	mux.HandleFunc("DELETE /Users/{userId}/FavoriteItems/{itemId}", s.requireAuth(s.handleRemoveFavorite))

	// --- Playback (authenticated) ---
	mux.HandleFunc("GET /Items/{itemId}/PlaybackInfo", s.requireAuth(s.handlePlaybackInfo))
	mux.HandleFunc("POST /Items/{itemId}/PlaybackInfo", s.requireAuth(s.handlePlaybackInfo))
	// A GET pattern also matches HEAD in net/http's mux, so HEAD streaming and
	// image probes work without a separate (and here, conflicting) registration.
	mux.HandleFunc("GET /Videos/{id}/main.m3u8", s.requireAuth(s.handleHlsPlaylist))
	mux.HandleFunc("GET /Videos/{id}/master.m3u8", s.requireAuth(s.handleHlsPlaylist))
	mux.HandleFunc("GET /Videos/{id}/hls/{seg}", s.requireAuth(s.handleHlsSegment))
	mux.HandleFunc("GET /Videos/{id}/{file}", s.requireAuth(s.handleVideoStream))
	mux.HandleFunc("POST /Sessions/Playing", s.requireAuth(s.handlePlaybackReport))
	mux.HandleFunc("POST /Sessions/Playing/Progress", s.requireAuth(s.handlePlaybackReport))
	mux.HandleFunc("POST /Sessions/Playing/Stopped", s.requireAuth(s.handlePlaybackReport))

	// --- Item images (authenticated; clients append ?api_key=) ---
	// Registered for GET and HEAD, with and without the optional image index.
	mux.HandleFunc("GET /Items/{id}/Images/{type}", s.requireAuth(s.handleItemImage))
	mux.HandleFunc("GET /Items/{id}/Images/{type}/{index}", s.requireAuth(s.handleItemImage))

	// --- Library administration ---
	mux.HandleFunc("GET /Library/VirtualFolders", s.requireAdmin(s.handleVirtualFolders))
	mux.HandleFunc("POST /Library/Refresh", s.requireAdmin(s.handleLibraryRefresh))

	// --- Display preferences (authenticated) ---
	mux.HandleFunc("GET /DisplayPreferences/{id}", s.requireAuth(s.handleGetDisplayPreferences))
	mux.HandleFunc("POST /DisplayPreferences/{id}", s.requireAuth(s.handleUpdateDisplayPreferences))

	// --- Sessions (authenticated) ---
	mux.HandleFunc("POST /Sessions/Logout", s.requireAuth(s.handleLogout))
	mux.HandleFunc("POST /Sessions/Capabilities/Full", s.requireAuth(s.handleSessionCapabilities))

	return mux
}

// GET /health — operational health check, distinct from the Jellyfin client
// ping. Returns 200 with a tiny JSON body for load balancers and uptime probes.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
