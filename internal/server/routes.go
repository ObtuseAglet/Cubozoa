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
