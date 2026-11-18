package testhelpers

//go:generate go tool mockgen -destination=mock_dns_handler.go -package=testhelpers -mock_names=Handler=MockDnsHandler github.com/miekg/dns Handler
//go:generate go tool mockgen -destination=mock_eds_client.go -package=testhelpers github.com/seagalia/vigie/sdk/vigie/eds/v1/edsconnect EndpointDiscoveryServiceClient
//go:generate go tool mockgen -destination=mock_lease_client.go -package=testhelpers github.com/seagalia/flock/sdk/flock/services/lease/v1/svcleaseconnect LeaseClient
