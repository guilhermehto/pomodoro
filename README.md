# understory

A terminal pomodoro timer with Taskwarrior tracking and an audio-reactive spectrum visualizer. Big ASCII clock, classic 25/5/15 cycle, long break every 4 focus sessions. State persists to `~/.understory.json`: stats (including the past week of sessions), task links, the current task, whether the visualizer is on, and any in-flight session - quitting mid-session acts as pause, and the next launch resumes where you left off.

## Build and run

```
go build -o understory . && ./understory
```

Or `go run .`. No cgo, no setup for the timer itself.

## Keys

- `space` start / pause, and start the next phase once one completes
- `s` skip phase
- `r` reset phase
- `t` open the task view
- `g` weekly activity heatmap - one column per day, one cell per two sessions (half-block for odd counts), 14 tops a day; goal tick at 4, streak counter in the footer
- `v` toggle the visualizer
- `q` quit

In the task view: `↑`/`↓` move, `enter` set current task, `d` mark done, `a` add, `e` edit, `t`/`esc` back.

## Flags

- `-work N` / `-short N` / `-long N` durations in minutes (default 25/5/15). Handy for testing, e.g. `-work 1`.

## Tasks

Needs the `task` CLI (`brew install task`). The task view lists your pending tasks by urgency. Pick one as the current task, mark tasks done, add new ones, edit description/project/tags.

Each completed focus session credits one pomodoro to the current task plus anything you marked done that session, stored locally in `~/.understory.json`. Nothing is written back to Taskwarrior except the done/add/edit actions you take yourself.

## Visualizer (macOS 14.4+)

Press `v`. The app taps the system audio output directly through a Core Audio process tap — no loopback driver, no ffmpeg, nothing to install or reboot. Volume keys keep working and it hears exactly what you hear.

First press compiles a small Swift helper into `~/Library/Caches/understory/` (needs the Xcode command line tools: `xcode-select --install`) and triggers the System Audio Recording permission prompt — allow it. Manage the permission later under System Settings → Privacy & Security → Screen & System Audio Recording. If you hit "Don't Allow", macOS won't ask again and the bars stay flat — the status line reminds you until audio flows; flip the permission on in that same Settings pane and press `v` again.
