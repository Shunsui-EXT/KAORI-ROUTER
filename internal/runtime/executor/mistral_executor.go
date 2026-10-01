package executor

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/config"
	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/runtime/executor/helps"
	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/thinking"
	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/util"
	cliproxyauth "github.com/Shunsui-EXT/KAORI-ROUTER/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/Shunsui-EXT/KAORI-ROUTER/sdk/cliproxy/executor"
	sdktranslator "github.com/Shunsui-EXT/KAORI-ROUTER/sdk/translator"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	mistralDefaultBaseURL = "https://api.mistral.ai"
	mistralChatEndpoint   = "/v1/chat/completions"
)

// MistralExecutor is a stateless executor for the Mistral AI chat completions API.
// It is an OpenAI-compatible endpoint with a few quirks: it rejects several
// OpenAI reasoning/response fields outright and expects reasoning_effort to stay
// at "high" for Mistral's reasoning models, so requests are normalized before
// being forwarded.
type MistralExecutor struct {
	provider string
	cfg      *config.Config
}

var _ cliproxyauth.ProviderExecutor = (*MistralExecutor)(nil)

// NewMistralExecutor constructs a new Mistral executor.
func NewMistralExecutor(cfg *config.Config) *MistralExecutor {
	return &MistralExecutor{provider: "mistral", cfg: cfg}
}

// Identifier returns the provider identifier "mistral".
func (e *MistralExecutor) Identifier() string { return e.provider }

// HttpRequest injects the Mistral API key into the supplied request and executes it.
func (e *MistralExecutor) HttpRequest(ctx context.Context, auth *cliproxyauth.Auth, req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("mistral executor: request is nil")
	}
	if ctx == nil {
		ctx = req.Context()
	}
	httpReq := req.WithContext(ctx)
	apiKey := mistralAPIKey(auth)
	if apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}
	var attrs map[string]string
	if auth != nil {
		attrs = auth.Attributes
	}
	util.ApplyCustomHeadersFromAttrs(httpReq, attrs)
	httpClient := helps.NewProxyAwareHTTPClient(ctx, e.cfg, auth, 0)
	return httpClient.Do(httpReq)
}

// Execute handles non-streaming chat completions against Mistral.
func (e *MistralExecutor) Execute(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (resp cliproxyexecutor.Response, err error) {
	baseModel := thinking.ParseSuffix(req.Model).ModelName
	baseModel = resolveMistralModelName(e.cfg, auth, baseModel)

	reporter := helps.NewUsageReporter(ctx, e.Identifier(), baseModel, auth)
	defer reporter.TrackFailure(ctx, &err)

	apiKey := mistralAPIKey(auth)
	if apiKey == "" {
		err = statusErr{code: http.StatusUnauthorized, msg: "mistral: missing API key"}
		return
	}

	from := opts.SourceFormat
	to := sdktranslator.FromString("openai")
	translated := sdktranslator.TranslateRequest(from, to, baseModel, bytes.Clone(req.Payload), false)
	translated = stripMistralUnsupportedFields(translated)
	translated = normalizeMistralReasoningEffort(baseModel, translated)

	url := mistralBaseURL(auth) + mistralChatEndpoint
	httpReq, errReq := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(translated))
	if errReq != nil {
		return resp, errReq
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	var attrs map[string]string
	if auth != nil {
		attrs = auth.Attributes
	}
	util.ApplyCustomHeadersFromAttrs(httpReq, attrs)

	var authID, authLabel, authType, authValue string
	if auth != nil {
		authID = auth.ID
		authLabel = auth.Label
		authType, authValue = auth.AccountInfo()
	}
	helps.RecordAPIRequest(ctx, e.cfg, helps.UpstreamRequestLog{
		URL:       url,
		Method:    http.MethodPost,
		Headers:   httpReq.Header.Clone(),
		Body:      translated,
		Provider:  e.Identifier(),
		AuthID:    authID,
		AuthLabel: authLabel,
		AuthType:  authType,
		AuthValue: authValue,
	})

	httpClient := helps.NewProxyAwareHTTPClient(ctx, e.cfg, auth, 0)
	httpResp, errDo := httpClient.Do(httpReq)
	if errDo != nil {
		helps.RecordAPIResponseError(ctx, e.cfg, errDo)
		return resp, errDo
	}
	defer func() {
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("mistral executor: close response body error: %v", errClose)
		}
	}()

	helps.RecordAPIResponseMetadata(ctx, e.cfg, httpResp.StatusCode, httpResp.Header.Clone())
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		b, _ := io.ReadAll(httpResp.Body)
		helps.AppendAPIResponseChunk(ctx, e.cfg, b)
		err = statusErr{code: httpResp.StatusCode, msg: string(b)}
		return resp, err
	}
	body, errRead := io.ReadAll(httpResp.Body)
	if errRead != nil {
		helps.RecordAPIResponseError(ctx, e.cfg, errRead)
		return resp, errRead
	}
	helps.AppendAPIResponseChunk(ctx, e.cfg, body)
	reporter.Publish(ctx, helps.ParseOpenAIUsage(body))
	reporter.EnsurePublished(ctx)

	var param any
	out := sdktranslator.TranslateNonStream(ctx, to, from, req.Model, opts.OriginalRequest, translated, body, &param)
	resp = cliproxyexecutor.Response{Payload: out, Headers: httpResp.Header.Clone()}
	return resp, nil
}

