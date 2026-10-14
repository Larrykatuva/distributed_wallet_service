package logger

import "io"

type asyncWriter struct{ lines chan []byte }

// NewAsyncWriter writes to w in order on a background goroutine, so callers
// never wait on w. It blocks only when queue writes are already pending.
func NewAsyncWriter(w io.Writer, queue int) io.Writer {
	a := &asyncWriter{lines: make(chan []byte, queue)}
	go func() {
		for b := range a.lines {
			_, _ = w.Write(b)
		}
	}()
	return a
}

func (a *asyncWriter) Write(p []byte) (int, error) {
	a.lines <- append([]byte(nil), p...)
	return len(p), nil
}
