package serves

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/rs/zerolog"
	"github.com/seagalia/flock/controller/announcers"
	svcdiscovery "github.com/seagalia/flock/sdk/flock/services/discovery/v1"
	"github.com/seagalia/flock/sdk/logging"
)

type DoneEmit struct {
	Err error
}

type ErrEmit struct {
	Err error
}

type DiscoAnList struct {
	entry   zerolog.Logger
	ctx     context.Context
	stream  DiscoveryStream
	done    chan *DoneEmit
	errEmit chan *ErrEmit
	nonce   uint64
	mutex   *sync.Mutex
}

// NewDiscoAnList gives the listener of a discovery stream, ctx is the context of the call serving it
func NewDiscoAnList(ctx context.Context, stream DiscoveryStream, done chan *DoneEmit, errEmit chan *ErrEmit) *DiscoAnList {
	clientIp := clientAddr(ctx)
	xForwardFor := requestHeaderValues(ctx, "x-forwarded-for")
	if len(xForwardFor) > 0 && xForwardFor[0] != "" {
		clientIp = strings.Split(xForwardFor[0], ",")[0]
	}
	entry := logging.Component("discovery_listener")
	if clientIp != "" {
		entry = entry.With().Str("client_ip", clientIp).Logger()
	}
	return &DiscoAnList{
		entry:   entry,
		ctx:     ctx,
		stream:  stream,
		done:    done,
		errEmit: errEmit,
		nonce:   1,
		mutex:   &sync.Mutex{},
	}
}

func (d *DiscoAnList) OnAnnounce(evName announcers.EventName, res []*svcdiscovery.Resource) {
	d.mutex.Lock()
	defer func() {
		d.nonce++
		d.mutex.Unlock()
	}()
	d.entry.Debug().Msgf("receive event %s", evName)
	nonceStr := fmt.Sprintf("%d", d.nonce)

	waitRecv := make(chan struct{}, 1)
	var err error
	var resp *svcdiscovery.DeltaDiscoveryRequest
	go func() {
		resp, err = d.stream.Receive()
		d.entry.Debug().Msg("received response nonce from agent")
		close(waitRecv)
	}()

	if evName == announcers.ChangeEvent {
		d.entry.Debug().Msg("sending change delta response")
		err := d.stream.Send(&svcdiscovery.DeltaDiscoveryResponse{
			Resources:     res,
			ResponseNonce: nonceStr,
		})
		if err != nil {
			emitDone(d.ctx, d.done, &DoneEmit{Err: err})
			return
		}
	}
	if evName == announcers.RemoveEvent {
		toRemove := make([]string, len(res))
		for i, r := range res {
			toRemove[i] = r.GetUuid()
		}
		d.entry.Debug().Msg("sending remove delta response")
		err := d.stream.Send(&svcdiscovery.DeltaDiscoveryResponse{
			RemovedResources: toRemove,
			ResponseNonce:    nonceStr,
		})
		if err != nil {
			emitDone(d.ctx, d.done, &DoneEmit{Err: err})
			return
		}
	}

	ticker := time.NewTicker(waitForResponse)
	defer ticker.Stop()
	select {
	case <-ticker.C:
		d.entry.Debug().Msg("wait for request nonce timed out")
		emitDone(d.ctx, d.done, &DoneEmit{Err: connect.Errorf(connect.CodeDeadlineExceeded, "Wait for request nonce timed out")})
		return
	case <-waitRecv:
	}
	if errors.Is(err, io.EOF) {
		// the agent half-closed the stream, there is no ack to check and the stream ends cleanly
		d.entry.Debug().Msg("agent closed the stream before acking")
		emitDone(d.ctx, d.done, &DoneEmit{Err: io.EOF})
		return
	}
	if err != nil {
		d.entry.Debug().Msgf("error receiving request: %s", err)
		emitDone(d.ctx, d.done, &DoneEmit{Err: fmt.Errorf("error receiving request: %w", err)})
		return
	}
	if resp.ResponseNonce != nonceStr {
		d.entry.Debug().Msgf("invalid nonce: %s", resp.ResponseNonce)
		d.emitErr(&ErrEmit{Err: fmt.Errorf("invalid nonce: %s", resp.ResponseNonce)})
		return
	}
	if resp.ErrorDetail != nil {
		d.entry.Debug().Msgf("error received from client: %s", resp.ErrorDetail)
		d.emitErr(&ErrEmit{Err: connect.NewError(connect.Code(resp.ErrorDetail.GetCode()), resp.ErrorDetail.GetMessage())})
		return
	}
	d.entry.Debug().Msgf("received request nonce %s", resp.ResponseNonce)
}

// emitDone and emitErr give up once the stream context is cancelled: the reader of
// these channels is gone by then and a plain send would block forever while holding
// d.mutex (or the init goroutine, which would leave the handler stuck on waitInit).
func emitDone(ctx context.Context, done chan *DoneEmit, result *DoneEmit) {
	select {
	case done <- result:
	case <-ctx.Done():
	}
}

func (d *DiscoAnList) emitErr(result *ErrEmit) {
	select {
	case d.errEmit <- result:
	case <-d.ctx.Done():
	}
}
