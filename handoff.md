# Handoff — pomodoro TUI

Go + bubbletea pomodoro timer with an audio-reactive FFT spectrum visualizer.
Everything is built and tested. Only the system-audio capture setup is pending a
machine reboot.

## Where we left off

- App is complete: big ASCII MM:SS clock, progress bar, session persistence
  (`~/.pomodoro.json`), and a `v`-toggled spectrum visualizer.
- The visualizer reads audio via `ffmpeg` (already installed), FFTs it, and draws
  colored bars. No cgo, no extra Go deps.
- To visualize the **music you're playing** (not the mic), macOS needs a loopback
  device. We chose **BlackHole**. Install started but **failed** — the `.pkg`
  needs `sudo`, which can't prompt for a password through the agent shell.

## Finish the BlackHole setup (do this after reboot)

1. In your **own** terminal (so sudo can prompt):
   ```
   brew install --cask blackhole-2ch
   ```
2. **Reboot** — the audio driver only loads after a restart.
3. Open **Audio MIDI Setup** (Applications → Utilities):
   - Click `+` (bottom-left) → **Create Multi-Output Device**.
   - Tick **both** your speakers/headphones **and** **BlackHole 2ch**.
   - This lets you still hear audio while BlackHole gets a copy.
4. Set that **Multi-Output Device** as your system output (Sound settings or the
   menu-bar volume control).
5. First run grants your terminal **Microphone** permission (macOS treats the
   BlackHole input as a mic). Allow it.

## Run it

```
go build -o pomodoro .   # or: go run .
./pomodoro               # classic 25 / 5 / 15
```

Then press `v` — it auto-selects BlackHole when present.

### Flags
- `-work N` / `-short N` / `-long N` — durations in minutes (default 25/5/15;
  handy for quick testing, e.g. `-work 1`).
- `-audio <device>` — visualizer input by index or name substring. Default:
  BlackHole → mic → first input. Examples: `-audio blackhole`, `-audio 5`.

### Keys
`space` start/pause (and start next phase after one completes) · `s` skip ·
`r` reset · `v` toggle visualizer · `q` quit.

## Verify device is detected (after reboot)

```
ffmpeg -f avfoundation -list_devices true -i "" 2>&1 | grep -A20 "audio devices"
```
BlackHole 2ch should appear in the audio list. If it does, `v` will use it.

## Files

- `main.go` — model, timer state machine, rendering (clock/bar/dots/spectrum),
  persistence.
- `audio.go` — ffmpeg capture, FFT, log-spaced banding, device discovery.
- `main_test.go`, `audio_test.go` — logic + a real end-to-end tone test.
  Run: `go test ./...` (the tone test skips if ffmpeg is missing).

## Known caveats / next steps

- If `v` shows an audio error, check: terminal has Microphone permission; the
  Multi-Output device is the active output; `ffmpeg` lists BlackHole.
- No system-audio option exists without a loopback (BlackHole) or ScreenCaptureKit
  (needs cgo/Obj-C). We deliberately stuck with the ffmpeg pipe.
- Until BlackHole is set up, `v` falls back to the mic (reacts to room sound).
