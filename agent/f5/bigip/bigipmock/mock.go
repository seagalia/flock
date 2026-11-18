// Package bigipmock is a fake BIG-IP for the specs and the e2e suite of the F5 agent. It answers the iControl REST
// and AS3 calls the agent makes, with the shapes captured on a BIG-IP 17.5 lab (testdata/), and keeps the declared
// tenants in memory as AS3 would. pool_members.json is from the F5 documentation, the unlicensed lab has no pool.
package bigipmock

import (
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
)

const (
	Username = "admin"
	Password = "mock-password"
)

//go:embed testdata/*.json
var fixtures embed.FS

func fixture(name string) []byte {
	b, err := fixtures.ReadFile("testdata/" + name)
	if err != nil {
		panic(err)
	}
	return b
}

// NotLicensedResult is the task result of a declaration refused by an unlicensed BIG-IP, for FailNextTask.
var NotLicensedResult = func() json.RawMessage {
	var body struct {
		Results []json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(fixture("declare_not_licensed.json"), &body); err != nil {
		panic(err)
	}
	return body.Results[0]
}()

// InvalidDeclaration is the body of the 422 AS3 answers to a declaration failing its schema, for FailNext.
var InvalidDeclaration = string(fixture("declare_invalid.json"))

type Declare struct {
	Tenant string
	Action string
	Body   []byte
}

type failure struct {
	status int
	body   string
}

type task struct {
	results []json.RawMessage
	polled  bool
}

type Mock struct {
	server *httptest.Server

	mu         sync.Mutex
	token      string
	logins     int
	active     bool
	as3Version string
	busy       int
	failures   []failure
	taskFails  []json.RawMessage
	tenants    map[string]json.RawMessage
	partitions []string
	declares   []Declare
	tasks      map[string]*task
	// members holds the member states of a pool by "tenant/app/pool", each keyed by "ip:port"
	members map[string]map[string]bool
}

// New starts an active BIG-IP on a TLS test server. Close stops it.
func New() *Mock {
	m := &Mock{
		active:     true,
		as3Version: "3.56.0",
		tenants:    map[string]json.RawMessage{},
		tasks:      map[string]*task{},
		members:    map[string]map[string]bool{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /mgmt/shared/authn/login", m.login)
	mux.HandleFunc("GET /mgmt/tm/cm/failover-status", m.authed(m.failoverStatus))
	mux.HandleFunc("GET /mgmt/tm/auth/partition", m.authed(m.listPartitions))
	mux.HandleFunc("GET /mgmt/shared/appsvcs/info", m.authed(m.as3Info))
	mux.HandleFunc("POST /mgmt/shared/appsvcs/declare", m.authed(m.declare))
	mux.HandleFunc("GET /mgmt/shared/appsvcs/declare", m.authed(m.listTenants))
	mux.HandleFunc("DELETE /mgmt/shared/appsvcs/declare/{tenant}", m.authed(m.deleteTenant))
	mux.HandleFunc("GET /mgmt/shared/appsvcs/task/{id}", m.authed(m.getTask))
	mux.HandleFunc("GET /mgmt/tm/ltm/pool/{pool}/members", m.authed(m.poolMembers))
	m.server = httptest.NewTLSServer(mux)
	return m
}

func (m *Mock) URL() string {
	return m.server.URL
}

func (m *Mock) TLSConfig() *tls.Config {
	roots := x509.NewCertPool()
	roots.AddCert(m.server.Certificate())
	return &tls.Config{RootCAs: roots}
}

func (m *Mock) Close() {
	m.server.Close()
}

func (m *Mock) SetActive(active bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.active = active
}

func (m *Mock) SetAS3Version(version string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.as3Version = version
}

// SetBusy makes the next n declarations answer 503, as AS3 does while it processes another one.
func (m *Mock) SetBusy(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.busy = n
}

// FailNext makes the next declaration answer status with body, synchronously.
func (m *Mock) FailNext(status int, body string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failures = append(m.failures, failure{status: status, body: body})
}

// FailNextTask makes the task of the next declaration end with this result, NotLicensedResult for instance.
func (m *Mock) FailNextTask(result json.RawMessage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.taskFails = append(m.taskFails, result)
}

func (m *Mock) ExpireToken() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.token = ""
}

func (m *Mock) Logins() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.logins
}

// AddPartition adds a partition not managed by AS3, like one an operator created by hand.
func (m *Mock) AddPartition(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.partitions = append(m.partitions, name)
}

// AddTenant adds an AS3 tenant as if another tool declared it.
func (m *Mock) AddTenant(name, remark string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tenants[name], _ = json.Marshal(map[string]string{"class": "Tenant", "remark": remark})
}

// SetMemberStates sets the monitor states of the members of a pool, keyed by "ip:port".
func (m *Mock) SetMemberStates(tenant, app, pool string, states map[string]bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.members[tenant+"/"+app+"/"+pool] = maps.Clone(states)
}

func (m *Mock) Tenants() map[string]json.RawMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	return maps.Clone(m.tenants)
}

