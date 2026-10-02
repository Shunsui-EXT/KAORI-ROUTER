package executor

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/auth/alysis"
	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/config"
	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/registry"
	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/runtime/executor/helps"
	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/thinking"
	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/util"
	cliproxyauth "github.com/Shunsui-EXT/KAORI-ROUTER/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/Shunsui-EXT/KAORI-ROUTER/sdk/cliproxy/executor"
	sdktranslator "github.com/Shunsui-EXT/KAORI-ROUTER/sdk/translator"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
)

// AlysisExecutor forwards OpenAI-compatible requests to the Alysis Code Pro
// hosted gateway, which meters subscription credits server-side.
type AlysisExecutor struct {
	cfg *config.Config
}

var _ cliproxyauth.ProviderExecutor = (*AlysisExecutor)(nil)

// NewAlysisExecutor creates a new Alysis executor instance.
func NewAlysisExecutor(cfg *config.Config) *AlysisExecutor {
	return &AlysisExecutor{cfg: cfg}
}

func (e *AlysisExecutor) Identifier() string { return "alysis" }

// alysisGatewayBase is overridable in tests.
var alysisGatewayBase = alysis.SupabaseURL + alysis.GatewayPathPrefix

func (e *AlysisExecutor) chatCompletionsURL() string {
	return alysisGatewayBase + "/chat/completions"
}

// HttpRequest injects the Alysis gateway key into the supplied request and executes it.
func (e *AlysisExecutor) HttpRequest(ctx context.Context, auth *cliproxyauth.Auth, req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("alysis executor: request is nil")
	}
	if ctx == nil {
		ctx = req.Context()
	}
	httpReq := req.WithContext(ctx)
	if key := alysisCredentials(auth); key != "" {
		httpReq.Header.Set("Authorization", "Bearer "+key)
	}
	var attrs map[string]string
	if auth != nil {
		attrs = auth.Attributes
	}
	util.ApplyCustomHeadersFromAttrs(httpReq, attrs)
	httpClient := helps.NewProxyAwareHTTPClient(ctx, e.cfg, auth, 0)
	return httpClient.Do(httpReq)
}

