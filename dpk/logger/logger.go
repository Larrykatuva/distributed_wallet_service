package logger

import (
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Declare global loggers for different log levels
var (
	WarningLog *log.Logger
	InfoLog    *log.Logger
	ErrorLog   *log.Logger
)

// StartLogger initializes logging configuration.
// It reads environment variables to determine if logging should go to a file or stdout.
// Logs are written to a file named with the current date if `LogToFile` is true.
func StartLogger() {

	// Get the current date in a more readable format (YYYY-MM-DD)
	year, month, day := time.Now().Date()
	date := strconv.Itoa(year) + "-" + month.String() + "-" + strconv.Itoa(day)
	fileName := date + "th-appLogs.log"

	// Ensure the logs directory exists
	logDir := "./logs"
	err := os.MkdirAll(logDir, os.ModePerm)
	if err != nil {
		log.Fatal("Error creating log directory: ", err)
	}

	// Create the log file in the logs directory
	logFilePath := filepath.Join(logDir, fileName)
	logFile, err := os.OpenFile(logFilePath, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0666)
	if err != nil {
		log.Fatal("Error opening log file: ", err)
	}

	// Check if LogToFile environment variable is set
	logToFile, _ := strconv.ParseBool(os.Getenv("LogToFile"))
	if logToFile {
		log.SetOutput(logFile)
	}

	// Initialize loggers with different log levels
	InfoLog = log.New(logFile, "INFO: ", log.Ldate|log.Ltime|log.Lshortfile)
	WarningLog = log.New(logFile, "WARNING: ", log.Ldate|log.Ltime|log.Lshortfile)
	ErrorLog = log.New(logFile, "ERROR: ", log.Ldate|log.Ltime|log.Lshortfile)
}
