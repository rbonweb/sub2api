package service

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const defaultAnthropicPlainAPIJSSDKVersion = "0.94.0"

// stripClaudeCodeBodyMarkers removes Claude Code identity markers from the request body:
// 1. Removes every system[] text block that starts with x-anthropic-billing-header.
// 2. Removes metadata.user_id, and removes metadata too if that leaves it empty.
func stripClaudeCodeBodyMarkers(body []byte) []byte {
	if len(body) == 0 {
		return body
	}

	systemRes := gjson.GetBytes(body, "system")
	if systemRes.Exists() && systemRes.IsArray() {
		var toDelete []int
		idx := 0
		systemRes.ForEach(func(_, item gjson.Result) bool {
			var textVal string
			if text := item.Get("text"); text.Exists() && text.Type == gjson.String {
				textVal = text.String()
			} else if item.Type == gjson.String {
				textVal = item.String()
			}
			if strings.HasPrefix(textVal, "x-anthropic-billing-header") {
				toDelete = append(toDelete, idx)
			}
			idx++
			return true
		})
		for i := len(toDelete) - 1; i >= 0; i-- {
			if updated, err := sjson.DeleteBytes(body, fmt.Sprintf("system.%d", toDelete[i])); err == nil {
				body = updated
			}
		}
	}

	if gjson.GetBytes(body, "metadata.user_id").Exists() {
		if updated, err := sjson.DeleteBytes(body, "metadata.user_id"); err == nil {
			body = updated
		}
	}
	metaRes := gjson.GetBytes(body, "metadata")
	if metaRes.Exists() && metaRes.IsObject() && len(metaRes.Map()) == 0 {
		if updated, err := sjson.DeleteBytes(body, "metadata"); err == nil {
			body = updated
		}
	}

	return body
}

// stripClaudeCodeHeaderMarkers cleans Claude Code identity markers from headers:
// 1. Removes x-app and x-claude-code-session-id in any casing.
// 2. Removes claude-code-20250219 from anthropic-beta. If nothing is left, the header is dropped.
// 3. Sets user-agent to Anthropic/JS <version>, the format of the official TypeScript SDK.
//    The version comes from the client's x-stainless-package-version, so the user-agent
//    matches the x-stainless-* headers. If that header is missing, a fixed version is used.
func stripClaudeCodeHeaderMarkers(header http.Header) {
	if header == nil {
		return
	}

	// 1. Removes x-app and x-claude-code-session-id in any casing.
	for k := range header {
		if strings.EqualFold(k, "x-app") || strings.EqualFold(k, "x-claude-code-session-id") {
			delete(header, k)
		}
	}
	header.Del("x-app")
	header.Del("x-claude-code-session-id")

	// 2. Removes claude-code-20250219 from anthropic-beta. If nothing is left, the header is dropped.
	var betaTokens []string
	for k, vals := range header {
		if strings.EqualFold(k, "anthropic-beta") {
			for _, v := range vals {
				betaTokens = append(betaTokens, parseAnthropicBetaHeader(v)...)
			}
		}
	}

	for k := range header {
		if strings.EqualFold(k, "anthropic-beta") {
			delete(header, k)
		}
	}
	header.Del("anthropic-beta")

	var remainingBetas []string
	for _, tok := range betaTokens {
		if !strings.EqualFold(tok, "claude-code-20250219") {
			remainingBetas = append(remainingBetas, tok)
		}
	}
	if len(remainingBetas) > 0 {
		setHeaderRaw(header, resolveWireCasing("anthropic-beta"), strings.Join(remainingBetas, ","))
	}

	// 3. Sets user-agent to Anthropic/JS <version>.
	var pkgVersion string
	for k, vals := range header {
		if strings.EqualFold(k, "x-stainless-package-version") && len(vals) > 0 {
			for _, v := range vals {
				v = strings.TrimSpace(v)
				if v != "" {
					pkgVersion = v
					break
				}
			}
			if pkgVersion != "" {
				break
			}
		}
	}
	if pkgVersion == "" {
		pkgVersion = defaultAnthropicPlainAPIJSSDKVersion
	}

	for k := range header {
		if strings.EqualFold(k, "user-agent") {
			delete(header, k)
		}
	}
	header.Del("user-agent")
	setHeaderRaw(header, resolveWireCasing("user-agent"), "Anthropic/JS "+pkgVersion)
}

