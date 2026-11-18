// Command connhold holds many TCP connections open against http servers, each sending a request every few
// seconds, and writes how many it holds to a JSON status file. The load suite runs it in the client container:
// curl can't hold thousands of connections.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type status struct {
	// Established is the connections open right now, Failed the ones that could not open or broke
	Established int  `json:"established"`
	Failed      int  `json:"failed"`
	Requests    int  `json:"requests"`
	Done        bool `json:"done"`
	// LastError says why the latest failed connection failed
	LastError string `json:"last_error,omitempty"`
}

const (
	dialTimeout = 5 * time.Second
	// dialsInFlight keeps the dials from flooding the conntrack table and the accept queues at once
	dialsInFlight = 200
	request       = "GET /get HTTP/1.1\r\nHost: load\r\n\r\n"
)

func main() {
	addrs := flag.String("addrs", "", "comma separated ip:port to connect to, round robin")
	conns := flag.Int("conns", 100, "connections to hold")
	holdFor := flag.Duration("hold", time.Minute, "how long to hold the connections")
	every := flag.Duration("every", 5*time.Second, "interval between two requests on a connection")
	statusPath := flag.String("status", "/tmp/connhold.json", "status file")
	flag.Parse()

	raiseFileLimit()
	writeStatus := func(s status) {
		err := writeJSON(*statusPath, s)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
	}
	hold(context.Background(), strings.Split(*addrs, ","), *conns, *holdFor, *every, writeStatus)
}

// hold opens conns connections, keeps them busy for holdFor and reports every second and once done
func hold(ctx context.Context, addrs []string, conns int, holdFor, every time.Duration, report func(status)) {
	ctx, cancel := context.WithTimeout(ctx, holdFor)
	defer cancel()
	var established, failed, requests atomic.Int64
	var lastError atomic.Value
	lastError.Store("")
	fail := func(err error) {
		failed.Add(1)
		lastError.Store(err.Error())
	}
	snapshot := func(done bool) status {
		return status{
			Established: int(established.Load()),
			Failed:      int(failed.Load()),
			Requests:    int(requests.Load()),
			Done:        done,
			LastError:   lastError.Load().(string),
		}
	}

	dials := make(chan struct{}, dialsInFlight)
	var wg sync.WaitGroup
	for i := range conns {
		wg.Go(func() {
			dials <- struct{}{}
			conn, err := (&net.Dialer{Timeout: dialTimeout}).DialContext(ctx, "tcp", addrs[i%len(addrs)])
			<-dials
			if err != nil {
				fail(err)
				return
			}
			established.Add(1)
			err = keepBusy(ctx, conn, every, &requests)
			// the end of the hold closes the connection too, that one is still counted as held
			brokeBeforeEnd := err != nil && ctx.Err() == nil
			if brokeBeforeEnd {
				established.Add(-1)
				fail(err)
			}
		})
	}

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			report(snapshot(false))
		case <-ctx.Done():
			wg.Wait()
			report(snapshot(true))
			return
		}
	}
}

// keepBusy sends a request every `every` until ctx ends or the connection breaks
func keepBusy(ctx context.Context, conn net.Conn, every time.Duration, requests *atomic.Int64) error {
	stopClosing := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopClosing()
	defer conn.Close()
	reader := bufio.NewReader(conn)
	for {
		_, err := io.WriteString(conn, request)
		if err != nil {
			return err
		}
		resp, err := http.ReadResponse(reader, nil)
		if err != nil {
			return err
		}
		_, err = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return err
		}
		requests.Add(1)
		if resp.Close {
			return io.EOF
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(every):
		}
	}
}

// writeJSON replaces the file in one rename, the suite never reads half a status
func writeJSON(path string, s status) error {
	content, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	err = os.WriteFile(tmp, content, 0o644)
	if err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
