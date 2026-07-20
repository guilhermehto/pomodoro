package main

// Weekly activity heatmap: a contribution graph of completed focus sessions,
// lifted from the cmd/gardenproto prototype and recolored to the app palette.
// One column per day (today last, labeled by real weekday), one cell per two
// sessions filling bottom-up with a half-block for odd counts, so every
// session moves the column and 14 sessions — an achievable heavy day — tops
// it out exactly. A pure display of stats: no randomness.

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

const (
	heatW      = 60
	heatH      = 10
	heatRows   = 7  // cell rows per day column
	heatDayMax = 14 // achievable daily ceiling: 2 sessions per cell
	heatGoal   = 4  // faint target tick at this session count
)

// Intensity ramp derived from the focus palette, darkest to brightest, so
// the graph reads as focus accumulating; index 0 is the empty cell.
var heatLevels = func() []string {
	base, bright := focus.colors()
	return []string{
		colEmpty,
		hexLerp(colEmpty, base, 0.4),
		hexLerp(colEmpty, base, 0.7),
		base,
		bright,
	}
}()

type heatCell struct {
	r     rune
	color string
	bold  bool
}

type heatGrid [][]heatCell

func newHeatGrid() heatGrid {
	g := make(heatGrid, heatH)
	for y := range g {
		g[y] = make([]heatCell, heatW)
	}
	return g
}

func (g heatGrid) set(x, y int, r rune, color string, bold bool) {
	if x < 0 || x >= heatW || y < 0 || y >= heatH {
		return
	}
	g[y][x] = heatCell{r, color, bold}
}

func (g heatGrid) text(x, y int, s, color string, bold bool) {
	for i, r := range []rune(s) {
		g.set(x+i, y, r, color, bold)
	}
}

// ponytail: per-cell lipgloss styling is O(w·h) ANSI per frame, same tradeoff
// as renderSpectrum; fine at 60×10.
func (g heatGrid) render() string {
	var b strings.Builder
	for y := range g {
		if y > 0 {
			b.WriteByte('\n')
		}
		for x := range g[y] {
			c := g[y][x]
			if c.r == 0 {
				b.WriteByte(' ')
				continue
			}
			st := lipgloss.NewStyle().Foreground(lipgloss.Color(c.color))
			if c.bold {
				st = st.Bold(true)
			}
			b.WriteString(st.Render(string(c.r)))
		}
	}
	return b.String()
}

// renderHeatmap draws the past week of sessions (today last). Cells brighten
// as a column climbs; a faint tick marks the daily goal until fill swallows
// it. Today's next cell pulses with the frame (a half cell previews its
// completion); 15+ sessions crown the column.
func renderHeatmap(week [7]int, frame int) string {
	g := newHeatGrid()
	_, bright := focus.colors()
	now := time.Now()
	for d := range 7 {
		x := 4 + d*8
		label := strings.ToLower(now.AddDate(0, 0, d-6).Weekday().String()[:3])
		labelCol, bold := colTitle, false
		if d == 6 {
			labelCol, bold = colMuted, true
		}
		g.text(x-1, 0, label, labelCol, bold)
		n := week[d]
		full, half := n/2, n%2
		for c := range heatRows {
			y := heatH - 2 - c // rows 8..2, bottom-up
			lvl := heatLevels[1+c/2]
			switch {
			case c < full:
				g.set(x, y, '■', lvl, false)
			case c == full && half == 1:
				g.set(x, y, '▄', lvl, false)
			case c == heatGoal/2-1:
				g.set(x, y, '-', colTitle, false)
			default:
				g.set(x, y, '·', colEmpty, false)
			}
		}
		if n > heatDayMax {
			g.set(x, 1, '✦', bright, true)
		}
		// today's in-progress cell pulses: an empty next cell blinks a
		// hollow marker; a half cell blinks its completed preview.
		if d == 6 && n < heatDayMax && frame%10 < 5 { // 10Hz tick → 1s cycle
			y := heatH - 2 - n/2
			if half == 1 {
				g.set(x, y, '■', heatLevels[1+(n/2)/2], false)
			} else {
				g.set(x, y, '▫', colMuted, false)
			}
		}
	}
	sum := 0
	for _, n := range week {
		sum += n
	}
	// streak: consecutive active days ending at today; an empty today
	// doesn't break it (the day isn't over yet).
	streak := 0
	for d := 6; d >= 0; d-- {
		if week[d] > 0 {
			streak++
		} else if d < 6 {
			break
		}
	}
	left := fmt.Sprintf("%d this week", sum)
	if streak > 0 {
		left += fmt.Sprintf(" · %dd streak", streak)
	}
	g.text(2, heatH-1, left, colMuted, false)
	g.text(heatW-15, heatH-1, "less", colTitle, false)
	for i, c := range heatLevels {
		g.set(heatW-10+i, heatH-1, '■', c, false)
	}
	g.text(heatW-4, heatH-1, "more", colTitle, false)
	return g.render()
}
