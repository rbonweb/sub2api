package service

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const defaultAnthropicPlainAPIJSSDKVersion = "0.94.0"

var claudeCodeIdentityOpeners = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^[ \t\r\n]*You are Claude Code[^\r\n]*[\r\n]*`),
	regexp.MustCompile(`(?i)^[ \t\r\n]*You are a Claude agent[^\r\n]*[\r\n]*`),
	regexp.MustCompile(`(?i)^[ \t\r\n]*You are a file search specialist for Claude Code[^\r\n]*[\r\n]*`),
	regexp.MustCompile(`(?i)^[ \t\r\n]*You are an interactive CLI tool that helps users[^\r\n]*[\r\n]*`),
	regexp.MustCompile(`(?i)^[ \t\r\n]*You are a security monitor for autonomous AI coding agents[^\r\n]*[\r\n]*`),
	regexp.MustCompile(`(?i)^[ \t\r\n]*You are a helpful AI assistant tasked with summarizing conversations[^\r\n]*[\r\n]*`),
}

var claudeCodeVendorMentionRe = regexp.MustCompile(`(?i)\bClaude Code\b`)

// cleanClaudeCodeSystemText checks whether a system text block contains Claude Code
// attribution or identity lines, and either flags it for deletion or rewrites it
// to a vendor-neutral AI assistant persona.
func cleanClaudeCodeSystemText(text string) (shouldDelete bool, cleaned string) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return true, ""
	}

	// 1. Any billing block is completely deleted or stripped.
	if strings.HasPrefix(trimmed, "x-anthropic-billing-header") || strings.Contains(trimmed, "x-anthropic-billing-header:") {
		if strings.HasPrefix(trimmed, "x-anthropic-billing-header") {
			end := strings.IndexAny(trimmed, "\r\n")
			if end < 0 {
				return true, ""
			}
			rest := strings.TrimSpace(trimmed[end:])
			if rest == "" {
				return true, ""
			}
			text = rest
		} else {
			return true, ""
		}
	}

	// 2. Check for Claude Code identity openers.
	for _, re := range claudeCodeIdentityOpeners {
		if loc := re.FindStringIndex(text); loc != nil && loc[0] == 0 {
			rest := strings.TrimSpace(text[loc[1]:])
			if rest == "" {
				// The entire block was just the identity statement.
				return true, ""
			}
			cleaned = "You are an AI assistant.\n\n" + rest
			cleaned = sanitizeClaudeCodeMentions(cleaned)
			return false, strings.TrimSpace(cleaned)
		}
	}

	// 3. If the block mentions Claude Code identity mid-text.
	if strings.Contains(text, "You are Claude Code") || strings.Contains(text, "official CLI for Claude") || strings.Contains(text, "Claude Agent SDK") {
		cleaned = sanitizeClaudeCodeMentions(text)
		return false, strings.TrimSpace(cleaned)
	}

	return false, text
}

func sanitizeClaudeCodeMentions(text string) string {
	res := strings.ReplaceAll(text, "You are Claude Code, Anthropic's official CLI for Claude.", "You are an AI assistant.")
	res = strings.ReplaceAll(res, "Anthropic's official CLI for Claude", "an AI assistant")
	res = strings.ReplaceAll(res, "Claude Agent SDK", "Agent SDK")
	return claudeCodeVendorMentionRe.ReplaceAllString(res, "AI Assistant")
}

// stripClaudeCodeBodyMarkers removes Claude Code identity markers and internal
// features from the request body:
//  1. Removes billing attribution blocks and cleans Claude Code identity from system prompt.
//     If system becomes empty, removes the system field completely.
//  2. Removes metadata.user_id, and removes metadata too if empty.
//  3. Removes Claude-Code-exclusive fields: context_management, thinking.block_binding,
//     fallbacks, fallback_credit_token, messages[].output_config.
func stripClaudeCodeBodyMarkers(body []byte) []byte {
	if len(body) == 0 {
		return body
	}

	// 1. System prompt handling
	systemRes := gjson.GetBytes(body, "system")
	if systemRes.Exists() {
		if systemRes.IsArray() {
			var toDelete []int
			type replacement struct {
				idx  int
				text string
			}
			var toReplace []replacement

			idx := 0
			systemRes.ForEach(func(_, item gjson.Result) bool {
				var textVal string
				isObj := item.IsObject()
				if isObj {
					if t := item.Get("text"); t.Exists() && t.Type == gjson.String {
						textVal = t.String()
					}
				} else if item.Type == gjson.String {
					textVal = item.String()
				}

				shouldDel, cleaned := cleanClaudeCodeSystemText(textVal)
				if shouldDel {
					toDelete = append(toDelete, idx)
				} else if cleaned != textVal {
					toReplace = append(toReplace, replacement{idx: idx, text: cleaned})
				}
				idx++
				return true
			})

			// Apply in-place text replacements
			for _, r := range toReplace {
				path := fmt.Sprintf("system.%d.text", r.idx)
				if !systemRes.Get(fmt.Sprintf("%d", r.idx)).IsObject() {
					path = fmt.Sprintf("system.%d", r.idx)
				}
				if updated, err := sjson.SetBytes(body, path, r.text); err == nil {
					body = updated
				}
			}

			// Delete items in reverse order
			for i := len(toDelete) - 1; i >= 0; i-- {
				if updated, err := sjson.DeleteBytes(body, fmt.Sprintf("system.%d", toDelete[i])); err == nil {
					body = updated
				}
			}

			// If system array is now empty, delete system property completely
			afterSystem := gjson.GetBytes(body, "system")
			if afterSystem.Exists() && afterSystem.IsArray() && len(afterSystem.Array()) == 0 {
				if updated, err := sjson.DeleteBytes(body, "system"); err == nil {
					body = updated
				}
			}
		} else if systemRes.Type == gjson.String {
			shouldDel, cleaned := cleanClaudeCodeSystemText(systemRes.String())
			if shouldDel {
				if updated, err := sjson.DeleteBytes(body, "system"); err == nil {
					body = updated
				}
			} else if cleaned != systemRes.String() {
				if updated, err := sjson.SetBytes(body, "system", cleaned); err == nil {
					body = updated
				}
			}
		}
	}

	// 2. Metadata handling
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

	// 3. Claude Code internal/exclusive fields
	if gjson.GetBytes(body, "context_management").Exists() {
		if updated, err := sjson.DeleteBytes(body, "context_management"); err == nil {
			body = updated
		}
	}
	if gjson.GetBytes(body, "thinking.block_binding").Exists() {
		if updated, err := sjson.DeleteBytes(body, "thinking.block_binding"); err == nil {
			body = updated
		}
	}
	if gjson.GetBytes(body, "fallbacks").Exists() {
		if updated, err := sjson.DeleteBytes(body, "fallbacks"); err == nil {
			body = updated
		}
	}
	if gjson.GetBytes(body, "fallback_credit_token").Exists() {
		if updated, err := sjson.DeleteBytes(body, "fallback_credit_token"); err == nil {
			body = updated
		}
	}
	msgs := gjson.GetBytes(body, "messages")
	if msgs.Exists() && msgs.IsArray() {
		for idx, msg := range msgs.Array() {
			if msg.Get("output_config").Exists() {
				if updated, err := sjson.DeleteBytes(body, fmt.Sprintf("messages.%d.output_config", idx)); err == nil {
					body = updated
				}
			}
		}
	}

	return body
}

// isClaudeCodeBetaToken returns true if the beta token is exclusive to Claude Code
// or internal tooling (not a standard public Anthropic Messages API beta).
func isClaudeCodeBetaToken(token string) bool {
	tok := strings.ToLower(strings.TrimSpace(token))
	return strings.HasPrefix(tok, "claude-code") ||
		strings.HasPrefix(tok, "oauth-") ||
		strings.HasPrefix(tok, "prompt-caching-scope-") ||
		strings.HasPrefix(tok, "thinking-binding-controls-") ||
		strings.HasPrefix(tok, "mid-conversation-output-config-") ||
		strings.HasPrefix(tok, "context-management-") ||
		strings.HasPrefix(tok, "extended-cache-ttl-") ||
		strings.HasPrefix(tok, "effort-") ||
		strings.HasPrefix(tok, "server-side-fallback-") ||
		strings.HasPrefix(tok, "fallback-credit-")
}

// stripClaudeCodeHeaderMarkers cleans Claude Code identity markers from headers:
//  1. Removes x-app, x-claude-code-session-id, x-client-request-id,
//     anthropic-dangerous-direct-browser-access, and all x-stainless-* headers in any casing.
//  2. Removes Claude-Code-exclusive beta tokens from anthropic-beta. If nothing is left,
//     the anthropic-beta header is dropped completely.
//  3. Sets user-agent to Anthropic/JS <version> if incoming UA is from Claude Code or empty.
//     Non-Claude user-agents (e.g. curl, python) are preserved.
func stripClaudeCodeHeaderMarkers(header http.Header) {
	if header == nil {
		return
	}

	// Extract package version from x-stainless-package-version before deleting it.
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

	// 1. Remove x-app, x-claude-* (including session ID, compaction, agent ID, etc.),
	// x-client-request-id, anthropic-dangerous-direct-browser-access, and all x-stainless-* headers.
	var toDeleteKeys []string
	for k := range header {
		lower := strings.ToLower(k)
		if lower == "x-app" ||
			lower == "x-client-request-id" ||
			lower == "anthropic-dangerous-direct-browser-access" ||
			strings.HasPrefix(lower, "x-claude-") ||
			strings.HasPrefix(lower, "x-stainless-") {
			toDeleteKeys = append(toDeleteKeys, k)
		}
	}
	for _, k := range toDeleteKeys {
		delete(header, k)
	}
	deleteHeaderAllForms(header, "x-app")
	deleteHeaderAllForms(header, "x-claude-code-session-id")
	deleteHeaderAllForms(header, "x-client-request-id")
	deleteHeaderAllForms(header, "anthropic-dangerous-direct-browser-access")

	// 2. Clean anthropic-beta: remove all Claude Code internal betas.
	var betaTokens []string
	for k, vals := range header {
		if strings.EqualFold(k, "anthropic-beta") {
			for _, v := range vals {
				betaTokens = append(betaTokens, parseAnthropicBetaHeader(v)...)
			}
		}
	}

	deleteHeaderAllForms(header, "anthropic-beta")

	var remainingBetas []string
	for _, tok := range betaTokens {
		if !isClaudeCodeBetaToken(tok) {
			remainingBetas = append(remainingBetas, tok)
		}
	}
	if len(remainingBetas) > 0 {
		setHeaderRaw(header, resolveWireCasing("anthropic-beta"), strings.Join(remainingBetas, ","))
	}

	// 3. User-Agent: if empty or Claude Code, normalize to official JS SDK UA.
	currentUA := getHeaderRaw(header, "user-agent")
	lowerUA := strings.ToLower(currentUA)
	if currentUA == "" || strings.Contains(lowerUA, "claude") {
		deleteHeaderAllForms(header, "user-agent")
		setHeaderRaw(header, resolveWireCasing("user-agent"), "Anthropic/JS "+pkgVersion)
	}
}

// stripClaudeCodeRequestMarkers cleans Claude Code identity markers from headers and request URL:
// 1. Calls stripClaudeCodeHeaderMarkers(req.Header)
// 2. Removes ?beta=true query parameter from req.URL if present.
func stripClaudeCodeRequestMarkers(req *http.Request) {
	if req == nil {
		return
	}
	stripClaudeCodeHeaderMarkers(req.Header)
	if req.URL != nil {
		q := req.URL.Query()
		if _, ok := q["beta"]; ok {
			q.Del("beta")
			req.URL.RawQuery = q.Encode()
		}
	}
}
