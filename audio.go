package main

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"math/cmplx"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

const (
	sampleRate = 44100
	fftSize    = 1024 // ~43 frames/sec at 44.1kHz
	numBands   = 64
)

// audioFrame carries one analysed spectrum frame, or a terminal error.
type audioFrame struct {
	levels []float64
	err    error
}

// captureAudio pipes mono PCM out of ffmpeg (avfoundation), analyses each block
// into log-spaced spectrum bands, and streams frames on out until ctx is
// cancelled or ffmpeg dies. Non-blocking sends drop frames if the UI is busy —
// a visualizer wants the latest frame, not a backlog.
func captureAudio(ctx context.Context, device string, out chan<- audioFrame) {
	defer close(out)

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-f", "avfoundation", "-i", ":"+device,
		"-f", "f32le", "-ac", "1", "-ar", strconv.Itoa(sampleRate), "-")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		out <- audioFrame{err: err}
		return
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		out <- audioFrame{err: err}
		return
	}

	an := newAnalyzer()
	raw := make([]byte, fftSize*4)
	samples := make([]float64, fftSize)

	for {
		if _, err := io.ReadFull(stdout, raw); err != nil {
			werr := cmd.Wait()
			if ctx.Err() != nil {
				return // user toggled off — not an error
			}
			msg := strings.TrimSpace(stderr.String())
			if msg == "" && werr != nil {
				msg = werr.Error()
			}
			if msg == "" {
				msg = err.Error()
			}
			out <- audioFrame{err: errors.New(msg)}
			return
		}
		for i := 0; i < fftSize; i++ {
			samples[i] = float64(math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:])))
		}
		select {
		case out <- audioFrame{levels: an.bands(samples)}:
		default: // UI not ready; drop this frame
		}
	}
}

// analyzer holds the Hann window and an adaptive gain so bars auto-scale to the
// current volume without a hand-tuned dB floor.
type analyzer struct {
	window     []float64
	runningMax float64
}

func newAnalyzer() *analyzer {
	w := make([]float64, fftSize)
	for i := range w {
		w[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(fftSize-1))
	}
	return &analyzer{window: w, runningMax: 1e-6}
}

func (a *analyzer) bands(samples []float64) []float64 {
	buf := make([]complex128, fftSize)
	for i := 0; i < fftSize; i++ {
		buf[i] = complex(samples[i]*a.window[i], 0)
	}
	fft(buf)

	half := fftSize / 2
	binHz := float64(sampleRate) / float64(fftSize)
	const fmin, fmax = 40.0, 16000.0

	bands := make([]float64, numBands)
	frameMax := 1e-9
	for b := 0; b < numBands; b++ {
		f0 := fmin * math.Pow(fmax/fmin, float64(b)/numBands)
		f1 := fmin * math.Pow(fmax/fmin, float64(b+1)/numBands)
		lo, hi := int(f0/binHz), int(f1/binHz)
		if lo < 1 {
			lo = 1
		}
		if hi <= lo {
			hi = lo + 1
		}
		if hi > half {
			hi = half
		}
		sum := 0.0
		for i := lo; i < hi; i++ {
			sum += cmplx.Abs(buf[i])
		}
		v := sum / float64(hi-lo)
		bands[b] = v
		if v > frameMax {
			frameMax = v
		}
	}

	// adaptive gain: jump up instantly, decay slowly toward quiet passages.
	if frameMax > a.runningMax {
		a.runningMax = frameMax
	} else {
		a.runningMax = a.runningMax*0.995 + frameMax*0.005
	}
	if a.runningMax < 1e-6 {
		a.runningMax = 1e-6
	}
	for b := range bands {
		l := bands[b] / a.runningMax
		if l > 1 {
			l = 1
		}
		bands[b] = math.Sqrt(l) // perceptual curve
	}
	return bands
}

// fft is an in-place iterative radix-2 Cooley-Tukey transform; len(x) must be a
// power of two.
func fft(x []complex128) {
	n := len(x)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			x[i], x[j] = x[j], x[i]
		}
	}
	for length := 2; length <= n; length <<= 1 {
		wlen := cmplx.Rect(1, -2*math.Pi/float64(length))
		for i := 0; i < n; i += length {
			w := complex(1, 0)
			for k := 0; k < length/2; k++ {
				u := x[i+k]
				v := x[i+k+length/2] * w
				x[i+k] = u + v
				x[i+k+length/2] = u - v
				w *= wlen
			}
		}
	}
}

// ── device discovery ──────────────────────────────────────────────────────

var deviceLine = regexp.MustCompile(`\[(\d+)\]\s+(.*)$`)

// listAudioDevices returns avfoundation input names indexed by ffmpeg's index
// (gaps are empty strings).
func listAudioDevices() []string {
	cmd := exec.Command("ffmpeg", "-f", "avfoundation", "-list_devices", "true", "-i", "")
	var b strings.Builder
	cmd.Stderr = &b
	_ = cmd.Run() // always "errors" (no real input) — we only want the listing
	return parseAudioDevices(b.String())
}

func parseAudioDevices(s string) []string {
	var out []string
	inAudio := false
	for _, ln := range strings.Split(s, "\n") {
		switch {
		case strings.Contains(ln, "AVFoundation audio devices"):
			inAudio = true
			continue
		case strings.Contains(ln, "AVFoundation video devices"):
			inAudio = false
			continue
		}
		if !inAudio {
			continue
		}
		if m := deviceLine.FindStringSubmatch(ln); m != nil {
			idx, _ := strconv.Atoi(m[1])
			for len(out) <= idx {
				out = append(out, "")
			}
			out[idx] = strings.TrimSpace(m[2])
		}
	}
	return out
}

// resolveDevice picks an input: explicit flag (index or name substring) →
// BlackHole (system audio) → any Microphone → first available.
func resolveDevice(flag string) (index, name string, ok bool) {
	devs := listAudioDevices()
	pick := -1
	if flag != "" {
		if n, err := strconv.Atoi(flag); err == nil && n >= 0 && n < len(devs) && devs[n] != "" {
			pick = n
		} else {
			pick = findDevice(devs, flag)
		}
	}
	for _, want := range []string{"blackhole", "microphone"} {
		if pick < 0 {
			pick = findDevice(devs, want)
		}
	}
	if pick < 0 {
		for i, d := range devs {
			if d != "" {
				pick = i
				break
			}
		}
	}
	if pick < 0 {
		return "", "", false
	}
	return strconv.Itoa(pick), devs[pick], true
}

func findDevice(devs []string, sub string) int {
	sub = strings.ToLower(sub)
	for i, d := range devs {
		if d != "" && strings.Contains(strings.ToLower(d), sub) {
			return i
		}
	}
	return -1
}
