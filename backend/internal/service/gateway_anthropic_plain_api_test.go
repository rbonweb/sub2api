package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestStripClaudeCodeBodyMarkers(t *testing.T) {
	t.Run("billing block and claude code identity only removes system entirely", func(t *testing.T) {
		body := []byte(`{
			"model": "claude-3-7-sonnet-20250219",
			"system": [
				{"type": "text", "text": "x-anthropic-billing-header: cc_version=2.1.258; cc_entrypoint=cli;"},
				{"type": "text", "text": "You are Claude Code, Anthropic's official CLI for Claude.", "cache_control": {"type": "ephemeral"}}
			],
			"messages": [{"role": "user", "content": "hi"}]
		}`)

		cleaned := stripClaudeCodeBodyMarkers(body)
		require.NotContains(t, string(cleaned), "x-anthropic-billing-header")
		require.NotContains(t, string(cleaned), "You are Claude Code")
		require.False(t, gjson.GetBytes(cleaned, "system").Exists(), "empty system should be dropped from body")
	})

	t.Run("claude code identity with instructions rewrites identity to neutral persona", func(t *testing.T) {
		body := []byte(`{
			"model": "claude-3-7-sonnet-20250219",
			"system": [
				{"type": "text", "text": "You are Claude Code, Anthropic's official CLI for Claude.\n\nPlease follow these instructions: write clean Go code."}
			],
			"messages": [{"role": "user", "content": "hi"}]
		}`)

		cleaned := stripClaudeCodeBodyMarkers(body)
		require.NotContains(t, string(cleaned), "You are Claude Code")
		require.Contains(t, string(cleaned), "You are an AI assistant.")
		require.Contains(t, string(cleaned), "Please follow these instructions: write clean Go code.")
	})

	t.Run("billing block missing leaves normal system unchanged", func(t *testing.T) {
		body := []byte(`{
			"model": "claude-3-7-sonnet-20250219",
			"system": [
				{"type": "text", "text": "You are a helpful assistant."}
			],
			"messages": [{"role": "user", "content": "hi"}]
		}`)

		cleaned := stripClaudeCodeBodyMarkers(body)
		require.Equal(t, string(body), string(cleaned))
	})

	t.Run("string system prompt with claude code identity only is dropped", func(t *testing.T) {
		body := []byte(`{
			"model": "claude-3-7-sonnet-20250219",
			"system": "You are Claude Code, Anthropic's official CLI for Claude.",
			"messages": [{"role": "user", "content": "hi"}]
		}`)

		cleaned := stripClaudeCodeBodyMarkers(body)
		require.False(t, gjson.GetBytes(cleaned, "system").Exists())
	})

	t.Run("string system prompt with neutral persona is untouched", func(t *testing.T) {
		body := []byte(`{
			"model": "claude-3-7-sonnet-20250219",
			"system": "You are a helpful assistant.",
			"messages": [{"role": "user", "content": "hi"}]
		}`)

		cleaned := stripClaudeCodeBodyMarkers(body)
		require.Equal(t, string(body), string(cleaned))
	})

	t.Run("strips claude-code-exclusive fields: context_management, block_binding, fallbacks", func(t *testing.T) {
		body := []byte(`{
			"model": "claude-3-7-sonnet-20250219",
			"context_management": {"edits": []},
			"thinking": {
				"type": "enabled",
				"budget_tokens": 2048,
				"block_binding": true
			},
			"fallbacks": ["claude-3-5-haiku-20241022"],
			"fallback_credit_token": "token-xyz",
			"messages": [
				{"role": "user", "content": "hi", "output_config": {"effort": "low"}}
			]
		}`)

		cleaned := stripClaudeCodeBodyMarkers(body)
		require.False(t, gjson.GetBytes(cleaned, "context_management").Exists())
		require.False(t, gjson.GetBytes(cleaned, "thinking.block_binding").Exists())
		require.Equal(t, "enabled", gjson.GetBytes(cleaned, "thinking.type").String())
		require.Equal(t, int64(2048), gjson.GetBytes(cleaned, "thinking.budget_tokens").Int())
		require.False(t, gjson.GetBytes(cleaned, "fallbacks").Exists())
		require.False(t, gjson.GetBytes(cleaned, "fallback_credit_token").Exists())
		require.False(t, gjson.GetBytes(cleaned, "messages.0.output_config").Exists())
	})

	t.Run("metadata with user_id only removes metadata completely", func(t *testing.T) {
		body := []byte(`{
			"model": "claude-3-7-sonnet-20250219",
			"metadata": {
				"user_id": "user_xyz123"
			},
			"messages": []
		}`)

		cleaned := stripClaudeCodeBodyMarkers(body)
		require.False(t, gjson.GetBytes(cleaned, "metadata").Exists())
		require.False(t, gjson.GetBytes(cleaned, "metadata.user_id").Exists())
	})

	t.Run("metadata with user_id and other keys preserves other keys", func(t *testing.T) {
		body := []byte(`{
			"model": "claude-3-7-sonnet-20250219",
			"metadata": {
				"user_id": "user_xyz123",
				"tenant_id": "tenant_456",
				"app_version": "1.0.0"
			},
			"messages": []
		}`)

		cleaned := stripClaudeCodeBodyMarkers(body)
		require.True(t, gjson.GetBytes(cleaned, "metadata").Exists())
		require.False(t, gjson.GetBytes(cleaned, "metadata.user_id").Exists())
		require.Equal(t, "tenant_456", gjson.GetBytes(cleaned, "metadata.tenant_id").String())
		require.Equal(t, "1.0.0", gjson.GetBytes(cleaned, "metadata.app_version").String())
	})
}

