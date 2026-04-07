package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type Ollama struct {
	host   string
	model  string
	client *http.Client
}

func NewOllama(host, model string) *Ollama {
	return &Ollama{
		host:   strings.TrimRight(host, "/"),
		model:  model,
		client: &http.Client{},
	}
}

func (o *Ollama) Name() string          { return "ollama" }
func (o *Ollama) Model() string         { return o.model }
func (o *Ollama) SetModel(model string) { o.model = model }
func (o *Ollama) SupportsImages() bool  { return false }
func (o *Ollama) SendWithStatus(ctx context.Context, systemPrompt string, messages []Message, tools []Tool, _ StatusCallback) (*Response, error) {
	return o.Send(ctx, systemPrompt, messages, tools)
}

func (o *Ollama) Send(ctx context.Context, systemPrompt string, messages []Message, tools []Tool) (*Response, error) {
	apiMessages := o.convertMessages(systemPrompt, messages)

	reqBody := map[string]any{
		"model":    o.model,
		"messages": apiMessages,
		"stream":   false,
	}

	if len(tools) > 0 {
		reqBody["tools"] = o.convertTools(tools)
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/api/chat", o.host)
	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(data)))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	var apiResp struct {
		Message struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Function struct {
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
		PromptEvalCount int `json:"prompt_eval_count"`
		EvalCount       int `json:"eval_count"`
	}

	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	result := &Response{
		Content: apiResp.Message.Content,
		Usage: TokenUsage{
			PromptTokens: apiResp.PromptEvalCount,
			OutputTokens: apiResp.EvalCount,
			TotalTokens:  apiResp.PromptEvalCount + apiResp.EvalCount,
		},
	}

	for i, tc := range apiResp.Message.ToolCalls {
		var args map[string]string
		json.Unmarshal(tc.Function.Arguments, &args)
		result.ToolCalls = append(result.ToolCalls, ToolCall{
			ID:        fmt.Sprintf("ollama_%d", i),
			Name:      tc.Function.Name,
			Arguments: args,
		})
	}

	return result, nil
}

func (o *Ollama) convertMessages(systemPrompt string, messages []Message) []map[string]any {
	var result []map[string]any

	result = append(result, map[string]any{
		"role":    "system",
		"content": systemPrompt,
	})

	for _, msg := range messages {
		if msg.Role == RoleSystem {
			continue
		}

		role := string(msg.Role)
		if msg.Role == RoleTool {
			role = "tool"
		}

		result = append(result, map[string]any{
			"role":    role,
			"content": msg.Content,
		})
	}

	return result
}

func (o *Ollama) convertTools(tools []Tool) []map[string]any {
	var result []map[string]any
	for _, t := range tools {
		properties := map[string]any{}
		var required []string
		for _, p := range t.Parameters {
			properties[p.Name] = map[string]any{
				"type":        p.Type,
				"description": p.Description,
			}
			if p.Required {
				required = append(required, p.Name)
			}
		}

		tool := map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters": map[string]any{
					"type":       "object",
					"properties": properties,
					"required":   required,
				},
			},
		}
		result = append(result, tool)
	}
	return result
}
