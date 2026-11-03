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
	t.Run("billing block present and prompt preserved", func(t *testing.T) {
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
		require.Contains(t, string(cleaned), "You are Claude Code, Anthropic's official CLI for Claude.")

		system := gjson.GetBytes(cleaned, "system")
		require.True(t, system.IsArray())
		require.Equal(t, int64(1), system.Get("#").Int())
		require.Equal(t, "You are Claude Code, Anthropic's official CLI for Claude.", system.Get("0.text").String())
		require.Equal(t, "ephemeral", system.Get("0.cache_control.type").String())
	})

	t.Run("billing block missing leaves system unchanged", func(t *testing.T) {
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

	t.Run("string system prompt is untouched", func(t *testing.T) {
		body := []byte(`{
			"model": "claude-3-7-sonnet-20250219",
			"system": "You are a helpful assistant.",
			"messages": [{"role": "user", "content": "hi"}]
		}`)

		cleaned := stripClaudeCodeBodyMarkers(body)
		require.Equal(t, string(body), string(cleaned))
	})

	t.Run("multiple billing blocks are all removed", func(t *testing.T) {
		body := []byte(`{
			"model": "claude-3-7-sonnet-20250219",
			"system": [
				{"type": "text", "text": "x-anthropic-billing-header: cc_version=2.1.1;"},
				{"type": "text", "text": "Prompt line 1"},
				{"type": "text", "text": "x-anthropic-billing-header: cc_version=2.1.2;"},
				{"type": "text", "text": "Prompt line 2"}
			]
		}`)

		cleaned := stripClaudeCodeBodyMarkers(body)
		require.NotContains(t, string(cleaned), "x-anthropic-billing-header")
		system := gjson.GetBytes(cleaned, "system")
		require.Equal(t, int64(2), system.Get("#").Int())
		require.Equal(t, "Prompt line 1", system.Get("0.text").String())
		require.Equal(t, "Prompt line 2", system.Get("1.text").String())
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

	t.Run("metadata without user_id remains untouched", func(t *testing.T) {
		body := []byte(`{
			"model": "claude-3-7-sonnet-20250219",
			"metadata": {
				"custom": "value"
			}
		}`)

		cleaned := stripClaudeCodeBodyMarkers(body)
		require.Equal(t, "value", gjson.GetBytes(cleaned, "metadata.custom").String())
	})
}

func TestStripClaudeCodeHeaderMarkers(t *testing.T) {
	t.Run("removes x-app and x-claude-code-session-id in any casing", func(t *testing.T) {
		casings := []struct {
			name      string
			appKey    string
			sessKey   string
			appVal    string
			sessVal   string
		}{
			{"canonical", "X-App", "X-Claude-Code-Session-Id", "cli", "sess-1"},
			{"lowercase", "x-app", "x-claude-code-session-id", "cli", "sess-2"},
			{"uppercase", "X-APP", "X-CLAUDE-CODE-SESSION-ID", "cli", "sess-3"},
			{"mixed", "X-aPp", "x-Claude-Code-Session-Id", "cli", "sess-4"},
		}

		for _, tc := range casings {
			t.Run(tc.name, func(t *testing.T) {
				h := http.Header{}
				h[tc.appKey] = []string{tc.appVal}
				h[tc.sessKey] = []string{tc.sessVal}
				h.Set("x-stainless-package-version", "0.33.0")

				stripClaudeCodeHeaderMarkers(h)

				for k := range h {
					lower := strings.ToLower(k)
					require.NotEqual(t, "x-app", lower, "x-app must be removed: key="+k)
					require.NotEqual(t, "x-claude-code-session-id", lower, "x-claude-code-session-id must be removed: key="+k)
				}
				require.Equal(t, "", getHeaderRaw(h, "x-app"))
				require.Equal(t, "", getHeaderRaw(h, "x-claude-code-session-id"))
			})
		}
	})

	t.Run("beta list with claude-code token only is dropped completely", func(t *testing.T) {
		for _, key := range []string{"anthropic-beta", "Anthropic-Beta", "ANTHROPIC-BETA"} {
			h := http.Header{}
			h[key] = []string{"claude-code-20250219"}

			stripClaudeCodeHeaderMarkers(h)

			require.Equal(t, "", getHeaderRaw(h, "anthropic-beta"))
			require.Equal(t, "", h.Get("anthropic-beta"))
			require.Equal(t, "", h.Get("Anthropic-Beta"))
		}
	})

	t.Run("beta list with claude-code and other tokens retains others", func(t *testing.T) {
		h := http.Header{}
		h.Set("anthropic-beta", "claude-code-20250219,prompt-caching-scope-2026-01-05,interleaved-thinking-2025-05-14")

		stripClaudeCodeHeaderMarkers(h)

		beta := getHeaderRaw(h, "anthropic-beta")
		require.NotContains(t, beta, "claude-code-20250219")
		require.Contains(t, beta, "prompt-caching-scope-2026-01-05")
		require.Contains(t, beta, "interleaved-thinking-2025-05-14")
	})

	t.Run("beta list without claude-code token retains all tokens", func(t *testing.T) {
		h := http.Header{}
		h.Set("anthropic-beta", "prompt-caching-scope-2026-01-05,interleaved-thinking-2025-05-14")

		stripClaudeCodeHeaderMarkers(h)

		beta := getHeaderRaw(h, "anthropic-beta")
		require.Contains(t, beta, "prompt-caching-scope-2026-01-05")
		require.Contains(t, beta, "interleaved-thinking-2025-05-14")
	})

	t.Run("beta header missing remains absent", func(t *testing.T) {
		h := http.Header{}
		stripClaudeCodeHeaderMarkers(h)
		require.Equal(t, "", getHeaderRaw(h, "anthropic-beta"))
	})

	t.Run("sets user-agent to Anthropic/JS with package version when present", func(t *testing.T) {
		h := http.Header{}
		h.Set("user-agent", "Claude-Code/2.1.258")
		h.Set("x-stainless-package-version", "0.33.1")

		stripClaudeCodeHeaderMarkers(h)

		require.Equal(t, "Anthropic/JS 0.33.1", getHeaderRaw(h, "user-agent"))
		require.Equal(t, "Anthropic/JS 0.33.1", h.Get("User-Agent"))
	})

	t.Run("sets user-agent to Anthropic/JS with fixed default version when missing", func(t *testing.T) {
		h := http.Header{}
		h.Set("user-agent", "Claude-Code/2.1.258")

		stripClaudeCodeHeaderMarkers(h)

		require.Equal(t, "Anthropic/JS 0.94.0", getHeaderRaw(h, "user-agent"))
		require.Equal(t, "Anthropic/JS 0.94.0", h.Get("User-Agent"))
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
	h.Set("anthropic-beta", "claude-code-20250219,prompt-caching-scope-2026-01-05")
	h.Set("x-stainless-package-version", "0.33.1")
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

		// Wire body assertions
		require.NotContains(t, string(wireBody), "x-anthropic-billing-header")
		require.Contains(t, string(wireBody), "You are Claude Code, Anthropic's official CLI for Claude.")
		require.False(t, gjson.GetBytes(wireBody, "metadata").Exists())
		require.False(t, gjson.GetBytes(wireBody, "metadata.user_id").Exists())

		// Wire headers assertions
		require.Equal(t, "", getHeaderRaw(req.Header, "x-app"))
		require.Equal(t, "", getHeaderRaw(req.Header, "x-claude-code-session-id"))
		require.NotContains(t, getHeaderRaw(req.Header, "anthropic-beta"), "claude-code-20250219")
		require.Contains(t, getHeaderRaw(req.Header, "anthropic-beta"), "prompt-caching-scope-2026-01-05")
		require.Equal(t, "Anthropic/JS 0.33.1", getHeaderRaw(req.Header, "user-agent"))
	})

	t.Run("Path 2: buildUpstreamRequestAnthropicAPIKeyPassthrough (passthrough path)", func(t *testing.T) {
		c := newPlainAPITestContext(newPlainAPIClientHeader(), newPlainAPIClientBody())
		req, wireBody, err := svc.buildUpstreamRequestAnthropicAPIKeyPassthrough(
			context.Background(), c, accountPlain, newPlainAPIClientBody(), "sk-ant-test-key",
		)
		require.NoError(t, err)
		require.NotNil(t, req)

		// Wire body assertions
		require.NotContains(t, string(wireBody), "x-anthropic-billing-header")
		require.Contains(t, string(wireBody), "You are Claude Code, Anthropic's official CLI for Claude.")
		require.False(t, gjson.GetBytes(wireBody, "metadata").Exists())
		require.False(t, gjson.GetBytes(wireBody, "metadata.user_id").Exists())

		// Wire headers assertions
		require.Equal(t, "", getHeaderRaw(req.Header, "x-app"))
		require.Equal(t, "", getHeaderRaw(req.Header, "x-claude-code-session-id"))
		require.NotContains(t, getHeaderRaw(req.Header, "anthropic-beta"), "claude-code-20250219")
		require.Contains(t, getHeaderRaw(req.Header, "anthropic-beta"), "prompt-caching-scope-2026-01-05")
		require.Equal(t, "Anthropic/JS 0.33.1", getHeaderRaw(req.Header, "user-agent"))
	})

	t.Run("Path 3: buildCountTokensRequest (normal path)", func(t *testing.T) {
		c := newPlainAPITestContext(newPlainAPIClientHeader(), newPlainAPIClientBody())
		req, wireBody, err := svc.buildCountTokensRequest(
			context.Background(), c, accountPlain, newPlainAPIClientBody(),
			"sk-ant-test-key", "apikey", "claude-3-7-sonnet-20250219", false,
		)
		require.NoError(t, err)
		require.NotNil(t, req)

		// Wire body assertions
		require.NotContains(t, string(wireBody), "x-anthropic-billing-header")
		require.Contains(t, string(wireBody), "You are Claude Code, Anthropic's official CLI for Claude.")
		require.False(t, gjson.GetBytes(wireBody, "metadata").Exists())
		require.False(t, gjson.GetBytes(wireBody, "metadata.user_id").Exists())

		// Wire headers assertions
		require.Equal(t, "", getHeaderRaw(req.Header, "x-app"))
		require.Equal(t, "", getHeaderRaw(req.Header, "x-claude-code-session-id"))
		require.NotContains(t, getHeaderRaw(req.Header, "anthropic-beta"), "claude-code-20250219")
		require.Contains(t, getHeaderRaw(req.Header, "anthropic-beta"), "prompt-caching-scope-2026-01-05")
		require.Equal(t, "Anthropic/JS 0.33.1", getHeaderRaw(req.Header, "user-agent"))
	})

	t.Run("Path 4: buildCountTokensRequestAnthropicAPIKeyPassthrough (passthrough path)", func(t *testing.T) {
		c := newPlainAPITestContext(newPlainAPIClientHeader(), newPlainAPIClientBody())
		req, err := svc.buildCountTokensRequestAnthropicAPIKeyPassthrough(
			context.Background(), c, accountPlain, newPlainAPIClientBody(), "sk-ant-test-key",
		)
		require.NoError(t, err)
		require.NotNil(t, req)

		bodyBytes, err := io.ReadAll(req.Body)
		require.NoError(t, err)

		// Wire body assertions
		require.NotContains(t, string(bodyBytes), "x-anthropic-billing-header")
		require.Contains(t, string(bodyBytes), "You are Claude Code, Anthropic's official CLI for Claude.")
		require.False(t, gjson.GetBytes(bodyBytes, "metadata").Exists())
		require.False(t, gjson.GetBytes(bodyBytes, "metadata.user_id").Exists())

		// Wire headers assertions
		require.Equal(t, "", getHeaderRaw(req.Header, "x-app"))
		require.Equal(t, "", getHeaderRaw(req.Header, "x-claude-code-session-id"))
		require.NotContains(t, getHeaderRaw(req.Header, "anthropic-beta"), "claude-code-20250219")
		require.Contains(t, getHeaderRaw(req.Header, "anthropic-beta"), "prompt-caching-scope-2026-01-05")
		require.Equal(t, "Anthropic/JS 0.33.1", getHeaderRaw(req.Header, "user-agent"))
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
			"api_key":                     "sk-ant-test-key",
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
