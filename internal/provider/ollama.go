package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// ollamaModel implements model.LLM for the native Ollama API.
type ollamaModel struct {
	modelName     string
	client        *ollamaClient
	thinkingLevel string // "none", "low", "medium", "high"
}

// Ollama's two default endpoints. Which one a model belongs to is decided by
// its tag, never by whether a key happens to be exported.
const (
	ollamaLocalURL = "http://localhost:11434"
	ollamaCloudURL = "https://api.ollama.com"
)

// OllamaRouting carries the facts that decide which Ollama server a model
// reaches. They travel together because every caller that builds an Ollama
// client — the CLI, the TUI's /model switch, the ACP server, ping — has to
// reach the same answer; each one deciding for itself is how the
// key-as-destination bug survived in three places after ResolveOllamaEndpoint
// stopped making that mistake.
type OllamaRouting struct {
	// Model is the Ollama model name, tag included.
	Model string
	// BaseURL is an explicit endpoint (OLLAMA_HOST or --url), empty if unset.
	BaseURL string
	// APIKey is OLLAMA_API_KEY, empty if unset.
	APIKey string
	// ForceLocal records that the caller named the model with the explicit
	// ollama/ prefix, which the CLI help documents as "Ollama, local".
	ForceLocal bool
}

// ResolveOllamaEndpoint picks the server a model should be sent to.
//
// The order is:
//
//  1. An explicit BaseURL (OLLAMA_HOST, --url) always wins — that is how
//     someone points at another machine, a container, or an authenticated proxy.
//  2. The ollama/ prefix means the local daemon. The prefix is the one way a
//     user states the destination outright, so a tag cannot overrule it:
//     ollama/deepseek-v4-flash:0731-cloud is a request for the model of that
//     name on localhost, and answering it with api.ollama.com ignores what was
//     asked for.
//  3. A cloud-tagged model with a key goes to api.ollama.com.
//  4. A cloud-tagged model with no key goes to the local daemon, which has
//     proxied cloud models on the user's `ollama signin` identity since 0.12.
//     api.ollama.com rejects an unauthenticated request with 401 before it
//     looks at the model, so the alternative is not a different result but a
//     guaranteed failure.
//  5. Everything else is local.
//
// Rule 4 is the only one that reads the key, and it only ever routes *away*
// from the cloud. That is deliberate, and it is not the bug this function was
// written to kill: routing used to follow the presence of a key in the other
// direction, which made OLLAMA_API_KEY a global switch — exporting it once for
// a :cloud model silently sent every *local* model to api.ollama.com too, where
// a privately pulled name like qwen3.8:27b-mlx does not exist. A key still
// never promotes an untagged model to the cloud; its absence only declines to
// send a request that cannot be served.
//
// Who receives the key is deliberately not decided here. It still goes to
// whatever endpoint is chosen whenever one is set, unchanged, because an
// authenticated daemon may be reached over loopback as easily as over the
// network and this function cannot tell the two apart.
func ResolveOllamaEndpoint(r OllamaRouting) string {
	if endpoint := normalizeBaseURL(r.BaseURL); endpoint != "" {
		return endpoint
	}
	if r.ForceLocal {
		return ollamaLocalURL
	}
	if IsOllamaCloudModel(r.Model) && r.APIKey != "" {
		return ollamaCloudURL
	}
	return ollamaLocalURL
}

// IsOllamaCloudEndpoint reports whether an already-resolved endpoint is
// ollama.com's hosted API.
//
// Callers need this to tell a reachability problem from a credential problem:
// the TCP-and-GET health check in CheckOllama is a statement about a daemon on
// a host someone controls, and running it against api.ollama.com turns a
// missing OLLAMA_API_KEY into a misleading "ollama not reachable".
//
// It matches on host, not on a string compare with ollamaCloudURL, so an
// OLLAMA_HOST pointed at the cloud API by hand is recognized too.
func IsOllamaCloudEndpoint(baseURL string) bool {
	u, err := url.Parse(normalizeBaseURL(baseURL))
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "api.ollama.com" || host == "ollama.com"
}

