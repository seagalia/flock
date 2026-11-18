package manager

import (
	"context"
	"encoding/json"
	"os"
	"sync"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/client"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakeDocker answers the containers the spec sets, and the events of the last subscription the spec sends
type fakeDocker struct {
	mu            sync.Mutex
	containers    []container.Summary
	lists         int
	subscriptions int
	messages      chan events.Message
	errs          chan error
}

func (d *fakeDocker) setContainers(containers []container.Summary) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.containers = containers
}

func (d *fakeDocker) listCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lists
}

func (d *fakeDocker) subscriptionCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.subscriptions
}

func (d *fakeDocker) ContainerList(context.Context, client.ContainerListOptions) (client.ContainerListResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.lists++
	return client.ContainerListResult{Items: d.containers}, nil
}

func (d *fakeDocker) Events(context.Context, client.EventsListOptions) client.EventsResult {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.subscriptions++
	d.messages = make(chan events.Message)
	d.errs = make(chan error, 1)
	return client.EventsResult{Messages: d.messages, Err: d.errs}
}

func (d *fakeDocker) channels() (chan events.Message, chan error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.messages, d.errs
}

// composeProject reads the containers of a compose project where whoami-1 runs two replicas and whoami-2 one, beside a
// flock container which didn't opt in. Each whoami gets its own domain, so they land in their own virtual host.
func composeProject() []container.Summary {
	GinkgoHelper()
	raw, err := os.ReadFile("../eds/testdata/list.json")
	Expect(err).NotTo(HaveOccurred())
	listed := client.ContainerListResult{}
	Expect(json.Unmarshal(raw, &listed)).To(Succeed())
	for _, summary := range listed.Items {
		service := summary.Labels["com.docker.compose.service"]
		if summary.Labels["io.seagalia.flock.enable"] == "true" {
			summary.Labels["io.seagalia.flock.domains"] = service + ".example.com"
		}
	}
	return listed.Items
}

func withoutService(containers []container.Summary, service string) []container.Summary {
	var kept []container.Summary
	for _, summary := range containers {
		if summary.Labels["com.docker.compose.service"] != service {
			kept = append(kept, summary)
		}
	}
	return kept
}
