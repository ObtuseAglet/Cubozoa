// Package jellyfin contains the wire-format DTOs and protocol helpers that make
// Cubozoa speak the Jellyfin client API.
//
// These types mirror the JSON shapes that existing Jellyfin clients expect.
// Field names and casing are significant: clients deserialize by PascalCase
// name, so the struct tags here are part of the compatibility contract.
package jellyfin

const (
	// EmulatedVersion is the Jellyfin API version Cubozoa reports to clients.
	// Clients use this for feature gating, so it must look like a real, recent
	// Jellyfin release. This is distinct from Cubozoa's own product version.
	EmulatedVersion = "10.10.3"

	// ProductName is advertised to clients as the server product.
	ProductName = "Cubozoa"
)