// NewOllama creates an Ollama model.LLM using the native Ollama Go client.
// The server is chosen by ResolveOllamaEndpoint from the routing facts in r.
// thinkingLevel controls extended thinking: "none", "low", "medium", "high".
func NewOllama(_ context.Context, r OllamaRouting, thinkingLevel string, opts *LLMOptions) (model.LLM, error) {
	modelName := r.Model
	if modelName == "" {
		return nil, fmt.Errorf("model name is required")
	}
	apiKey := r.APIKey
	baseURL := ResolveOllamaEndpoint(r)
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid Ollama URL %q: %w", baseURL, err)
	}
	httpClient, err := BuildHTTPClient(opts, 10*time.Minute)
	if err != nil {
		return nil, err
	}
	// Inject Bearer token for the ollama.com cloud API when an API key is provided.
	if apiKey != "" {
		baseTransport := httpClient.Transport
		if baseTransport == nil {
			baseTransport = http.DefaultTransport
		}
		httpClient.Transport = &bearerTransport{
			base:  baseTransport,
			token: apiKey,
		}
	}
	client := &ollamaClient{baseURL: u, httpClient: httpClient}
	return &ollamaModel{
		modelName:     modelName,
		client:        client,
		thinkingLevel: thinkingLevel,
	}, nil
}

// bearerTransport injects an Authorization: Bearer header into every request.
type bearerTransport struct {
	base  http.RoundTripper
	token string
}

func (t *bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(req)
}

func (m *ollamaModel) Name() string { return m.modelName }

func (m *ollamaModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		chatReq := m.buildChatRequest(req)

		if !stream {
			chatReq.Stream = new(false)
			ollamaRunNonStreaming(ctx, m.client, chatReq, yield)
			return
		}

		retryStream(ctx, streamRetryConfig(), yield, func(y func(*model.LLMResponse, error) bool) {
			ollamaRunStreaming(ctx, m.client, chatReq, y)
		})
	}
}

// buildChatRequest assembles the /api/chat request for one turn.
//
// No num_ctx is set, for cloud models or any other. api.ollama.com already
// serves each model at its native window, and sending a fixed value caps it
// instead of raising it: deepseek-v4-flash:0731-cloud has 1M, so the 256K that
// used to be set here would have thrown away three quarters of it.
//
// The old test also only matched ":cloud", never the ":<size>-cloud" form that
// most of the cloud catalog uses, so it silently did nothing for those models —
// the routing checks in config.go and provider.go accept both suffixes.
func (m *ollamaModel) buildChatRequest(req *model.LLMRequest) *ollamaChatRequest {
	messages, systemPrompt := ollamaContentsToMessages(req.Contents, req.Config)

	// Prepend system message if present.
	if systemPrompt != "" {
		messages = append([]ollamaChatMessage{{Role: "system", Content: systemPrompt}}, messages...)
	}

	modelName := m.modelName
	if req.Model != "" {
		modelName = req.Model
	}

	chatReq := &ollamaChatRequest{
		Model:    modelName,
		Messages: messages,
		Options:  ollamaChatOptions(),
		Think:    ollamaThinkValue(modelName, m.thinkingLevel),
	}

	// Convert tools.
	if req.Config != nil && len(req.Config.Tools) > 0 {
		chatReq.Tools = ollamaGenaiToolsToOllama(req.Config.Tools)
	}

	return chatReq
}

// ollamaChatOptions collects the per-request entries of the Ollama options map,
// returning nil when none apply so the field stays absent and the server's own
// defaults hold.
func ollamaChatOptions() map[string]any {
	opts := ollamaSamplingOptions()
	if n := ollamaNumPredict(); n > 0 {
		opts["num_predict"] = n
	}
	if len(opts) == 0 {
		return nil
	}
	return opts
}

// ollamaThinkValue resolves the think field for one request. nothink models must
// not have thinking forced on, whichever level the session is running at; a nil
// result leaves the field off so the model's own default applies.
func ollamaThinkValue(modelName, thinkingLevel string) *ollamaThink {
	if strings.Contains(strings.ToLower(modelName), "nothink") {
		return &ollamaThink{Value: false}
	}
	return ollamaThinkingConfig(thinkingLevel)
}

// ollamaThinkingConfig maps a thinking level string to Ollama ThinkValue.
//
// "none" returns an explicit false rather than nil: omitting the think field
// leaves the model's own default in force, and thinking-capable models such as
// gemma-4 then think anyway, spending latency and tokens the user asked to
// avoid. Unrecognized levels (including "") still return nil so the model
// default applies.
func ollamaThinkingConfig(level string) *ollamaThink {
	switch level {
	case "none":
		return &ollamaThink{Value: false}
	case "low", "medium", "high":
		return &ollamaThink{Value: level}
	default:
		return nil
	}
}

// ollamaFinishReasonToGenai maps Ollama done_reason to genai.FinishReason.
// defaultOllamaNumPredict bounds the output of a single Ollama turn.
//
// Ollama defaults to no client-side limit, so a model that falls into a
// repetition loop streams until the server's own ceiling: one
// deepseek-v4-flash:0731:cloud turn emitted 148 KB of the same sentence over 87
// seconds before it stopped. The cap matches what the Anthropic path already
// allows a thinking turn (16K), which is far above a normal coding reply.
const defaultOllamaNumPredict = 16384

