package proxmetrics

import (
	"compress/gzip"
	"context"
	"crypto/tls"
	"fmt"
	"github.com/seagalia/flock/sdk/flock/api/config/agent/v1"
	"io"
	"net"
	"net/http"
	"time"
)

const (
	acceptHeader = `application/openmetrics-text; version=0.0.1,text/plain;version=0.0.4;q=0.5,*/*;q=0.1`

	// pingTimeout bounds the dial of the health check of an agent
	pingTimeout = 5 * time.Second
)

type Scraper struct {
	httpClient *http.Client
}

func NewScraper(tlsConf *tls.Config) *Scraper {
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	httpClient := &http.Client{
		Timeout: time.Second * 10,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           dialer.DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			TLSClientConfig:       tlsConf,
		},
	}
	return &Scraper{
		httpClient: httpClient,
	}

}

func (s Scraper) Ping(ctx context.Context, agentInfo *agent.Agent) bool {
	dialer := &net.Dialer{Timeout: pingTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", agentInfo.Host)
	if err != nil {
		return false
	}
	defer conn.Close()
	return true
}

func (s Scraper) Scrape(
	ctx context.Context, agentInfo *agent.Agent, path string, instanceUuid string,
) (io.ReadCloser, error) {
	urlRaw := fmt.Sprintf("https://%s%s", agentInfo.Host, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlRaw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Add("Accept", acceptHeader)
	req.Header.Add("Accept-Encoding", "gzip")
	req.Header.Set("X-Prometheus-Scrape-Timeout-Seconds", fmt.Sprintf("%f", (30*time.Second).Seconds()))
	req.Header.Set("X-Controller-Scrapping", "true")
	req.Header.Set("X-Ctx-Instance-Uuid", instanceUuid)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode >= 400 && resp.StatusCode <= 499 {
			return nil, ErrNoEndpointFound(
				fmt.Sprintf(
					"agent %s - host: %s (status code %d)",
					agentInfo.Name,
					agentInfo.Host,
					resp.StatusCode,
				), path,
			)
		}
		return nil, fmt.Errorf("server returned HTTP status %s", resp.Status)
	}

	if resp.Header.Get("Content-Encoding") != "gzip" {
		return resp.Body, nil
	}
	gzReader, err := NewReaderGzip(resp.Body)
	if err != nil {
		resp.Body.Close()
		return nil, err
	}
	return gzReader, nil
}

type ReaderGzip struct {
	main io.ReadCloser
	gzip *gzip.Reader
}

func NewReaderGzip(main io.ReadCloser) (*ReaderGzip, error) {
	gzReader, err := gzip.NewReader(main)
	if err != nil {
		return nil, err
	}
	return &ReaderGzip{
		main: main,
		gzip: gzReader,
	}, nil
}

func (r ReaderGzip) Read(p []byte) (n int, err error) {
	return r.gzip.Read(p)
}

func (r ReaderGzip) Close() error {
	r.gzip.Close()
	r.main.Close()
	return nil
}
