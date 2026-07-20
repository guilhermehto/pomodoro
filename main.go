package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ── phases ────────────────────────────────────────────────────────────────

type phase int

const (
	focus phase = iota
	shortBreak
	longBreak
)

const focusesPerLongBreak = 4

func (p phase) name() string {
	switch p {
	case focus:
		return "FOCUS"
	case shortBreak:
		return "SHORT BREAK"
	default:
		return "LONG BREAK"
	}
}

// base and bright colors used for the pulsing fill, per phase. Neutral palette —
// "pomodoro" is just the technique's name, no tomato theme.
func (p phase) colors() (base, bright string) {
	switch p {
	case focus:
		return "#7aa2f7", "#b4cbff" // blue
	case shortBreak:
		return "#9ece6a", "#c7ee9c" // green
	default:
		return "#bb9af7", "#d8c4ff" // purple
	}
}

const (
	colEmpty = "#2a2b36"
	colMuted = "#7f8497"
	colTitle = "#565a6e"
)

// ── model ─────────────────────────────────────────────────────────────────

type status int

const (
	idle      status = iota // full timer, not started
	running                 // counting down
	paused                  // started, held
	completed               // reached zero, awaiting ack
)

type stats struct {
	Total int    `json:"total"`
	Today int    `json:"today"`
	Date  string `json:"date"` // YYYY-MM-DD
}

type model struct {
	width, height int

	ph        phase
	status    status
	remaining time.Duration
	lastTick  time.Time
	frame     int
	cycle     int // focus sessions completed (drives long-break cadence)

	durations [3]time.Duration
	stats     stats

	// audio visualizer
	audioFlag string // -audio device override (index or name substring)
	audioDev  string // resolved avfoundation index
	audioName string // resolved device name (for display)
	resolved  bool   // device lookup done (lazy, on first 'v')
	capturing bool
	levels    []float64 // smoothed spectrum band levels, 0..1
	bands     chan audioFrame
	cancel    context.CancelFunc
	audioErr  string
}

type tickMsg time.Time

type (
	bandsMsg          []float64
	audioErrMsg       string
	captureStoppedMsg struct{}
)

// waitForBands blocks on the capture channel and turns each frame into a Msg.
func waitForBands(ch chan audioFrame) tea.Cmd {
	return func() tea.Msg {
		f, ok := <-ch
		if !ok {
			return captureStoppedMsg{}
		}
		if f.err != nil {
			return audioErrMsg(f.err.Error())
		}
		return bandsMsg(f.levels)
	}
}

func (m *model) stopCapture() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.capturing = false
}

// toggleAudio starts or stops the visualizer, resolving the device lazily so
// startup stays instant and no mic-permission prompt fires until asked.
func (m *model) toggleAudio() tea.Cmd {
	if m.capturing {
		m.stopCapture()
		return nil
	}
	if !m.resolved {
		m.audioDev, m.audioName, _ = resolveDevice(m.audioFlag)
		m.resolved = true
	}
	if m.audioDev == "" {
		m.audioErr = "no audio input device found"
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan audioFrame)
	m.cancel, m.bands, m.capturing, m.audioErr = cancel, ch, true, ""
	go captureAudio(ctx, m.audioDev, ch)
	return waitForBands(ch)
}

