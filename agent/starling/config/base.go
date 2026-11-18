package config

import (
	"net"
	"net/url"
)

type URLConfig struct {
	URL *url.URL
	Raw string
}

func (uc *URLConfig) UnmarshalYAML(unmarshal func(any) error) error {
	var s string
	err := unmarshal(&s)
	if err != nil {
		return err
	}
	uc.Raw = s
	uc.URL, err = url.Parse(s)
	return err
}

func (uc URLConfig) MarshalYAML() (any, error) {
	return uc.Raw, nil
}

type CIDR struct {
	IPNet *net.IPNet
}

func (c *CIDR) UnmarshalYAML(unmarshal func(any) error) error {
	var rawCidr string
	err := unmarshal(&rawCidr)
	if err != nil {
		return err
	}
	_, cidrNet, err := net.ParseCIDR(rawCidr)
	if err != nil {
		return err
	}
	c.IPNet = cidrNet
	return nil
}

func (c CIDR) MarshalYAML() (any, error) {
	if c.IPNet == nil {
		return "", nil
	}
	return c.IPNet.String(), nil
}
