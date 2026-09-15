package main

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// progressBar renders a single-line progress indicator to w, redrawing in
// place with a carriage return. When enabled is false it prints nothing, so
// callers can drive it unconditionally and let non-TTY output stay clean.
type progressBar struct {
	w        io.Writer
	label    string
	total    int
	current  int
	barWidth int
	enabled  bool
	lastLen  int
	lastDraw time.Time
	done     bool
}

const progressRedrawInterval = 60 * time.Millisecond

func newProgressBar(w io.Writer, enabled bool, termWidth int, label string, total int) *progressBar {
	barWidth := termWidth - len(label) - 24
	if barWidth > 32 {
		barWidth = 32
	}
	if barWidth < 8 {
		barWidth = 8
	}
	p := &progressBar{
		w:        w,
		label:    label,
		total:    total,
		barWidth: barWidth,
		enabled:  enabled,
	}
	p.draw(true)
	return p
}

// SetTotal updates the expected item count. Listing calls this once the first
// API page reveals totalCount.
func (p *progressBar) SetTotal(total int) {
	p.total = total
	p.draw(true)
}

func (p *progressBar) Add(n int) {
	p.current += n
	p.draw(false)
}

// Finish leaves the completed bar on screen and moves to the next line.
func (p *progressBar) Finish() {
	if p.done {
		return
	}
	p.done = true
	if p.total < p.current {
		p.total = p.current
	}
	p.draw(true)
	if p.enabled {
		fmt.Fprintln(p.w)
	}
}

func (p *progressBar) draw(force bool) {
	if !p.enabled {
		return
	}
	if !force && time.Since(p.lastDraw) < progressRedrawInterval {
		return
	}
	p.lastDraw = time.Now()

	var line string
	if p.total > 0 {
		filled := p.current * p.barWidth / p.total
		if filled > p.barWidth {
			filled = p.barWidth
		}
		bar := strings.Repeat("█", filled) + strings.Repeat("░", p.barWidth-filled)
		line = fmt.Sprintf("%s [%s] %d/%d", p.label, bar, p.current, p.total)
	} else {
		line = fmt.Sprintf("%s %d", p.label, p.current)
	}

	width := utf8.RuneCountInString(line)
	pad := ""
	if n := p.lastLen - width; n > 0 {
		pad = strings.Repeat(" ", n)
	}
	p.lastLen = width
	fmt.Fprintf(p.w, "\r%s%s", line, pad)
}

// spinner is an indeterminate progress indicator, for work that reports no
// intermediate state. A single git ls-remote call is one round trip, so there
// is nothing to count until it returns.
type spinner struct {
	w       io.Writer
	label   string
	enabled bool
	stop    chan struct{}
	done    chan struct{}
	once    sync.Once
}

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const spinnerInterval = 80 * time.Millisecond

func newSpinner(w io.Writer, enabled bool, label string) *spinner {
	s := &spinner{
		w:       w,
		label:   label,
		enabled: enabled,
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	if !enabled {
		close(s.done)
		return s
	}
	go s.spin()
	return s
}

func (s *spinner) spin() {
	defer close(s.done)
	ticker := time.NewTicker(spinnerInterval)
	defer ticker.Stop()
	for i := 0; ; i++ {
		fmt.Fprintf(s.w, "\r%s %s", spinnerFrames[i%len(spinnerFrames)], s.label)
		select {
		case <-s.stop:
			return
		case <-ticker.C:
		}
	}
}

// Finish stops the animation and wipes the line, leaving the caller free to
// print the result in its place.
func (s *spinner) Finish() {
	s.once.Do(func() {
		if !s.enabled {
			return
		}
		close(s.stop)
		<-s.done
		fmt.Fprintf(s.w, "\r%s\r", strings.Repeat(" ", utf8.RuneCountInString(s.label)+2))
	})
}
