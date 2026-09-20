package typesafe

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestEvaluateForwardsBearerAndReturnsAnswers(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["model"] != DefaultModel {
			t.Fatalf("model = %v", body["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"model":"jev-1.13.0","answers":{"ok":{"type":"noul","noul":0.9}},"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()
	client, err := New(server.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Evaluate(context.Background(), "secret", Evaluation{State: "text", Questions: map[string]Question{"ok": {Type: "noul", Instructions: "Is this true?"}}})
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer secret" || result["answers"] == nil {
		t.Fatalf("auth=%q result=%v", gotAuth, result)
	}
}

func TestEvaluateRetriesRateLimit(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(429)
			return
		}
		w.Write([]byte(`{"answers":{}}`))
	}))
	defer server.Close()
	client, _ := New(server.URL, 2*time.Second)
	_, err := client.Evaluate(context.Background(), "secret", Evaluation{State: "x", Questions: map[string]Question{"q": {Type: "noul", Instructions: "q"}}})
	if err != nil || attempts != 2 {
		t.Fatalf("err=%v attempts=%d", err, attempts)
	}
}

func TestEvaluateRejectsInvalidQuestion(t *testing.T) {
	client, _ := New("https://api.typesafe.ai/v1", time.Second)
	_, err := client.Evaluate(context.Background(), "secret", Evaluation{State: "x", Questions: map[string]Question{"q": {Type: "choice", Instructions: "q", Criteria: map[string]any{}}}})
	if err == nil {
		t.Fatal("expected validation error")
	}
}
