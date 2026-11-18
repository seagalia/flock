package rpcserver

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"
)

// Shutdown stops servers in parallel. http.Server.Shutdown alone waits for every request to end, and a stream
// following logs never ends by itself, so cancelDrain, which cancels the BaseContext of the servers, is called once
// grace has passed. A server still busy at timeout is closed, dropping its connections.
func Shutdown(servers []*http.Server, cancelDrain context.CancelFunc, grace, timeout time.Duration) error {
	defer cancelDrain()
	graceTimer := time.AfterFunc(grace, cancelDrain)
	defer graceTimer.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	errs := make([]error, len(servers))
	var wg sync.WaitGroup
	for i, server := range servers {
		wg.Go(func() {
			err := server.Shutdown(ctx)
			if errors.Is(err, context.DeadlineExceeded) {
				err = errors.Join(err, server.Close())
			}
			errs[i] = err
		})
	}
	wg.Wait()
	return errors.Join(errs...)
}
