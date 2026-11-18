package as3

const (
	classAS3          = "AS3"
	classADC          = "ADC"
	classTenant       = "Tenant"
	classApplication  = "Application"
	classServiceTCP   = "Service_TCP"
	classServiceUDP   = "Service_UDP"
	classServiceHTTP  = "Service_HTTP"
	classServiceHTTPS = "Service_HTTPS"
	classPool         = "Pool"
	classMonitor      = "Monitor"
	classIRule        = "iRule"
	classCertificate  = "Certificate"
	classCABundle     = "CA_Bundle"
	classTLSServer    = "TLS_Server"
	classTLSClient    = "TLS_Client"
)

const virtualTypeInternal = "internal"

const (
	actionDeploy = "deploy"
	actionDryRun = "dry-run"
)

const (
	lbModeRoundRobin            = "round-robin"
	lbModeRatio                 = "ratio-member"
	lbModeLeastConnections      = "least-connections-member"
	lbModeRatioLeastConnections = "ratio-least-connections-member"
	persistenceSourceAddress    = "source-address"
	snatAuto                    = "auto"
	snatNone                    = "none"
	addressDiscoveryFQDN        = "fqdn"
	addressFamilyIPv4           = "IPv4"
	addressFamilyIPv6           = "IPv6"
	monitorTypeTCP              = "tcp"
	monitorTypeUDP              = "udp"
	monitorTypeHTTP             = "http"
	monitorTypeHTTPS            = "https"
)

const (
	iRulePurposeHTTP      = "http"
	iRulePurposeSNI       = "sni"
	iRulePurposeProxyRecv = "proxyrecv"
	iRulePurposeProxySend = "proxysend"
	iRulePurposeExtAuth   = "extauth"
)

// the F5 options of a virtual server, as named in the warnings when one is ignored
const (
	optionWafPolicy        = "waf_policy"
	optionHTTPProfile      = "http_profile"
	optionClientTLSProfile = "client_tls_profile"
	optionServerTLSProfile = "server_tls_profile"
	optionTCPProfile       = "tcp_profile"
	optionUDPProfile       = "udp_profile"
)