// ollamaNumPredict returns the per-turn output cap in tokens.
// PI_OLLAMA_NUM_PREDICT overrides the default; a value <= 0 removes the cap,
// restoring the old unbounded behavior for anyone who needs it.
func ollamaNumPredict() int {
	raw := strings.TrimSpace(os.Getenv("PI_OLLAMA_NUM_PREDICT"))
	if raw == "" {
		return defaultOllamaNumPredict
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return defaultOllamaNumPredict
	}
	return n
}

// ollamaSamplingOptions returns Ollama's repetition-control knobs, omitting any
// the operator has not set so the server's own defaults stay in force. Nothing
// is sent by default: these change generation quality for every model, and the
// value that helps one can degrade another.
//
// They exist because num_predict only bounds how far a degenerate turn runs, it
// does not stop the turn degenerating. Ollama applies repeat_penalty (default
// 1.1) across the last repeat_last_n (default 64) tokens only. Measured
// degenerate turns on deepseek-v4-flash cycle on phrases of 89-194 bytes,
// roughly 25-55 tokens, so a 64-token window may not span a full cycle and the
// penalty never sees the repetition it is meant to suppress. Widening
// PI_OLLAMA_REPEAT_LAST_N is the knob that addresses that directly.
//
//	PI_OLLAMA_REPEAT_PENALTY     float, Ollama default 1.1 (1.0 disables)
//	PI_OLLAMA_REPEAT_LAST_N      int, Ollama default 64 (0 disables, -1 = num_ctx)
//	PI_OLLAMA_PRESENCE_PENALTY   float, Ollama default 0.0
//	PI_OLLAMA_FREQUENCY_PENALTY  float, Ollama default 0.0
//
// An unparseable value is ignored rather than fatal: a typo in an env var
// should not take down a session that would otherwise run.
func ollamaSamplingOptions() map[string]any {
	opts := make(map[string]any, 4)
	if v, ok := ollamaEnvFloat("PI_OLLAMA_REPEAT_PENALTY"); ok {
		opts["repeat_penalty"] = v
	}
	if v, ok := ollamaEnvInt("PI_OLLAMA_REPEAT_LAST_N"); ok {
		opts["repeat_last_n"] = v
	}
	if v, ok := ollamaEnvFloat("PI_OLLAMA_PRESENCE_PENALTY"); ok {
		opts["presence_penalty"] = v
	}
	if v, ok := ollamaEnvFloat("PI_OLLAMA_FREQUENCY_PENALTY"); ok {
		opts["frequency_penalty"] = v
	}
	return opts
}

// ollamaEnvFloat reads a float env var, reporting whether it was set and valid.
func ollamaEnvFloat(name string) (float64, bool) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// ollamaEnvInt reads an int env var, reporting whether it was set and valid.
func ollamaEnvInt(name string) (int, bool) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return 0, false
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	return v, true
}

func ollamaFinishReasonToGenai(reason string) genai.FinishReason {
	switch reason {
	case "length":
		return genai.FinishReasonMaxTokens
	default:
		return genai.FinishReasonStop
	}
}

// ollamaContentsToMessages converts genai.Content to Ollama messages.
func ollamaContentsToMessages(contents []*genai.Content, config *genai.GenerateContentConfig) ([]ollamaChatMessage, string) {
	systemPrompt := genaiSystemInstruction(config)
	functionResponses := genaiFunctionResponses(contents)

	var messages []ollamaChatMessage
	for _, content := range contents {
		if content == nil {
			continue
		}
		role := strings.TrimSpace(content.Role)
		if role == "system" {
			continue
		}

		textParts, functionCalls := genaiSplitParts(content.Parts)

		switch {
		case len(functionCalls) > 0 && genaiIsAssistantRole(role):
			messages = append(messages, ollamaToolCallMessages(textParts, functionCalls, functionResponses)...)
		case len(textParts) > 0:
			messages = append(messages, ollamaTextMessage(role, strings.Join(textParts, "\n")))
		}
	}

	// Ensure at least one message.
	if len(messages) == 0 {
		messages = append(messages, ollamaChatMessage{
			Role:    "user",
			Content: "Hello",
		})
	}

	return messages, systemPrompt
}