func tick() tea.Cmd {
	return tea.Tick(time.Second/10, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m model) Init() tea.Cmd { return tick() }

func (m model) dur(p phase) time.Duration { return m.durations[p] }

// next returns the phase that follows the current one.
func (m model) next() phase {
	if m.ph != focus {
		return focus
	}
	if m.cycle%focusesPerLongBreak == 0 {
		return longBreak
	}
	return shortBreak
}

func (m *model) startPhase(p phase) {
	m.ph = p
	m.remaining = m.dur(p)
	m.status = running
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			m.stopCapture()
			m.save()
			return m, tea.Quit
		case " ", "enter":
			switch m.status {
			case idle, paused:
				m.status = running
			case running:
				m.status = paused
			case completed:
				m.startPhase(m.next())
			}
		case "s": // skip current phase
			m.ph = m.next()
			m.remaining = m.dur(m.ph)
			m.status = idle
		case "r": // reset current phase
			m.remaining = m.dur(m.ph)
			m.status = idle
		case "v": // toggle audio visualizer
			return m, m.toggleAudio()
		}

	case tickMsg:
		t := time.Time(msg)
		m.frame++
		if m.status == running {
			if !m.lastTick.IsZero() {
				m.remaining -= t.Sub(m.lastTick)
			}
			if m.remaining <= 0 {
				m.remaining = 0
				m.complete()
			}
		}
		m.lastTick = t
		// let spectrum bars fall when audio is quiet between frames
		if m.capturing {
			for i := range m.levels {
				m.levels[i] *= 0.9
			}
		}
		return m, tick()

	case bandsMsg:
		if len(m.levels) != len(msg) {
			m.levels = make([]float64, len(msg))
		}
		for i, v := range msg { // fast attack, slow release
			if v > m.levels[i] {
				m.levels[i] = v
			} else {
				m.levels[i] = m.levels[i]*0.82 + v*0.18
			}
		}
		if m.capturing {
			return m, waitForBands(m.bands)
		}

	case audioErrMsg:
		m.audioErr = string(msg)
		m.capturing = false

	case captureStoppedMsg:
		m.capturing = false
	}
	return m, nil
}

// complete records a finished phase and parks in the completed state.
func (m *model) complete() {
	if m.ph == focus {
		m.cycle++
		m.rollDate()
		m.stats.Total++
		m.stats.Today++
		m.save()
	}
	m.status = completed
}

// ── rendering ───────────────────────────────────────────────────────────────

func (m model) View() string {
	if m.width == 0 {
		return ""
	}
	base, bright := m.ph.colors()
	// gentle breathing glow while running; steady otherwise.
	breath := 0.0
	if m.status == running {
		breath = (math.Sin(float64(m.frame)*0.12) + 1) / 2 // ~5s period
	}
	// accent flashes faster the moment a phase completes.
	accentT := breath
	if m.status == completed {
		accentT = (math.Sin(float64(m.frame)*0.4) + 1) / 2
	}
	accent := lipgloss.Color(hexLerp(base, bright, accentT))

	muted := lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted))
	title := lipgloss.NewStyle().Foreground(lipgloss.Color(colTitle)).Bold(true).
		Render("P O M O D O R O")

	var label string
	labelStyle := lipgloss.NewStyle().Foreground(accent).Bold(true)
	if m.status == completed {
		label = labelStyle.Render(m.ph.name() + " COMPLETE")
	} else {
		label = labelStyle.Render(m.ph.name())
	}

	// big ASCII clock; colon blinks each second while running.
	colonOn := m.status != running || int(m.remaining.Seconds())%2 == 0
	clockColor := lipgloss.Color(hexLerp(base, bright, breath))
	clock := renderBigTime(fmtDuration(m.remaining), clockColor, colonOn)

	barWidth := clamp(m.width/2, 24, 80)
	bar := m.renderBar(barWidth, base, bright)

	done := m.cycle % focusesPerLongBreak
	if done == 0 && m.cycle > 0 {
		done = focusesPerLongBreak
	}
	dots := renderDots(done, focusesPerLongBreak, base)

	statLine := muted.Render(fmt.Sprintf("%d today   ·   %d total", m.stats.Today, m.stats.Total))

	var hint string
	switch m.status {
	case idle:
		hint = "space  start"
	case running:
		hint = "space  pause"
	case paused:
		hint = "space  resume"
	case completed:
		hint = "space  next"
	}
	keys := muted.Render(hint + "   ·   s skip   ·   r reset   ·   v viz   ·   q quit")

	parts := []string{title, "", label, "", clock, "", bar}

	specRows := clamp(m.height-20, 0, 8)
	if m.capturing && len(m.levels) > 0 && specRows >= 3 {
		cols := clamp(m.width-8, 16, 140)
		parts = append(parts, "", renderSpectrum(m.levels, cols, specRows))
	}

	parts = append(parts, "", dots, "", statLine, "", keys, m.audioStatus())

	body := lipgloss.JoinVertical(lipgloss.Center, parts...)

	// Fill the terminal: content centered inside a full-window border frame.
	inner := lipgloss.Place(m.width-2, m.height-2, lipgloss.Center, lipgloss.Center, body)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accent).
		Render(inner)
}

