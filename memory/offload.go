package memory

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// offloadThreshold is the byte size above which a message is written to disk
// and replaced with a path pointer (refs/*.md).
const offloadThreshold = 50 * 1024 // 50 KB

// offloader writes large message payloads to external markdown files under
// a configurable refs directory, returning a compact path pointer instead.
type offloader struct {
	refsDir string
	log     *slog.Logger
}

// newOffloader creates an offloader that writes to refsDir.
func newOffloader(refsDir string, log *slog.Logger) *offloader {
	return &offloader{refsDir: refsDir, log: log}
}

// MaybeOffload returns content unchanged when it is below the threshold.
// When content exceeds the threshold it writes a file to refs/ and returns
// a single-line path pointer string: "refs/<sessionID>_<ts>.md".
func (o *offloader) MaybeOffload(sessionID, content string) (string, error) {
	if len(content) <= offloadThreshold {
		return content, nil
	}

	if err := os.MkdirAll(o.refsDir, 0750); err != nil {
		return content, fmt.Errorf("offload: mkdir %q: %w", o.refsDir, err)
	}

	ts := time.Now().UnixMilli()
	// Replace characters that are unsafe in filenames.
	safeID := strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(sessionID)
	filename := fmt.Sprintf("%s_%d.md", safeID, ts)
	fpath := filepath.Join(o.refsDir, filename)

	if err := os.WriteFile(fpath, []byte(content), 0640); err != nil {
		// Non-fatal: log and return original content so capture still proceeds.
		o.log.Warn("offload write failed, using inline content",
			slog.String("path", fpath),
			slog.String("err", err.Error()),
		)
		return content, nil
	}

	o.log.Debug("offloaded large payload",
		slog.String("session", sessionID),
		slog.String("path", fpath),
		slog.Int("original_bytes", len(content)),
	)
	return fpath, nil
}
