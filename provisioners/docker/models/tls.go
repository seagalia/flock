package models

type TLS struct {
	// io.seagalia.flock.tls.enable, terminates TLS on the virtual host
	Enable bool `mapstructure:"enable"`
	// io.seagalia.flock.tls.cert, uuid or name of a certificate known to the controller, ordered with ACME when empty
	Cert string `mapstructure:"cert"`
	// io.seagalia.flock.tls.redirect, redirects plain http requests to https
	Redirect bool `mapstructure:"redirect"`
	// io.seagalia.flock.tls.passthrough, tcp only, sends the connections still encrypted to the container, routed on
	// their SNI among io.seagalia.flock.domains
	Passthrough bool `mapstructure:"passthrough"`
}

type BackendTLS struct {
	// io.seagalia.flock.backend.tls.enable, TLS toward the container
	Enable bool `mapstructure:"enable"`
	// io.seagalia.flock.backend.tls.server_name, SNI sent to the container
	ServerName string `mapstructure:"server_name"`
}
