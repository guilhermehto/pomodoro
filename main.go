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
	Date  string `json:"date"`           // YYYY-MM-DD
	Week  [6]int `json:"week,omitempty"` // prior 6 days of sessions, oldest first
}

// taskLink is the local pomodoro↔task association (never written back to
// Taskwarrior — a task can outlive its pending state, so we cache Description).
type taskLink struct {
	Description string `json:"description"`
	Pomodoros   int    `json:"pomodoros"`
	LastWorked  string `json:"last_worked"` // YYYY-MM-DD
}

type viewMode int

const (
	viewTimer viewMode = iota
	viewTasks
	viewHeat
)

type inputKind int

const (
	inputAdd inputKind = iota
	inputEdit
)

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

	// taskwarrior
	view       viewMode
	tasks      []twTask
	taskCursor int
	curUUID    string // current task being worked on
	curDesc    string
	doneBlock  map[string]string   // uuid→desc marked done during this focus block
	links      map[string]taskLink // local pomodoro credits, persisted
	taskErr    string
	input      bool
	inputKind  inputKind
	inputBuf   string
	inputUUID  string // task being edited

	// audio visualizer
	capturing bool
	resumeViz bool      // visualizer was on at last quit; Init restarts it
	permOK    bool      // real audio frames seen once - the TCC prompt was accepted
	levels    []float64 // smoothed spectrum band levels, 0..1
	bands     chan audioFrame
	cancel    context.CancelFunc
	audioErr  string
	// focus site blocking (macOS, /etc/hosts)
	blocklist []string
	blocked   bool
	blockErr  string
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

// toggleAudio starts or stops the visualizer. First use compiles the capture
// helper and fires the System Audio Recording permission prompt, so nothing
// audio-related happens until 'v' is pressed.
func (m *model) toggleAudio() tea.Cmd {
	if m.capturing {
		m.stopCapture()
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan audioFrame)
	m.cancel, m.bands, m.capturing, m.audioErr = cancel, ch, true, ""
	go captureAudio(ctx, ch)
	return waitForBands(ch)
}

func tick() tea.Cmd {
	return tea.Tick(time.Second/10, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *model) Init() tea.Cmd {
	if m.resumeViz {
		m.resumeViz = false
		return tea.Batch(tick(), m.toggleAudio())
	}
	return tick()
}

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

// ── taskwarrior ─────────────────────────────────────────────────────────────

func (m *model) reloadTasks() {
	ts, err := listTasks()
	if err != nil {
		m.taskErr = err.Error()
		return
	}
	m.taskErr = ""
	m.tasks = ts
	m.taskCursor = clamp(m.taskCursor, 0, max(0, len(ts)-1))
}

// setCurrent switches the actively-tracked task, mirroring the change into
// Taskwarrior so its active timer reflects the selection: stop whatever TW
// currently has running (except the pick) and start the pick if it isn't
// already. Reading TW's real active set heals drift from a prior quit that left
// a different task active.
func (m *model) setCurrent(uuid, desc string) {
	active, err := activeTasks()
	if err != nil {
		m.taskErr = err.Error()
		return
	}
	stop, start := taskReconcile(active, uuid)
	for _, u := range stop {
		if err := taskStop(u); err != nil {
			m.taskErr = err.Error()
		}
	}
	if start {
		if err := taskStart(uuid); err != nil {
			m.taskErr = err.Error()
			return
		}
	}
	m.curUUID, m.curDesc = uuid, desc
}

func (m *model) selectedTask() *twTask {
	if m.taskCursor >= 0 && m.taskCursor < len(m.tasks) {
		return &m.tasks[m.taskCursor]
	}
	return nil
}

func (m *model) handleTaskKey(msg tea.KeyMsg) {
	switch msg.String() {
	case "q", "esc", "t":
		m.view = viewTimer
	case "up", "k":
		if m.taskCursor > 0 {
			m.taskCursor--
		}
	case "down", "j":
		if m.taskCursor < len(m.tasks)-1 {
			m.taskCursor++
		}
	case "enter": // set as current task
		if t := m.selectedTask(); t != nil {
			m.setCurrent(t.UUID, t.Description)
		}
	case "d": // mark done
		if t := m.selectedTask(); t != nil {
			if err := taskDone(t.UUID); err != nil {
				m.taskErr = err.Error()
				return
			}
			if m.ph == focus {
				m.doneBlock[t.UUID] = t.Description
			}
			if m.curUUID == t.UUID {
				m.curUUID, m.curDesc = "", ""
			}
			m.reloadTasks()
		}
	case "a": // add
		m.input, m.inputKind, m.inputBuf = true, inputAdd, ""
	case "e": // edit description/project/tags
		if t := m.selectedTask(); t != nil {
			m.input, m.inputKind, m.inputBuf, m.inputUUID = true, inputEdit, t.Description, t.UUID
		}
	}
}

func (m *model) handleInputKey(msg tea.KeyMsg) {
	switch msg.String() {
	case "esc":
		m.input, m.inputBuf = false, ""
	case "enter":
		if text := strings.TrimSpace(m.inputBuf); text != "" {
			var err error
			if m.inputKind == inputAdd {
				err = taskAdd(text)
			} else {
				err = taskModify(m.inputUUID, text)
			}
			if err != nil {
				m.taskErr = err.Error()
			} else {
				m.reloadTasks()
			}
		}
		m.input, m.inputBuf = false, ""
	case "backspace":
		if r := []rune(m.inputBuf); len(r) > 0 {
			m.inputBuf = string(r[:len(r)-1])
		}
	default:
		if len(msg.Runes) > 0 { // printable runes (incl. space)
			m.inputBuf += string(msg.Runes)
		}
	}
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			m.save() // before stopCapture: save records the live visualizer state
			m.stopCapture()
			m.stopBlock()
			return m, tea.Quit
		}
		if m.input {
			m.handleInputKey(msg)
			return m, nil
		}
		if m.view == viewTasks {
			m.handleTaskKey(msg)
			return m, nil
		}
		if m.view == viewHeat {
			switch msg.String() {
			case "g", "q", "esc":
				m.view = viewTimer
			}
			return m, nil
		}
		switch msg.String() {
		case "q", "esc":
			m.save()
			m.stopCapture()
			m.stopBlock()
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
			if m.ph == focus && m.status == running {
				m.startBlock()
			}
		case "s": // skip current phase
			m.ph = m.next()
			m.remaining = m.dur(m.ph)
			m.status = idle
			m.stopBlock()
		case "r": // reset current phase
			m.remaining = m.dur(m.ph)
			m.status = idle
			m.stopBlock()
		case "t": // toggle task view
			m.view = viewTasks
			m.reloadTasks()
		case "g": // weekly activity heatmap
			m.view = viewHeat
		case "v": // toggle audio visualizer
			return m, m.toggleAudio()
		}

	case tickMsg:
		t := time.Time(msg)
		m.frame++
		m.stats.roll() // live midnight rollover: today slides into the week
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
		if !m.permOK {
			for _, v := range msg {
				if v > 0 {
					m.permOK = true
					break
				}
			}
		}
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
		m.stopBlock()
		m.cycle++
		m.stats.roll()
		m.stats.Total++
		m.stats.Today++
		m.creditTasks()
		m.save()
	}
	m.status = completed
}

