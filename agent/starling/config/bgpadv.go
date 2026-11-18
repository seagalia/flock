package config

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/seagalia/flock/sdk/config"
)

type BGPConfig struct {
	Advertisements      []*BGPAdvConfig `yaml:"advertisements"`
	Peers               []*BGPPeer      `yaml:"peers"`
	LocalAsn            uint32          `yaml:"local_asn"`
	RouterId            string          `yaml:"router_id"`
	UseDynamicPlacement bool            `yaml:"use_dynamic_placement"`
}

func (c *BGPConfig) UnmarshalYAML(unmarshal func(any) error) error {
	type plain BGPConfig
	err := unmarshal((*plain)(c))
	if err != nil {
		return err
	}

	if len(c.Peers) == 0 {
		return fmt.Errorf("bgp config: at least one peer is required")
	}

	if c.LocalAsn == 0 {
		return fmt.Errorf("bgp config: local_asn is required")
	}

	return nil
}

type BGPCommunity struct {
	Raw   string
	Asn   uint32
	Value uint32
}

func (c *BGPCommunity) UnmarshalYAML(unmarshal func(any) error) error {
	var rawCommunity string
	err := unmarshal(&rawCommunity)
	if err != nil {
		return err
	}
	asn, val, err := ParseCommunity(rawCommunity)
	if err != nil {
		return err
	}
	c.Raw = rawCommunity
	c.Value = val
	c.Asn = asn
	return nil
}

func (c BGPCommunity) MarshalYAML() (any, error) {
	return fmt.Sprintf("%d:%d", c.Asn, c.Value), nil
}

func ParseCommunity(c string) (asn uint32, value uint32, err error) {
	fs := strings.Split(c, ":")
	if len(fs) != 2 {
		return 0, 0, fmt.Errorf("invalid community value: %s", c)
	}
	a, err := strconv.ParseUint(fs[0], 10, 16)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid community value: invalid first section of community %q: %s", fs[0], err)
	}
	b, err := strconv.ParseUint(fs[1], 10, 16)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid community value: invalid second section of community %q: %s", fs[1], err)
	}

	return uint32(a), uint32(b), nil
}

// BGPAdvConfig describes one translation from an IP address to a BGP advertisement.
type BGPAdvConfig struct {
	// Optional BGP LOCAL_PREF attribute which is used by BGP best path algorithm,
	// Path with higher localpref is preferred over one with lower localpref.
	LocalPref uint32 `yaml:"local_pref"`

	// Optional BGP communities of the form 1234:1234.
	Communities []*BGPCommunity `yaml:"communities"`

	// Optional list of Cidr to advertise via this advertisement, selected by name.
	CIDRs []*CIDR `yaml:"cidrs"`
}

func (c *BGPAdvConfig) UnmarshalYAML(unmarshal func(any) error) error {
	type plain BGPAdvConfig
	err := unmarshal((*plain)(c))
	if err != nil {
		return err
	}

	return nil
}

type BGPPeer struct {
	Name string `yaml:"name"`
	// AS number to use for the local end of the session.
	MyASN uint32 `yaml:"my_asn"`
	// AS number to expect from the remote end of the session.
	PeerAsn uint32 `yaml:"peer_asn"`
	// Peer Address to dial when establishing the session.
	Address string `json:"address"`
	// Source interface to use when establishing the session.
	SrcInterface string `yaml:"src_interface"`
	// Source address to use when establishing the session.
	SrcAddr string `yaml:"src_addr"`

	Port uint16 `yaml:"port"`
	// Requested BGP hold time, per RFC4271.
	HoldTime config.Duration `yaml:"hold_time"`
	// Requested BGP keepalive time, per RFC4271.
	KeepaliveInterval config.Duration `yaml:"keepalive_interval"`
	ConnectRetry      config.Duration `yaml:"connect_retry"`
	// Authentication password for routers enforcing TCP MD5 authenticated sessions
	Password string `yaml:"password"`
	// Optional ebgp peer is multi-hops away.
	EBGPMultiHop bool `yaml:"ebgp_multi_hop"`
	// Optional name of the vrf to establish the session from
	VRF string `yaml:"vrf"`
	// Optional Bfd configuration
	Bfd *BfdConfig `yaml:"bfd"`
}

func (c *BGPPeer) UnmarshalYAML(unmarshal func(any) error) error {
	type plain BGPPeer
	err := unmarshal((*plain)(c))
	if err != nil {
		return err
	}

	if c.Name == "" {
		return fmt.Errorf("bgp peer config: name is required")
	}

	if c.PeerAsn == 0 {
		return fmt.Errorf("bgp peer config: asn is required")
	}

	if c.SrcInterface != "" && c.SrcAddr != "" {
		return fmt.Errorf("bgp peer config: src_interface and src_addr are mutually exclusive")
	}

	if c.SrcInterface != "" {
		_, err := net.InterfaceByName(c.SrcInterface)
		if err != nil {
			return fmt.Errorf("bgp peer config: interface %q does not exist: %s", c.SrcInterface, err)
		}
	}

	if c.SrcInterface == "" && c.SrcAddr == "" {
		c.SrcInterface, err = getDefaultInterface()
		if err != nil {
			return fmt.Errorf("bgp peer config: src_interface or src_addr is required")
		}
	}
	if c.Port == 0 {
		c.Port = 179
	}

	return nil
}

type BfdConfig struct {
	// Enables BFD monitoring for this neighbor
	Enabled bool `yaml:"enabled"`
	// BFD port to use for this neighbor
	Port uint32 `yaml:"port"`
	// Requests sending BFD packets approximately every duration, subject to negotiation and jitter
	DesiredMinimumTxInterval config.Duration `yaml:"desired_minimum_tx_interval"`
	// Shortest interval between received BFD packets this system supports: it limits how fast the peer sends
	RequiredMinimumReceive config.Duration `yaml:"required_minimum_receive"`
	// Number of negotiated intervals without a BFD packet before the peer is declared down
	DetectionMultiplier uint32 `yaml:"detection_multiplier"`
}

func (c *BfdConfig) UnmarshalYAML(unmarshal func(any) error) error {
	type plain BfdConfig
	err := unmarshal((*plain)(c))
	if err != nil {
		return err
	}

	if c.Port == 0 {
		c.Port = 3784
	}
	if c.DesiredMinimumTxInterval == 0 {
		c.DesiredMinimumTxInterval = config.Duration(300 * time.Millisecond)
	}
	if c.RequiredMinimumReceive == 0 {
		c.RequiredMinimumReceive = config.Duration(300 * time.Millisecond)
	}
	if c.DetectionMultiplier == 0 {
		c.DetectionMultiplier = 3
	}
	return nil
}
