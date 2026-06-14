//go:build dolt_integration

package beads_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

// BenchmarkBdStoreOrderRunLookup measures the two order-run lookup shapes the
// orders feed has used, against a real bd CLI talking to a real dolt
// sql-server — the production stack, where every List is a subprocess spawn
// plus a dolt round-trip:
//
//   - per-order: one Limit-1 exact-label List per tracked order (the feed's
//     old N+1 pattern; N lists per op).
//   - prefix-scan: a single LabelPrefix List covering every order (the
//     batched replacement; one list per op).
//
// Run with:
//
//	go test -tags dolt_integration ./internal/beads \
//	  -run '^$' -bench BenchmarkBdStoreOrderRunLookup -benchtime 3x
//
// Select one store size with -bench 'BenchmarkBdStoreOrderRunLookup/orders=100'.
// Skips when bd or dolt is not installed.
func BenchmarkBdStoreOrderRunLookup(b *testing.B) {
	doltPath, err := exec.LookPath("dolt")
	if err != nil {
		b.Skipf("dolt not found: %v", err)
	}
	if _, err := exec.LookPath("bd"); err != nil {
		b.Skipf("bd not found: %v", err)
	}

	for _, n := range []int{10, 100, 500} {
		b.Run(fmt.Sprintf("orders=%d", n), func(b *testing.B) {
			store, names := newSeededDoltBenchStore(b, doltPath, n)

			b.Run("per-order", func(b *testing.B) {
				for i := 0; i < b.N; i++ {
					for _, name := range names {
						runs, err := store.List(beads.ListQuery{
							Label:    "order-run:" + name,
							Limit:    1,
							Sort:     beads.SortCreatedDesc,
							TierMode: beads.TierBoth,
						})
						if err != nil {
							b.Fatalf("per-order list %s: %v", name, err)
						}
						if len(runs) == 0 {
							b.Fatalf("per-order list %s returned no beads", name)
						}
					}
				}
				b.ReportMetric(float64(len(names)), "store_lists/op")
			})

			b.Run("prefix-scan", func(b *testing.B) {
				for i := 0; i < b.N; i++ {
					runs, err := store.List(beads.ListQuery{
						LabelPrefix: "order-run:",
						TierMode:    beads.TierBoth,
					})
					if err != nil {
						b.Fatalf("prefix scan: %v", err)
					}
					if len(runs) < len(names) {
						b.Fatalf("prefix scan returned %d beads, want >= %d", len(runs), len(names))
					}
				}
				b.ReportMetric(1, "store_lists/op")
			})
		})
	}
}

// newSeededDoltBenchStore initializes a bd store backed by a fresh dolt
// sql-server and seeds n tracked orders, each with one tracking bead and
// three run beads carrying the order-run:<name> label.
func newSeededDoltBenchStore(b *testing.B, doltPath string, n int) (beads.Store, []string) {
	b.Helper()
	dir := b.TempDir()
	dataDir := filepath.Join(dir, ".beads", "dolt")
	dbDir := filepath.Join(dataDir, "beads")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		b.Fatalf("mkdir db dir: %v", err)
	}
	runDoltBench(b, doltPath, dbDir, "init", "--name", "Gas City", "--email", "bench@example.com")

	port := startDoltBenchServer(b, doltPath, dataDir)
	waitForDoltBenchServer(b, doltPath, port)

	// Mirror the env the city runtime hands bd for dolt-server stores so
	// every command goes to the server instead of bd's on-disk fallback.
	runner := beads.ExecCommandRunnerWithEnv(map[string]string{
		"BEADS_DIR":              filepath.Join(dir, ".beads"),
		"BEADS_DOLT_SERVER_HOST": "127.0.0.1",
		"BEADS_DOLT_SERVER_PORT": strconv.Itoa(port),
		"BEADS_DOLT_SERVER_USER": "root",
		"BEADS_DOLT_PASSWORD":    "",
		"BEADS_DOLT_AUTO_START":  "0",
		"BD_EXPORT_AUTO":         "false",
	})
	store := beads.NewBdStore(dir, runner)
	if err := store.Init("bench", "127.0.0.1", strconv.Itoa(port)); err != nil {
		b.Fatalf("bd init: %v", err)
	}

	names := make([]string, 0, n)
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("order-%04d", i)
		names = append(names, name)
		if _, err := store.Create(beads.Bead{
			Title:  "order:" + name,
			Labels: []string{"order-tracking", "order-run:" + name},
		}); err != nil {
			b.Fatalf("create tracking bead %s: %v", name, err)
		}
		for r := 0; r < 3; r++ {
			if _, err := store.Create(beads.Bead{
				Title:  "run",
				Labels: []string{"order-run:" + name},
			}); err != nil {
				b.Fatalf("create run bead %s: %v", name, err)
			}
		}
	}
	return store, names
}

func runDoltBench(b *testing.B, doltPath, dir string, args ...string) {
	b.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, doltPath, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		b.Fatalf("dolt %s failed in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
}

func startDoltBenchServer(b *testing.B, doltPath, dataDir string) int {
	b.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatalf("allocating dolt port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		b.Fatalf("closing dolt port probe: %v", err)
	}

	logFile, err := os.Create(filepath.Join(dataDir, "sql-server.log"))
	if err != nil {
		b.Fatalf("create dolt server log: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, doltPath, "sql-server",
		"-H", "127.0.0.1",
		"-P", strconv.Itoa(port),
		"--data-dir", dataDir,
		"--loglevel", "warning",
	)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		cancel()
		b.Fatalf("start dolt sql-server: %v", err)
	}
	b.Cleanup(func() {
		cancel()
		_, _ = cmd.Process.Wait()
		_ = logFile.Close()
	})
	return port
}

func waitForDoltBenchServer(b *testing.B, doltPath string, port int) {
	b.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var lastOut []byte
	var lastErr error
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		cmd := exec.CommandContext(ctx, doltPath,
			"--host", "127.0.0.1",
			"--port", strconv.Itoa(port),
			"--user", "root",
			"--no-tls",
			"--use-db", "beads",
			"sql", "-q", "SELECT 1",
		)
		cmd.Env = append(os.Environ(), "DOLT_CLI_PASSWORD=")
		lastOut, lastErr = cmd.CombinedOutput()
		cancel()
		if lastErr == nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	b.Fatalf("dolt sql-server did not become query-ready on port %d: %v\n%s", port, lastErr, lastOut)
}
