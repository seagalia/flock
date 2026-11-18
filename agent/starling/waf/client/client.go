package client

import (
	"fmt"

	svcregister "github.com/seagalia/flock/sdk/starling_waf/services/register/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Client calls the Register service of the WAF on its unix socket.
type Client struct {
	svcregister.RegisterClient
	conn *grpc.ClientConn
}

// NewClient connects lazily and its calls wait for the socket, since the WAF may start after the agent.
func NewClient(socketPath string) (*Client, error) {
	conn, err := grpc.NewClient("unix:"+socketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.WaitForReady(true)),
	)
	if err != nil {
		return nil, fmt.Errorf("create WAF client on %s: %w", socketPath, err)
	}
	return &Client{
		RegisterClient: svcregister.NewRegisterClient(conn),
		conn:           conn,
	}, nil
}

func (c *Client) Close() error {
	return c.conn.Close()
}
