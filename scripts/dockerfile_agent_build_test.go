package scripts_test

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentImageBuildIsSourceOwnedAndContentBound(t *testing.T) {
	root := repoRoot(t)
	env := readDotenv(t, filepath.Join(root, "deps.env"))
	dockerfile := readFile(t, root, "contrib/k8s/Dockerfile.agent")

	for _, required := range []string{
		"ARG IMAGE_GO_BUILDER=" + env["IMAGE_GO_BUILDER"],
		"ARG BASE_IMAGE=gc-agent-base:latest",
		"FROM ${IMAGE_GO_BUILDER} AS gascity-build",
		"ARG GASCITY_COMMIT",
		`test "${#GASCITY_COMMIT}" -eq 40`,
		"-buildvcs=false",
		"main.commit=${GASCITY_COMMIT}",
		".github/scripts/build-bd-image.sh /out/bd",
		"COPY --from=gascity-build /out/gc /usr/local/bin/gc",
		"COPY --from=gascity-build /out/bd /usr/local/bin/bd",
	} {
		if !strings.Contains(dockerfile, required) {
			t.Fatalf("Dockerfile.agent must contain %q", required)
		}
	}
	firstFrom := strings.Index(dockerfile, "FROM ")
	for _, globalArg := range []string{"ARG IMAGE_GO_BUILDER=", "ARG BASE_IMAGE="} {
		if index := strings.Index(dockerfile, globalArg); index < 0 || index > firstFrom {
			t.Fatalf("%s must be declared before the first FROM", globalArg)
		}
	}
	for _, banned := range []string{
		"COPY gc /usr/local/bin/gc",
		"COPY bd /usr/local/bin/bd",
		"COPY br /usr/local/bin/br",
	} {
		if strings.Contains(dockerfile, banned) {
			t.Fatalf("Dockerfile.agent still consumes host-prepared input %q", banned)
		}
	}

	workflow := readFile(t, root, ".github/workflows/container-scan.yml")
	for _, retired := range []string{
		"name: Set up Go",
		"name: Prepare image build inputs",
		"BD_INSTALL_BIN_DIR=",
		"BR_INSTALL_BIN_DIR=",
		"go build -o gc ./cmd/gc",
	} {
		if strings.Contains(workflow, retired) {
			t.Fatalf("container scan still contains retired host preparation %q", retired)
		}
	}
	if !strings.Contains(workflow, `--build-arg GASCITY_COMMIT="$IMAGE_TAG"`) {
		t.Fatal("container scan must content-bind the source-owned agent build")
	}
}

func TestBrIsCachedAndValidatedAgainstRuntimeBase(t *testing.T) {
	root := repoRoot(t)
	dockerfile := readFile(t, root, "contrib/k8s/Dockerfile.agent")
	for _, required := range []string{
		"FROM ${BASE_IMAGE} AS br-tool",
		"GR7N_TOOL_DOWNLOAD_CACHE=/root/.cache/gr7n-downloads",
		"COPY --from=br-tool /out/br /usr/local/bin/br",
	} {
		if !strings.Contains(dockerfile, required) {
			t.Fatalf("Dockerfile.agent must contain %q", required)
		}
	}
	installer := readFile(t, root, ".github/scripts/install-br-archive.sh")
	for _, required := range []string{
		"GR7N_TOOL_DOWNLOAD_CACHE",
		`partial="${archive_path}.partial.$$"`,
		`rm -f "$archive_path"`,
		`"$target" --version`,
	} {
		if !strings.Contains(installer, required) {
			t.Fatalf("install-br-archive.sh must contain %q", required)
		}
	}
}
