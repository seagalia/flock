package as3

import (
	"github.com/seagalia/flock/sdk/flock/api/config/limit/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	flocktype "github.com/seagalia/flock/sdk/flock/type/v1"
)

// applyLimits maps the limits of a virtual server on the connection limit and the connection rate limit of the
// service. A BIG-IP virtual server has one of each, for all its clients together.
func (r *renderer) applyLimits(virtualServer *vs.VirtualServer, svc *Service) {
	for _, config := range virtualServer.GetLimitConfig() {
		switch action := config.GetAction().(type) {
		case *limit.Config_ConnectionLimit:
			connLimit := action.ConnectionLimit
			if connLimit.GetSourceAddress() || connLimit.GetDestinationAddress() {
				r.res.warn(virtualServer, perAddressLimitWarning)
				continue
			}
			if svc.MaxConnections != 0 {
				r.res.warn(virtualServer, "an F5 virtual server has one connection limit, the first one is used")
				continue
			}
			svc.MaxConnections = connLimit.GetMaxConnections()
		case *limit.Config_ConnectionRateLimit:
			rateLimit := action.ConnectionRateLimit
			if rateLimit.GetSourceAddress() || rateLimit.GetDestinationAddress() {
				r.res.warn(virtualServer, perAddressLimitWarning)
				continue
			}
			if svc.RateLimit != 0 {
				r.res.warn(virtualServer, "an F5 virtual server has one connection rate limit, the first one is used")
				continue
			}
			r.res.warn(virtualServer, "connection rate limit burst has no F5 equivalent, it is ignored")
			svc.RateLimit = perSecond(rateLimit.GetRate(), rateLimit.GetUnit())
		}
	}
}

const perAddressLimitWarning = "limits per source or destination address are not supported by the F5 agent, the limit is ignored"

// perSecond converts a rate to the connections per second of the BIG-IP, rounded up so a low rate still lets
// connections in.
func perSecond(rate uint64, unit flocktype.TimeUnit) uint64 {
	seconds := uint64(1)
	switch unit {
	case flocktype.TimeUnit_MINUTE:
		seconds = 60
	case flocktype.TimeUnit_HOUR:
		seconds = 3600
	case flocktype.TimeUnit_DAY:
		seconds = 86400
	}
	return max(1, (rate+seconds-1)/seconds)
}
