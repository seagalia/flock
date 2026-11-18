package core

import (
	"time"

	flocktype "github.com/seagalia/flock/sdk/flock/type/v1"
	_ "google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	_ "google.golang.org/protobuf/types/known/structpb"
)

func (x *HealthCheckPayload) GetData() []byte {
	if x == nil {
		return nil
	}
	if len(x.GetBinary()) > 0 {
		return x.GetBinary()
	}
	return []byte(x.GetText())
}

func (x *HealthCheck) ProvDefault() {
	if x == nil {
		return
	}
	if x.GetTimeout().AsDuration() == 0 {
		x.Timeout = durationpb.New(30 * time.Second)
	}
	if x.GetInterval().AsDuration() == 0 {
		x.Interval = durationpb.New(10 * time.Second)
	}
	if x.GetUnhealthyThreshold() == 0 {
		x.UnhealthyThreshold = 3
	}
	if x.GetHealthyThreshold() == 0 {
		x.HealthyThreshold = 1
	}
	if x.GetUnhealthyInterval().AsDuration() == 0 {
		x.UnhealthyInterval = &durationpb.Duration{
			Seconds: x.GetInterval().Seconds,
			Nanos:   x.GetInterval().Nanos,
		}
	}
	x.GetHttpHealthCheck().ProvDefault()
	x.GetUdpHealthCheck().ProvDefault()
}

func (x *UdpHealthCheck) ProvDefault() {
	if x == nil {
		return
	}
	if x.GetPingTimeout().AsDuration() == 0 {
		x.PingTimeout = durationpb.New(5 * time.Second)
	}
	if x.GetDelay().AsDuration() == 0 {
		x.Delay = durationpb.New(1 * time.Second)
	}

}

func (x *HttpHealthCheck) ProvDefault() {
	if x == nil {
		return
	}
	if x.GetExpectedStatuses() == nil {
		x.ExpectedStatuses = &flocktype.Int64Range{
			Start: 200,
			End:   300,
		}
	}
	if x.GetPath() == "" {
		x.Path = "/"
	}
}
