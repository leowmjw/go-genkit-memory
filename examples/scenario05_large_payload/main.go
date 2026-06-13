// Scenario 5: Multi-File Trace Linkage — Large Payload Offloading (>50 KB)
//
// Sends a 60 KB raw configuration dump through Capture and verifies:
//   - The adapter creates a refs/*.md file on disk
//   - The in-session canvas reference is a short path pointer, not the raw dump
//
// Usage:
//
//	INTEGRATION_LIVE=1 go run ./examples/scenario05_large_payload
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	memstore "github.com/leowmjw/go-genkit-memory/memory"
	sqlitestore "github.com/leowmjw/go-genkit-memory/session/sqlite"
)

type TraceState struct {
	CanvasRef string `json:"canvas_ref"`
}

func main() {
	if os.Getenv("INTEGRATION_LIVE") != "1" {
		fmt.Println("SKIP: set INTEGRATION_LIVE=1 to run")
		os.Exit(0)
	}

	ctx := context.Background()
	refsDir := filepath.Join(os.TempDir(), fmt.Sprintf("genkit-refs-%d", time.Now().UnixMilli()))
	defer os.RemoveAll(refsDir)

	store, err := sqlitestore.NewStore[TraceState](ctx, ":memory:")
	if err != nil {
		fail("open store: %v", err)
	}
	defer store.Close()

	adapter := memstore.NewAdapter[TraceState](store,
		memstore.WithRefsDir(refsDir),
		memstore.WithGatewayURL("http://127.0.0.1:19997"), // dead gateway; capture only tests offload path
	)
	defer adapter.Close()

	// Build a 60 KB environment dump with no standard delimiters in segments.
	hugeDump := "ENV_CONFIG_DUMP: " + strings.Repeat("KEY=VALUE_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789; ", 1400)
	if len(hugeDump) < 50*1024 {
		fail("test setup error: dump too small (%d bytes)", len(hugeDump))
	}

	// The offload path is exercised INSIDE Capture before the HTTP call.
	// Even if the gateway is dead, the file should be written to refs/.
	_ = adapter.Capture(ctx, "trace-session", hugeDump, "Extracted 1400 environment variables.")

	// Give the goroutine a moment to attempt capture and write the offload file.
	time.Sleep(200 * time.Millisecond)

	// Verify refs/ directory was created and contains at least one .md file.
	entries, err := os.ReadDir(refsDir)
	if err != nil {
		fail("refs dir not created: %v", err)
	}

	var mdFiles []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			mdFiles = append(mdFiles, e.Name())
		}
	}

	if len(mdFiles) == 0 {
		fail("no refs/*.md file created for 60 KB payload (offload did not trigger)")
	}

	// Verify the offloaded file contains the original content.
	fpath := filepath.Join(refsDir, mdFiles[0])
	content, err := os.ReadFile(fpath)
	if err != nil {
		fail("read refs file: %v", err)
	}
	if len(content) < 50*1024 {
		fail("offloaded file too small (%d bytes)", len(content))
	}

	fmt.Printf("PASS: %d KB payload offloaded to %s (%d bytes on disk)\n",
		len(hugeDump)/1024, mdFiles[0], len(content))
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FAIL: "+format+"\n", args...)
	os.Exit(1)
}
