package httpserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
)

func apiResponseFormatMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead || r.Method == http.MethodOptions || !apiWantsText(r) {
			next.ServeHTTP(w, r)
			return
		}

		recorder := httptest.NewRecorder()
		next.ServeHTTP(recorder, r)
		result := recorder.Result()
		defer result.Body.Close()
		for name, values := range result.Header {
			for _, value := range values {
				w.Header().Add(name, value)
			}
		}
		w.Header().Del("Content-Length")
		body := recorder.Body.Bytes()
		if strings.Contains(result.Header.Get("Content-Type"), "application/json") {
			body = serializeAPIText(body)
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(recorder.Code)
		_, _ = w.Write(ensureSingleNewline(body))
	})
}

func apiWantsText(r *http.Request) bool {
	if strings.HasSuffix(originalRequestPath(r), ".txt") {
		return true
	}
	accept := r.Header.Get("Accept")
	if strings.Contains(accept, "application/json") {
		return false
	}
	if strings.Contains(accept, "text/plain") {
		return true
	}
	return isHTTPTool(r)
}

func serializeAPIText(body []byte) []byte {
	var envelope struct {
		OK      bool            `json:"ok"`
		Data    json.RawMessage `json:"data"`
		Error   string          `json:"error"`
		Message string          `json:"message"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return body
	}
	if !envelope.OK {
		return []byte(fmt.Sprintf("ERROR: %s: %s", envelope.Error, envelope.Message))
	}
	if len(envelope.Data) == 0 || bytes.Equal(envelope.Data, []byte("null")) {
		message := envelope.Message
		if message == "" {
			message = "Success"
		}
		return []byte("OK: " + message)
	}
	var data any
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		return envelope.Data
	}
	if value, ok := data.(string); ok {
		return []byte(value)
	}
	var lines []string
	switch value := data.(type) {
	case []any:
		lines = append(lines, fmt.Sprintf("OK: %d items found", len(value)))
		for _, item := range value {
			if _, scalar := item.(map[string]any); scalar {
				lines = append(lines, flattenAPIValue(item, "")...)
				lines = append(lines, "")
			} else {
				lines = append(lines, fmt.Sprint(item))
			}
		}
		if len(lines) > 1 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
	default:
		lines = append(lines, "OK: Success")
		lines = append(lines, flattenAPIValue(data, "")...)
	}
	return []byte(strings.Join(lines, "\n"))
}

func flattenAPIValue(value any, prefix string) []string {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		var lines []string
		for _, key := range keys {
			name := key
			if prefix != "" {
				name = prefix + "." + key
			}
			lines = append(lines, flattenAPIValue(typed[key], name)...)
		}
		return lines
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			parts = append(parts, fmt.Sprint(item))
		}
		return []string{prefix + ": " + strings.Join(parts, ", ")}
	default:
		return []string{prefix + ": " + fmt.Sprint(typed)}
	}
}

func ensureSingleNewline(body []byte) []byte {
	return append(bytes.TrimRight(body, "\r\n"), '\n')
}
