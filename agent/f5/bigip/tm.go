package bigip

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
)

type DeviceHealth struct {
	URL            string
	Reachable      bool
	Active         bool
	FailoverStatus string
	AS3Version     string
	Err            error
}

func (c *Client) Devices(ctx context.Context) []DeviceHealth {
	ctx, cancel := context.WithTimeout(ctx, c.opts.RequestTimeout)
	defer cancel()
	devices := make([]DeviceHealth, 0, len(c.opts.URLs))
	for _, url := range c.opts.URLs {
		health := DeviceHealth{URL: url}
		health.FailoverStatus, health.Err = c.failoverStatus(ctx, url)
		if health.Err == nil {
			health.Reachable = true
			health.Active = health.FailoverStatus == "ACTIVE"
			health.AS3Version, health.Err = c.as3Version(ctx, url)
		}
		devices = append(devices, health)
	}
	return devices
}

// failoverStatus is ACTIVE, STANDBY, OFFLINE, FORCED OFFLINE... as the device says it.
func (c *Client) failoverStatus(ctx context.Context, device string) (string, error) {
	resp, err := c.do(ctx, device, http.MethodGet, "/mgmt/tm/cm/failover-status", nil)
	if err != nil {
		return "", err
	}
	var stats struct {
		Entries map[string]struct {
			NestedStats struct {
				Entries struct {
					Status struct {
						Description string `json:"description"`
					} `json:"status"`
				} `json:"entries"`
			} `json:"nestedStats"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(resp.body, &stats); err != nil {
		return "", &Error{Status: resp.status, Message: "reading the failover status: " + err.Error()}
	}
	for _, entry := range stats.Entries {
		return entry.NestedStats.Entries.Status.Description, nil
	}
	return "", &Error{Status: resp.status, Message: "the failover status holds no entry"}
}

func (c *Client) as3Version(ctx context.Context, device string) (string, error) {
	resp, err := c.do(ctx, device, http.MethodGet, "/mgmt/shared/appsvcs/info", nil)
	if err != nil {
		return "", err
	}
	var info struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(resp.body, &info); err != nil {
		return "", &Error{Status: resp.status, Message: "reading the AS3 info: " + err.Error()}
	}
	return info.Version, nil
}

// ListPartitions gives the names of every partition of the device, the ones AS3 doesn't manage included.
func (c *Client) ListPartitions(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.opts.RequestTimeout)
	defer cancel()
	device, err := c.device(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(ctx, device, http.MethodGet, "/mgmt/tm/auth/partition", nil)
	if err != nil {
		return nil, err
	}
	var collection struct {
		Items []struct {
			Name string `json:"name"`
		} `json:"items"`
	}
	if err := json.Unmarshal(resp.body, &collection); err != nil {
		return nil, &Error{Status: resp.status, Message: "reading the partitions: " + err.Error()}
	}
	names := make([]string, 0, len(collection.Items))
	for _, item := range collection.Items {
		names = append(names, item.Name)
	}
	return names, nil
}

// PoolMemberStates tells for each member of a pool, keyed by "ip:port" ("[ip]:port" for IPv6), whether its monitors
// see it up.
func (c *Client) PoolMemberStates(ctx context.Context, tenant, app, pool string) (map[string]bool, error) {
	ctx, cancel := context.WithTimeout(ctx, c.opts.RequestTimeout)
	defer cancel()
	device, err := c.device(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(ctx, device, http.MethodGet, "/mgmt/tm/ltm/pool/~"+tenant+"~"+app+"~"+pool+"/members", nil)
	if err != nil {
		return nil, err
	}
	return parseMemberStates(resp.body)
}

// parseMemberStates reads the members of a pool. A member is named "ip:port", or "ip.port" for IPv6, and its address
// may carry a route domain ("%0"). A pool without monitor leaves its members "unchecked", they take traffic. A FQDN
// member has no address ("any6") and takes no traffic, the members it populates from the DNS answers do.
func parseMemberStates(body []byte) (map[string]bool, error) {
	var collection struct {
		Items []struct {
			Name    string `json:"name"`
			Address string `json:"address"`
			State   string `json:"state"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &collection); err != nil {
		return nil, &Error{Message: "reading the pool members: " + err.Error()}
	}
	states := map[string]bool{}
	for _, item := range collection.Items {
		address, _, _ := strings.Cut(item.Address, "%")
		if net.ParseIP(address) == nil {
			continue
		}
		separator := ":"
		if strings.Contains(address, ":") {
			separator = "."
		}
		port := item.Name[strings.LastIndex(item.Name, separator)+1:]
		states[net.JoinHostPort(address, port)] = item.State == "up" || item.State == "unchecked"
	}
	return states, nil
}
