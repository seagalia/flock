package as3

import "strings"

// Object names come from the UUID of the flock resource, never its name, so a rename in flock changes nothing on the
// BIG-IP. AS3 names start with a letter and can't hold '-' in every class, hence the prefix and the '_'.

func id(uuid string) string {
	return strings.ReplaceAll(uuid, "-", "_")
}

func AppName(vipUUID string) string {
	return "vip_" + id(vipUUID)
}

func ServiceName(vsUUID string) string {
	return "vs_" + id(vsUUID)
}

func PoolName(backendUUID string) string {
	return "pool_" + id(backendUUID)
}

func MonitorName(backendUUID string) string {
	return "mon_" + id(backendUUID)
}

func CertName(certUUID string) string {
	return "cert_" + id(certUUID)
}

func CABundleName(uuid string) string {
	return "ca_" + id(uuid)
}

// ServerTLSName is the client SSL profile of a virtual server, AS3 calls it serverTLS as the BIG-IP is the server.
func ServerTLSName(vsUUID string) string {
	return "tlss_" + id(vsUUID)
}

// ClientTLSName is the server SSL profile towards the endpoints of a backend.
func ClientTLSName(backendUUID string) string {
	return "tlsc_" + id(backendUUID)
}

func IRuleName(purpose, uuid string) string {
	return "irule_" + purpose + "_" + id(uuid)
}

// ExtAuthServiceName is the internal virtual the external auth check of a policy connects to. A policy name only holds
// [a-z0-9-], so id gives a valid AS3 name and two policies never share one.
func ExtAuthServiceName(vsUUID, policyName string) string {
	return "auth_" + id(vsUUID) + "_" + id(policyName)
}

func ExtAuthIRuleName(vsUUID, policyName string) string {
	return IRuleName(iRulePurposeExtAuth, vsUUID) + "_" + id(policyName)
}

// remark cuts a flock name to the 64 characters an AS3 remark accepts.
func remark(name string) string {
	runes := []rune(name)
	if len(runes) <= 64 {
		return name
	}
	return string(runes[:64])
}
