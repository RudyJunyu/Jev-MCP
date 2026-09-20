// Package typesafe implements the documented TypeSafe System One HTTP API.
package typesafe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const DefaultURL = "https://api.typesafe.ai/v1"
const DefaultModel = "jev-latest"

type Question struct {
	Type         string `json:"type"`
	Instructions any    `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"`
}

type Evaluation struct {
	State     any                 `json:"state"`
	Model     string              `json:"model,omitempty"`
	Questions map[string]Question `json:"questions"`
}

func description(v any, nullable bool) bool {
	switch v.(type) {
	case string, map[string]any, []any:
		return true
	case nil:
		return nullable
	}
	return false
}

func (e Evaluation) Validate() error {
	if !description(e.State, false) {
		return errors.New("state must be a string, object or array")
	}
	if len(e.Questions) == 0 {
		return errors.New("questions must contain at least one question")
	}
	for id, q := range e.Questions {
		if strings.TrimSpace(id) == "" {
			return errors.New("question IDs cannot be empty")
		}
		if !description(q.Instructions, false) {
			return errors.New("instructions must be a string, object or array")
		}
		switch q.Type {
		case "noul":
			if q.Criteria == nil {
				continue
			}
			criteria, ok := q.Criteria.(map[string]any)
			if !ok {
				return errors.New("noul criteria must be an object")
			}
			for key, v := range criteria {
				if (key != "true" && key != "false") || !description(v, false) {
					return errors.New("noul criteria accepts true/false descriptions only")
				}
			}
		case "choice":
			criteria, ok := q.Criteria.(map[string]any)
			if !ok || len(criteria) < 1 || len(criteria) > 255 {
				return errors.New("choice criteria must contain 1 to 255 options")
			}
			for _, v := range criteria {
				if !description(v, true) {
					return errors.New("choice descriptions must be string, object, array or null")
				}
			}
		case "score":
			criteria, ok := q.Criteria.([]any)
			if !ok || len(criteria) < 2 || len(criteria) > 10 {
				return errors.New("score criteria must contain 2 to 10 ordered levels")
			}
			for _, v := range criteria {
				if !description(v, false) {
					return errors.New("score levels must be string, object or array")
				}
			}
		default:
			return errors.New("question type must be noul, choice or score")
		}
	}
	return nil
}

type Client struct {
	baseURL string
	http    *http.Client
}

func New(baseURL string, timeout time.Duration) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("invalid TypeSafe base URL")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")) {
		return nil, errors.New("TypeSafe base URL requires HTTPS (except loopback)")
	}
	if timeout <= 0 {
		return nil, errors.New("timeout must be positive")
	}
	return &Client{strings.TrimRight(baseURL, "/"), &http.Client{
		Timeout: timeout,
		// Never forward a user's API key through a redirect.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (c *Client) Evaluate(ctx context.Context, token string, e Evaluation) (map[string]any, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	if e.Model == "" {
		e.Model = DefaultModel
	}
	data, err := json.Marshal(e)
	if err != nil {
		return nil, errors.New("invalid JSON input")
	}
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/systemone", bytes.NewReader(data))
		if err != nil {
			return nil, errors.New("cannot create TypeSafe request")
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		res, err := c.http.Do(req)
		if err != nil {
			return nil, errors.New("TypeSafe request failed or timed out")
		}
		body, readErr := io.ReadAll(io.LimitReader(res.Body, (4<<20)+1))
		res.Body.Close()
		if readErr != nil || len(body) > 4<<20 {
			return nil, errors.New("invalid or oversized TypeSafe response")
		}
		if (res.StatusCode == 429 || res.StatusCode == 529) && attempt < 2 {
			delay := time.Duration(1<<attempt) * time.Second
			if seconds, err := strconv.Atoi(res.Header.Get("Retry-After")); err == nil && seconds > 0 {
				delay = time.Duration(seconds) * time.Second
			} else if when, err := http.ParseTime(res.Header.Get("Retry-After")); err == nil && time.Until(when) > 0 {
				delay = time.Until(when)
			}
			if delay > 30*time.Second {
				return nil, errors.New("TypeSafe rate limited; retry later")
			}
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, errors.New("TypeSafe request canceled")
			case <-timer.C:
			}
			continue
		}
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			// Upstream error bodies can echo state or credentials. Never expose them.
			switch res.StatusCode {
			case 401, 403:
				return nil, errors.New("TypeSafe rejected the token; check your API key and access")
			case 422:
				return nil, errors.New("TypeSafe rejected the input; check state, questions and model")
			default:
				return nil, fmt.Errorf("TypeSafe returned HTTP %d; retry later", res.StatusCode)
			}
		}
		var result map[string]any
		if json.Unmarshal(body, &result) != nil || result == nil {
			return nil, errors.New("TypeSafe returned invalid JSON")
		}
		if _, ok := result["answers"].(map[string]any); !ok {
			return nil, errors.New("TypeSafe response is missing answers")
		}
		return result, nil
	}
	return nil, errors.New("TypeSafe retry limit exceeded")
}