// audioStatus renders the one-line visualizer state below the key hints.
func (m model) audioStatus() string {
	switch {
	case m.audioErr != "":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#e06c75")).
			Render("audio: " + m.audioErr)
	case m.capturing:
		return lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted)).
			Render("♪ listening · " + m.audioName)
	default:
		return ""
	}
}

// renderBar draws the elapsed-progress bar with a bright head that sweeps the
// filled region while running (subtle "it's alive" motion).
func (m model) renderBar(width int, base, bright string) string {
	total := m.dur(m.ph)
	elapsed := 0.0
	if total > 0 {
		elapsed = 1 - float64(m.remaining)/float64(total)
	}
	elapsed = math.Max(0, math.Min(1, elapsed))
	filled := int(elapsed*float64(width) + 0.5)

	head := -1
	if m.status == running && filled > 0 {
		head = m.frame % filled // travels across the filled portion
	}
	baseStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(base))
	headStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(bright))

	var b strings.Builder
	for i := 0; i < filled; i++ {
		if i == head {
			b.WriteString(headStyle.Render("█"))
		} else {
			b.WriteString(baseStyle.Render("█"))
		}
	}
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(colEmpty)).Render(strings.Repeat("░", width-filled)))
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted)).Render(fmt.Sprintf("  %3d%%", int(elapsed*100+0.5))))
	return b.String()
}

// ── big ASCII digits ────────────────────────────────────────────────────────

var bigFont = map[rune][]string{
	'0': {" ███ ", "█   █", "█   █", "█   █", " ███ "},
	'1': {"  █  ", " ██  ", "  █  ", "  █  ", " ███ "},
	'2': {" ███ ", "█   █", "  ██ ", " █   ", "█████"},
	'3': {"████ ", "    █", " ███ ", "    █", "████ "},
	'4': {"█   █", "█   █", "█████", "    █", "    █"},
	'5': {"█████", "█    ", "████ ", "    █", "████ "},
	'6': {" ███ ", "█    ", "████ ", "█   █", " ███ "},
	'7': {"█████", "    █", "   █ ", "  █  ", " █   "},
	'8': {" ███ ", "█   █", " ███ ", "█   █", " ███ "},
	'9': {" ███ ", "█   █", " ████", "    █", " ███ "},
	':': {"     ", "  █  ", "     ", "  █  ", "     "},
	' ': {"     ", "     ", "     ", "     ", "     "},
}

// renderBigTime lays out MM:SS as 5-row block glyphs; a hidden colon (blink)
// still occupies its cell so the layout width never shifts.
func renderBigTime(s string, color lipgloss.Color, colonOn bool) string {
	rows := make([]string, 5)
	for _, ch := range s {
		g, ok := bigFont[ch]
		if !ok || (ch == ':' && !colonOn) {
			g = bigFont[' ']
		}
		for i := 0; i < 5; i++ {
			rows[i] += g[i] + "  "
		}
	}
	return lipgloss.NewStyle().Foreground(color).Render(strings.Join(rows, "\n"))
}

// ── spectrum visualizer ─────────────────────────────────────────────────────

