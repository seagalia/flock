//go:build e2e

package env

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"

	"github.com/seagalia/flock/e2e/images"
)

// ControllerNames are the container names the controller server certificate is valid for
var ControllerNames = []string{"controller-sqlite", "controller-postgres", "controller-a", "controller-b", "controller-lookup", "controller-health"}

// Env is the docker network every e2e container joins, with the pki of the run
type Env struct {
	Net   *testcontainers.DockerNetwork
	Certs *Certs

	repoRoot   string
	mutex      *sync.Mutex
	containers []testcontainers.Container
}

func Setup(ctx context.Context) (*Env, error) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		return nil, err
	}
	certs, err := NewCerts(ControllerNames...)
	if err != nil {
		return nil, fmt.Errorf("certs: %w", err)
	}
	dockerNet, err := network.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("network: %w", err)
	}
	return &Env{
		Net:      dockerNet,
		Certs:    certs,
		repoRoot: repoRoot,
		mutex:    &sync.Mutex{},
	}, nil
}

func (e *Env) Teardown(ctx context.Context) {
	e.mutex.Lock()
	containers := e.containers
	e.mutex.Unlock()
	for _, c := range containers {
		_ = c.Terminate(ctx)
	}
	_ = e.Net.Remove(ctx)
}

// Unique gives a name that does not collide with the containers of another spec or of a previous run
func Unique(prefix string) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return prefix + "-" + hex.EncodeToString(b)
}

// start joins the container to the network where other containers reach it by alias
func (e *Env) start(ctx context.Context, alias string, req testcontainers.ContainerRequest) (testcontainers.Container, error) {
	req.Name = Unique(alias)
	req.Networks = []string{e.Net.Name}
	req.NetworkAliases = map[string][]string{e.Net.Name: {alias}}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	createFailed := c == nil
	if createFailed {
		return nil, fmt.Errorf("start container %s: %w", alias, err)
	}
	if err != nil {
		// a container that did not boot must not keep its alias, the next spec taking it would get two containers
		// behind one name; ctx may be the spec context that just expired
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		logs := containerLogs(cleanupCtx, c)
		_ = c.Terminate(cleanupCtx)
		return nil, fmt.Errorf("start container %s: %w\ncontainer logs:\n%s", alias, err, logs)
	}
	e.mutex.Lock()
	e.containers = append(e.containers, c)
	e.mutex.Unlock()
	return c, nil
}

// image builds e2e/controller/docker/<name>.Dockerfile once, from the repository root
func (e *Env) image(ctx context.Context, name string) (string, error) {
	return images.Build(ctx, e.repoRoot, "e2e/controller/docker/"+name+".Dockerfile", name)
}

func containerLogs(ctx context.Context, c testcontainers.Container) string {
	reader, err := c.Logs(ctx)
	if err != nil {
		return fmt.Sprintf("failed to get logs: %s", err)
	}
	defer reader.Close()
	b, _ := io.ReadAll(reader)
	return string(b)
}

func stopContainer(ctx context.Context, c testcontainers.Container) error {
	timeout := 10 * time.Second
	return c.Stop(ctx, &timeout)
}

func fileFromBytes(content []byte, path string, mode int64) testcontainers.ContainerFile {
	return testcontainers.ContainerFile{Reader: bytes.NewReader(content), ContainerFilePath: path, FileMode: mode}
}

func findRepoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("unable to find repository root")
	}
	// walk up to the module root, the e2e suites live at different depths of the monorepo
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("unable to find repository root from %s", file)
		}
		dir = parent
	}
}
