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
- `-block a.com,b.com` sites to block system-wide during focus (persisted; `-block ''` clears).
- `-setup-block` one-time install of the passwordless hosts-block helper (asks for sudo once).

## Site blocking (macOS)

One-time setup, then set the list:

```
understory -setup-block                # asks for your sudo password once
understory -block x.com,reddit.com     # persists as `blocklist` in ~/.understory.json
```

While a focus session runs those domains are blocked in every browser and app: the app adds `0.0.0.0` entries to `/etc/hosts` (bare and `www.` forms) and flushes DNS, then removes them the moment the focus ends — complete, skip, reset, or quit. Pausing keeps the block. No prompts at focus time, and never run understory itself with sudo.

`-setup-block` installs a root-owned helper at `/usr/local/libexec/understory-block` plus a sudoers rule (`/etc/sudoers.d/understory`, visudo-checked) that lets only your user run only that helper without a password. The helper validates domains itself and can only add/remove its own tagged entries. Uninstall: `sudo rm /etc/sudoers.d/understory /usr/local/libexec/understory-block`.

Caveats: no wildcards — list each subdomain (`old.reddit.com`) explicitly; already-open tabs may live on cached DNS until reloaded. If the app dies mid-focus, leftover entries are cleaned at next launch.

## Tasks

Needs the `task` CLI (`brew install task`). The task view lists your pending tasks by urgency. Pick one as the current task, mark tasks done, add new ones, edit description/project/tags.

Each completed focus session credits one pomodoro to the current task plus anything you marked done that session, stored locally in `~/.understory.json`. Setting a current task starts it in Taskwarrior (`task start`) and stops any other active task, so TW's active timer follows your selection; otherwise nothing is written back except the done/add/edit actions you take yourself.

## Visualizer (macOS 14.4+)

Press `v`. The app taps the system audio output directly through a Core Audio process tap — no loopback driver, no ffmpeg, nothing to install or reboot. Volume keys keep working and it hears exactly what you hear.

First press compiles a small Swift helper into `~/Library/Caches/understory/` (needs the Xcode command line tools: `xcode-select --install`) and triggers the System Audio Recording permission prompt — allow it. Manage the permission later under System Settings → Privacy & Security → Screen & System Audio Recording. If you hit "Don't Allow", macOS won't ask again and the bars stay flat — the status line reminds you until audio flows; flip the permission on in that same Settings pane and press `v` again.