var eighthRunes = []rune{' ', '▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// renderSpectrum draws `cols` vertical bars `rows` tall from the band levels,
// each column a distinct hue (bass → treble) for the classic visualizer look.
// ponytail: per-cell styling is O(cols·rows) ANSI/frame; fine at these sizes,
// switch to run-length coloring if it ever shows up in a profile.
func renderSpectrum(levels []float64, cols, rows int) string {
	n := len(levels)
	colStyle := make([]lipgloss.Style, cols)
	height := make([]float64, cols)
	for c := 0; c < cols; c++ {
		idx := c * n / cols
		if idx >= n {
			idx = n - 1
		}
		height[c] = levels[idx]
		hue := math.Mod(200+140*float64(c)/float64(cols), 360)
		colStyle[c] = lipgloss.NewStyle().Foreground(lipgloss.Color(hsvToHex(hue, 0.65, 1)))
	}

	var b strings.Builder
	for r := 0; r < rows; r++ { // r=0 is the top row
		fromBottom := rows - 1 - r
		for c := 0; c < cols; c++ {
			eighths := int(height[c]*float64(rows)*8 - float64(fromBottom*8))
			eighths = clamp(eighths, 0, 8)
			if eighths == 0 {
				b.WriteByte(' ')
			} else {
				b.WriteString(colStyle[c].Render(string(eighthRunes[eighths])))
			}
		}
		if r < rows-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// hsvToHex converts HSV (h in degrees, s/v in 0..1) to a #rrggbb string.
func hsvToHex(h, s, v float64) string {
	c := v * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := v - c
	var r, g, bl float64
	switch {
	case h < 60:
		r, g, bl = c, x, 0
	case h < 120:
		r, g, bl = x, c, 0
	case h < 180:
		r, g, bl = 0, c, x
	case h < 240:
		r, g, bl = 0, x, c
	case h < 300:
		r, g, bl = x, 0, c
	default:
		r, g, bl = c, 0, x
	}
	return fmt.Sprintf("#%02x%02x%02x", int((r+m)*255+0.5), int((g+m)*255+0.5), int((bl+m)*255+0.5))
}

func renderDots(done, total int, color string) string {
	on := lipgloss.NewStyle().Foreground(lipgloss.Color(color))
	off := lipgloss.NewStyle().Foreground(lipgloss.Color("#3a3a3a"))
	var b strings.Builder
	for i := 0; i < total; i++ {
		if i < done {
			b.WriteString(on.Render("● "))
		} else {
			b.WriteString(off.Render("○ "))
		}
	}
	return strings.TrimRight(b.String(), " ")
}

func fmtDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d.Round(time.Second).Seconds())
	return fmt.Sprintf("%02d:%02d", total/60, total%60)
}

// hexLerp linearly interpolates between two #rrggbb colors.
func hexLerp(a, b string, t float64) string {
	ar, ag, ab := hexParse(a)
	br, bg, bb := hexParse(b)
	l := func(x, y int) int { return int(float64(x) + (float64(y)-float64(x))*t + 0.5) }
	return fmt.Sprintf("#%02x%02x%02x", l(ar, br), l(ag, bg), l(ab, bb))
}

func hexParse(s string) (r, g, b int) {
	s = strings.TrimPrefix(s, "#")
	r64, _ := strconv.ParseInt(s[0:2], 16, 0)
	g64, _ := strconv.ParseInt(s[2:4], 16, 0)
	b64, _ := strconv.ParseInt(s[4:6], 16, 0)
	return int(r64), int(g64), int(b64)
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// ── persistence ─────────────────────────────────────────────────────────────

func statePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".pomodoro.json"
	}
	return filepath.Join(home, ".pomodoro.json")
}

func today() string { return time.Now().Format("2006-01-02") }

// rollDate resets the daily counter when the calendar day changes.
func (m *model) rollDate() {
	if m.stats.Date != today() {
		m.stats.Date = today()
		m.stats.Today = 0
	}
}

func loadStats() stats {
	s := stats{Date: today()}
	data, err := os.ReadFile(statePath())
	if err != nil {
		return s
	}
	_ = json.Unmarshal(data, &s)
	if s.Date != today() { // stale day → keep total, reset today
		s.Date = today()
		s.Today = 0
	}
	return s
}

func (m model) save() {
	data, err := json.MarshalIndent(m.stats, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(statePath(), data, 0o644)
}

// ── main ──────────────────────────────────────────────────────────────────

func main() {
	// ponytail: flags exist mainly so you can test with short timers; classic defaults.
	work := flag.Int("work", 25, "focus minutes")
	short := flag.Int("short", 5, "short break minutes")
	long := flag.Int("long", 15, "long break minutes")
	audio := flag.String("audio", "", "visualizer input device (index or name substring; default: BlackHole, else mic)")
	flag.Parse()

	m := model{
		ph:        focus,
		status:    idle,
		durations: [3]time.Duration{minutes(*work), minutes(*short), minutes(*long)},
		stats:     loadStats(),
		audioFlag: *audio,
	}
	m.remaining = m.dur(focus)

	if _, err := tea.NewProgram(&m, tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func minutes(n int) time.Duration { return time.Duration(n) * time.Minute }