// ollamaToolCallMessages renders one assistant turn that called tools: the
// assistant message carrying the tool calls, then one "tool" message per call
// holding its result.
func ollamaToolCallMessages(
	textParts []string,
	functionCalls []*genai.FunctionCall,
	functionResponses map[string]*genai.FunctionResponse,
) []ollamaChatMessage {
	// Assistant message with tool calls.
	toolCalls := make([]ollamaToolCall, 0, len(functionCalls))
	for _, fc := range functionCalls {
		args := make(ollamaToolArguments, len(fc.Args))
		for k, v := range fc.Args {
			args[k] = v
		}
		toolCalls = append(toolCalls, ollamaToolCall{
			ID: fc.ID,
			Function: ollamaToolCallFunction{
				Name:      fc.Name,
				Arguments: args,
			},
		})
	}

	msg := ollamaChatMessage{
		Role:      "assistant",
		ToolCalls: toolCalls,
	}
	if len(textParts) > 0 {
		msg.Content = strings.Join(textParts, "\n")
	}
	messages := make([]ollamaChatMessage, 0, 1+len(functionCalls))
	messages = append(messages, msg)

	// Tool results as separate messages.
	for _, fc := range functionCalls {
		contentStr := ""
		if fr := functionResponses[fc.ID]; fr != nil {
			contentStr = oaiFunctionResponseContent(fr.Response) // reuse helper
		}
		messages = append(messages, ollamaChatMessage{
			Role:       "tool",
			Content:    contentStr,
			ToolCallID: fc.ID,
		})
	}
	return messages
}

// ollamaTextMessage renders a text-only turn under the Ollama role its genai
// role maps to.
func ollamaTextMessage(role, text string) ollamaChatMessage {
	msgRole := "user"
	if genaiIsAssistantRole(role) {
		msgRole = "assistant"
	}
	return ollamaChatMessage{
		Role:    msgRole,
		Content: text,
	}
}

// ollamaGenaiToolsToOllama converts genai tools to Ollama native tool format.
func ollamaGenaiToolsToOllama(tools []*genai.Tool) ollamaTools {
	var out ollamaTools
	for _, t := range tools {
		if t == nil {
			continue
		}
		for _, fd := range t.FunctionDeclarations {
			if fd == nil {
				continue
			}
			out = append(out, ollamaTool{
				Type: "function",
				Function: ollamaToolFunction{
					Name:        fd.Name,
					Description: fd.Description,
					Parameters:  ollamaToolParameters(fd.ParametersJsonSchema),
				},
			})
		}
	}
	return out
}

// ollamaToolParameters renders one declaration's JSON schema as Ollama's
// parameter object. The Properties map is always allocated, even for a
// declaration that takes no arguments, and Required is left nil unless the
// schema carries a list with at least one string in it.
func ollamaToolParameters(rawSchema any) ollamaToolFunctionParameters {
	params := ollamaToolFunctionParameters{
		Type:       "object",
		Properties: newToolPropertiesMap(),
	}
	m := schemaToMap(rawSchema)
	if m == nil {
		return params
	}
	if props, ok := m["properties"].(map[string]any); ok {
		for name, propRaw := range props {
			params.Properties.Set(name, convertToToolProperty(propRaw))
		}
	}
	params.Required = append(params.Required, jsonSchemaRequiredNames(m["required"])...)
	return params
}

// jsonSchemaRequiredNames reads the string entries of a JSON schema "required"
// list. Anything that is not a list, and any entry in it that is not a string,
// is dropped: the schema reaches here as `any` and a malformed one must not
// take down the request that carries it.
func jsonSchemaRequiredNames(raw any) []string {
	list, ok := raw.([]any)
	if !ok {
		return nil
	}
	names := make([]string, 0, len(list))
	for _, r := range list {
		if s, ok := r.(string); ok {
			names = append(names, s)
		}
	}
	return names
}

// schemaToMap normalizes a genai FunctionDeclaration.ParametersJsonSchema (typed
// as `any`) into a plain map[string]any. The pi-go tools registry sets this field
// to a typed *jsonschema.Schema, so a direct map assertion fails and the tool would
// be advertised to the model with no properties — leaving weaker models (e.g.
// minimax-m3) unable to tell which arguments to send, so every parameterized call
// arrives empty. Marshaling through JSON (jsonschema.Schema has a custom
// MarshalJSON) yields a faithful schema map regardless of the concrete type, while
// still accepting a raw map for callers/tests that pass one directly.
func schemaToMap(raw any) map[string]any {
	if raw == nil {
		return nil
	}
	if m, ok := raw.(map[string]any); ok {
		return m
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}
	return m
}

