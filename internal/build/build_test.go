package build

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kotlovyim-dev/kayly/internal/deploy"
)

// The versions install.sh pins.
var builder = &Builder{
	Docker:   "docker",
	Railpack: "railpack",
	Frontend: "ghcr.io/railwayapp/railpack-frontend:v0.40.1@sha256:f1973377693af30c9b37a92c97c661c07b277ccdc6be909213c74c771f8d2d6d",
}

func needDocker(t *testing.T) {
	t.Helper()
	if os.Getenv("KAYLY_DOCKER_TESTS") != "1" {
		t.Skip("set KAYLY_DOCKER_TESTS=1 to run tests against Docker")
	}
}

func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func image(t *testing.T) string {
	img := fmt.Sprintf("kayly/buildtest:%d", time.Now().UnixNano())
	t.Cleanup(func() { exec.Command("docker", "image", "rm", "-f", img).Run() })
	return img
}

func runImage(t *testing.T, img string) string {
	t.Helper()
	out, err := exec.Command("docker", "run", "--rm", img).CombinedOutput()
	if err != nil {
		t.Fatalf("run %s: %v\n%s", img, err, out)
	}
	return string(out)
}

func TestDockerfileWithTarget(t *testing.T) {
	needDocker(t)
	dir := writeFiles(t, map[string]string{"Dockerfile": `
FROM alpine:3.22@sha256:5291449c3df73caf6ed85e649dec1b9e818b39a5d8c871e97afc13e9cd5e8fa8 AS base
CMD ["echo", "base"]
FROM base AS app
CMD ["echo", "app target"]
FROM base AS other
CMD ["echo", "wrong target"]
`})
	img := image(t)
	var log bytes.Buffer
	err := builder.Build(context.Background(), deploy.BuildSpec{Dir: dir, Dockerfile: "Dockerfile", Target: "app", Image: img}, &log)
	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if !strings.Contains(log.String(), "$ docker buildx build") {
		t.Error("the build command is not printed")
	}
	if got := runImage(t, img); got != "app target\n" {
		t.Errorf("output = %q", got)
	}
}

func TestRailpackNode(t *testing.T) {
	needDocker(t)
	dir := writeFiles(t, map[string]string{
		"package.json": `{"name":"hello","private":true,"scripts":{"start":"node index.js"}}`,
		"index.js":     `console.log("hello from railpack")`,
	})
	img := image(t)
	var log bytes.Buffer
	err := builder.Build(context.Background(), deploy.BuildSpec{Dir: dir, Image: img, CacheKey: "buildtest"}, &log)
	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if got := runImage(t, img); !strings.Contains(got, "hello from railpack") {
		t.Errorf("output = %q", got)
	}
}

func TestBuildFailureIsReported(t *testing.T) {
	needDocker(t)
	dir := writeFiles(t, map[string]string{"Dockerfile": "FROM alpine:3.22@sha256:5291449c3df73caf6ed85e649dec1b9e818b39a5d8c871e97afc13e9cd5e8fa8\nRUN exit 7\n"})
	var log bytes.Buffer
	err := builder.Build(context.Background(), deploy.BuildSpec{Dir: dir, Dockerfile: "Dockerfile", Image: image(t)}, &log)
	if err == nil || !strings.Contains(log.String(), "exit 7") {
		t.Fatalf("err = %v, log:\n%s", err, log.String())
	}
}
