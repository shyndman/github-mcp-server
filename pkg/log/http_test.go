package log

import (
	"bytes"
	"net/http"
	"net/url"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func TestHTTPLogger(t *testing.T) {
	t.Run("LogRequest logs HTTP request details", func(t *testing.T) {
		// Setup
		var logBuffer bytes.Buffer
		logger := log.New()
		logger.SetOutput(&logBuffer)
		logger.SetFormatter(&log.TextFormatter{
			DisableTimestamp: true,
		})

		httpLogger := NewHTTPLogger(logger)

		// Create a test request
		req, _ := http.NewRequest("GET", "https://example.com/test?param=value", nil)

		// Log the request
		httpLogger.LogRequest(req)

		// Assertions
		logOutput := logBuffer.String()
		assert.Contains(t, logOutput, "method=GET")
		assert.Contains(t, logOutput, "https://example.com/test?param=value")
		assert.Contains(t, logOutput, "host=example.com")
		assert.Contains(t, logOutput, "path=/test")
		assert.Contains(t, logOutput, "HTTP request")
	})

	t.Run("LogResponse logs successful HTTP response details", func(t *testing.T) {
		// Setup
		var logBuffer bytes.Buffer
		logger := log.New()
		logger.SetOutput(&logBuffer)
		logger.SetFormatter(&log.TextFormatter{
			DisableTimestamp: true,
		})

		httpLogger := NewHTTPLogger(logger)

		// Create a test request and response
		req, _ := http.NewRequest("POST", "https://example.com/api", nil)
		res := &http.Response{
			StatusCode: 200,
			Request:    req,
		}

		// Log the response
		duration := 150 * time.Millisecond
		httpLogger.LogResponse(req, res, nil, duration)

		// Assertions
		logOutput := logBuffer.String()
		assert.Contains(t, logOutput, "method=POST")
		assert.Contains(t, logOutput, "https://example.com/api")
		assert.Contains(t, logOutput, "status=200")
		assert.Contains(t, logOutput, "durationMs=150")
		assert.Contains(t, logOutput, "HTTP response")
	})

	t.Run("LogResponse logs error HTTP response details", func(t *testing.T) {
		// Setup
		var logBuffer bytes.Buffer
		logger := log.New()
		logger.SetOutput(&logBuffer)
		logger.SetFormatter(&log.TextFormatter{
			DisableTimestamp: true,
		})

		httpLogger := NewHTTPLogger(logger)

		// Create a test request
		req, _ := http.NewRequest("DELETE", "https://example.com/resource/123", nil)

		// Create an error
		testErr := &url.Error{
			Op:  "Get",
			URL: "https://example.com/resource/123",
			Err: assert.AnError,
		}

		// Log the response with error
		duration := 75 * time.Millisecond
		httpLogger.LogResponse(req, nil, testErr, duration)

		// Assertions
		logOutput := logBuffer.String()
		assert.Contains(t, logOutput, "method=DELETE")
		assert.Contains(t, logOutput, "https://example.com/resource/123")
		assert.Contains(t, logOutput, "durationMs=75")
		assert.Contains(t, logOutput, "error=")
		assert.Contains(t, logOutput, "HTTP response error")
	})
}
