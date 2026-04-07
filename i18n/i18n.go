package i18n

import "strings"

type Messages struct {
	SessionReset      string
	AllProvidersFailed string
	RequestInProgress string
	ModelChanged      string
	ModelUsage        string
	ProviderNotFound  string
}

var langs = map[string]Messages{
	"korean": {
		SessionReset:      "세션이 초기화되었습니다.",
		AllProvidersFailed: "모든 AI 제공자에 연결할 수 없습니다. 잠시 후 다시 시도해 주세요.",
		RequestInProgress: "이전 요청 처리 중입니다. 잠시 후 다시 시도해 주세요.",
		ModelChanged:      "%s 모델이 `%s`로 변경되었습니다.",
		ModelUsage:        "사용법: `!model <provider> <model>`\n예: `!model ollama gemma4:e4b`",
		ProviderNotFound:  "provider '%s'를 찾을 수 없습니다.",
	},
	"japanese": {
		SessionReset:      "セッションがリセットされました。",
		AllProvidersFailed: "すべてのAIプロバイダーに接続できません。しばらくしてからもう一度お試しください。",
		RequestInProgress: "前のリクエストを処理中です。しばらくお待ちください。",
		ModelChanged:      "%s モデルが `%s` に変更されました。",
		ModelUsage:        "使い方: `!model <provider> <model>`\n例: `!model ollama gemma4:e4b`",
		ProviderNotFound:  "プロバイダー '%s' が見つかりません。",
	},
	"chinese": {
		SessionReset:      "会话已重置。",
		AllProvidersFailed: "无法连接到任何AI提供商。请稍后重试。",
		RequestInProgress: "正在处理上一个请求，请稍后再试。",
		ModelChanged:      "%s 模型已更改为 `%s`。",
		ModelUsage:        "用法: `!model <provider> <model>`\n例: `!model ollama gemma4:e4b`",
		ProviderNotFound:  "找不到提供商 '%s'。",
	},
	"": defaultMessages,
}

var defaultMessages = Messages{
	SessionReset:      "Session has been reset.",
	AllProvidersFailed: "Unable to connect to any AI provider. Please try again later.",
	RequestInProgress: "Previous request still processing. Please wait.",
	ModelChanged:      "%s model changed to `%s`.",
	ModelUsage:        "Usage: `!model <provider> <model>`\nExample: `!model ollama gemma4:e4b`",
	ProviderNotFound:  "Provider '%s' not found.",
}

func Get(language string) Messages {
	lang := strings.ToLower(language)
	for key, msgs := range langs {
		if key != "" && strings.Contains(lang, key) {
			return msgs
		}
	}
	return defaultMessages
}
