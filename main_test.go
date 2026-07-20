package main

import (
	"testing"
	"time"
)

func TestCadence(t *testing.T) {
	// Simulate: 4 focus sessions → long break every 4th, short otherwise.
	m := model{ph: focus}
	want := []phase{shortBreak, shortBreak, shortBreak, longBreak}
	for i, w := range want {
		m.ph = focus
		m.cycle++ // a focus just completed
		if got := m.next(); got != w {
			t.Fatalf("focus #%d: next=%v want %v", m.cycle, got, w)
		}
		_ = i
		m.ph = w // pretend we took the break; break always → focus
		if got := m.next(); got != focus {
			t.Fatalf("after %v: next=%v want focus", w, got)
		}
	}
}

func TestFmtDuration(t *testing.T) {
	cases := map[time.Duration]string{
		25 * time.Minute:                   "25:00",
		90 * time.Second:                   "01:30",
		0:                                  "00:00",
		-5 * time.Second:                   "00:00",
		time.Minute + 59500*time.Millisecond: "02:00", // rounds to nearest second
	}
	for d, want := range cases {
		if got := fmtDuration(d); got != want {
			t.Errorf("fmtDuration(%v)=%q want %q", d, got, want)
		}
	}
}

func TestHexLerp(t *testing.T) {
	if got := hexLerp("#000000", "#ffffff", 0); got != "#000000" {
		t.Errorf("t=0 got %s", got)
	}
	if got := hexLerp("#000000", "#ffffff", 1); got != "#ffffff" {
		t.Errorf("t=1 got %s", got)
	}
	if got := hexLerp("#000000", "#ffffff", 0.5); got != "#808080" {
		t.Errorf("t=0.5 got %s", got)
	}
}

func TestDateRollResetsTodayKeepsTotal(t *testing.T) {
	m := model{stats: stats{Total: 10, Today: 3, Date: "2000-01-01"}}
	m.rollDate()
	if m.stats.Today != 0 {
		t.Errorf("today not reset: %d", m.stats.Today)
	}
	if m.stats.Total != 10 {
		t.Errorf("total changed: %d", m.stats.Total)
	}
	if m.stats.Date != today() {
		t.Errorf("date not rolled: %s", m.stats.Date)
	}
}
