//go:build e2e

package f5_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/seagalia/flock/agent/f5/bigip"
)

const (
	labTenant = "flock_e2e_lab"
	// l7NotLicensed is how the unlicensed lab refuses HTTP:: commands in a proc, where it can't see they run in an http
	// event. The same commands compile inside when HTTP_REQUEST.
	l7NotLicensed = "ltm_rule_L7 feature not licensed"
	extAuthProc   = "proc check {} {\n"
)

// goldenPoolPath and goldenAuthPath match the pool and external auth virtual paths of the golden iRules: the probes
// compile them in /Common where the objects of the golden tenant don't exist, so they reference a variable instead.
var (
	goldenPoolPath = regexp.MustCompile(`/flock/vip_[0-9a-f_]+/pool_[0-9a-f_]+`)
	goldenAuthPath = regexp.MustCompile(`/flock/vip_[0-9a-f_]+/auth_[0-9a-z_]+`)
)

var compileError = regexp.MustCompile(`error: \[([^\]]*)\]`)

// The lab specs run against a real BIG-IP, only when F5_LAB_URL and F5_LAB_PASSWORD are set (make e2e-agent-f5-lab).
// The lab is unlicensed: AS3 validates and dry-runs declarations, a real deployment is refused as not licensed.
var _ = Describe("BIG-IP lab", Label("lab"), func() {
	var (
		url      string
		password string
		username string
		device   *bigip.Client
	)

	BeforeEach(func() {
		url, password = os.Getenv("F5_LAB_URL"), os.Getenv("F5_LAB_PASSWORD")
		if url == "" || password == "" {
			Skip("F5_LAB_URL and F5_LAB_PASSWORD are not set")
		}
		username = os.Getenv("F5_LAB_USERNAME")
		if username == "" {
			username = "admin"
		}
		device = bigip.New(bigip.Options{
			URLs: []string{url}, Username: username, Password: password,
			TLSConfig:      &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // the lab has a self-signed certificate
			RequestTimeout: 2 * time.Minute,
		})
	})

	It("reaches the device and reads its AS3 version", func(ctx SpecContext) {
		devices := device.Devices(ctx)
		Expect(devices).To(HaveLen(1))
		Expect(devices[0].Err).ToNot(HaveOccurred())
		Expect(devices[0].Reachable).To(BeTrue())
		Expect(devices[0].AS3Version).ToNot(BeEmpty())
	}, SpecTimeout(time.Minute))

	It("accepts every golden declaration in a dry run", func(ctx SpecContext) {
		goldens, err := filepath.Glob(filepath.Join("..", "..", "..", "agent", "f5", "as3", "testdata", "*.json"))
		Expect(err).ToNot(HaveOccurred())
		Expect(goldens).ToNot(BeEmpty())
		for _, golden := range goldens {
			body, err := os.ReadFile(golden)
			Expect(err).ToNot(HaveOccurred())
			_, err = device.Declare(ctx, "flock", body)
			Expect(err).ToNot(HaveOccurred(), "dry run of %s", filepath.Base(golden))
		}
	}, SpecTimeout(10*time.Minute))

	It("deploys a tenant, or is refused only for the missing license", func(ctx SpecContext) {
		DeferCleanup(func(ctx SpecContext) {
			Expect(device.DeleteTenant(ctx, labTenant)).To(Succeed())
		}, NodeTimeout(2*time.Minute))
		body := labDeployment()

		_, err := device.Declare(ctx, labTenant, body)

		if err != nil {
			Expect(bigip.IsNotLicensed(err)).To(BeTrue(), "unexpected refusal: %v", err)
		}
	}, SpecTimeout(5*time.Minute))

	It("compiles every golden iRule", func(ctx SpecContext) {
		token := labLogin(ctx, url, username, password)
		for name, rule := range goldenIRules() {
			probe := "flock_probe_" + name
			DeferCleanup(func(ctx SpecContext) {
				status, _ := labCall(ctx, url, token, http.MethodDelete, "/mgmt/tm/ltm/rule/~Common~"+probe, nil)
				Expect(status).To(Or(Equal(http.StatusOK), Equal(http.StatusNotFound)))
			}, NodeTimeout(time.Minute))

			payload := labJSON(map[string]string{"name": probe, "partition": "Common", "apiAnonymous": rule})
			status, answer := labCall(ctx, url, token, http.MethodPost, "/mgmt/tm/ltm/rule", payload)
			if !strings.Contains(name, "_irule_extauth_") {
				Expect(status).To(Equal(http.StatusOK), "iRule %s: %s", name, answer)
				continue
			}
			refusedForLicense := status == http.StatusBadRequest && onlyNotLicensed(answer)
			Expect(status == http.StatusOK || refusedForLicense).To(BeTrue(), "iRule %s: %s", name, answer)
			if refusedForLicense {
				GinkgoWriter.Printf("iRule %s refused only for the L7 license, its body is compiled in HTTP_REQUEST\n", name)
			}

			bodyProbe := probe + "_body"
			DeferCleanup(func(ctx SpecContext) {
				status, _ := labCall(ctx, url, token, http.MethodDelete, "/mgmt/tm/ltm/rule/~Common~"+bodyProbe, nil)
				Expect(status).To(Or(Equal(http.StatusOK), Equal(http.StatusNotFound)))
			}, NodeTimeout(time.Minute))
			payload = labJSON(map[string]string{"name": bodyProbe, "partition": "Common", "apiAnonymous": extAuthBodyProbe(rule)})
			status, answer = labCall(ctx, url, token, http.MethodPost, "/mgmt/tm/ltm/rule", payload)
			Expect(status).To(Equal(http.StatusOK), "body of iRule %s in HTTP_REQUEST: %s", name, answer)
		}
	}, SpecTimeout(5*time.Minute))
})

