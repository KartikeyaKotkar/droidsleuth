package evidence

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type crashTailShell struct {
	output   string
	commands []string
}

func (s *crashTailShell) Shell(_ context.Context, command string) (string, error) {
	s.commands = append(s.commands, command)
	if strings.HasPrefix(command, "logcat") {
		return s.output, nil
	}
	return "boot\n", nil
}

func TestNumericLogcatTailRecoversCrashFromEmptyMergedRead(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "compatibility", "api37-crash-buffer-tail.txt"))
	if err != nil {
		t.Fatal(err)
	}
	shell := &crashTailShell{output: crashTailBoundary + string(raw)}
	var marker string
	got, issue := readLogcat(context.Background(), shell, target, &marker)
	if issue != "" || len(got) != 1 || got[0].Events[0].Kind != "java_fatal" || got[0].Events[0].PID != 42 {
		t.Fatalf("crash tail was not recovered: %+v, %q", got, issue)
	}
	if len(shell.commands) != 3 || strings.Count(shell.commands[1], "logcat -d -t 512") != 2 || !strings.Contains(shell.commands[1], "-b crash 'AndroidRuntime:E'") {
		t.Fatalf("numeric tails exceeded ADB call budget or had wrong arguments: %v", shell.commands)
	}

	shell.output, shell.commands = string(raw), nil
	got, issue = readLogcat(context.Background(), shell, target, &marker)
	if issue != "" || len(got) != 1 || len(shell.commands) != 3 || !strings.Contains(shell.commands[1], "-T '1700000000.123455'") || strings.Count(shell.commands[1], "logcat") != 1 {
		t.Fatalf("crash marker did not seed the existing cursor path: %v, %q", shell.commands, issue)
	}
}

func TestLogcatCrashTailRejectsUnverifiedFrames(t *testing.T) {
	merged := java("10123", 42, target.Package)
	badBlocks := []string{
		java("10124", 42, target.Package),
		strings.Replace(merged, "PID: 42", "PID: 41", 1),
		logline("10123", 42, "AndroidRuntime", "Process: com.example.app, PID: 42"),
	}
	shell := &crashTailShell{}
	for _, frame := range badBlocks {
		var marker string
		shell.output = crashTailBoundary + frame
		if got, _ := readLogcat(context.Background(), shell, target, &marker); len(got) != 0 {
			t.Fatalf("unverified record became evidence: %+v", got)
		}
	}
	var marker string
	shell.output = merged
	got, issue := readLogcat(context.Background(), shell, target, &marker)
	if len(got) != 0 || issue != "logcat crash tail framing incomplete" || marker != "" {
		t.Fatalf("incomplete reply was accepted: %+v, %q", got, issue)
	}
}

func TestLogcatNumericTailsDoNotSplicePartialStacks(t *testing.T) {
	header := logline("10123", 42, "AndroidRuntime", "FATAL EXCEPTION: main")
	process := logline("10123", 42, "AndroidRuntime", "Process: com.example.app, PID: 42")
	shell := &crashTailShell{output: header + crashTailBoundary + process}
	var marker string
	got, issue := readLogcat(context.Background(), shell, target, &marker)
	if len(got) != 0 || issue == "" {
		t.Fatalf("separate partial reads were combined: %+v, %q", got, issue)
	}
}

func TestLogcatNumericTailsDeduplicateOverlappingStacks(t *testing.T) {
	raw := java("10123", 42, target.Package)
	var marker string
	shell := &crashTailShell{output: raw + crashTailBoundary + raw}
	got, issue := readLogcat(context.Background(), shell, target, &marker)
	if issue != "" || len(got) != 1 {
		t.Fatalf("overlap consumed the poll observation budget: %+v, %q", got, issue)
	}
}

func TestFailedCrashTailKeepsVerifiedMergedEvidence(t *testing.T) {
	merged := java("10123", 42, target.Package)
	companion := java("10123", 43, target.Package)
	var marker string
	shell := &crashTailShell{output: merged + crashTailBoundary + companion + crashTailFailure}
	got, issue := readLogcat(context.Background(), shell, target, &marker)
	if len(got) != 1 || got[0].Events[0].PID != 42 || issue != "logcat crash-only tail read failed" {
		t.Fatalf("failed auxiliary read affected trusted merged evidence: %+v, %q", got, issue)
	}
}
