package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"

	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/protobuf/types/known/emptypb"
)

const (
	lockExplanation      = "Managed automatically (metadata flock.autoprovision). Change it where it was provisioned."
	provisionerKeyPrefix = "flock.provisioner."
)

func isLocked(metadata map[string]string) bool {
	return metadata[helpers.MetadataLockKey] == "true"
}

// refuseLocked is the error of a change aimed at a locked resource, nil when it is not locked.
func refuseLocked(kind, name string, metadata map[string]string) error {
	if !isLocked(metadata) {
		return nil
	}
	return fmt.Errorf("%s %s is locked. %s", kind, name, lockExplanation)
}

// refuseLockedDelete is refuseLocked for the delete of a whole resource, which an admin may force with forceLocked.
// The controller never checks the lock: the admin rule only lives in the clients, like the lock itself.
func refuseLockedDelete(
	ctx context.Context, client *clients.Client, kind, name string, metadata map[string]string, forceLocked bool,
) error {
	refusal := refuseLocked(kind, name, metadata)
	if refusal == nil {
		return nil
	}
	me, err := client.Login().Me(ctx, &emptypb.Empty{})
	if !forceLocked {
		// the refusal is what matters, a failed Me only costs the hint
		if err == nil && me.GetIsAdmin() {
			return fmt.Errorf("%w Add --force-locked to delete it anyway", refusal)
		}
		return refusal
	}
	if err != nil {
		return fmt.Errorf("check that you are an admin: %w", err)
	}
	if !me.GetIsAdmin() {
		return fmt.Errorf("%s %s is locked and only an admin can force its delete", kind, name)
	}
	msg.Warningf("%s %s is locked, %s. It may create it again.", kind, name, provisionedBy(metadata))
	return nil
}

// provisionedBy names the provisioners and the agent that marked the resource, as far as its metadata tells.
func provisionedBy(metadata map[string]string) string {
	var names []string
	for key, value := range metadata {
		if value == "true" && strings.HasPrefix(key, provisionerKeyPrefix) {
			names = append(names, strings.TrimPrefix(key, provisionerKeyPrefix))
		}
	}
	slices.Sort(names)
	text := "managed by a provisioner"
	if len(names) > 0 {
		text = "managed by the provisioner " + strings.Join(names, ", ")
	}
	if agent := metadata[helpers.MetadataProvisionBy]; agent != "" {
		text += fmt.Sprintf(" (agent %s)", agent)
	}
	return text
}

// refuseLockAdded refuses a desired resource that would set the lock key, which belongs to the provisioner. Keeping a
// key the stored resource already holds is not an addition. Pass nil as before for a new resource.
func refuseLockAdded(before, after map[string]string) error {
	_, isPresent := after[helpers.MetadataLockKey]
	wasPresent := false
	if before != nil {
		_, wasPresent = before[helpers.MetadataLockKey]
	}
	if !isPresent || wasPresent {
		return nil
	}
	return fmt.Errorf("metadata %s is reserved to automatic provisioning, remove it from the resource", helpers.MetadataLockKey)
}
