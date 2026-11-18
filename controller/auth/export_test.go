package auth

import "time"

// ShortenTimeouts makes the ldap and provider timeouts short and returns the function restoring them
func ShortenTimeouts(d time.Duration) func() {
	ldap, provider := ldapTimeout, providerHTTPTimeout
	ldapTimeout, providerHTTPTimeout = d, d
	return func() { ldapTimeout, providerHTTPTimeout = ldap, provider }
}
