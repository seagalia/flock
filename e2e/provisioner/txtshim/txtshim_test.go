package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type received struct {
	method string
	path   string
	body   map[string]string
}

var _ = Describe("the txt shim", func() {
	var (
		challtestsrv *httptest.Server
		shim         *httptest.Server
		calls        chan received
		answer       int
	)

	BeforeEach(func() {
		calls = make(chan received, 10)
		answer = http.StatusOK
		challtestsrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body := map[string]string{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			calls <- received{method: r.Method, path: r.URL.Path, body: body}
			w.WriteHeader(answer)
		}))
		DeferCleanup(challtestsrv.Close)
		shim = httptest.NewServer(newHandler(challtestsrv.URL))
		DeferCleanup(shim.Close)
	})

	post := func(ctx SpecContext, path string, body map[string]string) int {
		GinkgoHelper()
		payload, err := json.Marshal(body)
		Expect(err).ToNot(HaveOccurred())
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, shim.URL+path, bytes.NewReader(payload))
		Expect(err).ToNot(HaveOccurred())
		resp, err := http.DefaultClient.Do(req)
		Expect(err).ToNot(HaveOccurred())
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	It("forwards a present as a TXT record set with its host and value", func(ctx SpecContext) {
		status := post(ctx, "/present", map[string]string{"fqdn": "_acme-challenge.shop.flock.test.", "value": "token"})

		Expect(status).To(Equal(http.StatusOK))
		Expect(calls).To(Receive(Equal(received{
			method: http.MethodPost,
			path:   "/set-txt",
			body:   map[string]string{"host": "_acme-challenge.shop.flock.test.", "value": "token"},
		})))
	})

	It("forwards a cleanup as a TXT record cleared with its host", func(ctx SpecContext) {
		status := post(ctx, "/cleanup", map[string]string{"fqdn": "_acme-challenge.shop.flock.test.", "value": "token"})

		Expect(status).To(Equal(http.StatusOK))
		Expect(calls).To(Receive(Equal(received{
			method: http.MethodPost,
			path:   "/clear-txt",
			body:   map[string]string{"host": "_acme-challenge.shop.flock.test."},
		})))
	})

	It("answers 502 when challtestsrv refuses the record", func(ctx SpecContext) {
		answer = http.StatusInternalServerError

		status := post(ctx, "/present", map[string]string{"fqdn": "_acme-challenge.shop.flock.test.", "value": "token"})

		Expect(status).To(Equal(http.StatusBadGateway))
	})
})
