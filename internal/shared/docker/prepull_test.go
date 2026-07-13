package docker

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// stubComposeConfig writes a docker stub that records its invocation args
// (space-joined) to recorderPath and prints json on stdout, exiting 0.
func stubComposeConfig(t *testing.T, recorderPath, jsonOut string) string {
	t.Helper()
	body := fmt.Sprintf(`echo "$@" > %q
cat <<'PREPULL_EOF'
%s
PREPULL_EOF
`, recorderPath, jsonOut)
	return writeStub(t, body)
}

func writeDockerfile(t *testing.T, dir, contents string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "Dockerfile")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	return path
}

func TestDeriveBuildBases_MultiServiceDedupe(t *testing.T) {
	tmp := t.TempDir()
	apiDir := filepath.Join(tmp, "api")
	workerDir := filepath.Join(tmp, "worker")
	writeDockerfile(t, apiDir, "FROM golang:1.22\n")
	writeDockerfile(t, workerDir, "FROM golang:1.22\nFROM alpine:3.19\n")

	cfgJSON := fmt.Sprintf(`{"services":{"api":{"build":{"context":%q,"dockerfile":"Dockerfile"}},"worker":{"build":{"context":%q,"dockerfile":"Dockerfile"}}}}`, apiDir, workerDir)
	recorder := filepath.Join(tmp, "recorder.txt")
	stub := stubComposeConfig(t, recorder, cfgJSON)

	c := &Compose{Bin: stub, ProjectName: "proj", Files: []string{"compose.yaml"}}
	refs, err := c.DeriveBuildBases(nil)
	if err != nil {
		t.Fatalf("DeriveBuildBases: %v", err)
	}
	want := bases("alpine:3.19", "golang:1.22")
	if !reflect.DeepEqual(refs, want) {
		t.Fatalf("refs = %v, want %v", refs, want)
	}
}

func TestDeriveBuildBases_ServiceFilterHonored(t *testing.T) {
	tmp := t.TempDir()
	apiDir := filepath.Join(tmp, "api")
	workerDir := filepath.Join(tmp, "worker")
	writeDockerfile(t, apiDir, "FROM golang:1.22\n")
	writeDockerfile(t, workerDir, "FROM alpine:3.19\n")

	cfgJSON := fmt.Sprintf(`{"services":{"api":{"build":{"context":%q,"dockerfile":"Dockerfile"}},"worker":{"build":{"context":%q,"dockerfile":"Dockerfile"}}}}`, apiDir, workerDir)
	recorder := filepath.Join(tmp, "recorder.txt")
	stub := stubComposeConfig(t, recorder, cfgJSON)

	c := &Compose{Bin: stub, ProjectName: "proj", Files: []string{"compose.yaml"}}
	refs, err := c.DeriveBuildBases([]string{"api"})
	if err != nil {
		t.Fatalf("DeriveBuildBases: %v", err)
	}
	want := bases("golang:1.22")
	if !reflect.DeepEqual(refs, want) {
		t.Fatalf("refs = %v, want %v", refs, want)
	}
}

func TestDeriveBuildBases_ServiceWithoutBuildSkipped(t *testing.T) {
	tmp := t.TempDir()
	apiDir := filepath.Join(tmp, "api")
	writeDockerfile(t, apiDir, "FROM golang:1.22\n")

	cfgJSON := fmt.Sprintf(`{"services":{"api":{"build":{"context":%q,"dockerfile":"Dockerfile"}},"db":{"image":"postgres:16"}}}`, apiDir)
	recorder := filepath.Join(tmp, "recorder.txt")
	stub := stubComposeConfig(t, recorder, cfgJSON)

	c := &Compose{Bin: stub, ProjectName: "proj", Files: []string{"compose.yaml"}}
	refs, err := c.DeriveBuildBases(nil)
	if err != nil {
		t.Fatalf("DeriveBuildBases: %v", err)
	}
	want := bases("golang:1.22")
	if !reflect.DeepEqual(refs, want) {
		t.Fatalf("refs = %v, want %v", refs, want)
	}
}

func TestDeriveBuildBases_DockerfileInline(t *testing.T) {
	tmp := t.TempDir()
	inline := "FROM golang:1.22\nRUN go build ./...\n"
	inlineJSON, err := json.Marshal(inline)
	if err != nil {
		t.Fatal(err)
	}
	cfgJSON := fmt.Sprintf(`{"services":{"api":{"build":{"context":"/unused","dockerfile_inline":%s}}}}`, string(inlineJSON))
	recorder := filepath.Join(tmp, "recorder.txt")
	stub := stubComposeConfig(t, recorder, cfgJSON)

	c := &Compose{Bin: stub, ProjectName: "proj", Files: []string{"compose.yaml"}}
	refs, err := c.DeriveBuildBases(nil)
	if err != nil {
		t.Fatalf("DeriveBuildBases: %v", err)
	}
	want := bases("golang:1.22")
	if !reflect.DeepEqual(refs, want) {
		t.Fatalf("refs = %v, want %v", refs, want)
	}
}

