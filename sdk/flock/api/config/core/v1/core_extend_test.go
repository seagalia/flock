package core_test

import (
	"errors"
	"net"
	"time"

	"buf.build/go/protovalidate"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	. "github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	flocktype "github.com/seagalia/flock/sdk/flock/type/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

func ruleIdsOf(err error) []string {
	var validationErr *protovalidate.ValidationError
	if !errors.As(err, &validationErr) {
		return nil
	}
	ids := make([]string, 0, len(validationErr.Violations))
	for _, violation := range validationErr.Violations {
		ids = append(ids, violation.Proto.GetRuleId())
	}
	return ids
}

var _ = Describe("SemanticVersion", func() {
	It("should format the version with optional prerelease and metadata", func() {
		Expect((&SemanticVersion{Major: 1, Minor: 2, Patch: 3}).Format()).To(Equal("1.2.3"))
		Expect((&SemanticVersion{Major: 1, Prerelease: "rc1", Metadata: "abc"}).Format()).To(Equal("1.0.0-rc1+abc"))
		var nilVersion *SemanticVersion
		Expect(nilVersion.Format()).To(BeEmpty())
	})
})

var _ = Describe("HeaderValuesToMap", func() {
	It("should map header keys to values", func() {
		headers := []*HeaderValue{
			{Key: "a", ValueSource: &HeaderValue_Value{Value: "1"}},
			{Key: "b", ValueSource: &HeaderValue_Value{Value: "2"}},
		}
		Expect(HeaderValuesToMap(headers)).To(Equal(map[string]string{"a": "1", "b": "2"}))
		Expect(HeaderValuesToMap(nil)).To(BeEmpty())
	})
})

var _ = Describe("IPAddr", func() {
	It("should format ip and port", func() {
		Expect((&IPAddr{Ip: "10.0.0.1", Port: 80}).Format()).To(Equal("10.0.0.1:80"))
		Expect((&IPAddr{Ip: "fe80::1", Port: 80}).Format()).To(Equal("[fe80::1]:80"))
		var nilAddr *IPAddr
		Expect(nilAddr.Format()).To(BeEmpty())
	})

	It("should tell the ip version", func() {
		Expect((&IPAddr{Ip: "10.0.0.1"}).IsIPV4()).To(BeTrue())
		Expect((&IPAddr{Ip: "fe80::1"}).IsIPV6()).To(BeTrue())
		var nilAddr *IPAddr
		Expect(nilAddr.IsIPV4()).To(BeFalse())
		Expect(nilAddr.IsIPV6()).To(BeFalse())
	})

	It("should give the single host network of the ip", func() {
		Expect((&IPAddr{Ip: "10.0.0.1"}).ToIpNet().String()).To(Equal("10.0.0.1/32"))
		Expect((&IPAddr{Ip: "fe80::1"}).ToIpNet().String()).To(Equal("fe80::1/128"))
		var nilAddr *IPAddr
		Expect(nilAddr.ToIpNet()).To(BeNil())
	})

	It("should clone into an independent address", func() {
		addr := &IPAddr{Ip: "10.0.0.1", Port: 80, LoadBalancingWeight: 3}
		clone := addr.Clone()
		Expect(clone).ToNot(BeIdenticalTo(addr))
		Expect(clone.GetIp()).To(Equal("10.0.0.1"))
		Expect(clone.GetPort()).To(BeEquivalentTo(80))
		Expect(clone.GetLoadBalancingWeight()).To(Equal(addr.GetLoadBalancingWeight()))
		var nilAddr *IPAddr
		Expect(nilAddr.Clone()).To(BeNil())
	})
})

var _ = Describe("IPAddrGroup", func() {
	It("should give valid ips only", func() {
		group := &IPAddrGroup{IpAddresses: []*IPAddr{{Ip: "10.0.0.1"}, {Ip: "not an ip"}, {Ip: "fe80::1"}}}
		Expect(group.ToIps()).To(Equal([]net.IP{net.ParseIP("10.0.0.1"), net.ParseIP("fe80::1")}))
		var nilGroup *IPAddrGroup
		Expect(nilGroup.ToIps()).To(BeNil())
	})
})

var _ = Describe("URL", func() {
	It("should format with or without port", func() {
		Expect((&URL{Scheme: "https", Host: "example.com", Port: 8443, Path: "/api"}).Format()).To(Equal("https://example.com:8443/api"))
		Expect((&URL{Scheme: "https", Host: "example.com", Path: "/api"}).Format()).To(Equal("https://example.com/api"))
		Expect((&URL{Scheme: "http", Host: "fe80::1", Port: 80}).Format()).To(Equal("http://[fe80::1]:80"))
		var nilUrl *URL
		Expect(nilUrl.Format()).To(BeEmpty())
	})

	It("should convert to a go url with the user and never the password", func() {
		coreUrl := &URL{Scheme: "https", Host: "example.com", Port: 8443, Path: "/api",
			UserInfo: &URL_UserInfo{User: "user", PasswordSource: &URL_UserInfo_Password{Password: "pass"}}}
		Expect(coreUrl.ToUrl().String()).To(Equal("https://user@example.com:8443/api"))
		Expect((&URL{Scheme: "http", Host: "example.com"}).ToUrl().String()).To(Equal("http://example.com"))
		var nilUrl *URL
		Expect(nilUrl.ToUrl()).To(BeNil())
	})

	It("should be parsed from a raw url", func() {
		coreUrl, err := UrlToCoreUrl("https://user:pass@example.com:8443/api")
		Expect(err).ToNot(HaveOccurred())
		Expect(coreUrl.GetScheme()).To(Equal("https"))
		Expect(coreUrl.GetHost()).To(Equal("example.com"))
		Expect(coreUrl.GetPort()).To(BeEquivalentTo(8443))
		Expect(coreUrl.GetPath()).To(Equal("/api"))
		Expect(coreUrl.GetUserInfo().GetUser()).To(Equal("user"))
		Expect(coreUrl.GetUserInfo().GetPasswordSource()).To(BeNil())

		withoutPort, err := UrlToCoreUrl("http://example.com/")
		Expect(err).ToNot(HaveOccurred())
		Expect(withoutPort.GetHost()).To(Equal("example.com"))
		Expect(withoutPort.GetPort()).To(BeZero())
		Expect(withoutPort.GetUserInfo()).To(BeNil())

		_, err = UrlToCoreUrl("http://example.com:notaport/")
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("Secret references in typed fields", func() {
	userInfoUrl := func(userInfo *URL_UserInfo) *URL {
		return &URL{Scheme: "http", Host: "vigie", UserInfo: userInfo}
	}
	fromSecret := &flocktype.SecretKeyRef{SecretUuid: "s1", Key: "k"}

	It("refuses a plain password", func() {
		plain := userInfoUrl(&URL_UserInfo{User: "u", PasswordSource: &URL_UserInfo_Password{Password: "p"}})
		Expect(ruleIdsOf(helpers.Validate(plain))).To(Equal([]string{"agent_only"}))
	})

	It("accepts a password from a secret", func() {
		withSecret := userInfoUrl(&URL_UserInfo{User: "u", PasswordSource: &URL_UserInfo_PasswordFromSecret{PasswordFromSecret: fromSecret}})
		Expect(helpers.Validate(withSecret)).To(Succeed())
	})

	It("refuses a password from a secret with no user", func() {
		noUser := userInfoUrl(&URL_UserInfo{PasswordSource: &URL_UserInfo_PasswordFromSecret{PasswordFromSecret: fromSecret}})
		Expect(ruleIdsOf(helpers.Validate(noUser))).To(Equal([]string{"user_info.user"}))
	})

	It("refuses a secret key ref with no key", func() {
		noKey := &HeaderValue{Key: "x", ValueSource: &HeaderValue_ValueFromSecret{
			ValueFromSecret: &flocktype.SecretKeyRef{SecretUuid: "s1"},
		}}
		Expect(helpers.Validate(noKey)).ToNot(Succeed())
		Expect(helpers.Validate(&HeaderValue{Key: "x", ValueSource: &HeaderValue_ValueFromSecret{ValueFromSecret: fromSecret}})).To(Succeed())
	})
})

var _ = Describe("HealthCheck", func() {
	It("should set defaults for missing values", func() {
		healthCheck := &HealthCheck{HealthChecker: &HealthCheck_HttpHealthCheck{HttpHealthCheck: &HttpHealthCheck{}}}
		healthCheck.ProvDefault()
		Expect(healthCheck.GetTimeout().AsDuration()).To(Equal(30 * time.Second))
		Expect(healthCheck.GetInterval().AsDuration()).To(Equal(10 * time.Second))
		Expect(healthCheck.GetUnhealthyInterval().AsDuration()).To(Equal(10 * time.Second))
		Expect(healthCheck.GetUnhealthyThreshold()).To(BeEquivalentTo(3))
		Expect(healthCheck.GetHealthyThreshold()).To(BeEquivalentTo(1))
		Expect(healthCheck.GetHttpHealthCheck().GetPath()).To(Equal("/"))
		Expect(healthCheck.GetHttpHealthCheck().GetExpectedStatuses().GetStart()).To(BeEquivalentTo(200))
		Expect(healthCheck.GetHttpHealthCheck().GetExpectedStatuses().GetEnd()).To(BeEquivalentTo(300))
	})

	It("should use the interval as default unhealthy interval", func() {
		healthCheck := &HealthCheck{Interval: durationpbOf(2 * time.Second)}
		healthCheck.ProvDefault()
		Expect(healthCheck.GetUnhealthyInterval().AsDuration()).To(Equal(2 * time.Second))
	})

	It("should set udp health check defaults", func() {
		healthCheck := &HealthCheck{HealthChecker: &HealthCheck_UdpHealthCheck{UdpHealthCheck: &UdpHealthCheck{}}}
		healthCheck.ProvDefault()
		Expect(healthCheck.GetUdpHealthCheck().GetPingTimeout().AsDuration()).To(Equal(5 * time.Second))
		Expect(healthCheck.GetUdpHealthCheck().GetDelay().AsDuration()).To(Equal(time.Second))
	})

	It("should not panic on nil health checks", func() {
		var nilHealthCheck *HealthCheck
		Expect(nilHealthCheck.ProvDefault).ToNot(Panic())
	})
})

var _ = Describe("HealthCheckPayload", func() {
	It("should give binary or text data", func() {
		Expect((&HealthCheckPayload{Payload: &HealthCheckPayload_Binary{Binary: []byte{1, 2}}}).GetData()).To(Equal([]byte{1, 2}))
		Expect((&HealthCheckPayload{Payload: &HealthCheckPayload_Text{Text: "ping"}}).GetData()).To(Equal([]byte("ping")))
		var nilPayload *HealthCheckPayload
		Expect(nilPayload.GetData()).To(BeNil())
	})
})
