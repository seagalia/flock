// Command portserve answers 200 to every HTTP request on a range of ports. The load suite runs it in the client
// container: each port is a healthy endpoint for the health checks of the agent.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
)

func main() {
	from := flag.Int("from", 20000, "first port")
	count := flag.Int("count", 100, "number of ports, from the first one up")
	flag.Parse()

	raiseFileLimit()
	listeners := make([]net.Listener, *count)
	for n := range listeners {
		var err error
		listeners[n], err = net.Listen("tcp", ":"+strconv.Itoa(*from+n))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	err := serve(context.Background(), listeners)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// serve answers on every listener until ctx ends
func serve(ctx context.Context, listeners []net.Listener) error {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	servers := make([]*http.Server, len(listeners))
	errs := make([]error, len(listeners))
	var wg sync.WaitGroup
	for n, listener := range listeners {
		servers[n] = &http.Server{Handler: ok}
		wg.Go(func() {
			err := servers[n].Serve(listener)
			if !errors.Is(err, http.ErrServerClosed) {
				errs[n] = err
			}
		})
	}
	<-ctx.Done()
	for _, server := range servers {
		_ = server.Close()
	}
	wg.Wait()
	return errors.Join(errs...)
}
