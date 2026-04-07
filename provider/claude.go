package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type Claude struct {
	apiKey string
	model  string
	client *http.Client
}

func NewClaude(apiKey, model string) *Claude {
	return &Claude{
		apiKey: apiKey,
		model:  model,
		client: &http.Client{},
	}
}

func (c *Claude) Name() string          { return "claude" }
func (c *Claude) Model() string         { return c.model }
func (c *Claude) SetModel(model string) { c.model = model }
func (c *Claude) SupportsImages() bool  { return true }
func (c *Claude) SendWithStatus(ctx context.Context, systemPrompt string, messages []Message, tools []Tool, _ StatusCallback) (*Response, error) {
	return c.Send(ctx, systemPrompt, messages, tools)
}

func (c *Claude) Send(ctx context.Context, systemPrompt string, messages []Message, tools []Tool) (*Response, error) {
	reqBody := map[string]any{
		"model":      c.model,
		"max_tokens": 4096,
		"system":     systemPrompt,
		"messages":   c.convertMessages(messages),
	}

	if len(tools) > 0 {
		reqBody["tools"] = c.convertTools(tools)
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", strings.NewReader(string(data)))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.client.Do(req)
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
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}

	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	result := &Response{
		Usage: TokenUsage{
			PromptTokens: apiResp.Usage.InputTokens,
			OutputTokens: apiResp.Usage.OutputTokens,
			TotalTokens:  apiResp.Usage.InputTokens + apiResp.Usage.OutputTokens,
		},
	}
	for _, block := range apiResp.Content {
		switch block.Type {
		case "text":
			result.Content += block.Text
		case "tool_use":
			var args map[string]string
			json.Unmarshal(block.Input, &args)
			result.ToolCalls = append(result.ToolCalls, ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: args,
			})
		}
	}

	return result, nil
}

func (c *Claude) convertMessages(messages []Message) []map[string]any {
	var result []map[string]any

	for _, msg := range messages {
		if msg.Role == RoleSystem {
			continue
		}

		m := map[string]any{
			"role": string(msg.Role),
		}

		if msg.Role == RoleTool {
			m["role"] = "user"
			content := []map[string]any{
				{
					"type":        "tool_result",
					"tool_use_id": msg.ToolCallID,
					"content":     msg.Content,
				},
			}
			// Add image if present
			for _, part := range msg.Parts {
				if part.Type == ContentImage && len(part.ImageData) > 0 {
					content = append(content, map[string]any{
						"type": "image",
						"source": map[string]any{
							"type":       "base64",
							"media_type": part.MimeType,
							"data":       base64.StdEncoding.EncodeToString(part.ImageData),
						},
					})
				}
			}
			m["content"] = content
		} else if len(msg.Parts) > 0 {
			var content []map[string]any
			for _, part := range msg.Parts {
				switch part.Type {
				case ContentText:
					content = append(content, map[string]any{"type": "text", "text": part.Text})
				case ContentImage:
					content = append(content, map[string]any{
						"type": "image",
						"source": map[string]any{
							"type":       "base64",
							"media_type": part.MimeType,
							"data":       base64.StdEncoding.EncodeToString(part.ImageData),
						},
					})
				}
			}
			m["content"] = content
		} else {
			m["content"] = msg.Content
		}

		result = append(result, m)
	}

	return result
}

func (c *Claude) convertTools(tools []Tool) []map[string]any {
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
			"name":        t.Name,
			"description": t.Description,
			"input_schema": map[string]any{
				"type":       "object",
				"properties": properties,
				"required":   required,
			},
		}
		result = append(result, tool)
	}
	return result
}
