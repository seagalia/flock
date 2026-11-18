package cli

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/cli/internal/fakeconnect"
	"github.com/seagalia/flock/cli/resfile"
	"github.com/seagalia/flock/sdk/clients"
	backendv1 "github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	certv1 "github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	routev1 "github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	vipv1 "github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	vsv1 "github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	svcbackend "github.com/seagalia/flock/sdk/flock/services/backend/v1"
	"github.com/seagalia/flock/sdk/flock/services/backend/v1/svcbackendconnect"
	svccert "github.com/seagalia/flock/sdk/flock/services/cert/v1"
	"github.com/seagalia/flock/sdk/flock/services/cert/v1/svccertconnect"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcvip "github.com/seagalia/flock/sdk/flock/services/vip/v1"
	"github.com/seagalia/flock/sdk/flock/services/vip/v1/svcvipconnect"
	svcvs "github.com/seagalia/flock/sdk/flock/services/vs/v1"
	"github.com/seagalia/flock/sdk/flock/services/vs/v1/svcvsconnect"
	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/protobuf/proto"
)

const backendDoc = `kind: backend
name: web
ip_addr_group:
  ip_addresses:
  - ip: 10.0.0.10
    port: 8080
health_check:
  no_health_check: {}
`

const vsDoc = `kind: vs
name: web
port: 80
http_routes_config:
  virtual_hosts:
  - domains: ['*']
    routes:
    - name: default
      match: {prefix: /}
      backend_uuid: name:web
`

const vipDoc = `kind: vip
name: public
ip: 192.0.2.10
virtual_servers_uuid:
- name:web
`

// nameFilter is the value of the name filter of a list request.
func nameFilter(req proto.Message) string {
	filters := req.(interface{ GetFilters() []*svccore.ListFilter }).GetFilters()
	for _, filter := range filters {
		if filter.GetKey() == "name" {
			return filter.GetValue()
		}
	}
	return ""
}

func parseDocs(contents ...string) []resfile.Document {
	GinkgoHelper()
	var docs []resfile.Document
	for _, content := range contents {
		parsed, problems := resfile.Parse("test.yml", []byte(content))
		Expect(problems).To(BeEmpty())
		docs = append(docs, parsed...)
	}
	return docs
}

func changeNames(changes []change) []string {
	names := make([]string, 0, len(changes))
	for _, c := range changes {
		names = append(names, c.String())
	}
	return names
}