// creditTasks awards one pomodoro to the current task plus every task marked
// done during this focus block, then clears the block set.
// ponytail: a focus abandoned via skip/reset carries its done set into the next
// completed focus. Rare; add a clear-on-skip if it ever mis-credits in practice.
func (m *model) creditTasks() {
	credited := map[string]string{}
	if m.curUUID != "" {
		credited[m.curUUID] = m.curDesc
	}
	for uuid, desc := range m.doneBlock {
		credited[uuid] = desc
	}
	for uuid, desc := range credited {
		l := m.links[uuid]
		l.Description = desc
		l.Pomodoros++
		l.LastWorked = today()
		m.links[uuid] = l
	}
	m.doneBlock = map[string]string{}
}

// ── rendering ───────────────────────────────────────────────────────────────

func (m model) View() string {
	if m.width == 0 {
		return ""
	}
	if m.view == viewTasks {
		return m.taskView()
	}
	if m.view == viewHeat {
		return m.heatView()
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
		Render("U N D E R S T O R Y")

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
	keys := muted.Render(hint + "   ·   s skip   ·   r reset   ·   t tasks   ·   g activity   ·   v viz   ·   q quit")

	quote := lipgloss.NewStyle().Foreground(lipgloss.Color(colTitle)).Italic(true).
		Render(quoteFor(time.Now()))
	parts := []string{title, quote, "", label, "", clock}
	if m.curDesc != "" {
		parts = append(parts, "", muted.Render("▸ "+m.curDesc))
	}
	parts = append(parts, "", bar)

	specRows := clamp(m.height-20, 0, 8)
	if m.capturing && len(m.levels) > 0 && specRows >= 3 {
		cols := clamp(m.width-8, 16, 140)
		parts = append(parts, "", renderSpectrum(m.levels, cols, specRows))
	}

	parts = append(parts, "", dots, "", statLine, "", keys, m.audioStatus())
	if bs := m.blockStatus(); bs != "" {
		parts = append(parts, bs)
	}

	body := lipgloss.JoinVertical(lipgloss.Center, parts...)

	// Fill the terminal: content centered inside a full-window border frame.
	inner := lipgloss.Place(m.width-2, m.height-2, lipgloss.Center, lipgloss.Center, body)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accent).
		Render(inner)
}