func TestStripClaudeCodeHeaderMarkers(t *testing.T) {
	t.Run("removes claude code headers and all x-stainless headers in any casing", func(t *testing.T) {
		casings := []struct {
			name    string
			appKey  string
			sessKey string
			reqKey  string
			dirKey  string
		}{
			{"canonical", "X-App", "X-Claude-Code-Session-Id", "X-Client-Request-Id", "Anthropic-Dangerous-Direct-Browser-Access"},
			{"lowercase", "x-app", "x-claude-code-session-id", "x-client-request-id", "anthropic-dangerous-direct-browser-access"},
			{"uppercase", "X-APP", "X-CLAUDE-CODE-SESSION-ID", "X-CLIENT-REQUEST-ID", "ANTHROPIC-DANGEROUS-DIRECT-BROWSER-ACCESS"},
			{"mixed", "X-aPp", "x-Claude-Code-Session-Id", "x-Client-Request-Id", "Anthropic-Dangerous-Direct-Browser-Access"},
		}

		for _, tc := range casings {
			t.Run(tc.name, func(t *testing.T) {
				h := http.Header{}
				h[tc.appKey] = []string{"cli"}
				h[tc.sessKey] = []string{"sess-1"}
				h[tc.reqKey] = []string{"req-1"}
				h[tc.dirKey] = []string{"true"}
				h.Set("x-stainless-lang", "js")
				h.Set("x-stainless-package-version", "0.33.0")
				h.Set("x-stainless-os", "MacOS")
				h.Set("x-stainless-arch", "arm64")
				h.Set("x-stainless-runtime", "node")
				h.Set("x-stainless-runtime-version", "v20.0.0")

				stripClaudeCodeHeaderMarkers(h)

				for k := range h {
					lower := strings.ToLower(k)
					require.NotEqual(t, "x-app", lower)
					require.NotEqual(t, "x-claude-code-session-id", lower)
					require.NotEqual(t, "x-client-request-id", lower)
					require.NotEqual(t, "anthropic-dangerous-direct-browser-access", lower)
					require.False(t, strings.HasPrefix(lower, "x-stainless-"), "all x-stainless headers must be removed: key="+k)
				}
			})
		}
	})

	t.Run("all internal Claude Code beta tokens are dropped completely", func(t *testing.T) {
		h := http.Header{}
		h.Set("anthropic-beta", "claude-code-20250219,prompt-caching-scope-2026-01-05,thinking-binding-controls-2026-08-01,mid-conversation-output-config-2026-07-01,context-management-2025-06-27,extended-cache-ttl-2025-04-11")

		stripClaudeCodeHeaderMarkers(h)

		require.Equal(t, "", getHeaderRaw(h, "anthropic-beta"), "all internal betas dropped means header must be removed")
	})

	t.Run("public betas are preserved while internal Claude Code betas are dropped", func(t *testing.T) {
		h := http.Header{}
		h.Set("anthropic-beta", "claude-code-20250219,prompt-caching-2024-07-31,prompt-caching-scope-2026-01-05,output-128k-2025-02-19")

		stripClaudeCodeHeaderMarkers(h)

		beta := getHeaderRaw(h, "anthropic-beta")
		require.NotContains(t, beta, "claude-code-20250219")
		require.NotContains(t, beta, "prompt-caching-scope-2026-01-05")
		require.Contains(t, beta, "prompt-caching-2024-07-31")
		require.Contains(t, beta, "output-128k-2025-02-19")
	})

	t.Run("sets user-agent to Anthropic/JS with package version when present", func(t *testing.T) {
		h := http.Header{}
		h.Set("user-agent", "Claude-Code/2.1.258")
		h.Set("x-stainless-package-version", "0.33.1")

		stripClaudeCodeHeaderMarkers(h)

		require.Equal(t, "Anthropic/JS 0.33.1", getHeaderRaw(h, "user-agent"))
	})

	t.Run("preserves non-claude user-agent like curl", func(t *testing.T) {
		h := http.Header{}
		h.Set("user-agent", "curl/8.7.1")

		stripClaudeCodeHeaderMarkers(h)

		require.Equal(t, "curl/8.7.1", getHeaderRaw(h, "user-agent"))
	})
}

