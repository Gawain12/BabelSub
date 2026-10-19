package syncer

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
)

type Syncer struct {
	FFsubsyncPath string
}

func NewSyncer(customPath string) *Syncer {
	bin := customPath
	if bin == "" {
		// Look up in PATH
		if p, err := exec.LookPath("ffsubsync"); err == nil {
			bin = p
		} else {
			bin = "ffsubsync"
		}
	}
	return &Syncer{FFsubsyncPath: bin}
}

func (s *Syncer) Sync(referencePath, targetSubPath, outputPath string) error {
	return s.SyncWithContext(context.Background(), referencePath, targetSubPath, outputPath, nil)
}

func (s *Syncer) SyncWithContext(
	ctx context.Context,
	referencePath, targetSubPath, outputPath string,
	progressCallback func(string),
) error {
	cmd := exec.CommandContext(ctx, s.FFsubsyncPath, referencePath, "-i", targetSubPath, "-o", outputPath, "--encoding", "utf-8")
	
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to open stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start ffsubsync: %w", err)
	}

	scanner := bufio.NewScanner(stderrPipe)
	for scanner.Scan() {
		line := scanner.Text()
		if progressCallback != nil {
			progressCallback(line)
		}
	}

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("ffsubsync execution failed: %w", err)
	}

	if info, err := os.Stat(outputPath); err != nil || info.Size() == 0 {
		return fmt.Errorf("sync output file empty or not generated")
	}
	return nil
}
