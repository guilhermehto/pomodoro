package main

import (
	"slices"
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
		25 * time.Minute:                     "25:00",
		90 * time.Second:                     "01:30",
		0:                                    "00:00",
		-5 * time.Second:                     "00:00",
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

func TestCreditTasksCurrentPlusDoneBlock(t *testing.T) {
	m := model{
		curUUID:   "a",
		curDesc:   "A",
		doneBlock: map[string]string{"b": "B", "a": "A"}, // "a" also current
		links:     map[string]taskLink{},
	}
	m.creditTasks()
	if got := m.links["a"].Pomodoros; got != 1 { // deduped, not double-counted
		t.Errorf("a pomodoros=%d want 1", got)
	}
	if got := m.links["b"].Pomodoros; got != 1 {
		t.Errorf("b pomodoros=%d want 1", got)
	}
	if len(m.doneBlock) != 0 {
		t.Errorf("doneBlock not cleared: %v", m.doneBlock)
	}
	m.creditTasks() // second focus, current still "a"
	if got := m.links["a"].Pomodoros; got != 2 {
		t.Errorf("a pomodoros after 2nd=%d want 2", got)
	}
	if got := m.links["b"].Pomodoros; got != 1 {
		t.Errorf("b pomodoros after 2nd=%d want 1 (b not current)", got)
	}
}

func TestTaskReconcile(t *testing.T) {
	cases := []struct {
		name      string
		active    []string
		target    string
		wantStop  []string
		wantStart bool
	}{
		{"first selection, nothing active", nil, "b", nil, true},
		{"switch from one active", []string{"a"}, "b", []string{"a"}, true},
		{"reselect already-active is a no-op", []string{"b"}, "b", nil, false},
		{"drift: stop every stray active", []string{"a", "c"}, "b", []string{"a", "c"}, true},
		{"target active alongside a stray", []string{"a", "b"}, "b", []string{"a"}, false},
	}
	for _, c := range cases {
		stop, start := taskReconcile(c.active, c.target)
		if start != c.wantStart || !slices.Equal(stop, c.wantStop) {
			t.Errorf("%s: taskReconcile(%v,%q)=(%v,%v) want (%v,%v)",
				c.name, c.active, c.target, stop, start, c.wantStop, c.wantStart)
		}
	}
}

func TestDecodeStateMigratesOldFormat(t *testing.T) {
	old := []byte(`{"total":10,"today":3,"date":"` + today() + `"}`)
	p := decodeState(old)
	if p.Stats.Total != 10 || p.Stats.Today != 3 {
		t.Errorf("old-format stats lost: %+v", p.Stats)
	}
	if p.Links == nil || len(p.Links) != 0 {
		t.Errorf("links should be empty non-nil, got %v", p.Links)
	}
	if p.Session != nil {
		t.Errorf("old format should carry no session, got %+v", p.Session)
	}

	newFmt := []byte(`{"stats":{"total":5,"today":2,"date":"` + today() + `"},"links":{"u1":{"description":"x","pomodoros":4,"last_worked":"` + today() + `"}},"session":{"phase":1,"remaining_sec":90,"cycle":2},"current_uuid":"u1","current_desc":"x","visualizer":true,"audio_perm":true}`)
	p2 := decodeState(newFmt)
	if p2.Stats.Total != 5 || p2.Stats.Today != 2 {
		t.Errorf("new-format stats wrong: %+v", p2.Stats)
	}
	if p2.Links["u1"].Pomodoros != 4 {
		t.Errorf("new-format links wrong: %+v", p2.Links)
	}
	if p2.Session == nil || p2.Session.Phase != shortBreak || p2.Session.RemainingSec != 90 || p2.Session.Cycle != 2 {
		t.Errorf("session wrong: %+v", p2.Session)
	}
	if p2.CurrentUUID != "u1" || p2.CurrentDesc != "x" || !p2.Visualizer || !p2.AudioPerm {
		t.Errorf("app state wrong: %+v", p2)
	}
}

// Quit acts as pause: a saved session resumes paused with its remaining time,
// clamped to the (possibly shrunk) phase duration. Current task and visualizer
// state come back regardless of whether a timer was in flight.
func TestRestoreSession(t *testing.T) {
	base := model{durations: [3]time.Duration{25 * time.Minute, 5 * time.Minute, 15 * time.Minute}, ph: focus, status: idle}
	base.remaining = base.dur(focus)

	m := base
	m.restore(persisted{
		Session:     &session{Phase: shortBreak, RemainingSec: 90, Cycle: 2},
		CurrentUUID: "u1", CurrentDesc: "write docs", Visualizer: true, AudioPerm: true,
	})
	if m.ph != shortBreak || m.status != paused || m.remaining != 90*time.Second || m.cycle != 2 {
		t.Errorf("restore = phase %v status %v remaining %v cycle %d", m.ph, m.status, m.remaining, m.cycle)
	}
	if m.curUUID != "u1" || m.curDesc != "write docs" || !m.resumeViz || !m.permOK {
		t.Errorf("app state not restored: uuid=%q desc=%q viz=%v perm=%v", m.curUUID, m.curDesc, m.resumeViz, m.permOK)
	}

	m = base
	m.restore(persisted{Session: &session{Phase: focus, RemainingSec: 3600}}) // saved before -work shrank
	if m.remaining != 25*time.Minute {
		t.Errorf("remaining not clamped: %v", m.remaining)
	}

	m = base
	m.restore(persisted{CurrentUUID: "u2", Visualizer: true}) // no timer in flight
	if m.ph != focus || m.status != idle || m.remaining != 25*time.Minute {
		t.Errorf("sessionless restore mutated timer: %+v", m)
	}
	if m.curUUID != "u2" || !m.resumeViz {
		t.Errorf("sessionless restore dropped app state: %+v", m)
	}

	m = base
	m.restore(persisted{Session: &session{Phase: 9, RemainingSec: 60}})
	m.restore(persisted{Session: &session{Phase: focus, RemainingSec: 0}})
	if m.ph != focus || m.status != idle || m.remaining != 25*time.Minute {
		t.Errorf("invalid sessions mutated model: %+v", m)
	}
}

func TestDateRollResetsTodayKeepsTotal(t *testing.T) {
	m := model{stats: stats{Total: 10, Today: 3, Date: "2000-01-01"}}
	m.stats.roll()
	if m.stats.Today != 0 {
		t.Errorf("today not reset: %d", m.stats.Today)
	}
	if m.stats.Total != 10 {
		t.Errorf("total changed: %d", m.stats.Total)
	}
	if m.stats.Date != today() {
		t.Errorf("date not rolled: %s", m.stats.Date)
	}
	if m.stats.Week != [6]int{} { // ancient gap: history clears
		t.Errorf("week not cleared: %v", m.stats.Week)
	}
}

func TestWeekRoll(t *testing.T) {
	day := func(back int) string { return time.Now().AddDate(0, 0, -back).Format("2006-01-02") }

	// same day: no-op
	s := stats{Today: 3, Date: today(), Week: [6]int{1, 2, 3, 4, 5, 6}}
	s.roll()
	if s.Today != 3 || s.Week != [6]int{1, 2, 3, 4, 5, 6} {
		t.Errorf("same-day roll mutated state: today=%d week=%v", s.Today, s.Week)
	}

	// one day: today slides into the newest slot
	s = stats{Today: 4, Date: day(1), Week: [6]int{1, 2, 3, 4, 5, 6}}
	s.roll()
	if want := [6]int{2, 3, 4, 5, 6, 4}; s.Week != want || s.Today != 0 {
		t.Errorf("1-day roll: today=%d week=%v want %v", s.Today, s.Week, want)
	}

	// three-day gap: two zero rest days follow the carried count
	s = stats{Today: 4, Date: day(3), Week: [6]int{1, 2, 3, 4, 5, 6}}
	s.roll()
	if want := [6]int{4, 5, 6, 4, 0, 0}; s.Week != want {
		t.Errorf("3-day roll: week=%v want %v", s.Week, want)
	}

	// week-long gap: everything ages out
	s = stats{Today: 4, Date: day(9), Week: [6]int{1, 2, 3, 4, 5, 6}}
	s.roll()
	if s.Week != [6]int{} {
		t.Errorf("9-day roll: week=%v want empty", s.Week)
	}
}

func TestQuoteRotation(t *testing.T) {
	t0 := time.Unix(0, 0)
	if quoteFor(t0) != quoteFor(t0.Add(179*time.Second)) {
		t.Error("quote changed within a 3-minute window")
	}
	if quoteFor(t0) == quoteFor(t0.Add(3*time.Minute)) {
		t.Error("quote did not rotate after 3 minutes")
	}
	if quoteFor(t0.Add(time.Duration(len(quotes))*3*time.Minute)) != quoteFor(t0) {
		t.Error("rotation did not wrap around")
	}
}