// ExecuteStream handles streaming chat completions against Mistral.
func (e *MistralExecutor) ExecuteStream(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (_ *cliproxyexecutor.StreamResult, err error) {
	baseModel := thinking.ParseSuffix(req.Model).ModelName
	baseModel = resolveMistralModelName(e.cfg, auth, baseModel)

	reporter := helps.NewUsageReporter(ctx, e.Identifier(), baseModel, auth)
	defer reporter.TrackFailure(ctx, &err)

	apiKey := mistralAPIKey(auth)
	if apiKey == "" {
		err = statusErr{code: http.StatusUnauthorized, msg: "mistral: missing API key"}
		return nil, err
	}

	from := opts.SourceFormat
	to := sdktranslator.FromString("openai")
	translated := sdktranslator.TranslateRequest(from, to, baseModel, bytes.Clone(req.Payload), true)
	translated = stripMistralUnsupportedFields(translated)
	translated = normalizeMistralReasoningEffort(baseModel, translated)

	url := mistralBaseURL(auth) + mistralChatEndpoint
	httpReq, errReq := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(translated))
	if errReq != nil {
		return nil, errReq
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	var attrs map[string]string
	if auth != nil {
		attrs = auth.Attributes
	}
	util.ApplyCustomHeadersFromAttrs(httpReq, attrs)

	var authID, authLabel, authType, authValue string
	if auth != nil {
		authID = auth.ID
		authLabel = auth.Label
		authType, authValue = auth.AccountInfo()
	}
	helps.RecordAPIRequest(ctx, e.cfg, helps.UpstreamRequestLog{
		URL:       url,
		Method:    http.MethodPost,
		Headers:   httpReq.Header.Clone(),
		Body:      translated,
		Provider:  e.Identifier(),
		AuthID:    authID,
		AuthLabel: authLabel,
		AuthType:  authType,
		AuthValue: authValue,
	})

	httpClient := helps.NewProxyAwareHTTPClient(ctx, e.cfg, auth, 0)
	httpResp, errDo := httpClient.Do(httpReq)
	if errDo != nil {
		helps.RecordAPIResponseError(ctx, e.cfg, errDo)
		return nil, errDo
	}

	helps.RecordAPIResponseMetadata(ctx, e.cfg, httpResp.StatusCode, httpResp.Header.Clone())
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		b, _ := io.ReadAll(httpResp.Body)
		helps.AppendAPIResponseChunk(ctx, e.cfg, b)
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("mistral executor: close response body error: %v", errClose)
		}
		err = statusErr{code: httpResp.StatusCode, msg: string(b)}
		return nil, err
	}

	out := make(chan cliproxyexecutor.StreamChunk)
	go func() {
		defer close(out)
		defer func() {
			if errClose := httpResp.Body.Close(); errClose != nil {
				log.Errorf("mistral executor: close response body error: %v", errClose)
			}
		}()

		scanner := bufio.NewScanner(httpResp.Body)
		scanner.Buffer(nil, 52_428_800)
		var param any
		for scanner.Scan() {
			line := scanner.Bytes()
			trimmed := bytes.TrimSpace(line)
			if len(trimmed) == 0 {
				continue
			}
			helps.AppendAPIResponseChunk(ctx, e.cfg, trimmed)
			if detail, ok := helps.ParseOpenAIStreamUsage(trimmed); ok {
				reporter.Publish(ctx, detail)
			}
			chunks := sdktranslator.TranslateStream(ctx, to, from, req.Model, opts.OriginalRequest, translated, bytes.Clone(trimmed), &param)
			for i := range chunks {
				select {
				case out <- cliproxyexecutor.StreamChunk{Payload: chunks[i]}:
				case <-ctx.Done():
					return
				}
			}
		}
		if errScan := scanner.Err(); errScan != nil {
			helps.RecordAPIResponseError(ctx, e.cfg, errScan)
			reporter.PublishFailure(ctx, errScan)
			select {
			case out <- cliproxyexecutor.StreamChunk{Err: errScan}:
			case <-ctx.Done():
			}
			return
		}
		reporter.EnsurePublished(ctx)
	}()

	return &cliproxyexecutor.StreamResult{
		Headers: httpResp.Header.Clone(),
		Chunks:  out,
	}, nil
}

// Refresh is a no-op for Mistral: credentials are static API keys, nothing to refresh.
func (e *MistralExecutor) Refresh(ctx context.Context, auth *cliproxyauth.Auth) (*cliproxyauth.Auth, error) {
	return auth, nil
}