func labDeployment() []byte {
	GinkgoHelper()
	body, err := os.ReadFile(filepath.Join("..", "..", "..", "agent", "f5", "as3", "testdata", "tcp.json"))
	Expect(err).ToNot(HaveOccurred())
	var doc map[string]any
	Expect(json.Unmarshal(body, &doc)).To(Succeed())
	declaration := doc["declaration"].(map[string]any)
	declaration[labTenant] = declaration["flock"]
	delete(declaration, "flock")
	doc["action"] = "deploy"
	body, err = json.Marshal(doc)
	Expect(err).ToNot(HaveOccurred())
	return bytes.ReplaceAll(body, []byte("/flock/"), []byte("/"+labTenant+"/"))
}

// goldenIRules gives the iRules of the golden declarations by golden and object name, their pools replaced.
func goldenIRules() map[string]string {
	GinkgoHelper()
	goldens, err := filepath.Glob(filepath.Join("..", "..", "..", "agent", "f5", "as3", "testdata", "*.json"))
	Expect(err).ToNot(HaveOccurred())
	rules := map[string]string{}
	for _, golden := range goldens {
		body, err := os.ReadFile(golden)
		Expect(err).ToNot(HaveOccurred())
		var doc struct {
			Declaration map[string]json.RawMessage `json:"declaration"`
		}
		Expect(json.Unmarshal(body, &doc)).To(Succeed())
		var tenant map[string]json.RawMessage
		Expect(json.Unmarshal(doc.Declaration["flock"], &tenant)).To(Succeed())
		for _, appRaw := range tenant {
			var app map[string]json.RawMessage
			if json.Unmarshal(appRaw, &app) != nil {
				continue
			}
			for objectName, objectRaw := range app {
				var object struct {
					Class string `json:"class"`
					IRule string `json:"iRule"`
				}
				if json.Unmarshal(objectRaw, &object) != nil || object.Class != "iRule" {
					continue
				}
				base := filepath.Base(golden)
				name := base[:len(base)-len(filepath.Ext(base))] + "_" + objectName
				rule := goldenPoolPath.ReplaceAllString(object.IRule, "$$flock_probe_pool")
				rules[name] = goldenAuthPath.ReplaceAllString(rule, "$$flock_probe_auth")
			}
		}
	}
	return rules
}

func onlyNotLicensed(answer []byte) bool {
	errs := compileError.FindAllSubmatch(answer, -1)
	for _, e := range errs {
		if string(e[1]) != l7NotLicensed {
			return false
		}
	}
	return len(errs) > 0
}

// extAuthBodyProbe puts the body of the check proc in an HTTP_REQUEST event, where the unlicensed lab compiles its
// HTTP:: commands, so its TCL is still checked.
func extAuthBodyProbe(rule string) string {
	GinkgoHelper()
	Expect(rule).To(HavePrefix(extAuthProc))
	Expect(rule).To(HaveSuffix("\n}\n"))
	body := strings.TrimSuffix(strings.TrimPrefix(rule, extAuthProc), "}\n")
	return "when HTTP_REQUEST {\n" + body + "}\n"
}

// labJSON encodes without the HTML escaping of json.Marshal: iControl REST misreads the escaped < and & of an iRule.
func labJSON(v any) []byte {
	GinkgoHelper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	Expect(enc.Encode(v)).To(Succeed())
	return buf.Bytes()
}

func labLogin(ctx context.Context, url, username, password string) string {
	GinkgoHelper()
	payload := labJSON(map[string]string{"username": username, "password": password, "loginProviderName": "tmos"})
	status, answer := labCall(ctx, url, "", http.MethodPost, "/mgmt/shared/authn/login", payload)
	// the answer of a failed login is not printed, it may echo what was sent
	Expect(status).To(Equal(http.StatusOK), "login on the lab failed")
	var login struct {
		Token struct {
			Token string `json:"token"`
		} `json:"token"`
	}
	Expect(json.Unmarshal(answer, &login)).To(Succeed())
	return login.Token.Token
}

func labCall(ctx context.Context, url, token, method, path string, body []byte) (int, []byte) {
	GinkgoHelper()
	req, err := http.NewRequestWithContext(ctx, method, url+path, bytes.NewReader(body))
	Expect(err).ToNot(HaveOccurred())
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-F5-Auth-Token", token)
	}
	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // the lab has a self-signed certificate
	}}
	resp, err := client.Do(req)
	Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("%s %s", method, path))
	defer func() { _ = resp.Body.Close() }()
	answer, err := io.ReadAll(resp.Body)
	Expect(err).ToNot(HaveOccurred())
	return resp.StatusCode, answer
}