// audioStatus renders visualizer problems below the key hints; healthy
// capture shows nothing (the bars speak for themselves).
func (m model) audioStatus() string {
	switch {
	case m.audioErr != "":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#e06c75")).
			Render("audio: " + m.audioErr)
	case m.capturing && !m.permOK:
		return lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted)).
			Render("♪ no signal - denied the permission? System Settings -> Privacy & Security -> Screen & System Audio Recording")
	default:
		return ""
	}
}

// taskView renders the pending-task picker (opened with `t`). The timer keeps
// ticking underneath; only the rendering swaps.
func (m model) taskView() string {
	title := lipgloss.NewStyle().Foreground(lipgloss.Color(colTitle)).Bold(true).Render("T A S K S")
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted))
	sel := lipgloss.NewStyle().Foreground(lipgloss.Color("#b4cbff")).Bold(true)

	var rows []string
	if len(m.tasks) == 0 {
		rows = append(rows, dim.Render("no pending tasks"))
	}

	// window the list around the cursor so long lists never overflow the frame.
	visible := clamp(m.height-10, 3, max(3, len(m.tasks)))
	start := 0
	if m.taskCursor >= visible {
		start = m.taskCursor - visible + 1
	}
	end := min(len(m.tasks), start+visible)
	for i := start; i < end; i++ {
		t := m.tasks[i]
		cursor := "  "
		if i == m.taskCursor {
			cursor = "▸ "
		}
		mark := "  "
		if t.UUID == m.curUUID {
			mark = "● "
		}
		meta := ""
		if t.Project != "" {
			meta += "  " + t.Project
		}
		if len(t.Tags) > 0 {
			meta += "  +" + strings.Join(t.Tags, " +")
		}
		if l, ok := m.links[t.UUID]; ok && l.Pomodoros > 0 {
			meta += fmt.Sprintf("  ●×%d", l.Pomodoros)
		}
		style := dim
		if i == m.taskCursor {
			style = sel
		}
		rows = append(rows, style.Render(cursor+mark+t.Description)+dim.Render(meta))
	}

	parts := []string{title, "", lipgloss.JoinVertical(lipgloss.Left, rows...)}
	if m.taskErr != "" {
		parts = append(parts, "", lipgloss.NewStyle().Foreground(lipgloss.Color("#e06c75")).Render("task: "+m.taskErr))
	}
	if m.input {
		prompt := "add › "
		if m.inputKind == inputEdit {
			prompt = "edit › "
		}
		parts = append(parts, "", sel.Render(prompt+m.inputBuf+"▌"))
	} else {
		parts = append(parts, "", dim.Render("↑/↓ move · enter select · d done · a add · e edit · t/esc back"))
	}

	body := lipgloss.JoinVertical(lipgloss.Left, parts...)
	inner := lipgloss.Place(m.width-2, m.height-2, lipgloss.Center, lipgloss.Center, body)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(colTitle)).
		Render(inner)
}

// week is the last 7 days of completed sessions, today last.
func (m model) week() [7]int {
	var w [7]int
	copy(w[:6], m.stats.Week[:])
	w[6] = m.stats.Today
	return w
}

// heatView renders the weekly activity heatmap (opened with `g`). The timer
// keeps ticking underneath; only the rendering swaps.
func (m model) heatView() string {
	title := lipgloss.NewStyle().Foreground(lipgloss.Color(colTitle)).Bold(true).Render("A C T I V I T Y")
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted))
	body := lipgloss.JoinVertical(lipgloss.Center,
		title, "", renderHeatmap(m.week(), m.frame), "",
		dim.Render("g/esc back"))
	inner := lipgloss.Place(m.width-2, m.height-2, lipgloss.Center, lipgloss.Center, body)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(colTitle)).
		Render(inner)
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
		return ".understory.json"
	}
	return filepath.Join(home, ".understory.json")
}

func today() string { return time.Now().Format("2006-01-02") }

// roll shifts the daily counter into the week history when the calendar day
// changes, inserting zero days for any gap; unparseable or week-long gaps
// clear the history. Total is untouched.
func (s *stats) roll() {
	if s.Date == today() {
		return
	}
	gap := 7
	if old, err := time.Parse("2006-01-02", s.Date); err == nil {
		now, _ := time.Parse("2006-01-02", today())
		if d := int(now.Sub(old).Hours() / 24); d >= 1 && d < 7 {
			gap = d
		}
	}
	for range gap {
		copy(s.Week[:5], s.Week[1:])
		s.Week[5] = s.Today
		s.Today = 0
	}
	s.Date = today()
}

