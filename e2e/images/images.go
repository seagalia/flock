//go:build e2e

package images

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/moby/moby/client"
	"github.com/testcontainers/testcontainers-go"
)

// BinaryPath is where Build puts the linux flock binary, relative to the repository root: the Dockerfiles copy it
const BinaryPath = "e2e/.build/flock"

// label marks the final stage of every e2e image, so that pruning never touches other images
const label = "flock-e2e=true"

var (
	mutex       sync.Mutex
	built       = make(map[string]string)
	binaryBuilt bool
)

// Build builds dockerfile, relative to the repository root, as flock-e2e/<name>:dev once per test process.
// The flock binary is compiled on the host first: a go build stage in docker would leave a multi-GB
// untagged image behind on every run. The images replaced by the build are removed.
func Build(ctx context.Context, repoRoot, dockerfile, name string) (string, error) {
	mutex.Lock()
	defer mutex.Unlock()
	if tag, ok := built[name]; ok {
		return tag, nil
	}
	provider, err := testcontainers.NewDockerProvider()
	if err != nil {
		return "", err
	}
	defer provider.Close()
	if !binaryBuilt {
		if err := buildBinary(ctx, provider.Client(), repoRoot); err != nil {
			return "", err
		}
		binaryBuilt = true
	}
	logFile, err := os.CreateTemp("", "flock-e2e-build-"+name+"-*.log")
	if err != nil {
		return "", err
	}
	defer logFile.Close()
	tag, err := provider.BuildImage(ctx, &testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{
			Context:        repoRoot,
			Dockerfile:     dockerfile,
			Repo:           "flock-e2e/" + name,
			Tag:            "dev",
			KeepImage:      true,
			BuildLogWriter: logFile,
		},
	})
	if err != nil {
		return "", fmt.Errorf("build image %s (log in %s): %w", name, logFile.Name(), err)
	}
	_, err = provider.Client().ImagePrune(ctx, client.ImagePruneOptions{
		Filters: make(client.Filters).Add("dangling", "true").Add("label", label),
	})
	if err != nil {
		return "", fmt.Errorf("remove the images replaced by %s: %w", name, err)
	}
	built[name] = tag
	return tag, nil
}

// buildBinary compiles flock for the architecture of the docker server, with a stub dashboard: the dashboard
// specs assert on its files, and the overlay leaves the webui build of the host untouched
func buildBinary(ctx context.Context, docker client.APIClient, repoRoot string) error {
	overlay, err := stubDashboardOverlay(repoRoot)
	if err != nil {
		return err
	}
	return goBuildLinux(ctx, docker, repoRoot, "-overlay", overlay, "-o", BinaryPath, ".")
}

// BuildLinuxBinary compiles the go package pkg for the docker server into output, both relative to the repository
// root, for programs the suites copy into running containers
func BuildLinuxBinary(ctx context.Context, repoRoot, pkg, output string) error {
	provider, err := testcontainers.NewDockerProvider()
	if err != nil {
		return err
	}
	defer provider.Close()
	return goBuildLinux(ctx, provider.Client(), repoRoot, "-o", output, pkg)
}

func goBuildLinux(ctx context.Context, docker client.APIClient, repoRoot string, args ...string) error {
	version, err := docker.ServerVersion(ctx, client.ServerVersionOptions{})
	if err != nil {
		return fmt.Errorf("docker server version: %w", err)
	}
	build := exec.CommandContext(ctx, "go", append([]string{"build"}, args...)...)
	build.Dir = repoRoot
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+version.Arch)
	out, err := build.CombinedOutput()
	if err != nil {
		return fmt.Errorf("go build %v for linux/%s: %w: %s", args, version.Arch, err, out)
	}
	return nil
}

// stubDashboardOverlay writes a go build overlay replacing the built dashboard by index.html and the hashed
// bundle main-E2ESTUB1.js, it gives the path of the overlay file
func stubDashboardOverlay(repoRoot string) (string, error) {
	stubDir, err := os.MkdirTemp("", "flock-e2e-dashboard-")
	if err != nil {
		return "", err
	}
	stubs := map[string]string{
		"index.html":       "<html>flock e2e dashboard</html>\n",
		"main-E2ESTUB1.js": "console.log(\"flock e2e bundle\")\n",
	}
	appDir := filepath.Join(repoRoot, "webui", "dist", "app")
	replace := make(map[string]string)
	// an empty replacement hides a file of the host build from the binary
	err = filepath.WalkDir(appDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		replace[path] = ""
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("list the dashboard build: %w", err)
	}
	for file, content := range stubs {
		stubPath := filepath.Join(stubDir, file)
		if err := os.WriteFile(stubPath, []byte(content), 0o644); err != nil {
			return "", err
		}
		replace[filepath.Join(appDir, "browser", file)] = stubPath
	}
	overlay, err := json.Marshal(map[string]any{"Replace": replace})
	if err != nil {
		return "", err
	}
	overlayPath := filepath.Join(stubDir, "overlay.json")
	return overlayPath, os.WriteFile(overlayPath, overlay, 0o644)
}