// Declares are every declaration posted, failed ones included, in order.
func (m *Mock) Declares() []Declare {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.declares)
}

func (m *Mock) ResetDeclares() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.declares = nil
}

func (m *Mock) login(w http.ResponseWriter, r *http.Request) {
	var creds struct {
		Username          string `json:"username"`
		Password          string `json:"password"`
		LoginProviderName string `json:"loginProviderName"`
	}
	_ = json.NewDecoder(r.Body).Decode(&creds)
	if creds.Username != Username || creds.Password != Password {
		writeJSON(w, http.StatusUnauthorized, `{"code":401,"message":"Authentication failed.","errorStack":[],"apiError":1}`)
		return
	}
	m.mu.Lock()
	m.logins++
	m.token = randomID()
	token := m.token
	m.mu.Unlock()
	writeJSON(w, http.StatusOK, strings.Replace(string(fixture("login.json")), "REDACTED", token, 1))
}

func (m *Mock) authed(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		valid := m.token != "" && r.Header.Get("X-F5-Auth-Token") == m.token
		m.mu.Unlock()
		if !valid {
			writeJSON(w, http.StatusUnauthorized, `{"code":401,"message":"Authorization failed: no user authentication header or token detected.","referer":"10.0.2.2","restOperationId":1,"kind":":resterrorresponse"}`)
			return
		}
		next(w, r)
	}
}

func (m *Mock) failoverStatus(w http.ResponseWriter, _ *http.Request) {
	m.mu.Lock()
	active := m.active
	m.mu.Unlock()
	body := string(fixture("failover_status.json"))
	if !active {
		body = strings.Replace(body, `"status":{"description":"ACTIVE"}`, `"status":{"description":"STANDBY"}`, 1)
	}
	writeJSON(w, http.StatusOK, body)
}

func (m *Mock) listPartitions(w http.ResponseWriter, _ *http.Request) {
	var body map[string]any
	_ = json.Unmarshal(fixture("partitions.json"), &body)
	common := body["items"].([]any)[0]
	m.mu.Lock()
	names := slices.Concat(m.partitions, slices.Collect(maps.Keys(m.tenants)))
	m.mu.Unlock()
	slices.Sort(names)
	items := make([]any, 0, 1+len(names))
	items = append(items, common)
	for _, name := range names {
		items = append(items, map[string]any{"kind": "tm:auth:partition:partitionstate", "name": name, "fullPath": name})
	}
	body["items"] = items
	b, _ := json.Marshal(body)
	writeJSON(w, http.StatusOK, string(b))
}

func (m *Mock) as3Info(w http.ResponseWriter, _ *http.Request) {
	m.mu.Lock()
	version := m.as3Version
	m.mu.Unlock()
	writeJSON(w, http.StatusOK, strings.ReplaceAll(string(fixture("appsvcs_info.json")), "3.56.0", version))
}