// persisted is the on-disk shape: stats, local task links, any in-flight timer
// session, and app state (current task, visualizer) restored on next launch.
type persisted struct {
	Stats       stats               `json:"stats"`
	Links       map[string]taskLink `json:"links"`
	Blocklist   []string            `json:"blocklist,omitempty"`
	Session     *session            `json:"session,omitempty"`
	CurrentUUID string              `json:"current_uuid,omitempty"`
	CurrentDesc string              `json:"current_desc,omitempty"`
	Visualizer  bool                `json:"visualizer,omitempty"`
	AudioPerm   bool                `json:"audio_perm,omitempty"`
}

// session snapshots an in-flight timer. Quit acts as pause: the clock doesn't
// run while the app is closed, so remaining is the whole state - no timestamps.
type session struct {
	Phase        phase `json:"phase"`
	RemainingSec int   `json:"remaining_sec"`
	Cycle        int   `json:"cycle"`
}

func load() persisted {
	data, err := os.ReadFile(statePath())
	if err != nil { // migrate: state file from before the understory rename
		if home, herr := os.UserHomeDir(); herr == nil {
			data, err = os.ReadFile(filepath.Join(home, ".pomodoro.json"))
		}
	}
	if err != nil {
		return persisted{Stats: stats{Date: today()}, Links: map[string]taskLink{}}
	}
	return decodeState(data)
}

// decodeState parses the state file, transparently migrating the old
// stats-only format (bare {total,today,date}) to the current nested shape.
func decodeState(data []byte) persisted {
	var p persisted
	if json.Unmarshal(data, &p) != nil || (p.Stats == (stats{}) && len(p.Links) == 0) {
		p = persisted{} // old format: bare stats object
		_ = json.Unmarshal(data, &p.Stats)
	}
	if p.Links == nil {
		p.Links = map[string]taskLink{}
	}
	p.Stats.roll() // stale day → today slides into the week, total kept
	return p
}

func (m model) save() {
	p := persisted{
		Stats: m.stats, Links: m.links, Blocklist: m.blocklist,
		CurrentUUID: m.curUUID, CurrentDesc: m.curDesc,
		Visualizer: m.capturing, AudioPerm: m.permOK,
	}
	if sec := int(m.remaining.Round(time.Second) / time.Second); sec > 0 && (m.status == running || m.status == paused) {
		p.Session = &session{Phase: m.ph, RemainingSec: sec, Cycle: m.cycle}
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(statePath(), data, 0o644)
}

// restore reapplies saved app state: current task, visualizer, and any
// in-flight session resumed as paused (quit == pause). Bad or oversized
// session values (e.g. durations shrunk via flags) are clamped or ignored.
func (m *model) restore(p persisted) {
	m.curUUID, m.curDesc = p.CurrentUUID, p.CurrentDesc
	m.resumeViz = p.Visualizer
	m.permOK = p.AudioPerm
	s := p.Session
	if s == nil || s.Phase < focus || s.Phase > longBreak || s.RemainingSec <= 0 {
		return
	}
	m.ph = s.Phase
	m.cycle = max(s.Cycle, 0)
	m.remaining = min(time.Duration(s.RemainingSec)*time.Second, m.dur(s.Phase))
	m.status = paused
}

// ── main ──────────────────────────────────────────────────────────────────

func main() {
	// ponytail: flags exist mainly so you can test with short timers; classic defaults.
	work := flag.Int("work", 25, "focus minutes")
	short := flag.Int("short", 5, "short break minutes")
	long := flag.Int("long", 15, "long break minutes")
	block := flag.String("block", "", "comma-separated sites to block during focus (persisted; -block '' clears)")
	setup := flag.Bool("setup-block", false, "install the passwordless hosts-block helper (asks for sudo once)")
	flag.Parse()

	if *setup {
		if err := setupBlock(); err != nil {
			fmt.Fprintln(os.Stderr, "setup failed:", err)
			os.Exit(1)
		}
		fmt.Println("hosts-block helper installed; focus blocking is now prompt-free")
		return
	}

	p := load()
	m := model{
		ph:        focus,
		status:    idle,
		durations: [3]time.Duration{minutes(*work), minutes(*short), minutes(*long)},
		stats:     p.Stats,
		links:     p.Links,
		doneBlock: map[string]string{},
	}
	m.remaining = m.dur(focus)
	m.restore(p)
	m.blocklist = p.Blocklist
	flag.Visit(func(f *flag.Flag) {
		if f.Name != "block" {
			return
		}
		m.blocklist = nil
		for _, d := range strings.Split(*block, ",") {
			if d = strings.TrimSpace(d); d != "" {
				m.blocklist = append(m.blocklist, d)
			}
		}
	})
	unblockStale() // crash leftovers: tagged /etc/hosts entries with no focus behind them

	if _, err := tea.NewProgram(&m, tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func minutes(n int) time.Duration { return time.Duration(n) * time.Minute }