// Execute handles non-streaming chat completions against Alysis.
func (e *AlysisExecutor) Execute(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (resp cliproxyexecutor.Response, err error) {
	baseModel := thinking.ParseSuffix(req.Model).ModelName

	reporter := helps.NewUsageReporter(ctx, e.Identifier(), baseModel, auth)
	defer reporter.TrackFailure(ctx, &err)

	apiKey := alysisCredentials(auth)
	if apiKey == "" {
		err = statusErr{code: http.StatusUnauthorized, msg: "alysis: missing gateway key"}
		return
	}

	from := opts.SourceFormat
	to := sdktranslator.FromString("openai")
	translated := sdktranslator.TranslateRequest(from, to, baseModel, bytes.Clone(req.Payload), false)

	url := e.chatCompletionsURL()
	httpReq, errReq := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(translated))
	if errReq != nil {
		return resp, errReq
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("Accept", "application/json")
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
			log.Errorf("alysis executor: close response body error: %v", errClose)
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

// ExecuteStream handles streaming chat completions against Alysis.
func (e *AlysisExecutor) ExecuteStream(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (_ *cliproxyexecutor.StreamResult, err error) {
	baseModel := thinking.ParseSuffix(req.Model).ModelName

	reporter := helps.NewUsageReporter(ctx, e.Identifier(), baseModel, auth)
	defer reporter.TrackFailure(ctx, &err)

	apiKey := alysisCredentials(auth)
	if apiKey == "" {
		err = statusErr{code: http.StatusUnauthorized, msg: "alysis: missing gateway key"}
		return nil, err
	}

	from := opts.SourceFormat
	to := sdktranslator.FromString("openai")
	translated := sdktranslator.TranslateRequest(from, to, baseModel, bytes.Clone(req.Payload), true)

	url := e.chatCompletionsURL()
	httpReq, errReq := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(translated))
	if errReq != nil {
		return nil, errReq
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Cache-Control", "no-cache")
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
			log.Errorf("alysis executor: close response body error: %v", errClose)
		}
		err = statusErr{code: httpResp.StatusCode, msg: string(b)}
		return nil, err
	}

	out := make(chan cliproxyexecutor.StreamChunk)
	go func() {
		defer close(out)
		defer func() {
			if errClose := httpResp.Body.Close(); errClose != nil {
				log.Errorf("alysis executor: close response body error: %v", errClose)
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

// Refresh validates the stored gateway key. The key is long-lived and has no
// server-side refresh endpoint, so the auth is returned unchanged.
func (e *AlysisExecutor) Refresh(ctx context.Context, auth *cliproxyauth.Auth) (*cliproxyauth.Auth, error) {
	if auth == nil {
		return nil, fmt.Errorf("missing auth")
	}
	return auth, nil
}

// CountTokens is not supported by the Alysis gateway.
func (e *AlysisExecutor) CountTokens(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, statusErr{code: http.StatusNotImplemented, msg: "alysis: count tokens not supported"}
}

// alysisCredentials extracts the gateway key from the auth record.
func alysisCredentials(auth *cliproxyauth.Auth) string {
	if auth == nil {
		return ""
	}
	if auth.Metadata != nil {
		if key, ok := auth.Metadata["gatewayKey"].(string); ok && key != "" {
			return key
		}
	}
	if auth.Attributes != nil {
		if key := auth.Attributes["gatewayKey"]; key != "" {
			return key
		}
	}
	return ""
}

// FetchAlysisModels fetches the live gateway model catalog (OpenAI-shaped
// {"data":[{"id":...}]}). Failures fall back to the static catalog.
func FetchAlysisModels(ctx context.Context, auth *cliproxyauth.Auth, cfg *config.Config) []*registry.ModelInfo {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	httpClient := helps.NewProxyAwareHTTPClient(ctx, cfg, auth, 0)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, alysisGatewayBase+"/models", nil)
	if err != nil {
		log.Warnf("alysis: failed to create model fetch request: %v", err)
		return registry.GetAlysisModels()
	}
	if key := alysisCredentials(auth); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	// Matches the official alysis-code CLI's User-Agent for model-catalog
	// discovery calls (provider_model_catalog.py: _USER_AGENT = "alysis-code").
	req.Header.Set("User-Agent", "alysis-code")

	resp, err := httpClient.Do(req)
	if err != nil {
		log.Warnf("alysis: using static models (API fetch failed: %v)", err)
		return registry.GetAlysisModels()
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Warnf("alysis: failed to read models response: %v", err)
		return registry.GetAlysisModels()
	}
	if resp.StatusCode != http.StatusOK {
		log.Warnf("alysis: fetch models failed: status %d, body: %s", resp.StatusCode, string(body))
		return registry.GetAlysisModels()
	}

	result := gjson.GetBytes(body, "data")
	if !result.Exists() {
		log.Warnf("alysis: invalid models response format (expected data field)")
		return registry.GetAlysisModels()
	}

	now := time.Now().Unix()
	seen := make(map[string]struct{})
	merged := make([]*registry.ModelInfo, 0, 8)
	for _, model := range registry.GetAlysisModels() {
		if model == nil || model.ID == "" {
			continue
		}
		seen[model.ID] = struct{}{}
		merged = append(merged, model)
	}
	result.ForEach(func(_, value gjson.Result) bool {
		id := strings.TrimSpace(value.Get("id").String())
		if id == "" {
			return true
		}
		if _, exists := seen[id]; exists {
			return true
		}
		seen[id] = struct{}{}
		displayName := strings.TrimSpace(value.Get("name").String())
		if displayName == "" {
			displayName = id
		}
		merged = append(merged, &registry.ModelInfo{
			ID:          id,
			DisplayName: displayName,
			Name:        id,
			OwnedBy:     "alysis",
			Type:        "alysis",
			Object:      "model",
			Created:     now,
		})
		return true
	})

	if len(merged) == 0 {
		return registry.GetAlysisModels()
	}
	return merged
}
