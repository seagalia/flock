package limit

import (
	"fmt"
	"github.com/seagalia/flock/sdk/flock/extensions/limit/starling_limit/v1"
	flocktype "github.com/seagalia/flock/sdk/flock/type/v1"
)

func (x *Config) GetStarlingLimit() (*starling_limit.StarlingRateLimit, error) {
	if x.GetTypedLimit() == nil {
		return nil, nil
	}
	options := &starling_limit.StarlingRateLimit{}
	err := x.GetTypedLimit().UnmarshalTo(options)
	if err != nil {
		return options, err
	}
	return options, nil
}

// RateAsNftables gives the rate as a nftables limit rate, per second without unit
func (x *ConnectionRateLimit) RateAsNftables() string {
	unit := "/second"
	switch x.GetUnit() {
	case flocktype.TimeUnit_MINUTE:
		unit = "/minute"
	case flocktype.TimeUnit_HOUR:
		unit = "/hour"
	case flocktype.TimeUnit_DAY:
		unit = "/day"
	}
	return fmt.Sprintf("%d%s", x.GetRate(), unit)
}
