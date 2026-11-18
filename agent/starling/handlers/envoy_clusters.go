package handlers

import (
	oerrors "github.com/seagalia/flock/sdk/errors"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"

	"github.com/seagalia/flock/agent/starling/backendctl"
	"github.com/seagalia/flock/agent/starling/envoyctl"
)

// releaseEnvoyCluster stops following the backend once no virtual server uses it anymore in envoy
func releaseEnvoyCluster(jointer backendctl.Jointer, configWatcher *envoyctl.ConfigWatcher, aVip *vip.VIP, virtServ *vs.VirtualServer, backendUuid string) error {
	if !configWatcher.ReleaseBackend(aVip, virtServ, backendUuid) {
		return nil
	}
	err := jointer.RemoveEndpointListener(backendUuid, configWatcher)
	// backend may have been removed before the virtual server, listener must still be removed
	if err != nil && !oerrors.IsErrNotFound(err) {
		return err
	}
	jointer.RemoveListener(backendUuid, configWatcher)
	return nil
}

func releaseDroppedEnvoyClusters(jointer backendctl.Jointer, configWatcher *envoyctl.ConfigWatcher, aVip *vip.VIP, virtServ *vs.VirtualServer, previous, current []*backend.Backend) error {
	kept := make(map[string]bool)
	for _, aBackend := range current {
		kept[aBackend.GetUUID()] = true
	}
	for _, aBackend := range previous {
		if kept[aBackend.GetUUID()] {
			continue
		}
		if err := releaseEnvoyCluster(jointer, configWatcher, aVip, virtServ, aBackend.GetUUID()); err != nil {
			return err
		}
	}
	return nil
}