func TestDeriveBuildBases_BuildArgsOverrideReachesParser(t *testing.T) {
	tmp := t.TempDir()
	apiDir := filepath.Join(tmp, "api")
	writeDockerfile(t, apiDir, "ARG BASE_IMAGE=golang:1.22\nFROM ${BASE_IMAGE}\n")

	cfgJSON := fmt.Sprintf(`{"services":{"api":{"build":{"context":%q,"dockerfile":"Dockerfile","args":{"BASE_IMAGE":"golang:1.23"}}}}}`, apiDir)
	recorder := filepath.Join(tmp, "recorder.txt")
	stub := stubComposeConfig(t, recorder, cfgJSON)

	c := &Compose{Bin: stub, ProjectName: "proj", Files: []string{"compose.yaml"}}
	refs, err := c.DeriveBuildBases(nil)
	if err != nil {
		t.Fatalf("DeriveBuildBases: %v", err)
	}
	want := bases("golang:1.23")
	if !reflect.DeepEqual(refs, want) {
		t.Fatalf("refs = %v, want %v", refs, want)
	}
}

func TestDeriveBuildBases_NullBuildArgFallsBackToArgDefault(t *testing.T) {
	tmp := t.TempDir()
	apiDir := filepath.Join(tmp, "api")
	writeDockerfile(t, apiDir, "ARG BASE_IMAGE=golang:1.22\nFROM ${BASE_IMAGE}\n")

	// compose config emits an unset build.args override as JSON null; it
	// must not be treated as an empty-string override.
	cfgJSON := fmt.Sprintf(`{"services":{"api":{"build":{"context":%q,"dockerfile":"Dockerfile","args":{"BASE_IMAGE":null}}}}}`, apiDir)
	recorder := filepath.Join(tmp, "recorder.txt")
	stub := stubComposeConfig(t, recorder, cfgJSON)

	c := &Compose{Bin: stub, ProjectName: "proj", Files: []string{"compose.yaml"}}
	refs, err := c.DeriveBuildBases(nil)
	if err != nil {
		t.Fatalf("DeriveBuildBases: %v", err)
	}
	want := bases("golang:1.22")
	if !reflect.DeepEqual(refs, want) {
		t.Fatalf("refs = %v, want %v", refs, want)
	}
}

// TestDeriveBuildBases_ServicePlatformThreaded guards that a service-level
// `platform:` becomes the effective platform of a base whose FROM pins none of
// its own — otherwise buildkit would still fetch the pinned variant from the
// registry the prepull exists to avoid.
func TestDeriveBuildBases_ServicePlatformThreaded(t *testing.T) {
	tmp := t.TempDir()
	apiDir := filepath.Join(tmp, "api")
	writeDockerfile(t, apiDir, "FROM golang:1.22\n")

	cfgJSON := fmt.Sprintf(`{"services":{"api":{"platform":"linux/amd64","build":{"context":%q,"dockerfile":"Dockerfile"}}}}`, apiDir)
	recorder := filepath.Join(tmp, "recorder.txt")
	stub := stubComposeConfig(t, recorder, cfgJSON)

	c := &Compose{Bin: stub, ProjectName: "proj", Files: []string{"compose.yaml"}}
	refs, err := c.DeriveBuildBases(nil)
	if err != nil {
		t.Fatalf("DeriveBuildBases: %v", err)
	}
	want := []BaseRef{{Ref: "golang:1.22", Platform: "linux/amd64"}}
	if !reflect.DeepEqual(refs, want) {
		t.Fatalf("refs = %v, want %v", refs, want)
	}
}

// TestDeriveBuildBases_BuildPlatformsWinsOverServicePlatform guards the
// precedence (build.platforms beats service-level platform) and that a
// multi-entry list yields one ref per target platform.
func TestDeriveBuildBases_BuildPlatformsWinsOverServicePlatform(t *testing.T) {
	tmp := t.TempDir()
	apiDir := filepath.Join(tmp, "api")
	writeDockerfile(t, apiDir, "FROM golang:1.22\n")

	cfgJSON := fmt.Sprintf(`{"services":{"api":{"platform":"linux/arm64","build":{"context":%q,"dockerfile":"Dockerfile","platforms":["linux/amd64","linux/arm/v7"]}}}}`, apiDir)
	recorder := filepath.Join(tmp, "recorder.txt")
	stub := stubComposeConfig(t, recorder, cfgJSON)

	c := &Compose{Bin: stub, ProjectName: "proj", Files: []string{"compose.yaml"}}
	refs, err := c.DeriveBuildBases(nil)
	if err != nil {
		t.Fatalf("DeriveBuildBases: %v", err)
	}
	want := []BaseRef{
		{Ref: "golang:1.22", Platform: "linux/amd64"},
		{Ref: "golang:1.22", Platform: "linux/arm/v7"},
	}
	if !reflect.DeepEqual(refs, want) {
		t.Fatalf("refs = %v, want %v", refs, want)
	}
}