// convertToToolProperty converts a raw JSON schema property to Ollama ToolProperty.
func convertToToolProperty(raw any) ollamaToolProperty {
	prop := ollamaToolProperty{}
	m, ok := raw.(map[string]any)
	if !ok {
		return prop
	}
	if t, ok := m["type"].(string); ok {
		prop.Type = ollamaPropertyType{t}
	}
	if d, ok := m["description"].(string); ok {
		prop.Description = d
	}
	if e, ok := m["enum"].([]any); ok {
		prop.Enum = e
	}
	return prop
}

// ollamaStreamState accumulates the chunks of an Ollama chat stream while
// yielding each one as a partial response, and builds the final response from
// what it accumulated.
type ollamaStreamState struct {
	yield func(*model.LLMResponse, error) bool

	aggregatedText     string
	aggregatedThinking string
	toolCalls          []ollamaToolCall
	doneReason         string
	promptTokens       int
	evalTokens         int
	splitter           thinkSplitter
}

// emitThinking yields reasoning text on the thinking stream.
func (s *ollamaStreamState) emitThinking(text string) error {
	if text == "" {
		return nil
	}
	s.aggregatedThinking += text
	if !s.yield(&model.LLMResponse{
		Partial:      true,
		TurnComplete: false,
		Content:      &genai.Content{Role: "thinking", Parts: []*genai.Part{{Text: text}}},
	}, nil) {
		return fmt.Errorf("yield canceled")
	}
	return nil
}

// emitText yields answer text on the model stream.
func (s *ollamaStreamState) emitText(text string) error {
	if text == "" {
		return nil
	}
	s.aggregatedText += text
	if !s.yield(&model.LLMResponse{
		Partial:      true,
		TurnComplete: false,
		Content:      &genai.Content{Role: string(genai.RoleModel), Parts: []*genai.Part{{Text: text}}},
	}, nil) {
		return fmt.Errorf("yield canceled")
	}
	return nil
}

// emitSplit yields one thinkSplitter result: its reasoning half, then its
// answer half.
func (s *ollamaStreamState) emitSplit(thinking, text string) error {
	if err := s.emitThinking(thinking); err != nil {
		return err
	}
	return s.emitText(text)
}

// handleChunk folds one streamed chat response into the state.
func (s *ollamaStreamState) handleChunk(resp ollamaChatResponse) error {
	msg := resp.Message

	// Reasoning that Ollama already separated out.
	if err := s.emitThinking(msg.Thinking); err != nil {
		return err
	}

	// Reasoning the model left inline as <think>...</think> is routed to
	// the thinking stream instead of surfacing as the answer.
	if msg.Content != "" {
		inlineThinking, text := s.splitter.split(msg.Content)
		if err := s.emitSplit(inlineThinking, text); err != nil {
			return err
		}
	}

	if resp.Done {
		inlineThinking, text := s.splitter.flush()
		if err := s.emitSplit(inlineThinking, text); err != nil {
			return err
		}
	}

	// Accumulate tool calls.
	if len(msg.ToolCalls) > 0 {
		s.toolCalls = append(s.toolCalls, msg.ToolCalls...)
	}

	// Capture metrics from final response.
	if resp.Done {
		s.doneReason = resp.DoneReason
		s.promptTokens = resp.PromptEvalCount
		if s.promptTokens == 0 && resp.Metrics.PromptEvalCount != 0 {
			s.promptTokens = resp.Metrics.PromptEvalCount
		}
		s.evalTokens = resp.EvalCount
		if s.evalTokens == 0 && resp.Metrics.EvalCount != 0 {
			s.evalTokens = resp.Metrics.EvalCount
		}
	}

	return nil
}

// finalParts assembles the parts of the completed turn: the answer text (or the
// thinking fallback), then one part per tool call.
func (s *ollamaStreamState) finalParts() []*genai.Part {
	finalParts := make([]*genai.Part, 0, 1+len(s.toolCalls))
	if s.aggregatedText != "" {
		finalParts = append(finalParts, &genai.Part{Text: s.aggregatedText})
	} else if s.aggregatedThinking != "" && len(s.toolCalls) == 0 {
		// Fallback: model responded entirely via thinking tokens (e.g. thinking forced
		// on a nothink model). Surface the thinking content rather than returning nothing.
		//
		// Only when the turn produced no tool call. A turn that thinks and then
		// calls a tool has already said something, so the fallback isn't needed
		// — and firing it there restates the reasoning as if it were the answer
		// (seen with minimax-m3:cloud: "The user wants me to run a bash
		// command..." printed above the real reply).
		finalParts = append(finalParts, &genai.Part{Text: s.aggregatedThinking})
	}
	for _, tc := range s.toolCalls {
		args := tc.Function.Arguments.ToMap()
		p := newFunctionCallPart(tc.Function.Name, args)
		p.FunctionCall.ID = tc.ID
		finalParts = append(finalParts, p)
	}
	return finalParts
}

