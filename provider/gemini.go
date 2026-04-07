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

type Gemini struct {
	apiKey string
	model  string
	client *http.Client
}

func NewGemini(apiKey, model string) *Gemini {
	return &Gemini{
		apiKey: apiKey,
		model:  model,
		client: &http.Client{},
	}
}

func (g *Gemini) Name() string          { return "gemini" }
func (g *Gemini) Model() string         { return g.model }
func (g *Gemini) SetModel(model string) { g.model = model }
func (g *Gemini) SupportsImages() bool  { return true }
func (g *Gemini) SendWithStatus(ctx context.Context, systemPrompt string, messages []Message, tools []Tool, _ StatusCallback) (*Response, error) {
	return g.Send(ctx, systemPrompt, messages, tools)
}

func (g *Gemini) Send(ctx context.Context, systemPrompt string, messages []Message, tools []Tool) (*Response, error) {
	reqBody := map[string]any{
		"contents": g.convertMessages(messages),
		"systemInstruction": map[string]any{
			"parts": []map[string]any{
				{"text": systemPrompt},
			},
		},
	}

	if len(tools) > 0 {
		reqBody["tools"] = []map[string]any{
			{"functionDeclarations": g.convertTools(tools)},
		}
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", g.model, g.apiKey)
	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(data)))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(req)
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
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text         string `json:"text"`
					FunctionCall *struct {
						Name string          `json:"name"`
						Args json.RawMessage `json:"args"`
					} `json:"functionCall"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}

	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if len(apiResp.Candidates) == 0 {
		return nil, fmt.Errorf("no candidates in response")
	}

	result := &Response{}
	for _, part := range apiResp.Candidates[0].Content.Parts {
		if part.Text != "" {
			result.Content += part.Text
		}
		if part.FunctionCall != nil {
			var args map[string]string
			json.Unmarshal(part.FunctionCall.Args, &args)
			result.ToolCalls = append(result.ToolCalls, ToolCall{
				ID:        fmt.Sprintf("gemini_%s", part.FunctionCall.Name),
				Name:      part.FunctionCall.Name,
				Arguments: args,
			})
		}
	}

	return result, nil
}

func (g *Gemini) convertMessages(messages []Message) []map[string]any {
	var result []map[string]any

	for _, msg := range messages {
		if msg.Role == RoleSystem {
			continue
		}

		role := "user"
		if msg.Role == RoleAssistant {
			role = "model"
		}

		var parts []map[string]any

		if msg.Role == RoleTool {
			parts = append(parts, map[string]any{
				"functionResponse": map[string]any{
					"name":     msg.ToolCallID,
					"response": map[string]any{"result": msg.Content},
				},
			})
			result = append(result, map[string]any{"role": "function", "parts": parts})
			continue
		}

		// Handle multimodal content
		if len(msg.Parts) > 0 {
			for _, part := range msg.Parts {
				switch part.Type {
				case ContentText:
					parts = append(parts, map[string]any{"text": part.Text})
				case ContentImage:
					parts = append(parts, map[string]any{
						"inlineData": map[string]any{
							"mimeType": part.MimeType,
							"data":     base64.StdEncoding.EncodeToString(part.ImageData),
						},
					})
				}
			}
		} else {
			parts = append(parts, map[string]any{"text": msg.Content})
		}

		result = append(result, map[string]any{"role": role, "parts": parts})
	}

	return result
}

func (g *Gemini) convertTools(tools []Tool) []map[string]any {
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

		fn := map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"parameters": map[string]any{
				"type":       "object",
				"properties": properties,
				"required":   required,
			},
		}
		result = append(result, fn)
	}
	return result
}
