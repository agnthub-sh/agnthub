//go:build e2e && linux

package e2e

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bytedance/sonic"
)

// modelStub is a model API on the container's loopback that records every
// request and answers with a fixed text. Harnesses without an offline listing
// command run headless against it: the agents they loaded appear in the first
// model request (as subagent tool descriptions or system instructions), so the
// real CLI proves it loaded them without a login, a network or a real model.
// It speaks the Gemini API and the OpenAI Chat Completions API.
type modelStub struct {
	url      string
	requests chan stubRequest
}

type stubRequest struct {
	path string
	body []byte
}

const stubReply = "agnthub e2e stub reply"

func startModelStub(t *testing.T) *modelStub {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	stub := &modelStub{
		url:      "http://" + listener.Addr().String(),
		requests: make(chan stubRequest, 256),
	}
	server := &http.Server{Handler: stub, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	return stub
}

func (m *modelStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	select {
	case m.requests <- stubRequest{path: r.URL.Path, body: body}:
	default: // the check reads the first requests only
	}
	path := r.URL.Path
	switch {
	case strings.Contains(path, ":streamGenerateContent"):
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: " + geminiResponse() + "\n\n"))
	case strings.Contains(path, ":generateContent"):
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(geminiResponse()))
	case strings.Contains(path, ":countTokens"):
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"totalTokens":1}`))
	case strings.HasSuffix(path, "/chat/completions"):
		m.chatCompletion(w, body)
	case strings.HasSuffix(path, "/models"):
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"stub-model","object":"model"}]}`))
	default:
		http.NotFound(w, r)
	}
}

func geminiResponse() string {
	out, _ := sonic.MarshalString(map[string]any{
		"candidates": []any{
			map[string]any{
				"content": map[string]any{
					"role":  "model",
					"parts": []any{map[string]any{"text": stubReply}},
				},
				"finishReason": "STOP",
				"index":        0,
			},
		},
		"usageMetadata": map[string]any{
			"promptTokenCount": 1, "candidatesTokenCount": 1, "totalTokenCount": 2,
		},
	})
	return out
}

func (m *modelStub) chatCompletion(w http.ResponseWriter, body []byte) {
	var req struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	_ = sonic.Unmarshal(body, &req)
	usage := map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}
	if !req.Stream {
		out, _ := sonic.MarshalString(map[string]any{
			"id": "stub", "object": "chat.completion", "created": 0, "model": req.Model,
			"choices": []any{map[string]any{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": stubReply},
				"finish_reason": "stop",
			}},
			"usage": usage,
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(out))
		return
	}
	chunk := func(choices []any, extra map[string]any) string {
		c := map[string]any{
			"id": "stub", "object": "chat.completion.chunk", "created": 0, "model": req.Model,
			"choices": choices,
		}
		for k, v := range extra {
			c[k] = v
		}
		out, _ := sonic.MarshalString(c)
		return "data: " + out + "\n\n"
	}
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = w.Write([]byte(
		chunk([]any{map[string]any{
			"index": 0, "delta": map[string]any{"role": "assistant", "content": stubReply},
			"finish_reason": nil,
		}}, nil) +
			chunk(
				[]any{
					map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"},
				},
				nil,
			) +
			chunk([]any{}, map[string]any{"usage": usage}) +
			"data: [DONE]\n\n"))
}

// received returns the requests recorded so far.
func (m *modelStub) received() []stubRequest {
	var out []stubRequest
	for {
		select {
		case r := <-m.requests:
			out = append(out, r)
		default:
			return out
		}
	}
}
