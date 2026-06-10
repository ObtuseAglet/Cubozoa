package server

import (
	"net/http"

	"github.com/obtuseaglet/cubozoa/internal/jellyfin"
)

// localAddress determines the address Cubozoa advertises to clients. An
// explicitly configured public URL wins; otherwise we reflect the host the
// client connected on, honoring forwarded headers only for trusted proxies.
func (s *Server) localAddress(r *http.Request) string {
	if s.cfg.PublicBaseURL != "" {
		return s.cfg.PublicBaseURL
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if s.cfg.TrustedProxies {
		if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
			scheme = proto
		}
	}
	host := r.Host
	if s.cfg.TrustedProxies {
		if fwd := r.Header.Get("X-Forwarded-Host"); fwd != "" {
			host = fwd
		}
	}
	return scheme + "://" + host
}

// GET /System/Info/Public — unauthenticated server discovery.
func (s *Server) handleSystemInfoPublic(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusOK, jellyfin.PublicSystemInfo{
		LocalAddress:           s.localAddress(r),
		ServerName:             s.cfg.ServerName,
		Version:                jellyfin.EmulatedVersion,
		ProductName:            jellyfin.ProductName,
		OperatingSystem:        "",
		ID:                     s.store.ServerID(),
		StartupWizardCompleted: true,
	})
}

// GET /System/Info — authenticated, fuller system information.
func (s *Server) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusOK, jellyfin.SystemInfo{
		LocalAddress:               s.localAddress(r),
		ServerName:                 s.cfg.ServerName,
		Version:                    jellyfin.EmulatedVersion,
		ProductName:                jellyfin.ProductName,
		OperatingSystem:            "",
		OperatingSystemDisplayName: "",
		ID:                         s.store.ServerID(),
		StartupWizardCompleted:     true,
		PackageName:                "cubozoa",
		SupportsLibraryMonitor:     true,
		CanSelfRestart:             false,
		CanLaunchWebBrowser:        false,
		WebSocketPortNumber:        portFromBind(s.cfg.BindAddress),
	})
}

// GET|POST /System/Ping — lightweight liveness probe used by clients.
func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusOK, jellyfin.ProductName)
}

// portFromBind extracts the numeric port from a bind address like ":8096".
func portFromBind(bind string) int {
	port := 0
	for i := len(bind) - 1; i >= 0; i-- {
		if bind[i] == ':' {
			for _, c := range bind[i+1:] {
				if c < '0' || c > '9' {
					return 8096
				}
				port = port*10 + int(c-'0')
			}
			break
		}
	}
	if port == 0 {
		return 8096
	}
	return port
}