func TestDeriveBuildBases_ComposeConfigFailureReturnsError(t *testing.T) {
	stub := writeStub(t, "echo boom 1>&2\nexit 1")

	c := &Compose{Bin: stub, ProjectName: "proj", Files: []string{"compose.yaml"}}
	if _, err := c.DeriveBuildBases(nil); err == nil {
		t.Fatal("expected error from failing compose config")
	}
}

func TestDeriveBuildBases_UnreadableDockerfileSkipsService(t *testing.T) {
	tmp := t.TempDir()
	apiDir := filepath.Join(tmp, "api")
	workerDir := filepath.Join(tmp, "worker")
	if err := os.MkdirAll(apiDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// api has no Dockerfile on disk -> unreadable.
	writeDockerfile(t, workerDir, "FROM alpine:3.19\n")

	cfgJSON := fmt.Sprintf(`{"services":{"api":{"build":{"context":%q,"dockerfile":"Dockerfile"}},"worker":{"build":{"context":%q,"dockerfile":"Dockerfile"}}}}`, apiDir, workerDir)
	recorder := filepath.Join(tmp, "recorder.txt")
	stub := stubComposeConfig(t, recorder, cfgJSON)

	c := &Compose{Bin: stub, ProjectName: "proj", Files: []string{"compose.yaml"}}
	refs, err := c.DeriveBuildBases(nil)
	if err != nil {
		t.Fatalf("DeriveBuildBases: %v", err)
	}
	want := bases("alpine:3.19")
	if !reflect.DeepEqual(refs, want) {
		t.Fatalf("refs = %v, want %v", refs, want)
	}
}

func TestDeriveBuildBases_UsesBuildInternalArgsNotGlobal(t *testing.T) {
	tmp := t.TempDir()
	apiDir := filepath.Join(tmp, "api")
	writeDockerfile(t, apiDir, "FROM golang:1.22\n")

	cfgJSON := fmt.Sprintf(`{"services":{"api":{"build":{"context":%q,"dockerfile":"Dockerfile"}}}}`, apiDir)
	recorder := filepath.Join(tmp, "recorder.txt")
	stub := stubComposeConfig(t, recorder, cfgJSON)

	c := &Compose{
		Bin:         stub,
		ProjectName: "proj",
		Files:       []string{"compose.yaml"},
		GlobalArgs:  []string{"--ansi", "always"},
		CommandArgs: map[string][]string{"config": {"--should-not-appear"}},
	}
	if _, err := c.DeriveBuildBases(nil); err != nil {
		t.Fatalf("DeriveBuildBases: %v", err)
	}

	recorded, err := os.ReadFile(recorder)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(string(recorded))
	want := strings.Join(c.BuildInternalArgs("config", "--format", "json"), " ")
	if got != want {
		t.Fatalf("recorded args = %q, want %q", got, want)
	}
	if strings.Contains(got, "--ansi") || strings.Contains(got, "--should-not-appear") {
		t.Fatalf("recorded args unexpectedly contain policy/global args: %q", got)
	}
}

func TestImageExists_TrueOnZeroExit(t *testing.T) {
	stub := writeStub(t, "exit 0")
	c := &Compose{Bin: stub}
	if !c.ImageExists("golang:1.22", "") {
		t.Fatal("ImageExists = false, want true")
	}
}

func TestImageExists_FalseOnNonZeroExit(t *testing.T) {
	stub := writeStub(t, "exit 1")
	c := &Compose{Bin: stub}
	if c.ImageExists("golang:1.22", "") {
		t.Fatal("ImageExists = true, want false")
	}
}

// TestImageExists_PassesPlatformFlag guards that a pinned platform reaches the
// `docker image inspect --platform` probe so a wrong-arch local variant is not
// mistaken for the pinned one.
func TestImageExists_PassesPlatformFlag(t *testing.T) {
	tmp := t.TempDir()
	recorder := filepath.Join(tmp, "recorder.txt")
	stub := writeStub(t, fmt.Sprintf(`echo "$@" > %q`+"\nexit 0", recorder))

	c := &Compose{Bin: stub}
	if !c.ImageExists("golang:1.22", "linux/amd64") {
		t.Fatal("ImageExists = false, want true")
	}

	recorded, err := os.ReadFile(recorder)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(string(recorded))
	want := "image inspect --platform linux/amd64 golang:1.22"
	if got != want {
		t.Fatalf("recorded args = %q, want %q", got, want)
	}
}

func TestPullImage_InvokesPullWithRef(t *testing.T) {
	tmp := t.TempDir()
	recorder := filepath.Join(tmp, "recorder.txt")
	stub := writeStub(t, fmt.Sprintf(`echo "$@" > %q`, recorder))

	c := &Compose{Bin: stub}
	if err := c.PullImage("golang:1.22", ""); err != nil {
		t.Fatalf("PullImage: %v", err)
	}

	recorded, err := os.ReadFile(recorder)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(string(recorded))
	want := "pull golang:1.22"
	if got != want {
		t.Fatalf("recorded args = %q, want %q", got, want)
	}
}

// TestPullImage_PassesPlatformFlag guards that a pinned platform reaches
// `docker pull --platform`, so buildkit finds the pinned variant locally
// instead of re-fetching it from the registry the prepull avoids.
func TestPullImage_PassesPlatformFlag(t *testing.T) {
	tmp := t.TempDir()
	recorder := filepath.Join(tmp, "recorder.txt")
	stub := writeStub(t, fmt.Sprintf(`echo "$@" > %q`, recorder))

	c := &Compose{Bin: stub}
	if err := c.PullImage("golang:1.22", "linux/amd64"); err != nil {
		t.Fatalf("PullImage: %v", err)
	}

	recorded, err := os.ReadFile(recorder)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(string(recorded))
	want := "pull --platform linux/amd64 golang:1.22"
	if got != want {
		t.Fatalf("recorded args = %q, want %q", got, want)
	}
}

func TestPullImage_PropagatesNonZeroExitAsError(t *testing.T) {
	stub := writeStub(t, "echo boom 1>&2\nexit 1")
	c := &Compose{Bin: stub}
	if err := c.PullImage("golang:1.22", ""); err == nil {
		t.Fatal("expected error from failing pull")
	}
}

// TestImageExists_PropagatesEnvAndDir guards the documented "deliberate
// deviation": ImageExists must set cmd.Env = BuildEnv() and cmd.Dir = BaseDir
// so DOCKER_HOST/context overrides from ProcessEnv reach the daemon probe.
func TestImageExists_PropagatesEnvAndDir(t *testing.T) {
	tmp := t.TempDir()
	recorder := filepath.Join(tmp, "recorder.txt")
	stub := writeStub(t, fmt.Sprintf("echo \"$DOCKER_HOST|$(pwd)\" > %q\nexit 0", recorder))

	c := &Compose{Bin: stub, BaseDir: tmp, ProcessEnv: map[string]string{"DOCKER_HOST": "tcp://probe:2375"}}
	if !c.ImageExists("golang:1.22", "") {
		t.Fatal("ImageExists = false, want true")
	}

	got := readRecordedEnvDir(t, recorder)
	wantDir, _ := filepath.EvalSymlinks(tmp)
	if got := got; !strings.HasPrefix(got, "tcp://probe:2375|") {
		t.Fatalf("DOCKER_HOST not propagated: %q", got)
	}
	if !strings.HasSuffix(got, "|"+wantDir) {
		t.Fatalf("BaseDir not propagated as cwd: %q, want suffix %q", got, wantDir)
	}
}

// TestPullImage_PropagatesEnvAndDir mirrors the ImageExists guard for PullImage.
func TestPullImage_PropagatesEnvAndDir(t *testing.T) {
	tmp := t.TempDir()
	recorder := filepath.Join(tmp, "recorder.txt")
	stub := writeStub(t, fmt.Sprintf("echo \"$DOCKER_HOST|$(pwd)\" > %q", recorder))

	c := &Compose{Bin: stub, BaseDir: tmp, ProcessEnv: map[string]string{"DOCKER_HOST": "tcp://probe:2375"}}
	if err := c.PullImage("golang:1.22", ""); err != nil {
		t.Fatalf("PullImage: %v", err)
	}

	got := readRecordedEnvDir(t, recorder)
	wantDir, _ := filepath.EvalSymlinks(tmp)
	if !strings.HasPrefix(got, "tcp://probe:2375|") {
		t.Fatalf("DOCKER_HOST not propagated: %q", got)
	}
	if !strings.HasSuffix(got, "|"+wantDir) {
		t.Fatalf("BaseDir not propagated as cwd: %q, want suffix %q", got, wantDir)
	}
}

func readRecordedEnvDir(t *testing.T, path string) string {
	t.Helper()
	recorded, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading recorder: %v", err)
	}
	return strings.TrimSpace(string(recorded))
}