var _ = Describe("apply plan", func() {
	var (
		conn   *fakeconnect.Conn
		client *clients.Client
		ctx    = context.Background()
	)

	// stores answers the list calls with the resources whose name is in the filter, like the controller.
	storeBackends := func(stored ...*backendv1.Backend) {
		conn.Handle(svcbackendconnect.BackendListBackendProcedure, func(req proto.Message) (proto.Message, error) {
			resp := &svcbackend.ListBackendResponse{}
			if req.(*svcbackend.ListBackendRequest).GetOffset() > 0 {
				return resp, nil
			}
			for _, b := range stored {
				if b.GetName() == nameFilter(req) {
					resp.Backends = append(resp.Backends, b)
				}
			}
			return resp, nil
		})
	}
	storeEmpty := func() {
		storeBackends()
		conn.Handle(svcvsconnect.VirtualServerListVSProcedure, firstPage(&svcvs.ListVSResponse{}))
		conn.Handle(svcvipconnect.VIPListVIPProcedure, firstPage(&svcvip.ListVIPResponse{}))
		conn.Handle(svccertconnect.CertificateListCertificateProcedure, firstPage(&svccert.ListCertificateResponse{}))
	}

	BeforeEach(func() {
		conn = fakeconnect.New()
		client = clients.New(conn.Client())
	})

	Describe("planApply", func() {
		It("creates what doesn't exist in kind order", func() {
			storeEmpty()

			plan, err := planApply(ctx, client, parseDocs(vipDoc, vsDoc, backendDoc))

			Expect(err).NotTo(HaveOccurred())
			Expect(changeNames(plan.Changes)).To(Equal([]string{"create backend web", "create vs web", "create vip public"}))
			By("keeping the references to the documents of the files")
			createdVs := plan.Changes[1].To.(*vsv1.VirtualServer)
			Expect(createdVs.GetHttpRoutesConfig().GetVirtualHosts()[0].GetRoutes()[0].GetBackendUuid()).To(Equal("name:web"))
		})

		It("does not change the documents it is given", func() {
			storeBackends(&backendv1.Backend{UUID: "b-1", Name: "web"})
			conn.Handle(svcvsconnect.VirtualServerListVSProcedure, reply(&svcvs.ListVSResponse{}))
			docs := parseDocs(backendDoc, vsDoc)
			before := []proto.Message{proto.Clone(docs[0].Message), proto.Clone(docs[1].Message)}

			plan, err := planApply(ctx, client, docs)

			Expect(err).NotTo(HaveOccurred())
			By("having resolved the uuid and the reference in the plan only")
			Expect(plan.Changes[0].To.(*backendv1.Backend).GetUUID()).To(Equal("b-1"))
			for i := range docs {
				Expect(proto.Equal(before[i], docs[i].Message)).To(BeTrue(), "document %d changed", i+1)
			}
		})

		It("updates a resource matched by name", func() {
			storeBackends(&backendv1.Backend{UUID: "b-1", Name: "web"})

			plan, err := planApply(ctx, client, parseDocs(backendDoc))

			Expect(err).NotTo(HaveOccurred())
			Expect(plan.Changes).To(HaveLen(1))
			Expect(plan.Changes[0].String()).To(Equal("update backend web"))
			Expect(plan.Changes[0].To.(*backendv1.Backend).GetUUID()).To(Equal("b-1"))
			Expect(plan.Changes[0].From.(*backendv1.Backend).GetUUID()).To(Equal("b-1"))
		})

		It("targets the UUID of a document", func() {
			conn.Handle(svcbackendconnect.BackendReadBackendProcedure, func(req proto.Message) (proto.Message, error) {
				return &svcbackend.BackendResponse{Backend: &backendv1.Backend{UUID: "b-9", Name: "old-name"}}, nil
			})

			plan, err := planApply(ctx, client, parseDocs("UUID: b-9\n"+backendDoc))

			Expect(err).NotTo(HaveOccurred())
			Expect(plan.Changes[0].String()).To(Equal("update backend web"))
			Expect(lastRequest[*svccore.UuidRequest](conn, svcbackendconnect.BackendReadBackendProcedure).GetUUID()).To(Equal("b-9"))
			Expect(conn.Requests(svcbackendconnect.BackendListBackendProcedure)).To(BeEmpty())
		})

		It("fails before any write on a UUID that doesn't exist", func() {
			conn.Handle(svcbackendconnect.BackendReadBackendProcedure, failWith(errors.New("not found")))

			_, err := planApply(ctx, client, parseDocs("UUID: nope\n"+backendDoc))

			Expect(err).To(MatchError(ContainSubstring("test.yml document 1: ")))
			Expect(err).To(MatchError(ContainSubstring("nope")))
			Expect(conn.Calls()).To(Equal([]string{svcbackendconnect.BackendReadBackendProcedure}))
		})

		It("fails on a UUID read without a resource", func() {
			conn.Handle(svcbackendconnect.BackendReadBackendProcedure, reply(&svcbackend.BackendResponse{}))

			_, err := planApply(ctx, client, parseDocs("UUID: b-9\n"+backendDoc))

			Expect(err).To(MatchError(`test.yml document 1: backend "web": uuid b-9: not found`))
		})

		It("refuses an ambiguous name", func() {
			storeBackends(&backendv1.Backend{UUID: "u1", Name: "web"}, &backendv1.Backend{UUID: "u2", Name: "web"})

			_, err := planApply(ctx, client, parseDocs(backendDoc))

			Expect(err).To(MatchError(`test.yml document 1: backend "web": 2 backends have this name (u1, u2), use its UUID in the document`))
			Expect(conn.Requests(svcbackendconnect.BackendCreateBackendProcedure)).To(BeEmpty())
			Expect(conn.Requests(svcbackendconnect.BackendUpdateBackendProcedure)).To(BeEmpty())
		})

		It("reports no change when a stored vs referring to a stored backend equals its document", func() {
			storedBackend := parseDocs(backendDoc)[0].Message.(*backendv1.Backend)
			storedBackend.UUID = "b-1"
			storeBackends(storedBackend)
			storedVs := parseDocs(vsDoc)[0].Message.(*vsv1.VirtualServer)
			storedVs.UUID = "vs-1"
			storedVs.GetHttpRoutesConfig().GetVirtualHosts()[0].GetRoutes()[0].Action = &routev1.Route_BackendUuid{BackendUuid: "b-1"}
			conn.Handle(svcvsconnect.VirtualServerListVSProcedure, firstPage(&svcvs.ListVSResponse{Vs: []*vsv1.VirtualServer{storedVs}}))

			plan, err := planApply(ctx, client, parseDocs(backendDoc, vsDoc))

			Expect(err).NotTo(HaveOccurred())
			Expect(plan.Changes).To(BeEmpty())
			Expect(plan.Unchanged).To(Equal([]string{"backend web", "vs web"}))
		})

		It("refuses a document that targets a locked resource", func() {
			storeBackends(&backendv1.Backend{UUID: "b-1", Name: "web", Metadata: map[string]string{helpers.MetadataLockKey: "true"}})

			_, err := planApply(ctx, client, parseDocs(backendDoc))

			Expect(err).To(MatchError(ContainSubstring("backend web is locked.")))
		})

		It("reports no change for a vip document without prefix_length", func() {
			storedVip := &vipv1.VIP{UUID: "vip-1", Name: "public", Ip: "192.0.2.10", PrefixLength: 32}
			conn.Handle(svcvipconnect.VIPListVIPProcedure, firstPage(&svcvip.ListVIPResponse{Vip: []*vipv1.VIP{storedVip}}))

			plan, err := planApply(ctx, client, parseDocs("kind: vip\nname: public\nip: 192.0.2.10\n"))

			Expect(err).NotTo(HaveOccurred())
			Expect(plan.Changes).To(BeEmpty())
			Expect(plan.Unchanged).To(Equal([]string{"vip public"}))
		})

		It("resolves a reference to an existing certificate", func() {
			storeEmpty()
			conn.Handle(svccertconnect.CertificateListCertificateProcedure, firstPage(&svccert.ListCertificateResponse{
				Certificates: []*certv1.Certificate{{UUID: "c-1", Name: "shop"}},
			}))
			withTls := vsDoc + "    tls_options:\n    - certificates_uuid: ['name:shop']\n"

			plan, err := planApply(ctx, client, parseDocs(backendDoc, withTls))

			Expect(err).NotTo(HaveOccurred())
			createdVs := plan.Changes[1].To.(*vsv1.VirtualServer)
			Expect(createdVs.GetHttpRoutesConfig().GetVirtualHosts()[0].GetTlsOptions()[0].GetCertificatesUuid()).To(Equal([]string{"c-1"}))
		})

		It("fails on a reference found nowhere", func() {
			storeEmpty()
			docs := parseDocs(vipDoc)

			_, err := planApply(ctx, client, docs)

			Expect(err).To(MatchError(ContainSubstring(`vs "web": no vs has this name`)))
		})

	})

	Describe("applyChanges", func() {
		createdBy := func(procedure string, resp proto.Message) {
			conn.Handle(procedure, reply(resp))
		}

		It("resolves a reference to a backend created by the same run", func() {
			createdBy(svcbackendconnect.BackendCreateBackendProcedure,
				&svcbackend.BackendResponse{Backend: &backendv1.Backend{UUID: "b-new", Name: "web"}})
			createdBy(svcvsconnect.VirtualServerCreateVSProcedure,
				&svcvs.VSResponse{Vs: &vsv1.VirtualServer{UUID: "vs-new"}})
			conn.Handle(svcvipconnect.VIPCreateVIPProcedure, reply(&svcvip.VIPResponse{}))
			storeEmpty()
			docs := parseDocs(backendDoc, vsDoc, vipDoc)
			plan, err := planApply(ctx, client, docs)
			Expect(err).NotTo(HaveOccurred())

			var applied []string
			err = applyChanges(ctx, client, plan.Changes, func(c change) { applied = append(applied, c.String()) })

			Expect(err).NotTo(HaveOccurred())
			Expect(applied).To(Equal([]string{"create backend web", "create vs web", "create vip public"}))
			sentVs := lastRequest[*svcvs.CreateVSRequest](conn, svcvsconnect.VirtualServerCreateVSProcedure).GetVs()
			Expect(sentVs.GetHttpRoutesConfig().GetVirtualHosts()[0].GetRoutes()[0].GetBackendUuid()).To(Equal("b-new"))
			sentVip := lastRequest[*svcvip.CreateVIPRequest](conn, svcvipconnect.VIPCreateVIPProcedure).GetVip()
			Expect(sentVip.GetVirtualServersUuid()).To(Equal([]string{"vs-new"}))
		})

		It("updates with the uuid of the store", func() {
			storeBackends(&backendv1.Backend{UUID: "b-1", Name: "web"})
			conn.Handle(svcbackendconnect.BackendUpdateBackendProcedure, reply(&svcbackend.BackendResponse{}))
			plan, err := planApply(ctx, client, parseDocs(backendDoc))
			Expect(err).NotTo(HaveOccurred())

			Expect(applyChanges(ctx, client, plan.Changes, nil)).To(Succeed())

			sent := lastRequest[*svcbackend.UpdateBackendRequest](conn, svcbackendconnect.BackendUpdateBackendProcedure)
			Expect(sent.GetUUID()).To(Equal("b-1"))
		})

		It("stops at the first failing write", func() {
			createdBy(svcbackendconnect.BackendCreateBackendProcedure,
				&svcbackend.BackendResponse{Backend: &backendv1.Backend{UUID: "b-new"}})
			conn.Handle(svcvsconnect.VirtualServerCreateVSProcedure, failWith(errors.New("port in use")))
			storeEmpty()
			plan, err := planApply(ctx, client, parseDocs(backendDoc, vsDoc, vipDoc))
			Expect(err).NotTo(HaveOccurred())

			err = applyChanges(ctx, client, plan.Changes, printApplied)

			var failure *applyFailure
			Expect(errors.As(err, &failure)).To(BeTrue())
			Expect(failure.Failed.String()).To(Equal("create vs web"))
			Expect(failure.Applied).To(Equal([]string{"create backend web"}))
			Expect(failure.NotApplied).To(Equal([]string{"create vip public"}))
			Expect(conn.Requests(svcvipconnect.VIPCreateVIPProcedure)).To(BeEmpty())
		})
	})

	Describe("applyChanges on a cancelled context", func() {
		It("stops after the write in flight and lists the rest as not applied", func() {
			conn.Handle(svcbackendconnect.BackendCreateBackendProcedure,
				reply(&svcbackend.BackendResponse{Backend: &backendv1.Backend{UUID: "b-new"}}))
			conn.Handle(svcvsconnect.VirtualServerCreateVSProcedure, reply(&svcvs.VSResponse{}))
			conn.Handle(svcvipconnect.VIPCreateVIPProcedure, reply(&svcvip.VIPResponse{}))
			storeEmpty()
			plan, err := planApply(ctx, client, parseDocs(backendDoc, vsDoc, vipDoc))
			Expect(err).NotTo(HaveOccurred())
			interruptible, interrupt := context.WithCancel(ctx)
			DeferCleanup(interrupt)

			err = applyChanges(interruptible, client, plan.Changes, func(change) { interrupt() })

			var failure *applyFailure
			Expect(errors.As(err, &failure)).To(BeTrue())
			Expect(failure.Err).To(MatchError("interrupted"))
			Expect(failure.Applied).To(Equal([]string{"create backend web"}))
			Expect(failure.Failed.String()).To(Equal("create vs web"))
			Expect(failure.Error()).To(HavePrefix("interrupted before create vs web\n"))
			Expect(failure.NotApplied).To(Equal([]string{"create vip public"}))
			Expect(conn.Requests(svcvsconnect.VirtualServerCreateVSProcedure)).To(BeEmpty())
		})
	})

	Describe("printChanges", func() {
		It("prints a create as the yaml of the resource", func() {
			storeEmpty()
			plan, err := planApply(ctx, client, parseDocs(backendDoc))
			Expect(err).NotTo(HaveOccurred())

			out := captureOutput(func() { err = printChanges(plan.Changes) })

			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(ContainSubstring("create backend web"))
			Expect(out).To(ContainSubstring("10.0.0.10"))
			Expect(out).NotTo(ContainSubstring("(root level)"))
		})

		It("prints an update as a diff", func() {
			storeBackends(&backendv1.Backend{UUID: "b-1", Name: "web"})
			plan, err := planApply(ctx, client, parseDocs(backendDoc))
			Expect(err).NotTo(HaveOccurred())

			out := captureOutput(func() { err = printChanges(plan.Changes) })

			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(ContainSubstring("update backend web"))
			Expect(out).To(ContainSubstring("10.0.0.10"))
		})
	})
})
