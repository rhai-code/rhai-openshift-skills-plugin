package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Tool definitions for the LLM
type ToolFunction struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// Chat message types for the OpenAI-compatible API
type ChatMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ChatRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Tools       []Tool        `json:"tools,omitempty"`
	Temperature float64       `json:"temperature,omitempty"`
	Stream      bool          `json:"stream"`
}

type ChatChoice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

type ChatResponse struct {
	ID      string       `json:"id"`
	Choices []ChatChoice `json:"choices"`
}

// Available tools for the agent
var agentTools = []Tool{
	{
		Type: "function",
		Function: ToolFunction{
			Name:        "shell",
			Description: "Execute a shell command and return stdout/stderr. Use this to run oc, kubectl, curl, or any CLI command to inspect or interact with the cluster.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"command": map[string]interface{}{
						"type":        "string",
						"description": "The shell command to execute",
					},
				},
				"required": []string{"command"},
			},
		},
	},
}

// ShellExecutor is a function that executes a shell command and returns the output.
// This allows the caller to control where commands run (locally, in a pod, etc).
type ShellExecutor func(command string) string

// RunAgentLoop executes the agentic loop:
// 1. Send skill content + task to LLM with tool definitions
// 2. LLM responds with tool calls or final answer
// 3. Execute tool calls via the provided shellExec function
// 4. Repeat until done or max iterations
//
// If shellExec is nil, commands are executed locally via sh -c.
func RunAgentLoop(completionsURL, token, model, systemPrompt, userMessage string, maxIterations int, shellExec ShellExecutor) (string, error) {
	if maxIterations <= 0 {
		maxIterations = 15
	}

	messages := []ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userMessage},
	}

	client := &http.Client{Timeout: 120 * time.Second}

	for iteration := 0; iteration < maxIterations; iteration++ {
		log.Printf("Agent iteration %d/%d, messages=%d", iteration+1, maxIterations, len(messages))

		// Call LLM with tools
		resp, err := callLLM(client, completionsURL, token, model, messages, agentTools)
		if err != nil {
			return "", fmt.Errorf("iteration %d: LLM call failed: %w", iteration+1, err)
		}

		if len(resp.Choices) == 0 {
			return "", fmt.Errorf("iteration %d: no choices in response", iteration+1)
		}

		choice := resp.Choices[0]
		assistantMsg := choice.Message

		// Add assistant message to history
		messages = append(messages, assistantMsg)

		// If no tool calls, we're done — return the final text
		if len(assistantMsg.ToolCalls) == 0 {
			content := stripThinkTags(assistantMsg.Content)
			if content == "" {
				content = "(Agent completed with no final response)"
			}
			log.Printf("Agent completed after %d iterations", iteration+1)
			return content, nil
		}

		// Execute each tool call
		for _, tc := range assistantMsg.ToolCalls {
			result := executeToolCall(tc, shellExec)
			log.Printf("Tool %s(%s): %d bytes output", tc.Function.Name, truncate(tc.Function.Arguments, 80), len(result))

			// Add tool result to messages
			messages = append(messages, ChatMessage{
				Role:       "tool",
				Content:    result,
				ToolCallID: tc.ID,
				Name:       tc.Function.Name,
			})
		}
	}

	// Collect any partial results
	var lastContent string
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" && messages[i].Content != "" {
			lastContent = messages[i].Content
			break
		}
	}
	if lastContent != "" {
		return stripThinkTags(lastContent) + "\n\n(Maximum iterations reached)", nil
	}
	return "", fmt.Errorf("maximum iterations (%d) reached without completing the task", maxIterations)
}

func callLLM(client *http.Client, completionsURL, token, model string, messages []ChatMessage, tools []Tool) (*ChatResponse, error) {
	req := ChatRequest{
		Model:       model,
		Messages:    messages,
		Tools:       tools,
		Temperature: 0.7,
		Stream:      false,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", completionsURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return &chatResp, nil
}

func executeToolCall(tc ToolCall, shellExec ShellExecutor) string {
	switch tc.Function.Name {
	case "shell":
		var args struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			return fmt.Sprintf("Error parsing arguments: %v", err)
		}
		if args.Command == "" {
			return "Error: no command provided"
		}
		log.Printf("Agent executing shell: %s", truncate(args.Command, 200))
		if shellExec != nil {
			return shellExec(args.Command)
		}
		return executeLocalShell(args.Command)
	default:
		return fmt.Sprintf("Error: unknown tool '%s'", tc.Function.Name)
	}
}

func executeLocalShell(command string) string {
	ctx, cancel := newTimeoutContext(60 * time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	var result []string
	if err != nil {
		result = append(result, fmt.Sprintf("Exit code: %d", cmd.ProcessState.ExitCode()))
	}
	if stdout.Len() > 0 {
		out := stdout.String()
		if len(out) > 10000 {
			out = out[:10000] + "\n...(truncated)"
		}
		result = append(result, "STDOUT:\n"+out)
	}
	if stderr.Len() > 0 {
		errOut := stderr.String()
		if len(errOut) > 5000 {
			errOut = errOut[:5000] + "\n...(truncated)"
		}
		result = append(result, "STDERR:\n"+errOut)
	}

	if len(result) == 0 {
		return "Command completed with no output"
	}
	return strings.Join(result, "\n")
}

var thinkTagRegex = regexp.MustCompile(`(?s)<think>.*?</think>`)

func stripThinkTags(text string) string {
	if text == "" {
		return text
	}
	return strings.TrimSpace(thinkTagRegex.ReplaceAllString(text, ""))
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
