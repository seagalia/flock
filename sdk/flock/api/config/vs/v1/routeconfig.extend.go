package vs

import (
	starling_vsopts "github.com/seagalia/flock/sdk/flock/extensions/vs/starling_options/v1"
)

type IsRouteConfig interface {
	isRouteConfig()
}

func (*TcpRouteConfig) isRouteConfig() {}

func (*UdpRouteConfig) isRouteConfig() {}

func (*HttpRoutesConfig) isRouteConfig() {}

func (*TlsTcpRoutesConfig) isRouteConfig() {}

func (x *TcpRouteConfig) GetStarlingOptions() (*starling_vsopts.TcpUdpOptions, error) {
	options := &starling_vsopts.TcpUdpOptions{}
	if x.GetTypedOptions() == nil {
		return options, nil
	}
	err := x.GetTypedOptions().UnmarshalTo(options)
	if err != nil {
		return options, err
	}
	return options, nil
}

func (x *UdpRouteConfig) GetStarlingOptions() (*starling_vsopts.TcpUdpOptions, error) {
	options := &starling_vsopts.TcpUdpOptions{}
	if x.GetTypedOptions() == nil {
		return options, nil
	}
	err := x.GetTypedOptions().UnmarshalTo(options)
	if err != nil {
		return options, err
	}
	return options, nil
}

func (x *TlsTcpRoutesConfig) GetStarlingOptions() (*starling_vsopts.TlsTcpOptions, error) {
	options := &starling_vsopts.TlsTcpOptions{}
	if x.GetTypedOptions() == nil {
		return options, nil
	}
	err := x.GetTypedOptions().UnmarshalTo(options)
	if err != nil {
		return options, err
	}
	return options, nil
}

func (x *HttpRoutesConfig) GetStarlingOptions() (*starling_vsopts.HttpOptions, error) {
	options := &starling_vsopts.HttpOptions{}
	if x.GetTypedOptions() == nil {
		return options, nil
	}
	err := x.GetTypedOptions().UnmarshalTo(options)
	if err != nil {
		return options, err
	}
	return options, nil
}
