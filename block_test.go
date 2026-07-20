package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestBlockHosts(t *testing.T) {
	// Dedup across bare/www forms, lowercase, trim, drop shell-unsafe input.
	got := blockHosts([]string{" X.com", "www.x.com", "bad;rm -rf /", ""})
	want := []string{"x.com", "www.x.com"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("host %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestHelperScriptShape(t *testing.T) {
	// The helper is the root trust boundary: it must validate domains itself
	// and only ever write 0.0.0.0 entries.
	for _, sub := range []string{"*[!a-z0-9.-]*", "printf '0.0.0.0 %s %s\\n'", blockTag} {
		if !strings.Contains(helperScript, sub) {
			t.Errorf("helper script missing %q", sub)
		}
	}
}

// TestBlockLifecycle drives the real key handling with a fake sudo in PATH:
// block on focus start, hold through pause/resume, unblock on skip.
func TestBlockLifecycle(t *testing.T) {
	dir := t.TempDir()
	callLog := filepath.Join(dir, "calls")
	if err := os.WriteFile(filepath.Join(dir, "sudo"),
		[]byte("#!/bin/sh\necho \"$@\" >> "+callLog+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	oldHelper := blockHelper
	blockHelper = filepath.Join(dir, "helper") // must exist for the setup check
	defer func() { blockHelper = oldHelper }()
	if err := os.WriteFile(blockHelper, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	m := model{ph: focus, status: idle, blocklist: []string{"x.com"}}
	key := func(s string) { m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}) }

	key(" ") // start focus
	if !m.blocked {
		t.Fatalf("focus started: want blocked (err=%q)", m.blockErr)
	}
	key(" ") // pause: block holds
	key(" ") // resume: still blocked, no duplicate helper call
	if !m.blocked {
		t.Fatal("pause/resume: block should hold")
	}
	key("s") // skip to break
	if m.blocked {
		t.Fatal("focus over: want unblocked")
	}

	data, _ := os.ReadFile(callLog)
	got := strings.TrimSpace(string(data))
	want := "-n " + blockHelper + " block x.com www.x.com\n-n " + blockHelper + " unblock"
	if got != want {
		t.Errorf("helper calls:\n%s\nwant:\n%s", got, want)
	}
}
