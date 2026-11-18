package observe_test

import (
	"github.com/ArthurHlt/emitter"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/seagalia/flock/sdk/flock/api/observer/v1"
	. "github.com/seagalia/flock/sdk/observe"
)

var _ = Describe("Observe", func() {
	var subjects chan *observer.Subject
	var listener ListenerFuncSubject

	BeforeEach(func() {
		subjects = make(chan *observer.Subject, 10)
		received := subjects
		listener = func(event *emitter.EventOf[*observer.Subject]) {
			received <- event.TypedSubject()
		}
		DeferCleanup(func() {
			Off(observer.EventType_CREATE_SUBJECT)
			Off(observer.EventType_DELETE_SUBJECT)
			OffAll()
		})
	})

	It("should give emitted subjects to listeners of the event type", func() {
		On(observer.EventType_CREATE_SUBJECT, listener)
		Expect(Listeners(observer.EventType_CREATE_SUBJECT)).To(HaveLen(1))

		subject := &observer.Subject{TypeUrl: "type", Identifier: "id-1"}
		<-Emit(observer.EventType_CREATE_SUBJECT, subject)
		Eventually(subjects).Should(Receive(Equal(subject)))

		<-EmitSubject(observer.EventType_DELETE_SUBJECT, subject)
		Consistently(subjects, "100ms").ShouldNot(Receive())
	})

	It("should give every events to listeners of all events", func() {
		OnAll(listener)
		<-EmitSubject(observer.EventType_DELETE_SUBJECT, &observer.Subject{Identifier: "id-2"})
		Eventually(subjects).Should(Receive(HaveField("Identifier", "id-2")))
	})

	It("should ignore events which are not about a subject", func() {
		On(observer.EventType_CREATE_SUBJECT, listener)
		<-Emit(observer.EventType_CREATE_SUBJECT, "not a subject")
		Consistently(subjects, "100ms").ShouldNot(Receive())
	})

	It("should call once listeners only one time", func() {
		Once(observer.EventType_CREATE_SUBJECT, listener)
		<-EmitSubject(observer.EventType_CREATE_SUBJECT, &observer.Subject{Identifier: "first"})
		<-EmitSubject(observer.EventType_CREATE_SUBJECT, &observer.Subject{Identifier: "second"})
		Eventually(subjects).Should(Receive(HaveField("Identifier", "first")))
		Consistently(subjects, "100ms").ShouldNot(Receive())
	})

	It("should stop calling removed listeners", func() {
		On(observer.EventType_CREATE_SUBJECT, listener)
		Off(observer.EventType_CREATE_SUBJECT)
		Expect(Listeners(observer.EventType_CREATE_SUBJECT)).To(BeEmpty())
		<-EmitSubject(observer.EventType_CREATE_SUBJECT, &observer.Subject{})
		Consistently(subjects, "100ms").ShouldNot(Receive())
	})
})