func TestStripClaudeCodeRequestMarkers(t *testing.T) {
	t.Run("strips beta=true query param from URL", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages?beta=true", nil)
		stripClaudeCodeRequestMarkers(req)
		require.Equal(t, "https://api.anthropic.com/v1/messages", req.URL.String())
	})

	t.Run("preserves other query params while stripping beta=true", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages?beta=true&proxy=http%3A%2F%2F127.0.0.1%3A8080", nil)
		stripClaudeCodeRequestMarkers(req)
		require.Equal(t, "https://api.anthropic.com/v1/messages?proxy=http%3A%2F%2F127.0.0.1%3A8080", req.URL.String())
	})
}

func newPlainAPITestContext(header http.Header, body []byte) *gin.Context {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	for k, vals := range header {
		for _, v := range vals {
			req.Header.Add(k, v)
		}
	}
	c.Request = req
	return c
}

func newPlainAPIClientHeader() http.Header {
	h := http.Header{}
	h.Set("x-app", "cli")
	h.Set("x-claude-code-session-id", "test-session-id-12345")
	h.Set("x-client-request-id", "test-client-req-999")
	h.Set("anthropic-dangerous-direct-browser-access", "true")
	h.Set("anthropic-beta", "claude-code-20250219,prompt-caching-scope-2026-01-05")
	h.Set("x-stainless-lang", "js")
	h.Set("x-stainless-package-version", "0.33.1")
	h.Set("x-stainless-os", "MacOS")
	h.Set("x-stainless-arch", "arm64")
	h.Set("user-agent", "Claude-Code/2.1.258 (arm64-apple-darwin)")
	return h
}

func newPlainAPIClientBody() []byte {
	return []byte(`{
		"model": "claude-3-7-sonnet-20250219",
		"system": [
			{"type": "text", "text": "x-anthropic-billing-header: cc_version=2.1.258; cc_entrypoint=cli;"},
			{"type": "text", "text": "You are Claude Code, Anthropic's official CLI for Claude."}
		],
		"context_management": {"edits": []},
		"thinking": {
			"type": "enabled",
			"budget_tokens": 2048,
			"block_binding": true
		},
		"messages": [{"role": "user", "content": "hello"}],
		"metadata": {
			"user_id": "test-user-id-abc"
		}
	}`)
}