func (m *Mock) declare(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var doc struct {
		Action      string                     `json:"action"`
		Declaration map[string]json.RawMessage `json:"declaration"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		writeJSON(w, http.StatusBadRequest, `{"code":400,"message":"invalid JSON"}`)
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	tenants := tenantsOf(doc.Declaration)
	for _, name := range tenants {
		m.declares = append(m.declares, Declare{Tenant: name, Action: doc.Action, Body: body})
	}
	if m.busy > 0 {
		m.busy--
		writeJSON(w, http.StatusServiceUnavailable, `{"code":503,"message":"Configuration operation in progress on device, please try again in 2 minutes"}`)
		return
	}
	if len(m.failures) > 0 {
		f := m.failures[0]
		m.failures = m.failures[1:]
		writeJSON(w, f.status, f.body)
		return
	}

	var results []json.RawMessage
	if len(m.taskFails) > 0 {
		results = append(results, m.taskFails[0])
		m.taskFails = m.taskFails[1:]
	} else {
		for _, name := range tenants {
			results = append(results, m.apply(name, doc.Declaration[name], doc.Action == "dry-run"))
		}
	}
	if r.URL.Query().Get("async") != "true" {
		b, _ := json.Marshal(map[string]any{"results": results})
		writeJSON(w, http.StatusOK, string(b))
		return
	}
	id := randomID()
	m.tasks[id] = &task{results: results}
	writeJSON(w, http.StatusAccepted, strings.ReplaceAll(string(fixture("declare_async.json")), "977289a2-139f-4aee-a7b0-e11367382102", id))
}

// apply declares a tenant as AS3 does: "no change" when it is the same, "success" otherwise.
func (m *Mock) apply(name string, tenant json.RawMessage, dryRun bool) json.RawMessage {
	message := "success"
	if current, ok := m.tenants[name]; ok && jsonEqual(current, tenant) {
		message = "no change"
	}
	if !dryRun {
		m.tenants[name] = tenant
	}
	result, _ := json.Marshal(map[string]any{"code": 200, "message": message, "host": "localhost", "tenant": name, "dryRun": dryRun})
	return result
}

func (m *Mock) getTask(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[r.PathValue("id")]
	if !ok {
		writeJSON(w, http.StatusNotFound, `{"code":404,"message":"task not found"}`)
		return
	}
	// the first poll finds the task still running, as it usually is on a real device
	if !t.polled {
		t.polled = true
		writeJSON(w, http.StatusOK, `{"id":"`+r.PathValue("id")+`","results":[{"message":"in progress","tenant":"","host":"","runTime":0,"code":0}]}`)
		return
	}
	b, _ := json.Marshal(map[string]any{"id": r.PathValue("id"), "results": t.results})
	writeJSON(w, http.StatusOK, string(b))
}

func (m *Mock) listTenants(w http.ResponseWriter, _ *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.tenants) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	declaration := map[string]any{"class": "ADC", "schemaVersion": "3.50.0"}
	for name, tenant := range m.tenants {
		declaration[name] = tenant
	}
	b, _ := json.Marshal(declaration)
	writeJSON(w, http.StatusOK, string(b))
}

func (m *Mock) deleteTenant(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	name := r.PathValue("tenant")
	message := "no change"
	if _, ok := m.tenants[name]; ok {
		message = "success"
		delete(m.tenants, name)
	}
	writeJSON(w, http.StatusOK, fmt.Sprintf(`{"results":[{"code":200,"message":%q,"host":"localhost","tenant":%q}]}`, message, name))
}

func (m *Mock) poolMembers(w http.ResponseWriter, r *http.Request) {
	path := strings.Split(strings.TrimPrefix(r.PathValue("pool"), "~"), "~")
	m.mu.Lock()
	states, ok := m.members[strings.Join(path, "/")]
	m.mu.Unlock()
	if len(path) != 3 || !ok {
		writeJSON(w, http.StatusNotFound, fmt.Sprintf(`{"code":404,"message":"01020036:3: The requested Pool (/%s) was not found.","errorStack":[],"apiError":3}`, strings.Join(path, "/")))
		return
	}
	var body map[string]any
	_ = json.Unmarshal(fixture("pool_members.json"), &body)
	delete(body, "_unverified")
	items := []any{}
	for _, hostPort := range slices.Sorted(maps.Keys(states)) {
		host, port, _ := net.SplitHostPort(hostPort)
		name := host + ":" + port
		if strings.Contains(host, ":") {
			name = host + "." + port
		}
		state := "down"
		if states[hostPort] {
			state = "up"
		}
		items = append(items, map[string]any{"name": name, "address": host, "state": state, "partition": path[0]})
	}
	body["items"] = items
	b, _ := json.Marshal(body)
	writeJSON(w, http.StatusOK, string(b))
}

func tenantsOf(declaration map[string]json.RawMessage) []string {
	var names []string
	for name, raw := range declaration {
		var obj struct {
			Class string `json:"class"`
		}
		if json.Unmarshal(raw, &obj) == nil && obj.Class == "Tenant" {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

func jsonEqual(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	ax, _ := json.Marshal(x)
	by, _ := json.Marshal(y)
	return string(ax) == string(by)
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func randomID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
