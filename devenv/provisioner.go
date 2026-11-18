package devenv

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/Masterminds/semver/v3"
	provisionerapp "github.com/seagalia/flock/provisioners/app"
	provisionerconfig "github.com/seagalia/flock/provisioners/config"
	"github.com/seagalia/flock/sdk/logging"
)

// provisionerConfigTemplate is text rather than a marshalled struct so the file the user edits keeps its comments
const provisionerConfigTemplate = `# The provisioner of flock dev, written once: edit it and restart flock dev, remove it to get the defaults back.
# flock dev ignores the log block: its --log-level sets the logging of every app it runs in its process.
controller:
  addr: %s
  server_name: localhost
  # the instance seeded in ~/.flock/dev/store.db: after removing the store, remove this file too to get the new one
  instance_uuid: %s
tls:
  cert_file: %s
  key_file: %s
  ca_file: %s
server:
  listen_host: %s
  # 18000 is the dev agent's, 18001 the default of a starling agent list
  listen_port: %d
acme:
  enabled: true
  # staging: its rate limits are loose and its certificates are not trusted
  default_ca_dir_url: https://acme-staging-v02.api.letsencrypt.org/directory
  challenges:
    http01_enabled: false
    tls_alpn_01_enabled: false
    host: 0.0.0.0
    # unprivileged ports, the CA reaches 80 and 443 only, so forward them here to pass a challenge
    http_port: 5002
    tls_port: 5001
`

// ensureProvisionerConfig writes the default provisioner config in path when it is missing, then reads the file as it
// is: a file the user edited is never rewritten. Its controller address follows controllerAddr, which changes with
// --listen-host, and its log level is replaced by logLevel, since the apps of one process share one logging setup.
func ensureProvisionerConfig(certs *pki, controllerAddr, instanceUuid, path, logLevel string) (*provisionerconfig.Config, error) {
	_, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		rendered := fmt.Sprintf(provisionerConfigTemplate,
			controllerAddr, instanceUuid,
			certs.ProvisionerCertFile, certs.ProvisionerKeyFile, certs.CaFile,
			provisionerListenHost, provisionerListenPort,
		)
		err = os.WriteFile(path, []byte(rendered), 0600)
	}
	if err != nil {
		return nil, err
	}
	cnf, err := provisionerconfig.ReadConfig(path)
	if err != nil {
		return nil, err
	}
	cnf.ControllerConfig.Addr = controllerAddr
	cnf.Log.Level = logLevel
	return cnf, nil
}

// startProvisioner runs the provisioner of path in the background until ctx is cancelled and gives the function
// waiting for it to be stopped, nil when it could not start. That failure is only logged because the controller and
// the agent are what flock dev is for.
func startProvisioner(ctx context.Context, certs *pki, controllerAddr, instanceUuid, path, logLevel string, version *semver.Version) (wait func()) {
	entry := logging.Component("dev")
	cnf, err := ensureProvisionerConfig(certs, controllerAddr, instanceUuid, path, logLevel)
	if err != nil {
		entry.Error().Err(err).Str("config", path).Msg("The provisioner is not started, its config can't be read")
		return nil
	}
	movedHost := cnf.ServerConfig.ListenHost != provisionerListenHost
	movedPort := cnf.ServerConfig.ListenPort != provisionerListenPort
	if movedHost || movedPort {
		entry.Warn().Str("config", path).Str("controller_declares", provisionerHost).
			Str("listen_host", cnf.ServerConfig.ListenHost).Uint16("listen_port", cnf.ServerConfig.ListenPort).
			Msg("The provisioner listens elsewhere than where the controller looks for it, the controller sees it unreachable: fix its server block or remove the file to get the defaults back")
	}
	provisioner, err := provisionerapp.NewApp(cnf, version)
	if err != nil {
		entry.Error().Err(err).Str("config", path).Msg("The provisioner is not started")
		return nil
	}
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		err := provisioner.Run(ctx)
		if err != nil {
			entry.Error().Err(err).Msg("The provisioner stopped")
		}
	}()
	return func() { <-stopped }
}
