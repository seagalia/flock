package challenges_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/seagalia/flock/provisioners/acme/challenges"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
)

var _ = Describe("SecretHandler", func() {
	var (
		table   *challenges.Table
		handler *challenges.SecretHandler
		data    map[string]string
		// the metadata the provisioner puts on its challenge secrets
		challengeMd map[string]string
	)

	BeforeEach(func() {
		table = challenges.NewTable()
		handler = challenges.NewSecretHandler(table)
		data = challenges.EncodeData([]challenges.Challenge{{Domain: "a.test", Token: "tok-1", KeyAuth: "ka-1"}})
		challengeMd = map[string]string{
			challenges.MetadataKey:   "true",
			"flock.autoprovision":    "true",
			"flock.provisioner.acme": "true",
		}
	})

	It("feeds the table from a challenge secret and drops it on removal", func() {
		aSecret := &secret.Secret{UUID: "s1", Metadata: challengeMd, Data: data}

		Expect(handler.OnMessageAdded(context.Background(), aSecret)).To(Succeed())
		keyAuth, found := table.KeyAuthByToken("tok-1")
		Expect(found).To(BeTrue())
		Expect(keyAuth).To(Equal("ka-1"))

		Expect(handler.OnMessageRemove(context.Background(), aSecret)).To(Succeed())
		_, found = table.KeyAuthByToken("tok-1")
		Expect(found).To(BeFalse())
	})

	It("ignores a secret without the challenge metadata", func() {
		aSecret := &secret.Secret{UUID: "s1", Data: data}

		Expect(handler.OnMessageAdded(context.Background(), aSecret)).To(Succeed())

		_, found := table.KeyAuthByToken("tok-1")
		Expect(found).To(BeFalse())
	})

	DescribeTable("ignores a challenge secret the provisioner did not write",
		func(missingKey string) {
			delete(challengeMd, missingKey)
			aSecret := &secret.Secret{UUID: "s1", Metadata: challengeMd, Data: data}

			Expect(handler.OnMessageAdded(context.Background(), aSecret)).To(Succeed())

			_, found := table.KeyAuthByToken("tok-1")
			Expect(found).To(BeFalse())
		},
		Entry("not locked", "flock.autoprovision"),
		Entry("not from the acme provisioner", "flock.provisioner.acme"),
	)

	It("drops the entries of a secret that lost the challenge metadata", func() {
		withMeta := &secret.Secret{UUID: "s1", Metadata: challengeMd, Data: data}
		Expect(handler.OnMessageAdded(context.Background(), withMeta)).To(Succeed())

		Expect(handler.OnMessageAdded(context.Background(), &secret.Secret{UUID: "s1", Data: data})).To(Succeed())

		_, found := table.KeyAuthByToken("tok-1")
		Expect(found).To(BeFalse())
	})
})
