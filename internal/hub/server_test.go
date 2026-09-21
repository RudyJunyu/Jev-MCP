package hub

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jev/mcphub/internal/typesafe"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func bearerClient(token string) *http.Client {
	return &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		r.Header.Set("Authorization", "Bearer "+token)
		return http.DefaultTransport.RoundTrip(r)
	})}
}

func TestStreamableHTTPToolCall(t *testing.T) {
	var upstreamAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamAuth = r.Header.Get("Authorization")
		var body typesafe.Evaluation
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		question, ok := body.Questions["result"]
		if !ok || question.Type != "noul" || body.State != "The deployment succeeded." {
			t.Fatalf("unexpected upstream payload: %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"jev-test","answers":{"result":{"type":"noul","noul":0.97}},"usage":{"input_tokens":5,"output_tokens":2}}`))
	}))
	defer upstream.Close()

	client, err := typesafe.New(upstream.URL, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	metrics := NewMetrics()
	mcpHTTP := httptest.NewServer(HandlerWithObservability(NewWithObservability(client, "", 2*time.Second, nil, metrics), nil, metrics))
	defer mcpHTTP.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	mcpClient := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil)
	session, err := mcpClient.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             mcpHTTP.URL + "/mcp",
		HTTPClient:           bearerClient("user-token"),
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 4 {
		t.Fatalf("got %d tools, want 4", len(tools.Tools))
	}

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "jev_noul",
		Arguments: map[string]any{
			"state":        "The deployment succeeded.",
			"instructions": "Did the deployment succeed?",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || upstreamAuth != "Bearer user-token" {
		t.Fatalf("result error=%v upstream authorization=%q", result.IsError, upstreamAuth)
	}
	structured, ok := result.StructuredContent.(map[string]any)
	if !ok || structured["answers"] == nil {
		t.Fatalf("unexpected structured result: %#v", result.StructuredContent)
	}
	res, err := http.Get(mcpHTTP.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `jev_mcp_tool_calls_total{tool="noul",outcome="success"} 1`) {
		t.Fatalf("missing successful noul metric: %s", body)
	}
}

func TestHTTPRequiresBearerAndRejectsBrowserOrigin(t *testing.T) {
	client, _ := typesafe.New("https://api.typesafe.ai/v1", time.Second)
	server := httptest.NewServer(Handler(New(client, "", time.Second)))
	defer server.Close()

	for _, tc := range []struct {
		name   string
		header http.Header
		want   int
	}{
		{"missing token", http.Header{}, http.StatusUnauthorized},
		{"browser origin", http.Header{"Authorization": []string{"Bearer token"}, "Origin": []string{"https://example.com"}}, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodPost, server.URL+"/mcp", nil)
			req.Header = tc.header
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			if res.StatusCode != tc.want {
				t.Fatalf("status=%d, want %d", res.StatusCode, tc.want)
			}
		})
	}
}

func TestMetricsEndpoint(t *testing.T) {
	server := httptest.NewServer(HandlerWithObservability(mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil), nil, NewMetrics()))
	defer server.Close()
	res, err := http.Get(server.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	res, err = http.Get(server.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), "jev_http_requests_total 1") {
		t.Fatalf("status=%d body=%s", res.StatusCode, body)
	}
}