// finalResponse builds the terminal response for the completed turn.
func (s *ollamaStreamState) finalResponse() *model.LLMResponse {
	finalParts := s.finalParts()

	var usage *genai.GenerateContentResponseUsageMetadata
	if s.promptTokens > 0 || s.evalTokens > 0 {
		usage = &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:     int32(s.promptTokens),
			CandidatesTokenCount: int32(s.evalTokens),
		}
	}
	return &model.LLMResponse{
		Partial:       false,
		TurnComplete:  true,
		FinishReason:  ollamaFinishReasonToGenai(s.doneReason),
		UsageMetadata: usage,
		Content:       &genai.Content{Role: string(genai.RoleModel), Parts: finalParts},
	}
}

func ollamaRunStreaming(ctx context.Context, client *ollamaClient, chatReq *ollamaChatRequest, yield func(*model.LLMResponse, error) bool) {
	state := &ollamaStreamState{yield: yield}

	if err := client.Chat(ctx, chatReq, state.handleChunk); err != nil {
		if ctx.Err() == context.Canceled {
			_ = yield(canceledResponse(), nil)
			return
		}
		_ = yield(&model.LLMResponse{ErrorCode: "STREAM_ERROR", ErrorMessage: err.Error()}, nil)
		return
	}

	_ = yield(state.finalResponse(), nil)
}

func ollamaRunNonStreaming(ctx context.Context, client *ollamaClient, chatReq *ollamaChatRequest, yield func(*model.LLMResponse, error) bool) {
	var finalResp ollamaChatResponse

	err := client.Chat(ctx, chatReq, func(resp ollamaChatResponse) error {
		finalResp = resp
		return nil
	})

	if err != nil {
		yield(nil, fmt.Errorf("ollama API error: %w", err))
		return
	}

	msg := finalResp.Message
	parts := make([]*genai.Part, 0, 1+len(msg.ToolCalls))

	// Include thinking content as text if present. Reasoning the model left
	// inline as <think>...</think> is pulled out of the content the same way
	// the streaming path does it, so the tags never reach the caller.
	thinking := msg.Thinking
	content := msg.Content
	if content != "" {
		var splitter thinkSplitter
		inlineThinking, text := splitter.split(content)
		trailingThinking, trailingText := splitter.flush()
		thinking += inlineThinking + trailingThinking
		content = text + trailingText
	}
	if thinking != "" {
		parts = append(parts, &genai.Part{Text: thinking})
	}
	if content != "" {
		parts = append(parts, &genai.Part{Text: content})
	}

	for _, tc := range msg.ToolCalls {
		args := tc.Function.Arguments.ToMap()
		p := newFunctionCallPart(tc.Function.Name, args)
		p.FunctionCall.ID = tc.ID
		parts = append(parts, p)
	}

	promptTokens := finalResp.PromptEvalCount
	if promptTokens == 0 {
		promptTokens = finalResp.Metrics.PromptEvalCount
	}
	evalTokens := finalResp.EvalCount
	if evalTokens == 0 {
		evalTokens = finalResp.Metrics.EvalCount
	}
	var usage *genai.GenerateContentResponseUsageMetadata
	if promptTokens > 0 || evalTokens > 0 {
		usage = &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:     int32(promptTokens),
			CandidatesTokenCount: int32(evalTokens),
		}
	}

	yield(&model.LLMResponse{
		Partial:       false,
		TurnComplete:  true,
		FinishReason:  ollamaFinishReasonToGenai(finalResp.DoneReason),
		UsageMetadata: usage,
		Content:       &genai.Content{Role: string(genai.RoleModel), Parts: parts},
	}, nil)
}

// OllamaListModels lists available models from the Ollama server. The daemon's
// /api/tags response carries each model's context window
// (details.context_length — num_ctx for local models, the published window for
// cloud models) and capability flags, so both come back filled in rather than
// from a second per-model /api/show round trip.
func OllamaListModels(ctx context.Context, baseURL string) ([]ModelInfo, error) {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	client := &ollamaClient{
		baseURL:    u,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
	resp, err := client.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing models: %w", err)
	}
	models := make([]ModelInfo, 0, len(resp.Models))
	for _, m := range resp.Models {
		info := ModelInfo{
			ID:            m.Name,
			OwnedBy:       m.Details.ParameterSize,
			ContextWindow: int64(m.Details.ContextLength),
			Capabilities:  ollamaCapabilities(m.Capabilities),
		}
		if m.RemoteModel != "" {
			info.OwnedBy = m.Details.ParameterSize + " · " + m.RemoteModel
		}
		models = append(models, info)
	}
	return models, nil
}

