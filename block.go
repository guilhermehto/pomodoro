package main

// Focus-time website blocking via /etc/hosts, prompt-free: a one-time
// `understory -setup-block` installs a root-owned helper plus a sudoers
// NOPASSWD rule scoped to it, so the app can run `sudo -n` silently at
// every focus start/end.
// ponytail: hosts blocking is best-effort — no wildcards (list subdomains
// explicitly) and browsers may serve cached DNS until a tab reload.

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const blockTag = "# understory-block"

// var, not const: the lifecycle test points it at a temp helper.
var blockHelper = "/usr/local/libexec/understory-block"

// helperScript runs as root via the sudoers rule, so it is its own trust
// boundary: it validates every domain and only ever writes 0.0.0.0 lines —
// a compromised caller can block sites, never redirect them.
const helperScript = `#!/bin/sh
# understory-block: add/remove tagged /etc/hosts entries.
# Installed root-owned by 'understory -setup-block'; runs via sudo NOPASSWD.
tag='# understory-block'
clean() { /usr/bin/sed -i '' "/$tag\$/d" /etc/hosts; }
flush() { /usr/bin/dscacheutil -flushcache; /usr/bin/killall -HUP mDNSResponder; }
case "$1" in
block)
	shift
	clean
	for d in "$@"; do
		case "$d" in ''|*[!a-z0-9.-]*) continue ;; esac
		printf '0.0.0.0 %s %s\n' "$d" "$tag" >>/etc/hosts
	done
	flush ;;
unblock)
	clean
	flush ;;
*)
	echo 'usage: understory-block block domain... | unblock' >&2
	exit 2 ;;
esac
`

// domains also validated app-side: allow only hostname characters.
var domainRe = regexp.MustCompile(`^[a-z0-9.-]+$`)

// blockHosts expands the blocklist into hostnames to block, covering both
// the bare and www. form of each valid domain.
func blockHosts(domains []string) []string {
	var hosts []string
	seen := map[string]bool{}
	for _, d := range domains {
		d = strings.ToLower(strings.TrimSpace(d))
		if d == "" || !domainRe.MatchString(d) {
			continue
		}
		bare := strings.TrimPrefix(d, "www.")
		for _, h := range []string{bare, "www." + bare} {
			if !seen[h] {
				seen[h] = true
				hosts = append(hosts, h)
			}
		}
	}
	return hosts
}

// runHelper invokes the root helper without prompting; the error carries
// stderr (e.g. sudo's "a password is required" when setup is missing).
func runHelper(args ...string) error {
	out, err := exec.Command("sudo", append([]string{"-n", blockHelper}, args...)...).CombinedOutput()
	if err == nil {
		return nil
	}
	if msg := strings.TrimSpace(string(out)); msg != "" {
		return fmt.Errorf("%s", msg)
	}
	return err
}

// startBlock arms the hosts block when a focus starts running. Synchronous:
// sudo -n never prompts, the whole call is a few hosts-file edits and a DNS
// flush.
func (m *model) startBlock() {
	if m.blocked || len(m.blocklist) == 0 {
		return
	}
	if runtime.GOOS != "darwin" {
		m.blockErr = "site blocking needs macOS"
		return
	}
	hosts := blockHosts(m.blocklist)
	if len(hosts) == 0 {
		m.blockErr = "no valid domains in blocklist"
		return
	}
	if _, err := os.Stat(blockHelper); err != nil {
		m.blockErr = "run `understory -setup-block` once to enable blocking"
		return
	}
	if err := runHelper(append([]string{"block"}, hosts...)...); err != nil {
		if strings.Contains(err.Error(), "password is required") {
			m.blockErr = "run `understory -setup-block` once to enable blocking"
		} else {
			m.blockErr = err.Error()
		}
		return
	}
	m.blocked = true
	m.blockErr = ""
}

// stopBlock removes the hosts entries and flushes DNS.
func (m *model) stopBlock() {
	if !m.blocked {
		return
	}
	m.blocked = false
	if err := runHelper("unblock"); err != nil {
		m.blockErr = err.Error()
	}
}

// unblockStale clears crash leftovers at startup: entries in /etc/hosts with
// no running focus behind them.
func unblockStale() {
	if data, err := os.ReadFile("/etc/hosts"); err == nil && strings.Contains(string(data), blockTag) {
		_ = runHelper("unblock") // best effort; startBlock refreshes entries anyway
	}
}

// setupBlock installs the helper and its sudoers rule; the one interactive
// sudo this feature ever needs. The helper must be root-owned and the rule
// visudo-checked before landing in /etc/sudoers.d.
func setupBlock() error {
	u, err := user.Current()
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "understory-setup")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	helper := filepath.Join(dir, "helper")
	sudoers := filepath.Join(dir, "sudoers")
	if err := os.WriteFile(helper, []byte(helperScript), 0o755); err != nil {
		return err
	}
	rule := fmt.Sprintf("%s ALL=(root) NOPASSWD: %s\n", u.Username, blockHelper)
	if err := os.WriteFile(sudoers, []byte(rule), 0o644); err != nil {
		return err
	}
	sh := fmt.Sprintf(
		"/usr/bin/install -d -o root -g wheel /usr/local/libexec && "+
			"/usr/bin/install -o root -g wheel -m 755 %s %s && "+
			"/usr/sbin/visudo -cf %s && "+
			"/usr/bin/install -o root -g wheel -m 440 %s /etc/sudoers.d/understory",
		helper, blockHelper, sudoers, sudoers)
	cmd := exec.Command("sudo", "sh", "-c", sh)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// blockStatus renders the one-line blocker state below the key hints.
func (m model) blockStatus() string {
	switch {
	case m.blockErr != "":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#e06c75")).
			Render("block: " + m.blockErr)
	case m.blocked:
		return lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted)).
			Render("blocking sites")
	default:
		return ""
	}
}
