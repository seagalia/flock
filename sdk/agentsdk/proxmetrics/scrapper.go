package proxmetrics

import (
	"compress/gzip"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"time"
)

const acceptHeader = `application/openmetrics-text; version=0.0.1,text/plain;version=0.0.4;q=0.5,*/*;q=0.1`

type Scraper struct {
	httpClient *http.Client
	dialer     *net.Dialer
}

func NewScraper(tlsConf *tls.Config) *Scraper {
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	httpClient := &http.Client{
		Timeout: time.Second * 10,
		Transport: &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			DialContext:         dialer.DialContext,
			ForceAttemptHTTP2:   true,
			MaxIdleConns:        100,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
			TLSClientConfig:     tlsConf,
		},
	}
	return &Scraper{
		httpClient: httpClient,
		dialer:     dialer,
	}
}
func (s *Scraper) Scrape(target *ProxyMetricsTarget) (io.ReadCloser, error) {
	targetURL := scrapeURL(target)
	req, err := http.NewRequest("GET", targetURL, nil)
	if err != nil {
		return nil, err
	}
	if target.User != "" {
		req.SetBasicAuth(target.User, target.Password)
	}
	maps.Copy(req.Header, target.Headers)
	scrapeTimeout := (30 * time.Second).Seconds()
	if target.ScrapeTimeout > 0 {
		scrapeTimeout = time.Duration(target.ScrapeTimeout).Seconds()
	}
	req.Header.Add("Accept", acceptHeader)
	req.Header.Add("Accept-Encoding", "gzip")
	req.Header.Set("X-Prometheus-Scrape-Timeout-Seconds", fmt.Sprintf("%f", scrapeTimeout))
	req.Header.Set("X-Controller-Scrapping", "true")
	resp, err := s.clientFor(target).Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		if resp.StatusCode >= 400 && resp.StatusCode <= 499 {
			return nil, ErrNoEndpointFound(
				fmt.Sprintf(
					"Target %s (status code %d)",
					target.Name,
					resp.StatusCode,
				), targetURL,
			)
		}
		return nil, fmt.Errorf("server returned HTTP status %s", resp.Status)
	}

	if resp.Header.Get("Content-Encoding") != "gzip" {
		return resp.Body, nil
	}
	gzReader, err := NewReaderGzip(resp.Body)
	if err != nil {
		_ = resp.Body.Close()
		return nil, err
	}
	return gzReader, nil
}

// scrapeURL gives /metrics to a target on a unix socket with no URL, the host of such a url is never resolved.
func scrapeURL(target *ProxyMetricsTarget) string {
	if target.URL == nil {
		return "http://localhost/metrics"
	}
	return target.URL.String()
}

func (s *Scraper) clientFor(target *ProxyMetricsTarget) *http.Client {
	if target.SocketPath == "" {
		return s.httpClient
	}

	return &http.Client{
		Timeout: s.httpClient.Timeout,
		Transport: &http.Transport{
			DisableKeepAlives: true,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return s.dialer.DialContext(ctx, "unix", target.SocketPath)
			},
		},
	}
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

func (r *ReaderGzip) Read(p []byte) (n int, err error) {
	return r.gzip.Read(p)
}

func (r *ReaderGzip) Close() error {
	r.gzip.Close()
	r.main.Close()
	return nil
}