// ollamaCapabilities maps the daemon's capability flags to the strings the
// model table shows. Unknown flags are dropped rather than guessed at.
func ollamaCapabilities(caps []string) []string {
	names := map[string]string{
		"completion": "completion",
		"tools":      "tools",
		"thinking":   "thinking",
		"vision":     "vision",
		"embedding":  "embedding",
	}
	out := make([]string, 0, len(caps))
	for _, c := range caps {
		if name, ok := names[c]; ok {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// OllamaContextWindowSize queries the Ollama server for the context window size
// of the given model. Returns 0 if the size cannot be determined.
func OllamaContextWindowSize(ctx context.Context, baseURL, modelName string) int64 {
	baseURL = normalizeBaseURL(baseURL)
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return 0
	}
	client := &ollamaClient{
		baseURL:    u,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
	resp, err := client.Show(ctx, &ollamaShowRequest{Model: modelName})
	if err != nil {
		return 0
	}

	// num_ctx takes precedence as it reflects the configured context window.
	if n, ok := ollamaNumCtxParameter(resp.Parameters); ok {
		return n
	}

	// Fall back to the model's native context length from ModelInfo.
	return ollamaNativeContextLength(resp.ModelInfo)
}

// ollamaClient is a lightweight HTTP client for the Ollama daemon.
type ollamaClient struct {
	baseURL    *url.URL
	httpClient *http.Client
}

func (c *ollamaClient) Chat(ctx context.Context, req *ollamaChatRequest, onChunk func(ollamaChatResponse) error) error {
	u := c.baseURL.ResolveReference(&url.URL{Path: "/api/chat"})
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshaling ollama chat request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating ollama request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/x-ndjson, application/json")

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(httpResp.Body)
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error != "" {
			return errors.New(errResp.Error)
		}
		return fmt.Errorf("status %d: %s", httpResp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	reader := bufio.NewReader(httpResp.Body)
	for {
		line, readErr := reader.ReadBytes('\n')
		line = bytes.TrimSpace(line)
		if len(line) > 0 {
			var chunk ollamaChatResponse
			if unmarshalErr := json.Unmarshal(line, &chunk); unmarshalErr != nil {
				return fmt.Errorf("decoding ollama response: %w", unmarshalErr)
			}
			if chunkErr := onChunk(chunk); chunkErr != nil {
				return chunkErr
			}
			if chunk.Done {
				break
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return readErr
		}
	}
	return nil
}

func (c *ollamaClient) List(ctx context.Context) (*ollamaListResponse, error) {
	u := c.baseURL.ResolveReference(&url.URL{Path: "/api/tags"})
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("creating ollama list request: %w", err)
	}

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(httpResp.Body)
		return nil, fmt.Errorf("status %d: %s", httpResp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var listResp ollamaListResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&listResp); err != nil {
		return nil, fmt.Errorf("decoding ollama list response: %w", err)
	}
	return &listResp, nil
}

func (c *ollamaClient) Show(ctx context.Context, req *ollamaShowRequest) (*ollamaShowResponse, error) {
	u := c.baseURL.ResolveReference(&url.URL{Path: "/api/show"})
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshaling ollama show request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating ollama show request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(httpResp.Body)
		return nil, fmt.Errorf("status %d: %s", httpResp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var showResp ollamaShowResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&showResp); err != nil {
		return nil, fmt.Errorf("decoding ollama show response: %w", err)
	}
	return &showResp, nil
}

// Ollama API types for chat, show, and list.
type ollamaChatRequest struct {
	Model    string              `json:"model"`
	Messages []ollamaChatMessage `json:"messages"`
	Options  map[string]any      `json:"options"`
	Stream   *bool               `json:"stream,omitempty"`
	Think    *ollamaThink        `json:"think,omitempty"`
	Tools    ollamaTools         `json:"tools,omitempty"`
}

type ollamaChatMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content"`
	Thinking   string           `json:"thinking,omitempty"`
	ToolCalls  []ollamaToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type ollamaToolCall struct {
	ID       string                 `json:"id,omitempty"`
	Function ollamaToolCallFunction `json:"function"`
}

type ollamaToolCallFunction struct {
	Index     int                 `json:"index"`
	Name      string              `json:"name"`
	Arguments ollamaToolArguments `json:"arguments"`
}

type ollamaToolArguments map[string]any

func (a *ollamaToolArguments) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		*a = make(map[string]any)
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			*a = make(map[string]any)
			return nil
		}
		*a = m
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	*a = m
	return nil
}

func (a ollamaToolArguments) ToMap() map[string]any {
	if a == nil {
		return make(map[string]any)
	}
	return map[string]any(a)
}

func (a ollamaToolArguments) Set(key string, val any) {
	a[key] = val
}

type ollamaThink struct {
	Value any
}

func (t ollamaThink) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.Value)
}