func TestBuilders_PlainAPI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &GatewayService{cfg: &config.Config{}}

	accountPlain := &Account{
		ID:       101,
		Platform: PlatformAnthropic,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key": "sk-ant-test-key",
		},
		Extra: map[string]any{
			"anthropic_plain_api": true,
		},
	}

	accountStandard := &Account{
		ID:       102,
		Platform: PlatformAnthropic,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key": "sk-ant-test-key",
		},
		Extra: map[string]any{
			"anthropic_plain_api": false,
		},
	}

	t.Run("Path 1: buildUpstreamRequest (normal path)", func(t *testing.T) {
		c := newPlainAPITestContext(newPlainAPIClientHeader(), newPlainAPIClientBody())
		req, wireBody, err := svc.buildUpstreamRequest(
			context.Background(), c, accountPlain, newPlainAPIClientBody(),
			"sk-ant-test-key", "apikey", "claude-3-7-sonnet-20250219", false, false,
		)
		require.NoError(t, err)
		require.NotNil(t, req)

		// Wire URL: beta=true is stripped
		require.Equal(t, "https://api.anthropic.com/v1/messages", req.URL.String())

		// Wire body assertions
		require.NotContains(t, string(wireBody), "x-anthropic-billing-header")
		require.NotContains(t, string(wireBody), "You are Claude Code")
		require.False(t, gjson.GetBytes(wireBody, "system").Exists())
		require.False(t, gjson.GetBytes(wireBody, "metadata").Exists())
		require.False(t, gjson.GetBytes(wireBody, "context_management").Exists())
		require.False(t, gjson.GetBytes(wireBody, "thinking.block_binding").Exists())
		require.Equal(t, "enabled", gjson.GetBytes(wireBody, "thinking.type").String())

		// Wire headers assertions
		require.Equal(t, "", getHeaderRaw(req.Header, "x-app"))
		require.Equal(t, "", getHeaderRaw(req.Header, "x-claude-code-session-id"))
		require.Equal(t, "", getHeaderRaw(req.Header, "x-client-request-id"))
		require.Equal(t, "", getHeaderRaw(req.Header, "anthropic-dangerous-direct-browser-access"))
		require.Equal(t, "", getHeaderRaw(req.Header, "anthropic-beta"))
		require.Equal(t, "Anthropic/JS 0.33.1", getHeaderRaw(req.Header, "user-agent"))
		for k := range req.Header {
			require.False(t, strings.HasPrefix(strings.ToLower(k), "x-stainless-"))
		}
	})

	t.Run("Path 2: buildUpstreamRequestAnthropicAPIKeyPassthrough (passthrough path)", func(t *testing.T) {
		c := newPlainAPITestContext(newPlainAPIClientHeader(), newPlainAPIClientBody())
		req, wireBody, err := svc.buildUpstreamRequestAnthropicAPIKeyPassthrough(
			context.Background(), c, accountPlain, newPlainAPIClientBody(), "sk-ant-test-key",
		)
		require.NoError(t, err)
		require.NotNil(t, req)

		// Wire URL: beta=true is stripped
		require.Equal(t, "https://api.anthropic.com/v1/messages", req.URL.String())

		// Wire body assertions
		require.NotContains(t, string(wireBody), "x-anthropic-billing-header")
		require.NotContains(t, string(wireBody), "You are Claude Code")
		require.False(t, gjson.GetBytes(wireBody, "system").Exists())
		require.False(t, gjson.GetBytes(wireBody, "metadata").Exists())
		require.False(t, gjson.GetBytes(wireBody, "context_management").Exists())
		require.False(t, gjson.GetBytes(wireBody, "thinking.block_binding").Exists())

		// Wire headers assertions
		require.Equal(t, "", getHeaderRaw(req.Header, "x-app"))
		require.Equal(t, "", getHeaderRaw(req.Header, "x-claude-code-session-id"))
		require.Equal(t, "", getHeaderRaw(req.Header, "x-client-request-id"))
		require.Equal(t, "", getHeaderRaw(req.Header, "anthropic-dangerous-direct-browser-access"))
		require.Equal(t, "", getHeaderRaw(req.Header, "anthropic-beta"))
		require.Equal(t, "Anthropic/JS 0.33.1", getHeaderRaw(req.Header, "user-agent"))
		for k := range req.Header {
			require.False(t, strings.HasPrefix(strings.ToLower(k), "x-stainless-"))
		}
	})

	t.Run("Path 3: buildCountTokensRequest (normal path)", func(t *testing.T) {
		c := newPlainAPITestContext(newPlainAPIClientHeader(), newPlainAPIClientBody())
		req, wireBody, err := svc.buildCountTokensRequest(
			context.Background(), c, accountPlain, newPlainAPIClientBody(),
			"sk-ant-test-key", "apikey", "claude-3-7-sonnet-20250219", false,
		)
		require.NoError(t, err)
		require.NotNil(t, req)

		// Wire URL: beta=true is stripped
		require.Equal(t, "https://api.anthropic.com/v1/messages/count_tokens", req.URL.String())

		// Wire body assertions
		require.NotContains(t, string(wireBody), "x-anthropic-billing-header")
		require.NotContains(t, string(wireBody), "You are Claude Code")
		require.False(t, gjson.GetBytes(wireBody, "system").Exists())
		require.False(t, gjson.GetBytes(wireBody, "metadata").Exists())

		// Wire headers assertions
		require.Equal(t, "", getHeaderRaw(req.Header, "x-app"))
		require.Equal(t, "", getHeaderRaw(req.Header, "x-claude-code-session-id"))
		require.Equal(t, "", getHeaderRaw(req.Header, "x-client-request-id"))
		require.Equal(t, "", getHeaderRaw(req.Header, "anthropic-dangerous-direct-browser-access"))
		require.Equal(t, "Anthropic/JS 0.33.1", getHeaderRaw(req.Header, "user-agent"))
		for k := range req.Header {
			require.False(t, strings.HasPrefix(strings.ToLower(k), "x-stainless-"))
		}
	})

	t.Run("Path 4: buildCountTokensRequestAnthropicAPIKeyPassthrough (passthrough path)", func(t *testing.T) {
		c := newPlainAPITestContext(newPlainAPIClientHeader(), newPlainAPIClientBody())
		req, err := svc.buildCountTokensRequestAnthropicAPIKeyPassthrough(
			context.Background(), c, accountPlain, newPlainAPIClientBody(), "sk-ant-test-key",
		)
		require.NoError(t, err)
		require.NotNil(t, req)

		// Wire URL: beta=true is stripped
		require.Equal(t, "https://api.anthropic.com/v1/messages/count_tokens", req.URL.String())

		bodyBytes, err := io.ReadAll(req.Body)
		require.NoError(t, err)

		// Wire body assertions
		require.NotContains(t, string(bodyBytes), "x-anthropic-billing-header")
		require.NotContains(t, string(bodyBytes), "You are Claude Code")
		require.False(t, gjson.GetBytes(bodyBytes, "system").Exists())
		require.False(t, gjson.GetBytes(bodyBytes, "metadata").Exists())

		// Wire headers assertions
		require.Equal(t, "", getHeaderRaw(req.Header, "x-app"))
		require.Equal(t, "", getHeaderRaw(req.Header, "x-claude-code-session-id"))
		require.Equal(t, "", getHeaderRaw(req.Header, "x-client-request-id"))
		require.Equal(t, "", getHeaderRaw(req.Header, "anthropic-dangerous-direct-browser-access"))
		require.Equal(t, "Anthropic/JS 0.33.1", getHeaderRaw(req.Header, "user-agent"))
		for k := range req.Header {
			require.False(t, strings.HasPrefix(strings.ToLower(k), "x-stainless-"))
		}
	})

	t.Run("Plain API disabled preserves markers", func(t *testing.T) {
		c := newPlainAPITestContext(newPlainAPIClientHeader(), newPlainAPIClientBody())
		req, wireBody, err := svc.buildUpstreamRequest(
			context.Background(), c, accountStandard, newPlainAPIClientBody(),
			"sk-ant-test-key", "apikey", "claude-3-7-sonnet-20250219", false, false,
		)
		require.NoError(t, err)
		require.Contains(t, string(wireBody), "x-anthropic-billing-header")
		require.True(t, gjson.GetBytes(wireBody, "metadata.user_id").Exists())
		require.Equal(t, "cli", getHeaderRaw(req.Header, "x-app"))
		require.Equal(t, "test-session-id-12345", getHeaderRaw(req.Header, "x-claude-code-session-id"))
		require.Contains(t, getHeaderRaw(req.Header, "anthropic-beta"), "claude-code-20250219")
	})
}

