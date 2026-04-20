package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/eformat/openshift-skills-plugin/pkg/kube"
	"github.com/eformat/openshift-skills-plugin/pkg/mlflow"
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
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Stream      bool          `json:"stream"`
}

// AgentOptions holds optional parameters for the agent loop.
type AgentOptions struct {
	Temperature    float64
	MaxTokens      int
	History        []ChatMessage // Prior conversation messages (inserted between system prompt and user message)
	Source         string        // Trace source label: "chat", "schedule-container", "schedule-llm"
	ExperimentName string        // MLflow experiment name (e.g. chat session name)
}

// AgentResult contains the response and metrics from an agent loop execution.
type AgentResult struct {
	Response   string
	Iterations int
	ToolCalls  int
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
			Description: "Execute a shell command and return stdout/stderr. Use this to run oc, kubectl, curl, or any CLI command. IMPORTANT: For multi-line scripts or commands with quotes/special characters, write the script to a temp file first using a heredoc (cat > /tmp/script.sh << 'SCRIPT'\\n...\\nSCRIPT) then run it with sh /tmp/script.sh. This avoids JSON escaping issues.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"command": map[string]interface{}{
						"type":        "string",
						"description": "The shell command to execute. For complex multi-line scripts, write to a temp file first.",
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
func RunAgentLoop(ctx context.Context, completionsURL, token, model, systemPrompt, userMessage string, maxIterations int, shellExec ShellExecutor, opts *AgentOptions) (*AgentResult, error) {
	if maxIterations <= 0 {
		maxIterations = 15
	}

	temperature := 0.7
	maxTokens := 0
	source := "agent"
	experimentName := "openshift-skills"
	if opts != nil {
		if opts.Temperature > 0 {
			temperature = opts.Temperature
		}
		if opts.MaxTokens > 0 {
			maxTokens = opts.MaxTokens
		}
		if opts.Source != "" {
			source = opts.Source
		}
		if opts.ExperimentName != "" {
			experimentName = opts.ExperimentName
		}
	}

	// Start root AGENT trace span (routed to the per-experiment MLflow tracer)
	ctx, agentSpan := mlflow.StartAgentSpan(ctx, experimentName, model, source, userMessage, temperature, maxTokens)

	messages := []ChatMessage{
		{Role: "system", Content: systemPrompt},
	}
	// Insert prior conversation history if provided
	if opts != nil && len(opts.History) > 0 {
		messages = append(messages, opts.History...)
	}
	messages = append(messages, ChatMessage{Role: "user", Content: userMessage})

	client := &http.Client{Timeout: 120 * time.Second}
	totalToolCalls := 0

	for iteration := 0; iteration < maxIterations; iteration++ {
		log.Printf("Agent iteration %d/%d, messages=%d", iteration+1, maxIterations, len(messages))

		// Start CHAT_MODEL span for this LLM call
		_, llmSpan := mlflow.StartLLMSpan(ctx, model, iteration)

		// Call LLM with tools
		resp, err := callLLM(client, completionsURL, token, model, messages, agentTools, temperature, maxTokens)
		if err != nil {
			llmErr := fmt.Errorf("iteration %d: LLM call failed: %w", iteration+1, err)
			mlflow.EndSpanError(llmSpan, llmErr)
			mlflow.EndSpanError(agentSpan, llmErr)
			return nil, llmErr
		}

		if len(resp.Choices) == 0 {
			llmErr := fmt.Errorf("iteration %d: no choices in response", iteration+1)
			mlflow.EndSpanError(llmSpan, llmErr)
			mlflow.EndSpanError(agentSpan, llmErr)
			return nil, llmErr
		}

		choice := resp.Choices[0]
		assistantMsg := choice.Message

		// End LLM span with the response
		mlflow.EndSpanOK(llmSpan, assistantMsg.Content)

		// Add assistant message to history
		messages = append(messages, assistantMsg)

		// If no tool calls, we're done — return the final text
		if len(assistantMsg.ToolCalls) == 0 {
			content := stripThinkTags(assistantMsg.Content)
			if content == "" {
				// Fall back to the last non-empty assistant content or tool output
				for i := len(messages) - 2; i >= 0; i-- {
					if messages[i].Role == "assistant" && stripThinkTags(messages[i].Content) != "" {
						content = stripThinkTags(messages[i].Content)
						break
					}
					if messages[i].Role == "tool" && messages[i].Content != "" {
						content = "Last tool output:\n" + messages[i].Content
						break
					}
				}
			}
			if content == "" {
				content = "(Agent completed with no final response)"
			}
			log.Printf("Agent completed after %d iterations", iteration+1)
			result := &AgentResult{Response: content, Iterations: iteration + 1, ToolCalls: totalToolCalls}
			mlflow.EndSpanOK(agentSpan, content)
			return result, nil
		}

		// Execute each tool call
		for _, tc := range assistantMsg.ToolCalls {
			totalToolCalls++

			// Start TOOL span
			_, toolSpan := mlflow.StartToolSpan(ctx, tc.Function.Name, tc.Function.Arguments)

			result := executeToolCall(tc, shellExec)
			log.Printf("Tool %s(%s): %d bytes output", tc.Function.Name, truncate(tc.Function.Arguments, 80), len(result))

			mlflow.EndSpanOK(toolSpan, result)

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
		content := stripThinkTags(lastContent) + "\n\n(Maximum iterations reached)"
		mlflow.EndSpanOK(agentSpan, content)
		return &AgentResult{Response: content, Iterations: maxIterations, ToolCalls: totalToolCalls}, nil
	}
	maxIterErr := fmt.Errorf("maximum iterations (%d) reached without completing the task", maxIterations)
	mlflow.EndSpanError(agentSpan, maxIterErr)
	return nil, maxIterErr
}

func callLLM(client *http.Client, completionsURL, token, model string, messages []ChatMessage, tools []Tool, temperature float64, maxTokens int) (*ChatResponse, error) {
	req := ChatRequest{
		Model:       model,
		Messages:    messages,
		Tools:       tools,
		Temperature: temperature,
		MaxTokens:   maxTokens,
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
		rawArgs := tc.Function.Arguments
		if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
			// Try to repair truncated/malformed JSON from the LLM
			repaired := repairToolCallJSON(rawArgs)
			if repairErr := json.Unmarshal([]byte(repaired), &args); repairErr != nil {
				log.Printf("Failed to parse tool call arguments (original: %s, repaired: %s): %v", truncate(rawArgs, 200), truncate(repaired, 200), repairErr)
				return fmt.Sprintf("Error parsing arguments: %v. The command JSON was malformed. Please use simpler commands or write scripts to a temp file.", err)
			}
			log.Printf("Repaired malformed tool call JSON")
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
	// Execute in the agent-shell sidecar container (no kube credentials)
	podName := os.Getenv("POD_NAME")
	podNS := os.Getenv("POD_NAMESPACE")
	container := os.Getenv("AGENT_SHELL_CONTAINER")
	if podName != "" && podNS != "" && container != "" {
		ep := &kube.ExecutorPod{Name: podName, Namespace: podNS, Container: container}
		output, err := kube.ExecCommand(ep, command, 60*time.Second)
		if err != nil {
			return "Error: " + err.Error()
		}
		return output
	}

	// Fallback for dev mode (no kube client) — run locally with stripped env
	log.Printf("Warning: no sidecar configured, executing locally")
	ctx, cancel := newTimeoutContext(60 * time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	var env []string
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "KUBERNETES_") {
			env = append(env, e)
		}
	}
	env = append(env, "KUBECONFIG=/dev/null")
	cmd.Env = env

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

// repairToolCallJSON attempts to fix common JSON issues in LLM-generated tool call arguments.
// Models often produce truncated or improperly escaped JSON, especially for multi-line shell commands.
func repairToolCallJSON(raw string) string {
	raw = strings.TrimSpace(raw)

	// If it already parses, return as-is
	var test json.RawMessage
	if json.Unmarshal([]byte(raw), &test) == nil {
		return raw
	}

	// Try to extract the command value using a regex approach
	// Match {"command": " then capture everything until we can't parse anymore
	cmdPrefix := `"command"`
	idx := strings.Index(raw, cmdPrefix)
	if idx < 0 {
		return raw
	}

	// Find the opening quote of the value
	afterKey := raw[idx+len(cmdPrefix):]
	colonIdx := strings.Index(afterKey, ":")
	if colonIdx < 0 {
		return raw
	}
	afterColon := strings.TrimSpace(afterKey[colonIdx+1:])
	if len(afterColon) == 0 || afterColon[0] != '"' {
		return raw
	}

	// Extract the string value, handling escapes manually
	var command strings.Builder
	i := 1 // skip opening quote
	for i < len(afterColon) {
		ch := afterColon[i]
		if ch == '\\' && i+1 < len(afterColon) {
			next := afterColon[i+1]
			switch next {
			case '"', '\\', '/':
				command.WriteByte(next)
			case 'n':
				command.WriteByte('\n')
			case 't':
				command.WriteByte('\t')
			case 'r':
				command.WriteByte('\r')
			default:
				command.WriteByte('\\')
				command.WriteByte(next)
			}
			i += 2
			continue
		}
		if ch == '"' {
			// Check if this is the real closing quote (followed by } or whitespace+})
			rest := strings.TrimSpace(afterColon[i+1:])
			if rest == "" || rest[0] == '}' || rest[0] == ',' {
				break
			}
			// Otherwise it's an unescaped quote in the middle — include it
			command.WriteByte(ch)
			i++
			continue
		}
		command.WriteByte(ch)
		i++
	}

	// Rebuild valid JSON
	cmdStr := command.String()
	rebuilt, err := json.Marshal(map[string]string{"command": cmdStr})
	if err != nil {
		return raw
	}
	return string(rebuilt)
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
