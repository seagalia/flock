package agentserv

import (
	"context"
	"errors"
	"io"

	"connectrpc.com/connect/v2"
	agentlogs "github.com/seagalia/flock/sdk/flock/services/agent/logs/v1"
	"github.com/seagalia/flock/sdk/flock/services/agent/logs/v1/agentlogsconnect"
	"google.golang.org/protobuf/types/known/emptypb"
)

const followBufferSize = 1000

type listenerStreamLog struct {
	lines chan string
}

// OnLogReceive is called concurrently, lines are queued as a stream must not be sent to concurrently
func (l *listenerStreamLog) OnLogReceive(logLine string) {
	select {
	case l.lines <- logLine:
	default:
		// client is too slow, drop line instead of blocking
	}
}

func (s *Server) Follow(ctx context.Context, _ *emptypb.Empty, fs agentlogsconnect.LogsFollowServerStream) error {
	listLog := &listenerStreamLog{
		lines: make(chan string, followBufferSize),
	}
	s.logListHook.AddListener(listLog)
	defer s.logListHook.RemoveListener(listLog)
	started := &agentlogs.LogStreamResponse{Event: &agentlogs.LogStreamResponse_Started{Started: &agentlogs.LogStreamResponse_StreamStarted{}}}
	if err := fs.Send(started); err != nil {
		return followSendError(ctx, err)
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case logLine := <-listLog.lines:
			err := fs.Send(&agentlogs.LogStreamResponse{
				Event: &agentlogs.LogStreamResponse_LogLine{LogLine: logLine},
			})
			if err != nil {
				return followSendError(ctx, err)
			}
		}
	}
}

func followSendError(ctx context.Context, err error) error {
	isClientGone := errors.Is(err, io.EOF) || ctx.Err() != nil
	if isClientGone {
		return nil
	}
	return connect.Errorf(connect.CodeInternal, "failed to send log: %v", err)
}

func (s *Server) Latest(ctx context.Context, req *emptypb.Empty) (*agentlogs.LogLatestResponse, error) {
	return &agentlogs.LogLatestResponse{
		LogLines: s.logListHook.LatestLines(),
	}, nil
}
