package mux

import (
	"sync"

	"github.com/cihub/seelog"
	logging "github.com/op/go-logging"
	"github.com/pkg/errors"
)

// Logger wraps all of the package loggers that are using the
// memory backed logger from op/go-logging package.
type Logger struct {
	pkgLoggers map[string]*PackageLogger
	lock       sync.RWMutex
	backend    *logging.ChannelMemoryBackend
}

// PackageLogger wraps the memory logger. It multiplexes logs to both
// memory logger and seelog, which is the default logger for the agent
type PackageLogger struct {
	memLogger *logging.Logger
}

// NewLogger creates a new Logger object, which can be used to create
// package level memory backed loggers
func NewLogger(size int) *Logger {
	backend := logging.NewChannelMemoryBackend(size)
	format := logging.MustStringFormatter(
		`%{time:15:04:05.000} [%{level:.4s}]  %{message}`,
	)
	backendFormatter := logging.NewBackendFormatter(backend, format)
	logging.SetBackend(backendFormatter)
	backend.Start()
	return &Logger{
		pkgLoggers: make(map[string]*PackageLogger),
		backend:    backend,
	}
}

// GetPackageLogger returns a memory-backed logger for a package
func (logger *Logger) GetPackageLogger(pkg string) *PackageLogger {
	logger.lock.Lock()
	defer logger.lock.Unlock()

	l, ok := logger.pkgLoggers[pkg]
	if ok {
		return l
	}

	logging.SetLevel(logging.DEBUG, pkg)
	l = &PackageLogger{
		memLogger: logging.MustGetLogger(pkg),
	}
	logger.pkgLoggers[pkg] = l

	return l
}

// GetLogLines returns all of the log lines in memory for a paticular
// package
func (logger *Logger) GetLogLines(pkg string) ([]string, error) {
	logger.lock.RLock()
	defer logger.lock.RUnlock()

	var logs []string
	_, ok := logger.pkgLoggers[pkg]
	if !ok {
		return logs, errors.Errorf("mux logger: unknown package '%s'", pkg)
	}

	cur := logger.backend.Head()
	if cur == nil {
		// Nothing to log
		return logs, nil
	}
	for ; cur != nil; cur = cur.Next() {
		rec := cur.Record
		if rec.Module == pkg {
			logs = append(logs, rec.Formatted(2))
		}
	}

	return logs, nil
}

func (l *PackageLogger) Debug(v ...interface{}) {
	seelog.Debug(v)
	l.memLogger.Debug(v)
}

func (l *PackageLogger) Debugf(format string, params ...interface{}) {
	seelog.Debugf(format, params)
	l.memLogger.Debugf(format, params)
}

func (l *PackageLogger) Info(v ...interface{}) {
	seelog.Info(v)
	l.memLogger.Info(v)
}

func (l *PackageLogger) Infof(format string, params ...interface{}) {
	seelog.Infof(format, params)
	l.memLogger.Infof(format, params)
}

func (l *PackageLogger) Warn(v ...interface{}) {
	seelog.Warn(v)
	l.memLogger.Warning(v)
}

func (l *PackageLogger) Warnf(format string, params ...interface{}) {
	seelog.Warnf(format, params)
	l.memLogger.Warningf(format, params)
}

func (l *PackageLogger) Error(v ...interface{}) {
	seelog.Error(v)
	l.memLogger.Error(v)
}

func (l *PackageLogger) Errorf(format string, params ...interface{}) {
	seelog.Errorf(format, params)
	l.memLogger.Errorf(format, params)
}
