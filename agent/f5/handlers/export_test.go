package handlers

import (
	"github.com/seagalia/flock/sdk/agentsdk/discoverers"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
)

// SetDiscoverers replaces how the handler makes discoverers, for specs without an EDS or DNS server.
func (h *BackendHandler) SetDiscoverers(makeDiscoverer func(*backend.Backend) (discoverers.Discoverer, error)) {
	h.makeDiscoverer = makeDiscoverer
}