func TestBuilders_OAuthAccountUnaffected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &GatewayService{cfg: &config.Config{}}

	oauthAccount := &Account{
		ID:       201,
		Platform: PlatformAnthropic,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "oauth-token-123",
		},
		Extra: map[string]any{
			"anthropic_plain_api": true, // Flag set on OAuth account
		},
	}
	require.False(t, oauthAccount.IsAnthropicPlainAPIEnabled(), "IsAnthropicPlainAPIEnabled must be false for OAuth")

	c := newPlainAPITestContext(newPlainAPIClientHeader(), newPlainAPIClientBody())
	req, _, err := svc.buildUpstreamRequest(
		context.Background(), c, oauthAccount, newPlainAPIClientBody(),
		"oauth-token-123", "oauth", "claude-3-7-sonnet-20250219", false, true,
	)
	require.NoError(t, err)
	require.NotNil(t, req)

	// In OAuth mimicry, official claude code mimic headers are applied, not Anthropic/JS
	ua := getHeaderRaw(req.Header, "user-agent")
	require.NotContains(t, ua, "Anthropic/JS", "OAuth mimicry UA must not be Anthropic/JS")
	require.Contains(t, ua, "claude-cli", "OAuth mimicry UA should be claude-cli")
	require.Contains(t, getHeaderRaw(req.Header, "anthropic-beta"), claude.BetaClaudeCode)
}

