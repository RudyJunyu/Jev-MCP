package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jev/mcphub/internal/typesafe"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const Version = "0.1.0"

func Token(header string) (string, bool) {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || len(parts[1]) > 4096 {
		return "", false
	}
	for _, c := range parts[1] {
		if c < 33 || c > 126 {
			return "", false
		}
	}
	return parts[1], true
}

type singleInput struct {
	State        any    `json:"state"`
	Model        string `json:"model,omitempty"`
	Instructions any    `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"`
}

func decode(raw json.RawMessage, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return errors.New("invalid tool arguments; check the tool schema")
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("expected one JSON object")
	}
	return nil
}

func New(client *typesafe.Client, stdioToken string, timeout time.Duration) *mcp.Server {
	return NewWithObservability(client, stdioToken, timeout, nil, nil)
}

func NewWithObservability(client *typesafe.Client, stdioToken string, timeout time.Duration, logger *slog.Logger, metrics *Metrics) *mcp.Server {
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	if metrics == nil {
		metrics = NewMetrics()
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "jev", Version: Version}, &mcp.ServerOptions{
		Instructions: "Use Jev for structured classification (choice), ordered scoring (score), yes/no probability (noul), or batch evaluation. Always supply the relevant state. Results include upstream answers and usage. Calls consume the user's TypeSafe quota.",
	})
	for _, kind := range []string{"evaluate", "choice", "score", "noul"} {
		s.AddTool(&mcp.Tool{Name: "jev_" + kind, Description: toolDescription(kind), InputSchema: inputSchema(kind)}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			started := time.Now()
			failed := true
			defer func() {
				elapsed := time.Since(started)
				metrics.observeTool(kind, failed, elapsed)
				attrs := []any{"tool", kind, "success", !failed, "duration_ms", elapsed.Seconds() * 1000}
				if failed {
					logger.Warn("mcp_tool_call", attrs...)
				} else {
					logger.Info("mcp_tool_call", attrs...)
				}
			}()
			token := stdioToken
			if req.Extra != nil && req.Extra.Header != nil {
				token, _ = Token(req.Extra.Header.Get("Authorization"))
			}
			if token == "" {
				return toolError(errors.New("missing TypeSafe token")), nil
			}
			var e typesafe.Evaluation
			if kind == "evaluate" {
				if err := decode(req.Params.Arguments, &e); err != nil {
					return toolError(err), nil
				}
			} else {
				var in singleInput
				if err := decode(req.Params.Arguments, &in); err != nil {
					return toolError(err), nil
				}
				e = typesafe.Evaluation{State: in.State, Model: in.Model, Questions: map[string]typesafe.Question{"result": {Type: kind, Instructions: in.Instructions, Criteria: in.Criteria}}}
			}
			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			result, err := client.Evaluate(ctx, token, e)
			if err != nil {
				return toolError(err), nil
			}
			data, _ := json.Marshal(result)
			failed = false
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}, StructuredContent: result}, nil
		})
	}
	return s
}

func toolError(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}
}

func toolDescription(kind string) string {
	switch kind {
	case "choice":
		return "Classify state into one of 1–255 named options. Returns choice, probabilities and confidence under answers.result. Consumes TypeSafe quota."
	case "score":
		return "Rate state against 2–10 ordered criteria. Returns a probability-weighted score, legend and confidence under answers.result. Consumes TypeSafe quota."
	case "noul":
		return "Evaluate a yes/no question about state. Returns the probability of yes (0–1) under answers.result.noul. Consumes TypeSafe quota."
	default:
		return "Evaluate multiple named choice, score or noul questions in one TypeSafe API call. Answers preserve question IDs; model defaults to jev-latest. Consumes TypeSafe quota."
	}
}

func descriptionSchema(nullable bool) map[string]any {
	types := []string{"string", "object", "array"}
	if nullable {
		types = append(types, "null")
	}
	return map[string]any{"type": types}
}

func criteriaSchema(kind string) map[string]any {
	switch kind {
	case "choice":
		return map[string]any{"type": "object", "minProperties": 1, "maxProperties": 255, "additionalProperties": descriptionSchema(true)}
	case "score":
		return map[string]any{"type": "array", "minItems": 2, "maxItems": 10, "items": descriptionSchema(false)}
	default:
		return map[string]any{"type": "object", "properties": map[string]any{"true": descriptionSchema(false), "false": descriptionSchema(false)}, "additionalProperties": false}
	}
}

func objectSchema(props map[string]any, required []string) map[string]any {
	return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
}

func inputSchema(kind string) map[string]any {
	props := map[string]any{"state": descriptionSchema(false), "model": map[string]any{"type": "string", "description": "TypeSafe model ID; defaults to jev-latest"}}
	required := []string{"state"}
	if kind == "evaluate" {
		variants := []any{}
		for _, k := range []string{"choice", "score", "noul"} {
			r := []string{"type", "instructions"}
			if k != "noul" {
				r = append(r, "criteria")
			}
			variants = append(variants, objectSchema(map[string]any{"type": map[string]any{"type": "string", "const": k}, "instructions": descriptionSchema(false), "criteria": criteriaSchema(k)}, r))
		}
		props["questions"] = map[string]any{"type": "object", "minProperties": 1, "additionalProperties": map[string]any{"oneOf": variants}}
		required = append(required, "questions")
	} else {
		props["instructions"] = descriptionSchema(false)
		props["criteria"] = criteriaSchema(kind)
		required = append(required, "instructions")
		if kind != "noul" {
			required = append(required, "criteria")
		}
	}
	return objectSchema(props, required)
}

// Handler is stateless: credentials are read from each request, never shared
// between sessions. API key validity is checked by TypeSafe on tool calls.
func Handler(s *mcp.Server) http.Handler {
	return HandlerWithObservability(s, slog.New(slog.NewJSONHandler(io.Discard, nil)), NewMetrics())
}

func HandlerWithObservability(s *mcp.Server, logger *slog.Logger, metrics *Metrics) http.Handler {
	if metrics == nil {
		metrics = NewMetrics()
	}
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"status":"ok","service":"jev-mcphub"}`)
	})
	mux.Handle("GET /metrics", metrics)
	mux.Handle("/mcp", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Native MCP clients do not send Origin. Browser calls are deliberately
		// disabled; this also protects a locally published Docker port.
		if r.Header.Get("Origin") != "" {
			http.Error(w, "browser MCP requests are not allowed", http.StatusForbidden)
			return
		}
		if _, ok := Token(r.Header.Get("Authorization")); !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="jev"`)
			http.Error(w, "TypeSafe bearer token required", http.StatusUnauthorized)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		mcpHandler.ServeHTTP(w, r)
	}))
	base := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	})
	return withObservability(base, logger, metrics)
}
