// Package logger exposes leveled loggers that always write to stdout (so
// container runtimes capture them) and, by default, to one file per calendar
// day as well. The file rolls over automatically at midnight.
package logger

import (
	"io"
	"log"
	"os"
	"strconv"
)

var (
	InfoLog    *log.Logger
	WarningLog *log.Logger
	ErrorLog   *log.Logger
)

// init guarantees the loggers are usable even before StartLogger runs
// (config.Load logs during startup).
func init() {
	configure(os.Stdout)
}

// StartLogger reads LOG_TO_FILE (default true) and LOG_DIR (default ./logs).
// When file logging is on, each day's output goes to <LOG_DIR>/<YYYY-MM-DD>-app.log.
func StartLogger() {
	toFile := true
	if v := os.Getenv("LOG_TO_FILE"); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			toFile = parsed
		}
	}
	if !toFile {
		configure(os.Stdout)
		return
	}

	dir := os.Getenv("LOG_DIR")
	if dir == "" {
		dir = "./logs"
	}

	fw, err := newDailyWriter(dir, nil)
	if err != nil {
		ErrorLog.Printf("logger: %v; logging to stdout only", err)
		return
	}

	configure(io.MultiWriter(os.Stdout, fw))
	InfoLog.Printf("logger: writing daily log files under %s", dir)
}

func configure(w io.Writer) {
	flags := log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile
	InfoLog = log.New(w, "INFO  ", flags)
	WarningLog = log.New(w, "WARN  ", flags)
	ErrorLog = log.New(w, "ERROR ", flags)
	log.SetOutput(w)
}