func TestBuilders_HeaderOverrideWinsOverPlainAPI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &GatewayService{cfg: &config.Config{}}

	accountWithOverride := &Account{
		ID:       301,
		Platform: PlatformAnthropic,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":                    "sk-ant-test-key",
			credKeyHeaderOverrideEnabled: true,
			credKeyHeaderOverrides: map[string]any{
				"user-agent": "Custom-SDK/4.2.0 (special-env)",
			},
		},
		Extra: map[string]any{
			"anthropic_plain_api": true,
		},
	}

	t.Run("buildUpstreamRequest normal path override wins", func(t *testing.T) {
		c := newPlainAPITestContext(newPlainAPIClientHeader(), newPlainAPIClientBody())
		req, _, err := svc.buildUpstreamRequest(
			context.Background(), c, accountWithOverride, newPlainAPIClientBody(),
			"sk-ant-test-key", "apikey", "claude-3-7-sonnet-20250219", false, false,
		)
		require.NoError(t, err)
		require.Equal(t, "Custom-SDK/4.2.0 (special-env)", getHeaderRaw(req.Header, "user-agent"))
	})

	t.Run("buildUpstreamRequestAnthropicAPIKeyPassthrough passthrough override wins", func(t *testing.T) {
		c := newPlainAPITestContext(newPlainAPIClientHeader(), newPlainAPIClientBody())
		req, _, err := svc.buildUpstreamRequestAnthropicAPIKeyPassthrough(
			context.Background(), c, accountWithOverride, newPlainAPIClientBody(), "sk-ant-test-key",
		)
		require.NoError(t, err)
		require.Equal(t, "Custom-SDK/4.2.0 (special-env)", getHeaderRaw(req.Header, "user-agent"))
	})
}
