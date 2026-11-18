package helpers

// MetadataLockKey is the metadata key that marks a resource as managed by an automatic provisioner. The controller never
// checks it, the cli and the web ui refuse to change such a resource so a manual edit does not fight the provisioner.
const MetadataLockKey = "flock.autoprovision"
const MetadataProvisionBy = "flock.provision.by"

// HeaderAgentUuid is the response header of a discovery stream giving the connecting agent the uuid of the declared
// agent its certificate names
const HeaderAgentUuid = "Flock-Agent-Uuid"
