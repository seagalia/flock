// Command txtshim turns the calls of lego's httpreq DNS provider into TXT records of pebble's challtestsrv, the DNS
// server pebble validates dns-01 against. The provisioner suite runs it next to each replica.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

const (
	forwardTimeout    = 10 * time.Second
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 10 * time.Second
	// an answer waits for the forward to challtestsrv
	writeTimeout = forwardTimeout + 5*time.Second
	idleTimeout  = time.Minute
)

// httpreqMessage is the body lego's httpreq provider posts in its default mode
type httpreqMessage struct {
	FQDN  string `json:"fqdn"`
	Value string `json:"value"`
}

type txtRecord struct {
	Host  string `json:"host"`
	Value string `json:"value,omitempty"`
}

func main() {
	listen := flag.String("listen", "127.0.0.1:8090", "address the httpreq provider posts to")
	management := flag.String("management", "", "base url of the management API of challtestsrv")
	flag.Parse()

	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// the suite waits for this line before it creates a certificate, a present sent earlier would be refused
	fmt.Println("txtshim listening on", listener.Addr())
	server := &http.Server{
		Handler:           newHandler(*management),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}
	err = server.Serve(listener)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newHandler(management string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /present", func(w http.ResponseWriter, r *http.Request) {
		forward(w, r, management+"/set-txt", true)
	})
	mux.HandleFunc("POST /cleanup", func(w http.ResponseWriter, r *http.Request) {
		forward(w, r, management+"/clear-txt", false)
	})
	return mux
}

// forward answers 502 when challtestsrv fails, so lego reports the failed challenge instead of waiting for a record
// that never comes
func forward(w http.ResponseWriter, r *http.Request, url string, withValue bool) {
	var msg httpreqMessage
	err := json.NewDecoder(r.Body).Decode(&msg)
	if err != nil {
		http.Error(w, fmt.Sprintf("decode httpreq message: %s", err), http.StatusBadRequest)
		return
	}
	record := txtRecord{Host: msg.FQDN}
	if withValue {
		record.Value = msg.Value
	}
	err = post(r.Context(), url, record)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func post(ctx context.Context, url string, record txtRecord) error {
	ctx, cancel := context.WithTimeout(ctx, forwardTimeout)
	defer cancel()
	payload, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode txt record: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build request to %s: %w", url, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("post to %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("post to %s: status %d", url, resp.StatusCode)
	}
	return nil
}
