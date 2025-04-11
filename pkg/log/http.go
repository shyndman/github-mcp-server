package log

import (
	"net/http"
	"time"

	log "github.com/sirupsen/logrus"
)

// HTTPLogger is a wrapper around logrus.Logger that implements the
// github.com/ernesto-jimenez/httplogger.HTTPLogger interface
type HTTPLogger struct {
	logger *log.Logger
}

// NewHTTPLogger creates a new HTTPLogger instance
func NewHTTPLogger(logger *log.Logger) *HTTPLogger {
	return &HTTPLogger{
		logger: logger,
	}
}

// LogRequest logs information about an HTTP request
func (l *HTTPLogger) LogRequest(req *http.Request) {
	l.logger.WithFields(log.Fields{
		"method": req.Method,
		"url":    req.URL.String(),
		"host":   req.Host,
		"path":   req.URL.Path,
	}).Info("HTTP request")
}

// LogResponse logs information about an HTTP response
func (l *HTTPLogger) LogResponse(req *http.Request, res *http.Response, err error, duration time.Duration) {
	durationMs := duration / time.Millisecond

	fields := log.Fields{
		"method":     req.Method,
		"url":        req.URL.String(),
		"host":       req.Host,
		"path":       req.URL.Path,
		"durationMs": durationMs,
	}

	if err != nil {
		fields["error"] = err.Error()
		l.logger.WithFields(fields).Error("HTTP response error")
	} else {
		fields["status"] = res.StatusCode
		l.logger.WithFields(fields).Info("HTTP response")
	}
}
