package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/cli/resfile"
	backendv1 "github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	vsv1 "github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	svcbackend "github.com/seagalia/flock/sdk/flock/services/backend/v1"
	"github.com/seagalia/flock/sdk/flock/services/backend/v1/svcbackendconnect"
	svccert "github.com/seagalia/flock/sdk/flock/services/cert/v1"
	"github.com/seagalia/flock/sdk/flock/services/cert/v1/svccertconnect"
	svcvip "github.com/seagalia/flock/sdk/flock/services/vip/v1"
	"github.com/seagalia/flock/sdk/flock/services/vip/v1/svcvipconnect"
	svcvs "github.com/seagalia/flock/sdk/flock/services/vs/v1"
	"github.com/seagalia/flock/sdk/flock/services/vs/v1/svcvsconnect"
	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/protobuf/proto"
)

var _ = Describe("apply", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
	})

	// storeEmpty makes every lookup by name find nothing.
	storeEmpty := func() {
		runner.conn.Handle(svcbackendconnect.BackendListBackendProcedure, firstPage(&svcbackend.ListBackendResponse{}))
		runner.conn.Handle(svcvsconnect.VirtualServerListVSProcedure, firstPage(&svcvs.ListVSResponse{}))
		runner.conn.Handle(svcvipconnect.VIPListVIPProcedure, firstPage(&svcvip.ListVIPResponse{}))
		runner.conn.Handle(svccertconnect.CertificateListCertificateProcedure, firstPage(&svccert.ListCertificateResponse{}))
	}
	handleCreates := func() {
		runner.conn.Handle(svcbackendconnect.BackendCreateBackendProcedure,
			reply(&svcbackend.BackendResponse{Backend: &backendv1.Backend{UUID: "b-new"}}))
		runner.conn.Handle(svcvsconnect.VirtualServerCreateVSProcedure,
			reply(&svcvs.VSResponse{Vs: &vsv1.VirtualServer{UUID: "vs-new"}}))
	}
	writes := func() int {
		return len(runner.conn.Requests(svcbackendconnect.BackendCreateBackendProcedure)) +
			len(runner.conn.Requests(svcvsconnect.VirtualServerCreateVSProcedure))
	}

	It("reports a document targeting a locked backend and writes nothing", func() {
		runner.conn.Handle(svcbackendconnect.BackendListBackendProcedure, firstPage(&svcbackend.ListBackendResponse{
			Backends: []*backendv1.Backend{{UUID: "b-1", Name: "web", Metadata: map[string]string{helpers.MetadataLockKey: "true"}}},
		}))
		file := writeTempFile("apply.yml", backendDoc)

		_, err := runner.run("apply", "-f", file, "--yes")

		Expect(err).To(MatchError(ContainSubstring("backend web is locked.")))
		Expect(writes()).To(BeZero())
		Expect(runner.conn.Requests(svcbackendconnect.BackendUpdateBackendProcedure)).To(BeEmpty())
	})

	It("prints every problem of the files and calls nothing", func() {
		broken := writeTempFile("broken.yml", "kind: nope\nname: a\n---\nname: b\n")
		missing := "/does/not/exist.yml"

		out, err := runner.run("apply", "-f", broken, "-f", missing, "--yes")

		Expect(err).To(MatchError("3 problems in the files"))
		Expect(out).To(ContainSubstring("broken.yml document 1"))
		Expect(out).To(ContainSubstring("broken.yml document 2"))
		Expect(out).To(ContainSubstring(missing))
		Expect(runner.conn.Calls()).To(BeEmpty())
	})

	It("reports a rule broken by a document", func() {
		file := writeTempFile("bad.yml", "kind: backend\nname: web\nUUID: not-a-uuid\n")

		_, err := runner.run("apply", "-f", file, "--yes")

		Expect(err).To(MatchError(ContainSubstring("problems in the files")))
		Expect(runner.conn.Calls()).To(BeEmpty())
	})

	It("shows the changes of --dry-run and writes nothing", func() {
		storeEmpty()
		handleCreates()
		file := writeTempFile("res.yml", backendDoc+"---\n"+vsDoc)

		out, err := runner.run("apply", "-f", file, "--dry-run")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("create backend web"))
		Expect(out).To(ContainSubstring("create vs web"))
		Expect(writes()).To(BeZero())
	})

	It("writes the changes with --yes and prints what it applied", func() {
		storeEmpty()
		handleCreates()
		file := writeTempFile("res.yml", backendDoc+"---\n"+vsDoc)

		out, err := runner.run("apply", "-f", file, "--yes")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("created backend web"))
		Expect(out).To(ContainSubstring("created vs web"))
		sent := lastRequest[*svcvs.CreateVSRequest](runner.conn, svcvsconnect.VirtualServerCreateVSProcedure).GetVs()
		Expect(sent.GetHttpRoutesConfig().GetVirtualHosts()[0].GetRoutes()[0].GetBackendUuid()).To(Equal("b-new"))
	})

	It("reads the yml files of a folder", func() {
		storeEmpty()
		handleCreates()
		dir := GinkgoT().TempDir()
		for name, content := range map[string]string{"1.yml": backendDoc, "2.yaml": vsDoc} {
			Expect(os.WriteFile(filepath.Join(dir, name), []byte(content), 0600)).To(Succeed())
		}

		_, err := runner.run("apply", "-f", dir, "--yes")

		Expect(err).NotTo(HaveOccurred())
		Expect(writes()).To(Equal(2))
	})

	It("says nothing is to change when everything is up to date", func() {
		stored := parseDocs(backendDoc)[0].Message.(*backendv1.Backend)
		stored.UUID = "b-1"
		runner.conn.Handle(svcbackendconnect.BackendListBackendProcedure,
			firstPage(&svcbackend.ListBackendResponse{Backends: []*backendv1.Backend{stored}}))

		out, err := runner.run("apply", "-f", writeTempFile("res.yml", backendDoc), "--yes")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Nothing to change, 1 resources are up to date."))
		Expect(runner.conn.Requests(svcbackendconnect.BackendUpdateBackendProcedure)).To(BeEmpty())
	})

	It("counts the resources already up to date next to the changes", func() {
		stored := parseDocs(backendDoc)[0].Message.(*backendv1.Backend)
		stored.UUID = "b-1"
		storeEmpty()
		runner.conn.Handle(svcbackendconnect.BackendListBackendProcedure, func(req proto.Message) (proto.Message, error) {
			if req.(*svcbackend.ListBackendRequest).GetOffset() > 0 {
				return &svcbackend.ListBackendResponse{}, nil
			}
			return &svcbackend.ListBackendResponse{Backends: []*backendv1.Backend{stored}}, nil
		})
		handleCreates()

		out, err := runner.run("apply", "-f", writeTempFile("res.yml", backendDoc+"---\n"+vsDoc), "--dry-run")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("create vs web"))
		Expect(out).To(ContainSubstring("1 resources are up to date."))
	})

	It("prints what was applied and what was not when a write fails", func() {
		storeEmpty()
		runner.conn.Handle(svcbackendconnect.BackendCreateBackendProcedure,
			reply(&svcbackend.BackendResponse{Backend: &backendv1.Backend{UUID: "b-new"}}))
		runner.conn.Handle(svcvsconnect.VirtualServerCreateVSProcedure, failWith(errors.New("port in use")))
		file := writeTempFile("res.yml", backendDoc+"---\n"+vsDoc+"---\n"+vipDoc)

		out, err := runner.run("apply", "-f", file, "--yes")

		Expect(err).To(MatchError(ContainSubstring("create vs web")))
		Expect(err.Error()).To(ContainSubstring("already applied:\n  - create backend web"))
		Expect(err.Error()).To(ContainSubstring("not applied:\n  - create vip public"))
		Expect(strings.Count(out, "created backend web")).To(Equal(1))
	})

	It("refuses files without any resource", func() {
		emptyDir := GinkgoT().TempDir()

		_, err := runner.run("apply", "-f", emptyDir, "--yes")

		Expect(err).To(MatchError("no resource in " + emptyDir))
		Expect(runner.conn.Calls()).To(BeEmpty())
	})
})

var _ = Describe("docs/cli/examples/resources.yml", func() {
	It("is read and checked without problems", func() {
		docs, problems := resfile.Read("../docs/cli/examples/resources.yml")

		Expect(problems).To(BeEmpty())
		Expect(docs).To(HaveLen(3))
		Expect(resfile.Check(docs)).To(BeEmpty())
	})
})

var _ = Describe("docs/cli/examples/vs-external-auth.yml", func() {
	It("is read and checked without problems, its policy backend a name reference", func() {
		docs, problems := resfile.Read("../docs/cli/examples/vs-external-auth.yml")

		Expect(problems).To(BeEmpty())
		Expect(docs).To(HaveLen(3))
		Expect(resfile.Check(docs)).To(BeEmpty())
		refs := resfile.Refs(docs[2])
		refNames := make([]string, 0, len(refs))
		for _, ref := range refs {
			refNames = append(refNames, ref.Name)
		}
		Expect(refNames).To(Equal([]string{"oauth2-proxy", "oauth2-proxy", "app"}))
	})
})
