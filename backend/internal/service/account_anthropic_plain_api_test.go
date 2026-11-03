package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccount_IsAnthropicPlainAPIEnabled(t *testing.T) {
	t.Run("Anthropic API Key 开启", func(t *testing.T) {
		account := &Account{
			Platform: PlatformAnthropic,
			Type:     AccountTypeAPIKey,
			Extra: map[string]any{
				"anthropic_plain_api": true,
			},
		}
		require.True(t, account.IsAnthropicPlainAPIEnabled())
	})

	t.Run("Anthropic API Key 关闭", func(t *testing.T) {
		account := &Account{
			Platform: PlatformAnthropic,
			Type:     AccountTypeAPIKey,
			Extra: map[string]any{
				"anthropic_plain_api": false,
			},
		}
		require.False(t, account.IsAnthropicPlainAPIEnabled())
	})

	t.Run("字段类型非法默认关闭", func(t *testing.T) {
		account := &Account{
			Platform: PlatformAnthropic,
			Type:     AccountTypeAPIKey,
			Extra: map[string]any{
				"anthropic_plain_api": "true",
			},
		}
		require.False(t, account.IsAnthropicPlainAPIEnabled())
	})

	t.Run("nil 或 Extra 为空默认关闭", func(t *testing.T) {
		var nilAcc *Account
		require.False(t, nilAcc.IsAnthropicPlainAPIEnabled())

		noExtra := &Account{
			Platform: PlatformAnthropic,
			Type:     AccountTypeAPIKey,
		}
		require.False(t, noExtra.IsAnthropicPlainAPIEnabled())
	})

	t.Run("非 Anthropic API Key 账号始终关闭", func(t *testing.T) {
		oauth := &Account{
			Platform: PlatformAnthropic,
			Type:     AccountTypeOAuth,
			Extra: map[string]any{
				"anthropic_plain_api": true,
			},
		}
		require.False(t, oauth.IsAnthropicPlainAPIEnabled())

		openai := &Account{
			Platform: PlatformOpenAI,
			Type:     AccountTypeAPIKey,
			Extra: map[string]any{
				"anthropic_plain_api": true,
			},
		}
		require.False(t, openai.IsAnthropicPlainAPIEnabled())
	})
}

