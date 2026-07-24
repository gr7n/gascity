package scripts_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalParallelPreparesOneCmdGCBinaryBeforeShardFanout(t *testing.T) {
	source, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "test-local-parallel"))
	if err != nil {
		t.Fatalf("read test-local-parallel: %v", err)
	}
	script := string(source)
	required := []string{
		`GO_TEST_PREPARE_BINARY="$cmd_gc_binary"`,
		`GO_TEST_PREPARE_MANIFEST="$cmd_gc_manifest"`,
		`GO_TEST_PREPARED_BINARY=\"\$TEST_LOCAL_CMD_GC_BINARY\"`,
		`GO_TEST_MANIFEST=\"\$TEST_LOCAL_CMD_GC_MANIFEST\"`,
		`TEST_LOCAL_CMD_GC_BINARY="${TEST_LOCAL_CMD_GC_BINARY-}"`,
		`TEST_LOCAL_CMD_GC_MANIFEST="${TEST_LOCAL_CMD_GC_MANIFEST-}"`,
		`trap cleanup_cmd_gc_prepared EXIT`,
	}
	for _, contract := range required {
		if !strings.Contains(script, contract) {
			t.Fatalf("test-local-parallel prepared-binary contract is missing %q", contract)
		}
	}
	if prepare := strings.Index(script, `echo "[cmd-gc-prepare] start"`); prepare < 0 {
		t.Fatal("test-local-parallel has no cmd/gc preparation gate")
	} else if fanout := strings.Index(script, `printf '%s\0' "${jobspecs[@]}"`); fanout < 0 || prepare >= fanout {
		t.Fatal("cmd/gc preparation must finish before any parallel test job starts")
	}
}
