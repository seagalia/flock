package ipams

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	zonev1 "github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

type Remote struct {
	baseURL string
	client  *http.Client
	headers map[string][]string
}

type remoteRequest struct {
	ZoneUUID string          `json:"zone_uuid,omitempty"`
	Zone     json.RawMessage `json:"zone,omitempty"`
	Pool     string          `json:"pool,omitempty"`
	Owner    string          `json:"owner,omitempty"`
	IP       string          `json:"ip,omitempty"`
}

type acquireResponse struct {
	IP string `json:"ip"`
}

type hasAllocatedResponse struct {
	HasAllocations bool `json:"has_allocations"`
}

type remoteError struct {
	Error string `json:"error"`
}

func NewRemote(baseURL string, insecureSkipVerify bool, headers map[string][]string) (*Remote, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" ||
		(u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("invalid remote IPAM URL: %q", baseURL)
	}
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           dialer.DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			TLSClientConfig:       &tls.Config{InsecureSkipVerify: insecureSkipVerify},
		},
		Timeout: 30 * time.Second,
	}
	return &Remote{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  client,
		headers: headers,
	}, nil
}

func newRemoteRequest(zone *zonev1.Zone) (remoteRequest, error) {
	req := remoteRequest{ZoneUUID: zone.GetUUID()}
	data, err := protojson.Marshal(zone)
	if err != nil {
		return req, fmt.Errorf("marshal zone: %w", err)
	}
	req.Zone = data

	return req, nil
}

// remoteErrorMessage gives the error field of a json answer, or the whole answer when it has none
func remoteErrorMessage(body io.Reader) string {
	raw, _ := io.ReadAll(io.LimitReader(body, 4096))
	var apiErr remoteError
	if json.Unmarshal(raw, &apiErr) == nil && apiErr.Error != "" {
		return apiErr.Error
	}
	return strings.TrimSpace(string(raw))
}

func (r *Remote) post(
	ctx context.Context,
	path string,
	payload remoteRequest,
	result any,
) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal IPAM request: %w", err)
	}

	endpoint, err := url.JoinPath(r.baseURL, path)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		endpoint,
		bytes.NewReader(data),
	)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	maps.Copy(req.Header, r.headers)

	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("remote IPAM request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound && path == "/v1/ipam/release" {
		return ErrIPNotAllocated
	}
	if resp.StatusCode == http.StatusConflict && path == "/v1/ipam/sync-zone" {
		return fmt.Errorf("remote IPAM refused the zone: %w", ErrIPsOutOfRanges)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		remoteErr := fmt.Errorf("remote IPAM HTTP %d: %s", resp.StatusCode, remoteErrorMessage(resp.Body))
		// typed errors are answered to the user as a refusal of the request, not as an internal error
		isConflict := resp.StatusCode == http.StatusConflict
		switch {
		case isConflict && path == "/v1/ipam/acquire-specific":
			return fmt.Errorf("%w: %w", ErrIPAllocated, remoteErr)
		case isConflict && path == "/v1/ipam/acquire":
			return fmt.Errorf("%w: %w", ErrPoolExhausted, remoteErr)
		}
		return remoteErr
	}

	if result == nil {
		return nil
	}

	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return fmt.Errorf("decode remote IPAM response: %w", err)
	}

	return nil
}

// AcquireIP allocates an available IP in the given zone.
func (r *Remote) AcquireIP(
	ctx context.Context,
	zone *zonev1.Zone,
	pool string,
	owner string,
) (netip.Addr, error) {
	if zone.CouldNotAllocateIP() {
		return netip.Addr{}, fmt.Errorf("zone %s: %w", zone.GetUUID(), ErrNoAllocatableIP)
	}
	payload, err := newRemoteRequest(zone)
	if err != nil {
		return netip.Addr{}, err
	}
	payload.Pool = pool
	payload.Owner = owner

	var result acquireResponse

	if err := r.post(
		ctx,
		"/v1/ipam/acquire",
		payload,
		&result,
	); err != nil {
		return netip.Addr{}, err
	}

	ip, err := netip.ParseAddr(result.IP)
	if err != nil {
		return netip.Addr{}, fmt.Errorf(
			"remote IPAM returned invalid IP %q: %w",
			result.IP, err,
		)
	}

	return ip.Unmap(), nil
}

// AcquireSpecificIP allocates a specific address in the zone.
func (r *Remote) AcquireSpecificIP(
	ctx context.Context,
	zone *zonev1.Zone,
	ip netip.Addr,
	owner string,
) error {
	if zone.CouldNotAllocateIP() {
		return nil
	}
	if !ip.IsValid() {
		return errors.New("invalid IP address")
	}

	payload, err := newRemoteRequest(zone)
	if err != nil {
		return err
	}

	payload.IP = ip.Unmap().String()
	payload.Owner = owner

	return r.post(
		ctx,
		"/v1/ipam/acquire-specific",
		payload,
		nil,
	)
}

// ReleaseIP releases an allocated address.
func (r *Remote) ReleaseIP(
	ctx context.Context,
	zone *zonev1.Zone,
	ip netip.Addr,
) error {
	if zone.CouldNotAllocateIP() {
		return nil
	}
	if !ip.IsValid() {
		return errors.New("invalid IP address")
	}

	payload, err := newRemoteRequest(zone)
	if err != nil {
		return err
	}

	payload.IP = ip.Unmap().String()

	return r.post(
		ctx,
		"/v1/ipam/release",
		payload,
		nil,
	)
}

// ReleaseOwner releases every address of the owner, in all zones.
func (r *Remote) ReleaseOwner(ctx context.Context, owner string) error {
	return r.post(
		ctx,
		"/v1/ipam/release-owner",
		remoteRequest{Owner: owner},
		nil,
	)
}

// HasAllocatedIPs reports whether the zone has active allocations.
func (r *Remote) HasAllocatedIPs(
	ctx context.Context,
	zone *zonev1.Zone,
) (bool, error) {
	if zone.CouldNotAllocateIP() {
		return false, nil
	}
	var result hasAllocatedResponse
	payload, err := newRemoteRequest(zone)
	if err != nil {
		return false, err
	}
	err = r.post(
		ctx,
		"/v1/ipam/has-allocations",
		payload,
		&result,
	)
	if err != nil {
		return false, err
	}

	return result.HasAllocations, nil
}

// SyncZone sends the zone to the remote ipam so it can update its ranges. A zone
// without pools is sent too: the remote has to drop the ranges it held.
func (r *Remote) SyncZone(ctx context.Context, zone *zonev1.Zone) error {
	payload, err := newRemoteRequest(zone)
	if err != nil {
		return err
	}

	return r.post(ctx, "/v1/ipam/sync-zone", payload, nil)
}