// CountTokens is not supported by the Mistral chat completions API.
func (e *MistralExecutor) CountTokens(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, statusErr{code: http.StatusNotImplemented, msg: "mistral: count tokens not supported"}
}

func mistralAPIKey(auth *cliproxyauth.Auth) string {
	if auth == nil || auth.Attributes == nil {
		return ""
	}
	return strings.TrimSpace(auth.Attributes["api_key"])
}

// mistralBaseURL returns the configured base URL for this credential, with any
// trailing "/v1" stripped since mistralChatEndpoint already includes it (so a
// base-url of either "https://api.mistral.ai" or "https://api.mistral.ai/v1"
// resolves to the same request path). Falls back to mistralDefaultBaseURL when
// unset, covering auths that bypass config-load sanitization (e.g. Home dispatch
// or plugin-sourced credentials).
func mistralBaseURL(auth *cliproxyauth.Auth) string {
	if auth == nil || auth.Attributes == nil {
		return mistralDefaultBaseURL
	}
	baseURL := strings.TrimSpace(auth.Attributes["base_url"])
	if baseURL == "" {
		return mistralDefaultBaseURL
	}
	baseURL = strings.TrimSuffix(baseURL, "/")
	baseURL = strings.TrimSuffix(baseURL, "/v1")
	return baseURL
}

func resolveMistralModelName(cfg *config.Config, auth *cliproxyauth.Auth, model string) string {
	if cfg == nil || auth == nil {
		return model
	}
	apiKey := mistralAPIKey(auth)
	for _, key := range cfg.MistralKey {
		if key.APIKey != apiKey {
			continue
		}
		for _, m := range key.Models {
			if m.Alias == model {
				return m.Name
			}
		}
	}
	return model
}

// stripMistralUnsupportedFields removes OpenAI reasoning/response fields that
// Mistral's chat completions endpoint rejects, and drops empty assistant
// messages (tool-call-only turns) that can appear after context trimming.
func stripMistralUnsupportedFields(payload []byte) []byte {
	paths := []string{"reasoning", "reasoningSummary", "include", "verbosity", "interleaved", "thinking", "stream_options"}
	for _, path := range paths {
		if updated, err := sjson.DeleteBytes(payload, path); err == nil {
			payload = updated
		}
	}

	messages := gjson.GetBytes(payload, "messages")
	if !messages.Exists() || !messages.IsArray() {
		return payload
	}

	for idx, msg := range messages.Array() {
		if strings.TrimSpace(msg.Get("role").String()) == "assistant" {
			path := "messages." + strconv.Itoa(idx) + ".reasoning_content"
			if updated, err := sjson.DeleteBytes(payload, path); err == nil {
				payload = updated
			}
		}
	}

	messages = gjson.GetBytes(payload, "messages")
	if messages.Exists() && messages.IsArray() {
		msgArray := messages.Array()
		kept := make([]string, 0, len(msgArray))
		dropped := 0
		for _, msg := range msgArray {
			if shouldDropEmptyMistralAssistantMessage(msg) {
				dropped++
				continue
			}
			kept = append(kept, msg.Raw)
		}
		if dropped > 0 {
			rawMessages := []byte("[" + strings.Join(kept, ",") + "]")
			if next, err := sjson.SetRawBytes(payload, "messages", rawMessages); err == nil {
				payload = next
			}
			log.WithField("dropped_assistant_messages", dropped).Debug("mistral executor: dropped empty assistant messages")
		}
	}
	return payload
}

func shouldDropEmptyMistralAssistantMessage(msg gjson.Result) bool {
	role := strings.TrimSpace(msg.Get("role").String())
	if role != "assistant" {
		return false
	}
	if msg.Get("tool_calls").Exists() && len(msg.Get("tool_calls").Array()) > 0 {
		return false
	}
	content := strings.TrimSpace(msg.Get("content").String())
	return content == ""
}

// normalizeMistralReasoningEffort forces reasoning_effort to "high" for Mistral
// reasoning models when the field is present, matching Mistral's reasoning-model
// behavior. Covers the whole Mistral AI model family, including the Magistral
// and Ministral lines, not just names containing the literal "mistral".
func normalizeMistralReasoningEffort(model string, body []byte) []byte {
	lowerModel := strings.ToLower(strings.TrimSpace(model))
	isMistralFamily := strings.Contains(lowerModel, "mistral") ||
		strings.Contains(lowerModel, "magistral") ||
		strings.Contains(lowerModel, "ministral")
	if !isMistralFamily {
		return body
	}
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return body
	}
	effort := gjson.GetBytes(body, "reasoning_effort")
	if !effort.Exists() || effort.String() == "high" {
		return body
	}
	if updated, err := sjson.SetBytes(body, "reasoning_effort", "high"); err == nil {
		return updated
	}
	return body
}
