package syncer

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

type Syncer struct {
	FFsubsyncPath string
}

func findExecutable(name string) string {
	// 1. Check current executable directory (for bundled distributions)
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidate := filepath.Join(dir, name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
		// Windows check (.exe)
		candidateExe := filepath.Join(dir, name+".exe")
		if info, err := os.Stat(candidateExe); err == nil && !info.IsDir() {
			return candidateExe
		}
	}

	// 2. Check ~/.config/babelsub/venv/bin/ffsubsync
	home, _ := os.UserHomeDir()
	candidateVenv := filepath.Join(home, ".config", "babelsub", "venv", "bin", name)
	if info, err := os.Stat(candidateVenv); err == nil && !info.IsDir() {
		return candidateVenv
	}
	candidateVenvWin := filepath.Join(home, ".config", "babelsub", "venv", "Scripts", name+".exe")
	if info, err := os.Stat(candidateVenvWin); err == nil && !info.IsDir() {
		return candidateVenvWin
	}

	// 3. Fallback to system PATH
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return name
}

func NewSyncer(customPath string) *Syncer {
	bin := customPath
	if bin == "" {
		bin = findExecutable("ffsubsync")
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
		return fmt.Errorf("failed to start ffsubsync (%s): %w", s.FFsubsyncPath, err)
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
