package scripts_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestImageGoToolchainAndSourcePins(t *testing.T) {
	root := repoRoot(t)
	env := readDotenv(t, filepath.Join(root, "deps.env"))

	goVersion := env["GO_VERSION"]
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(goVersion) {
		t.Fatalf("deps.env GO_VERSION = %q, want an exact patch release", goVersion)
	}
	builderPattern := regexp.MustCompile(`^docker\.io/library/golang:` + regexp.QuoteMeta(goVersion) + `-bookworm@sha256:[0-9a-f]{64}$`)
	if !builderPattern.MatchString(env["IMAGE_GO_BUILDER"]) {
		t.Fatalf("deps.env IMAGE_GO_BUILDER = %q, want a digest-pinned official Go %s Bookworm image", env["IMAGE_GO_BUILDER"], goVersion)
	}

	goMod := readFile(t, root, "go.mod")
	if !regexp.MustCompile(`(?m)^go ` + regexp.QuoteMeta(goVersion) + `$`).MatchString(goMod) {
		t.Fatalf("go.mod must pin go %s", goVersion)
	}
	for _, action := range []string{
		".github/actions/setup-gascity-ubuntu/action.yml",
		".github/actions/setup-gascity-macos/action.yml",
	} {
		if !strings.Contains(readFile(t, root, action), `default: "`+goVersion+`"`) {
			t.Fatalf("%s must default to Go %s", action, goVersion)
		}
	}

	for _, pair := range [][2]string{
		{"GH_VERSION", `^\d+\.\d+\.\d+$`},
		{"DOLT_VERSION", `^\d+\.\d+\.\d+$`},
		{"KUBECTL_VERSION", `^v\d+\.\d+\.\d+$`},
		{"GH_IMAGE_SOURCE_REF", `^[0-9a-f]{40}$`},
		{"GH_IMAGE_MODULE_SUM", `^h1:[A-Za-z0-9+/]{43}=$`},
		{"GH_IMAGE_GO_MOD_SUM", `^h1:[A-Za-z0-9+/]{43}=$`},
		{"DOLT_IMAGE_SOURCE_REF", `^[0-9a-f]{40}$`},
		{"KUBECTL_IMAGE_SOURCE_REF", `^[0-9a-f]{40}$`},
	} {
		if !regexp.MustCompile(pair[1]).MatchString(env[pair[0]]) {
			t.Fatalf("deps.env %s = %q, does not match %s", pair[0], env[pair[0]], pair[1])
		}
	}
	for _, name := range []string{"GH_REPO", "DOLT_REPO", "KUBECTL_REPO"} {
		if !regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`).MatchString(env[name]) {
			t.Fatalf("deps.env %s = %q, want owner/repository", name, env[name])
		}
	}

	builderPath := filepath.Join(root, ".github/scripts/build-image-go-tools.sh")
	builder := readFile(t, root, ".github/scripts/build-image-go-tools.sh")
	for _, pin := range []string{
		"GO_VERSION",
		"IMAGE_GO_BUILDER",
		"GH_IMAGE_SOURCE_REF",
		"GH_IMAGE_MODULE_SUM",
		"GH_IMAGE_GO_MOD_SUM",
		"DOLT_IMAGE_SOURCE_REF",
		"KUBECTL_IMAGE_SOURCE_REF",
		"GOTOOLCHAIN=local",
		"SOURCE_DATE_EPOCH",
		"mod download -json",
		"gh_origin_hash",
		"-mod=readonly",
		"gms_pure_go,timetzdata",
		"-mod=vendor",
		"CGO_ENABLED=1",
		"CGO_ENABLED=0",
	} {
		if !strings.Contains(builder, pin) {
			t.Fatalf("image tool builder does not enforce %q", pin)
		}
	}
	for _, banned := range []string{"@latest", "refs/heads/", "--depth=1 origin main", "trivyignore"} {
		if strings.Contains(builder, banned) {
			t.Fatalf("image tool builder contains mutable or bypassing input %q", banned)
		}
	}
	info, err := os.Stat(builderPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatal("build-image-go-tools.sh must be executable")
	}

	inputs := readFile(t, root, ".github/scripts/build-go-image-inputs.sh")
	for _, call := range []string{"build-bd-image.sh", "build-image-go-tools.sh"} {
		if !strings.Contains(inputs, call) {
			t.Fatalf("build-go-image-inputs.sh must call %s", call)
		}
	}
}

func TestRuntimeImagesConsumeOnlyReviewedGoToolOutputs(t *testing.T) {
	root := repoRoot(t)
	base := readFile(t, root, "contrib/k8s/Dockerfile.base")
	controller := readFile(t, root, "contrib/k8s/Dockerfile.controller")
	workflow := readFile(t, root, ".github/workflows/container-scan.yml")
	makefile := readFile(t, root, "Makefile")

	for _, want := range []string{
		"COPY image-tools/gh /usr/local/bin/gh",
		"COPY image-tools/dolt /usr/local/bin/dolt",
		"ARG GH_VERSION=",
		"ARG DOLT_VERSION=",
	} {
		if !strings.Contains(base, want) {
			t.Fatalf("Dockerfile.base missing %q", want)
		}
	}
	for _, banned := range []string{"cli.github.com/packages", "apt-get install -y --no-install-recommends gh", "install-dolt-archive.sh"} {
		if strings.Contains(base, banned) {
			t.Fatalf("Dockerfile.base still uses non-rebuilt runtime tool input %q", banned)
		}
	}
	if !strings.Contains(controller, "COPY image-tools/kubectl /usr/local/bin/kubectl") {
		t.Fatal("Dockerfile.controller must copy the reviewed kubectl rebuild")
	}
	if strings.Contains(controller, "https://dl.k8s.io/release/") {
		t.Fatal("Dockerfile.controller must not replace the reviewed kubectl rebuild with a release binary")
	}

	for _, want := range []string{
		"\"$IMAGE_GO_BUILDER\"",
		"bash .github/scripts/build-go-image-inputs.sh /out",
		"--platform linux/amd64",
		"--build-arg GH_VERSION=\"$GH_VERSION\"",
		"--build-arg DOLT_VERSION=\"$DOLT_VERSION\"",
		"--build-arg KUBECTL_VERSION=\"$KUBECTL_VERSION\"",
	} {
		if !strings.Contains(workflow, want) {
			t.Fatalf("container scan workflow missing %q", want)
		}
	}
	for _, want := range []string{"image-go-tools: check-docker", "$$IMAGE_GO_BUILDER", "bash .github/scripts/build-image-go-tools.sh /out", "docker build --platform linux/amd64"} {
		if !strings.Contains(makefile, want) {
			t.Fatalf("Makefile image build path missing %q", want)
		}
	}

	ignore := readFile(t, root, ".trivyignore.yaml")
	if strings.Contains(ignore, "CVE-2026-39822") {
		t.Fatal("CVE-2026-39822 must be fixed by the pinned rebuild, not waived")
	}
	if strings.Contains(ignore, "usr/local/bin/br") {
		t.Fatal("Trivy policy must not retain stale br findings absent from the raw image scan")
	}
	for _, stale := range []string{
		"CVE-2026-33811", "CVE-2026-39820", "CVE-2026-39823",
		"CVE-2026-39825", "CVE-2026-39826", "CVE-2026-39836",
		"CVE-2026-42499", "CVE-2026-42504", "CVE-2026-27145",
		"CVE-2026-25680", "CVE-2026-42502", "CVE-2026-42506",
	} {
		if strings.Contains(ignore, stale) {
			t.Fatalf("Trivy policy retains stale rebuilt-tool finding %s", stale)
		}
	}
	if !strings.Contains(ignore, "CVE-2026-33814") ||
		!strings.Contains(ignore, "kubectl v1.36.0 still carries this golang.org/x/net vulnerability") {
		t.Fatal("kubectl's remaining CVE-2026-33814 x/net finding must stay explicit")
	}
}
