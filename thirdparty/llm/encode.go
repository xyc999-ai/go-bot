package llm

import (
	"encoding/json"
	"fmt"
	"strings"
)

func encodeResponsesInput(input []Item) ([]any, error) {
	out := make([]any, 0, len(input))
	for _, item := range input {
		switch item.Type {
		case "message":
			parts, err := responsesParts(item.Role, item.Content)
			if err != nil {
				return nil, err
			}
			value := map[string]any{"type": "message", "role": item.Role, "content": parts}
			if item.ID != "" {
				value["id"] = item.ID
			}
			out = append(out, value)
		case "reasoning":
			value := map[string]any{"type": "reasoning"}
			if item.ID != "" {
				value["id"] = item.ID
			}
			if len(item.Content) > 0 {
				parts := make([]any, 0, len(item.Content))
				for _, block := range item.Content {
					if block.Type == "text" {
						parts = append(parts, map[string]string{"type": "reasoning_text", "text": block.Text})
					}
				}
				value["content"] = parts
			}
			out = append(out, value)
		case "tool_call":
			value := map[string]any{"type": "function_call", "call_id": item.CallID, "name": item.Name, "arguments": item.Arguments}
			if item.ID != "" {
				value["id"] = item.ID
			}
			out = append(out, value)
		case "tool_result":
			output, err := responsesToolOutput(item.Content)
			if err != nil {
				return nil, fmt.Errorf("tool result %q: %w", item.CallID, err)
			}
			out = append(out, map[string]any{"type": "function_call_output", "call_id": item.CallID, "output": output})
		default:
			if len(item.Raw) > 0 {
				out = append(out, json.RawMessage(item.Raw))
			}
		}
	}
	return out, nil
}

func responsesToolOutput(blocks []ContentBlock) (any, error) {
	texts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Type != "text" {
			return responsesParts("user", blocks)
		}
		texts = append(texts, block.Text)
	}
	// Strings also work with Responses providers that do not accept content
	// arrays in tool outputs. Keep multimodal results as ordered content blocks.
	return strings.Join(texts, "\n"), nil
}

func responsesParts(role string, blocks []ContentBlock) ([]any, error) {
	parts := make([]any, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case "text":
			kind := "input_text"
			if role == "assistant" {
				kind = "output_text"
			}
			parts = append(parts, map[string]string{"type": kind, "text": block.Text})
		case "image":
			if role != "user" {
				return nil, fmt.Errorf("Responses input_image requires user role")
			}
			if block.Source == nil || block.Source.Type != "url" || strings.TrimSpace(block.Source.URL) == "" {
				return nil, fmt.Errorf("image URL must be resolved before LLM request")
			}
			value := map[string]any{"type": "input_image", "image_url": block.Source.URL}
			if block.Detail != "" {
				value["detail"] = block.Detail
			}
			parts = append(parts, value)
		default:
			return nil, fmt.Errorf("unsupported content block type %q", block.Type)
		}
	}
	return parts, nil
}

func encodeChatMessages(input []Item) ([]any, error) {
	out := make([]any, 0, len(input))
	pendingReasoning := ""
	lastAssistant := -1
	appendPending := func() {
		if pendingReasoning != "" {
			out = append(out, map[string]any{"role": "assistant", "content": "", "reasoning_content": pendingReasoning})
			lastAssistant = len(out) - 1
			pendingReasoning = ""
		}
	}
	for _, item := range input {
		switch item.Type {
		case "message":
			if item.Role != "assistant" {
				appendPending()
			}
			parts := make([]any, 0, len(item.Content))
			for _, block := range item.Content {
				switch block.Type {
				case "text":
					parts = append(parts, map[string]string{"type": "text", "text": block.Text})
				case "image":
					if item.Role == "assistant" {
						return nil, fmt.Errorf("Chat Completions cannot replay assistant image blocks")
					}
					if block.Source == nil || block.Source.Type != "url" || block.Source.URL == "" {
						return nil, fmt.Errorf("image URL must be resolved before LLM request")
					}
					parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": block.Source.URL, "detail": defaultDetail(block.Detail)}})
				}
			}
			value := map[string]any{"role": item.Role, "content": parts}
			if item.Role == "assistant" {
				value["content"] = textContent(item.Content)
				if pendingReasoning != "" {
					value["reasoning_content"] = pendingReasoning
					pendingReasoning = ""
				}
			} else if len(parts) == 1 {
				if text, ok := parts[0].(map[string]string); ok {
					value["content"] = text["text"]
				}
			}
			out = append(out, value)
			if item.Role == "assistant" {
				lastAssistant = len(out) - 1
			} else {
				lastAssistant = -1
			}
		case "reasoning":
			text := textContent(item.Content)
			pendingReasoning += text
		case "tool_call":
			appendPending()
			if lastAssistant != len(out)-1 || lastAssistant < 0 {
				out = append(out, map[string]any{"role": "assistant", "content": ""})
				lastAssistant = len(out) - 1
			}
			assistant := out[lastAssistant].(map[string]any)
			calls, _ := assistant["tool_calls"].([]any)
			assistant["tool_calls"] = append(calls, map[string]any{"id": item.CallID, "type": "function", "function": map[string]any{"name": item.Name, "arguments": item.Arguments}})
		case "tool_result":
			appendPending()
			for _, block := range item.Content {
				if block.Type != "text" {
					return nil, fmt.Errorf("Chat Completions cannot replay %q tool result blocks", block.Type)
				}
			}
			out = append(out, map[string]any{"role": "tool", "tool_call_id": item.CallID, "content": textContent(item.Content)})
			lastAssistant = -1
		}
	}
	appendPending()
	return out, nil
}

func defaultDetail(value string) string {
	if value == "" {
		return "auto"
	}
	return value
}

func textContent(blocks []ContentBlock) string {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Type == "text" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "")
}

func encodeResponsesTools(tools []Tool) []any {
	out := make([]any, 0, len(tools))
	for _, tool := range tools {
		value := map[string]any{"type": "function", "name": tool.Name, "description": tool.Description, "parameters": toolParameters(tool)}
		if tool.Strict != nil {
			value["strict"] = *tool.Strict
		}
		out = append(out, value)
	}
	return out
}

func encodeChatTools(tools []Tool) []any {
	out := make([]any, 0, len(tools))
	for _, tool := range tools {
		function := map[string]any{"name": tool.Name, "description": tool.Description, "parameters": toolParameters(tool)}
		if tool.Strict != nil {
			function["strict"] = *tool.Strict
		}
		out = append(out, map[string]any{"type": "function", "function": function})
	}
	return out
}

func toolParameters(tool Tool) any {
	if len(tool.Parameters) == 0 {
		return map[string]any{}
	}
	return json.RawMessage(tool.Parameters)
}
