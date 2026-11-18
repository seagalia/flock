package resfile_test

import (
	"fmt"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/seagalia/flock/cli/resfile"
	vipv1 "github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	vsv1 "github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	starling_announce "github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"
)

const exampleFile = `kind: backend
name: web
ip_addr_group:
  ip_addresses:
  - ip: 10.0.0.10
    port: 8080
health_check:
  no_health_check: {}
---
kind: vs
name: web
port: 80
http_routes_config:
  virtual_hosts:
  - domains: ['*']
    routes:
    - name: default
      match: {prefix: /}
      backend_uuid: name:web
---
kind: vip
name: public
ip: 192.0.2.10
virtual_servers_uuid:
- 6b0c1c9e-0f43-4c47-a0a7-0d1f1d7b8e21
- name:web
`

var _ = Describe("resfile", func() {
	It("parses a multi-document file", func() {
		docs, problems := resfile.Parse("f.yml", []byte(exampleFile))
		Expect(problems).To(BeEmpty())
		Expect(docs).To(HaveLen(3))
		for i, kind := range []string{resfile.KindBackend, resfile.KindVs, resfile.KindVip} {
			Expect(docs[i].Kind).To(Equal(kind))
			Expect(docs[i].Index).To(Equal(i + 1))
			Expect(docs[i].File).To(Equal("f.yml"))
		}
		Expect(docs[0].Name()).To(Equal("web"))
		Expect(docs[1].Name()).To(Equal("web"))
		Expect(docs[2].Name()).To(Equal("public"))
		Expect(docs[2].UUID()).To(BeEmpty())
	})

	It("skips empty documents and keeps the numbering", func() {
		docs, problems := resfile.Parse("f.yml", []byte("# nothing\n---\n---\nkind: vip\nname: a\nip: 192.0.2.1\n---\n"))
		Expect(problems).To(BeEmpty())
		Expect(docs).To(HaveLen(1))
		Expect(docs[0].Index).To(Equal(2))
	})

	It("refuses an unknown or missing kind", func() {
		_, problems := resfile.Parse("f.yml", []byte("kind: vip\nname: a\nip: 192.0.2.1\n---\nkind: pool\nname: x\n---\nname: y\n"))
		Expect(problems).To(HaveLen(2))
		Expect(problems[0].String()).To(Equal(`f.yml document 2: kind: unknown kind "pool", expected vs, backend or vip`))
		Expect(problems[1].String()).To(Equal(`f.yml document 3: kind: missing kind, expected vs, backend or vip`))
	})

	It("reads a folder in name order without recursion", func() {
		dir := GinkgoT().TempDir()
		write := func(name, resourceName string) {
			path := filepath.Join(dir, name)
			Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
			Expect(os.WriteFile(path, []byte("kind: vip\nname: "+resourceName+"\nip: 192.0.2.1\n"), 0o600)).To(Succeed())
		}
		write("b.yml", "b")
		write("a.yaml", "a")
		write("sub/c.yml", "c")
		write("notes.txt", "d")

		docs, problems := resfile.Read(dir)
		Expect(problems).To(BeEmpty())
		Expect(docs).To(HaveLen(2))
		Expect(docs[0].Name()).To(Equal("a"))
		Expect(docs[1].Name()).To(Equal("b"))
	})

	It("reports a missing path as a problem without index", func() {
		_, problems := resfile.Read(filepath.Join(GinkgoT().TempDir(), "nope.yml"))
		Expect(problems).To(HaveLen(1))
		Expect(problems[0].Index).To(BeZero())
	})

	It("reports a document holding the autoprovision lock key on the metadata field", func() {
		content := "kind: backend\nname: web\nmetadata:\n  flock.autoprovision: \"true\"\n"
		docs, parseProblems := resfile.Parse("f.yml", []byte(content))
		Expect(parseProblems).To(BeEmpty())

		problems := resfile.Check(docs)

		Expect(problems).To(ContainElement(And(
			HaveField("Field", "metadata"), HaveField("Message", ContainSubstring("flock.autoprovision")))))
	})

	It("reports every broken rule with its field path", func() {
		content := "kind: backend\nname: web\n---\nkind: vs\nname: web\nport: 0\ntcp_route_config:\n  backend_uuid: x\n"
		docs, parseProblems := resfile.Parse("f.yml", []byte(content))
		Expect(parseProblems).To(BeEmpty())

		problems := resfile.Check(docs)
		fields := map[int]string{}
		for _, problem := range problems {
			fields[problem.Index] += problem.Field + ";"
		}
		Expect(fields[1]).To(ContainSubstring("endpoint"))
		Expect(fields[2]).To(ContainSubstring("port"))
	})

	It("refuses a port no agent can listen on", func() {
		content := `kind: backend
name: web
ip_addr_group:
  ip_addresses:
  - ip: 10.0.0.10
    port: 70000
health_check:
  no_health_check: {}
---
kind: vs
name: web
port: 70000
tcp_route_config:
  backend_uuid: name:web
`
		docs, parseProblems := resfile.Parse("f.yml", []byte(content))
		Expect(parseProblems).To(BeEmpty())

		problems := resfile.Check(docs)

		refused := make([]string, 0, len(problems))
		for _, problem := range problems {
			refused = append(refused, fmt.Sprintf("%d %s", problem.Index, problem.Field))
		}
		Expect(refused).To(ConsistOf("1 ip_addr_group.ip_addresses[0].port", "2 port"))
	})

	It("reads the typed_announce of a vip as the builder of the dashboard writes it", func() {
		// the yaml of the vip document webui/src/app/builder/documents.ts writes for a starling VIP
		vip := `kind: vip
name: public
ip: 192.0.2.10
virtual_servers_uuid:
  - name:web
typed_announce:
  type_announce: LAYER2
  "@type": type.googleapis.com/flock.extensions.announces.starling_announce.v1.StarlingAnnounce
`
		docs, problems := resfile.Parse("f.yml", []byte(vip))
		Expect(problems).To(BeEmpty())
		Expect(resfile.Check(docs)).To(BeEmpty())
		announce := &starling_announce.StarlingAnnounce{}
		Expect(docs[0].Message.(*vipv1.VIP).GetTypedAnnounce().UnmarshalTo(announce)).To(Succeed())
		Expect(announce.GetTypeAnnounce()).To(Equal(starling_announce.StarlingAnnounce_LAYER2))
	})

	It("refuses two documents of the same kind and name", func() {
		content := "kind: vip\nname: a\nip: 192.0.2.1\n---\nkind: vip\nname: a\nip: 192.0.2.2\n"
		docs, _ := resfile.Parse("f.yml", []byte(content))
		problems := resfile.Check(docs)
		Expect(problems).To(HaveLen(1))
		Expect(problems[0].Index).To(Equal(2))
		Expect(problems[0].Field).To(Equal("name"))
	})

	It("accepts a vs and a backend both named web", func() {
		docs, _ := resfile.Parse("f.yml", []byte(exampleFile))
		Expect(resfile.Check(docs)).To(BeEmpty())
	})

	It("formats a problem", func() {
		Expect(resfile.Problem{File: "f.yml", Index: 2, Field: "port", Message: "bad"}.String()).
			To(Equal("f.yml document 2: port: bad"))
		Expect(resfile.Problem{File: "f.yml", Index: 2, Message: "bad"}.String()).To(Equal("f.yml document 2: bad"))
		Expect(resfile.Problem{File: "f.yml", Message: "bad"}.String()).To(Equal("f.yml: bad"))
	})

	It("lists the references of a document", func() {
		content := `kind: vs
name: web
port: 443
http_routes_config:
  virtual_hosts:
  - domains: ['*']
    routes:
    - name: default
      match: {prefix: /}
      backend_uuid: name:web
    - name: other
      match: {prefix: /o}
      backend_uuid: 3ba3f9fa-0f43-4c47-a0a7-0d1f1d7b8e21
    tls_options:
    - certificates_uuid: [name:site]
`
		docs, problems := resfile.Parse("f.yml", []byte(content))
		Expect(problems).To(BeEmpty())

		refs := resfile.Refs(docs[0])
		Expect(refs).To(HaveLen(2))
		Expect(refs[0].Kind).To(Equal(resfile.KindBackend))
		Expect(refs[0].Name).To(Equal("web"))
		Expect(refs[1].Kind).To(Equal("certificate"))
		Expect(refs[1].Name).To(Equal("site"))

		refs[0].Set("u1")
		refs[1].Set("u2")
		host := docs[0].Message.(*vsv1.VirtualServer).GetHttpRoutesConfig().GetVirtualHosts()[0]
		Expect(host.Routes[0].GetBackendUuid()).To(Equal("u1"))
		Expect(host.TlsOptions[0].CertificatesUuid).To(Equal([]string{"u2"}))
		Expect(resfile.Refs(docs[0])).To(BeEmpty())
	})

	It("lists the references of the other route configs and of a vip", func() {
		content := `kind: vs
name: t
port: 1
tls_tcp_routes_config:
  routes:
  - backend_uuid: name:b
    tls:
      certificates_uuid: [name:c1, name:c2]
---
kind: vs
name: u
port: 2
udp_route_config: {backend_uuid: "name:b2"}
---
kind: vip
name: v
ip: 192.0.2.1
virtual_servers_uuid: [name:t, abc]
`
		docs, problems := resfile.Parse("f.yml", []byte(content))
		Expect(problems).To(BeEmpty())
		Expect(resfile.Refs(docs[0])).To(HaveLen(3))
		Expect(resfile.Refs(docs[1])).To(HaveLen(1))
		vipRefs := resfile.Refs(docs[2])
		Expect(vipRefs).To(HaveLen(1))
		Expect(vipRefs[0].Kind).To(Equal(resfile.KindVs))
	})

	It("lists the backend references of the external auth policies", func() {
		content := `kind: vs
name: web
port: 80
http_routes_config:
  external_auths:
  - name: sso
    backend_uuid: name:auth
    path: /check
  - name: other
    backend_uuid: 3ba3f9fa-0f43-4c47-a0a7-0d1f1d7b8e21
    path: /check
  virtual_hosts:
  - domains: ['*']
    external_auth: sso
    routes:
    - name: default
      match: {prefix: /}
      backend_uuid: name:web
`
		docs, problems := resfile.Parse("f.yml", []byte(content))
		Expect(problems).To(BeEmpty())

		refs := resfile.Refs(docs[0])
		Expect(refs).To(HaveLen(2))
		Expect(refs[0].Kind).To(Equal(resfile.KindBackend))
		Expect(refs[0].Name).To(Equal("auth"))
		Expect(refs[1].Name).To(Equal("web"))

		refs[0].Set("u1")
		policies := docs[0].Message.(*vsv1.VirtualServer).GetHttpRoutesConfig().GetExternalAuths()
		Expect(policies[0].GetBackendUuid()).To(Equal("u1"))
		Expect(policies[1].GetBackendUuid()).To(Equal("3ba3f9fa-0f43-4c47-a0a7-0d1f1d7b8e21"))
	})

	It("tells a reference from a uuid", func() {
		name, isRef := resfile.RefName("name:web")
		Expect(name).To(Equal("web"))
		Expect(isRef).To(BeTrue())

		name, isRef = resfile.RefName("3ba3f9fa-0f43-4c47-a0a7-0d1f1d7b8e21")
		Expect(name).To(BeEmpty())
		Expect(isRef).To(BeFalse())
	})
})
