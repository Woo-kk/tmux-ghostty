package logx

import (
	"encoding/json"
	"io"
	"log"
	"os"
	"sync"
	"time"
)

type Logger struct {
	mu     sync.Mutex
	logger *log.Logger
	file   *os.File
}

func New(path string) (*Logger, error) {
	if path == "" {
		return &Logger{logger: log.New(io.Discard, "", 0)}, nil
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &Logger{
		logger: log.New(file, "", 0),
		file:   file,
	}, nil
}

func (l *Logger) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	return l.file.Close()
}

func (l *Logger) Info(msg string, fields map[string]any) {
	l.log("info", msg, fields)
}

func (l *Logger) Error(msg string, fields map[string]any) {
	l.log("error", msg, fields)
}

func (l *Logger) log(level, msg string, fields map[string]any) {
	if l == nil || l.logger == nil {
		return
	}
	payload := map[string]any{
		"ts":    time.Now().UTC().Format(time.RFC3339Nano),
		"level": level,
		"msg":   msg,
	}
	for key, value := range fields {
		payload[key] = value
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.logger.Print(string(buf))
}
