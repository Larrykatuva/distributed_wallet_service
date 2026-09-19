package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// dailyWriter writes to <dir>/<YYYY-MM-DD>-app.log and switches to a new file
// the first time it is written to on a new calendar day. A process that runs
// across midnight therefore never appends yesterday's and today's logs into
// the same file.
type dailyWriter struct {
	mu   sync.Mutex
	dir  string
	now  func() time.Time
	day  string // date of the currently open file
	file *os.File
}

func newDailyWriter(dir string, now func() time.Time) (*dailyWriter, error) {
	if now == nil {
		now = time.Now
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create log dir %s: %w", dir, err)
	}
	w := &dailyWriter{dir: dir, now: now}
	if err := w.rotate(w.today()); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *dailyWriter) today() string {
	return w.now().Format("2006-01-02")
}

// fileName is the path for a given day.
func (w *dailyWriter) fileName(day string) string {
	return filepath.Join(w.dir, day+"-app.log")
}

// rotate closes the current file (if any) and opens the file for day.
// Caller must hold mu, or be in the constructor.
func (w *dailyWriter) rotate(day string) error {
	f, err := os.OpenFile(w.fileName(day), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	if w.file != nil {
		_ = w.file.Close()
	}
	w.file = f
	w.day = day
	return nil
}

func (w *dailyWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if day := w.today(); day != w.day {
		if err := w.rotate(day); err != nil {
			// Keep writing to the old file rather than losing the line.
			fmt.Fprintf(os.Stderr, "logger: rotate to %s failed: %v\n", day, err)
		}
	}
	return w.file.Write(p)
}

func (w *dailyWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}
