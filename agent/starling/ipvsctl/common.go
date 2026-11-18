package ipvsctl

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"

	"github.com/cloudflare/ipvs"
)

const ipvsModule = "ip_vs"

// ensureIpvsLoaded skips loading the module when the host provisioning (modprobe) already loaded it,
// the go loader doesn't support every module format (e.g. zstd compressed)
func ensureIpvsLoaded(sysModuleDir string, load func() error) error {
	if _, err := os.Stat(filepath.Join(sysModuleDir, ipvsModule)); err == nil {
		return nil
	}
	if err := load(); err != nil {
		return fmt.Errorf("failed to load %s kernel module: %w", ipvsModule, err)
	}
	return nil
}

func makeKeyService(service ipvs.Service) string {
	return service.Address.Unmap().String() + "_" +
		net.IPMask(service.Netmask.AsSlice()).String() + "_" +
		strconv.Itoa(int(service.Port)) + "_" +
		service.Protocol.String()
}

func makeKeyDest(service ipvs.Service, destination ipvs.Destination) string {
	return makeKeyService(service) + "_" +
		destination.Address.Unmap().String() + "_" +
		strconv.Itoa(int(destination.Port))
}

type errClient struct {
	err error
}

func (c *errClient) Info() (ipvs.Info, error) {
	return ipvs.Info{}, c.err
}

func (c *errClient) Config() (ipvs.Config, error) {
	return ipvs.Config{}, c.err
}

func (c *errClient) SetConfig(ipvs.Config) error {
	return c.err
}

func (c *errClient) Services() ([]ipvs.ServiceExtended, error) {
	return nil, c.err
}

func (c *errClient) Service(ipvs.Service) (ipvs.ServiceExtended, error) {
	return ipvs.ServiceExtended{}, c.err
}

func (c *errClient) CreateService(ipvs.Service) error {
	return c.err
}

func (c *errClient) UpdateService(ipvs.Service) error {
	return c.err
}

func (c *errClient) RemoveService(ipvs.Service) error {
	return c.err
}

func (c *errClient) Destinations(ipvs.Service) ([]ipvs.DestinationExtended, error) {
	return nil, c.err
}

func (c *errClient) CreateDestination(ipvs.Service, ipvs.Destination) error {
	return c.err
}

func (c *errClient) UpdateDestination(ipvs.Service, ipvs.Destination) error {
	return c.err
}

func (c *errClient) RemoveDestination(ipvs.Service, ipvs.Destination) error {
	return c.err
}

func fwdTypeToString(fwdMethod ipvs.ForwardType) string {
	switch fwdMethod {
	case ipvs.Masquerade:
		return "masq"
	case ipvs.Local:
		return "local"
	case ipvs.DirectRoute:
		return "direct_route"
	default:
		return "unknown"
	}
}
