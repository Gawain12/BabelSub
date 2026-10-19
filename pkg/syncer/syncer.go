package syncer

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Syncer struct {
	FFsubsyncPath string
}

// FindExecutable locates a sidecar binary: next to the BabelSub executable, in the
// BabelSub venv, then on PATH. It returns the bare name when nothing is found.
func FindExecutable(name string) string {
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
	// "ffsubsync" is the shipped default; still prefer a bundled or venv copy over PATH.
	if bin == "" || bin == "ffsubsync" {
		bin = FindExecutable("ffsubsync")
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

	// ffsubsync writes progress to stderr and some errors to stdout; read both.
	pr, pw, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("failed to open output pipe: %w", err)
	}
	cmd.Stdout = pw
	cmd.Stderr = pw

	if err := cmd.Start(); err != nil {
		pw.Close()
		pr.Close()
		if errors.Is(err, exec.ErrNotFound) {
			return fmt.Errorf("未找到 ffsubsync (%s)：请运行 scripts/install.sh，或在设置中填写 ffsubsync 路径", s.FFsubsyncPath)
		}
		return fmt.Errorf("failed to start ffsubsync (%s): %w", s.FFsubsyncPath, err)
	}
	pw.Close()

	scanner := bufio.NewScanner(pr)
	scanner.Split(scanLinesOrCR)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && progressCallback != nil {
			progressCallback(line)
		}
	}
	pr.Close()

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("ffsubsync execution failed: %w", err)
	}

	if info, err := os.Stat(outputPath); err != nil || info.Size() == 0 {
		return fmt.Errorf("sync output file empty or not generated")
	}
	return nil
}

// scanLinesOrCR splits on \n or \r so tqdm-style progress updates arrive as lines.
func scanLinesOrCR(data []byte, atEOF bool) (int, []byte, error) {
	for i, b := range data {
		if b == '\n' || b == '\r' {
			return i + 1, data[:i], nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}
