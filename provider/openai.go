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

type OpenAI struct {
	apiKey string
	model  string
	client *http.Client
}

func NewOpenAI(apiKey, model string) *OpenAI {
	return &OpenAI{
		apiKey: apiKey,
		model:  model,
		client: &http.Client{},
	}
}

func (o *OpenAI) Name() string          { return "openai" }
func (o *OpenAI) Model() string         { return o.model }
func (o *OpenAI) SetModel(model string) { o.model = model }
func (o *OpenAI) SupportsImages() bool  { return true }
func (o *OpenAI) SendWithStatus(ctx context.Context, systemPrompt string, messages []Message, tools []Tool, _ StatusCallback) (*Response, error) {
	return o.Send(ctx, systemPrompt, messages, tools)
}

func (o *OpenAI) Send(ctx context.Context, systemPrompt string, messages []Message, tools []Tool) (*Response, error) {
	apiMessages := o.convertMessages(systemPrompt, messages)

	reqBody := map[string]any{
		"model":    o.model,
		"messages": apiMessages,
	}

	if len(tools) > 0 {
		reqBody["tools"] = o.convertTools(tools)
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/chat/completions", strings.NewReader(string(data)))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.apiKey)

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
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}

	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if len(apiResp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	choice := apiResp.Choices[0]
	result := &Response{
		Content: choice.Message.Content,
		Usage: TokenUsage{
			PromptTokens: apiResp.Usage.PromptTokens,
			OutputTokens: apiResp.Usage.CompletionTokens,
			TotalTokens:  apiResp.Usage.TotalTokens,
		},
	}

	for _, tc := range choice.Message.ToolCalls {
		var args map[string]string
		json.Unmarshal([]byte(tc.Function.Arguments), &args)
		result.ToolCalls = append(result.ToolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: args,
		})
	}

	return result, nil
}

func (o *OpenAI) convertMessages(systemPrompt string, messages []Message) []map[string]any {
	var result []map[string]any

	// System message
	result = append(result, map[string]any{
		"role":    "system",
		"content": systemPrompt,
	})

	for _, msg := range messages {
		if msg.Role == RoleSystem {
			continue
		}

		m := map[string]any{}

		switch msg.Role {
		case RoleTool:
			m["role"] = "tool"
			m["tool_call_id"] = msg.ToolCallID
			m["content"] = msg.Content
		case RoleAssistant:
			m["role"] = "assistant"
			m["content"] = msg.Content
		case RoleUser:
			m["role"] = "user"
			if len(msg.Parts) > 0 {
				var content []map[string]any
				for _, part := range msg.Parts {
					switch part.Type {
					case ContentText:
						content = append(content, map[string]any{"type": "text", "text": part.Text})
					case ContentImage:
						content = append(content, map[string]any{
							"type": "image_url",
							"image_url": map[string]any{
								"url": fmt.Sprintf("data:%s;base64,%s", part.MimeType, base64.StdEncoding.EncodeToString(part.ImageData)),
							},
						})
					}
				}
				m["content"] = content
			} else {
				m["content"] = msg.Content
			}
		}

		result = append(result, m)
	}

	return result
}

func (o *OpenAI) convertTools(tools []Tool) []map[string]any {
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