func (t *ollamaThink) UnmarshalJSON(b []byte) error {
	return json.Unmarshal(b, &t.Value)
}

type ollamaTools []ollamaTool

type ollamaTool struct {
	Type     string             `json:"type"`
	Function ollamaToolFunction `json:"function"`
}

type ollamaToolFunction struct {
	Name        string                       `json:"name"`
	Description string                       `json:"description,omitempty"`
	Parameters  ollamaToolFunctionParameters `json:"parameters"`
}

type ollamaToolFunctionParameters struct {
	Type       string               `json:"type"`
	Properties ollamaToolProperties `json:"properties"`
	Required   []string             `json:"required,omitempty"`
}

type ollamaToolProperties map[string]ollamaToolProperty

func newToolPropertiesMap() ollamaToolProperties {
	return make(ollamaToolProperties)
}

func (p ollamaToolProperties) Set(name string, prop ollamaToolProperty) {
	p[name] = prop
}

func (p ollamaToolProperties) Get(name string) (ollamaToolProperty, bool) {
	prop, ok := p[name]
	return prop, ok
}

type ollamaToolProperty struct {
	Type        ollamaPropertyType `json:"type,omitempty"`
	Description string             `json:"description,omitempty"`
	Enum        []any              `json:"enum,omitempty"`
}

type ollamaPropertyType []string

func (t ollamaPropertyType) String() string {
	if len(t) == 0 {
		return ""
	}
	return t[0]
}

func (t ollamaPropertyType) MarshalJSON() ([]byte, error) {
	if len(t) == 1 {
		return json.Marshal(t[0])
	}
	return json.Marshal([]string(t))
}

type ollamaMetrics struct {
	PromptEvalCount int `json:"prompt_eval_count"`
	EvalCount       int `json:"eval_count"`
}

type ollamaChatResponse struct {
	Metrics         ollamaMetrics     `json:"metrics,omitempty"`
	PromptEvalCount int               `json:"prompt_eval_count"`
	EvalCount       int               `json:"eval_count"`
	Model           string            `json:"model"`
	CreatedAt       time.Time         `json:"created_at"`
	Message         ollamaChatMessage `json:"message"`
	Done            bool              `json:"done"`
	DoneReason      string            `json:"done_reason"`
}

type ollamaListResponse struct {
	Models []ollamaModelItem `json:"models"`
}

type ollamaModelItem struct {
	Name         string             `json:"name"`
	Model        string             `json:"model"`
	Details      ollamaModelDetails `json:"details"`
	RemoteModel  string             `json:"remote_model"`
	Capabilities []string           `json:"capabilities"`
}

type ollamaModelDetails struct {
	ParameterSize string `json:"parameter_size"`
	ContextLength int    `json:"context_length"`
}

type ollamaShowRequest struct {
	Model string `json:"model"`
}

type ollamaShowResponse struct {
	Parameters string         `json:"parameters"`
	ModelInfo  map[string]any `json:"model_info"`
}

// ollamaNumCtxParameter reads num_ctx out of a /api/show parameters block, which
// is a newline-separated list of "key value" pairs. The second return says
// whether a value was found at all, so a configured num_ctx of 0 is reported as
// itself rather than falling through to the native context length.
func ollamaNumCtxParameter(parameters string) (int64, bool) {
	for _, line := range strings.Split(parameters, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[0] != "num_ctx" {
			continue
		}
		if n, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
			return n, true
		}
	}
	return 0, false
}

// ollamaNativeContextLength reads the model's native context length out of a
// /api/show model_info map, returning 0 when no key carries one. A value that
// arrived over HTTP is always a float64; the integer cases are here for a map
// built in process.
func ollamaNativeContextLength(modelInfo map[string]any) int64 {
	for key, val := range modelInfo {
		if !strings.HasSuffix(key, ".context_length") {
			continue
		}
		switch v := val.(type) {
		case float64:
			return int64(v)
		case int64:
			return v
		case int:
			return int64(v)
		}
	}
	return 0
}
