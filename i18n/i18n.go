package i18n

import "strings"

type Messages struct {
	SessionReset       string
	AllProvidersFailed string
	RequestInProgress  string
	ModelChanged       string
	ModelUsage         string
	ProviderNotFound   string
	SeeAttachment      string
}

var langs = map[string]Messages{
	"korean": {
		SessionReset:       "세션이 초기화되었습니다.",
		AllProvidersFailed: "모든 AI 제공자에 연결할 수 없습니다. 🔄 이모지를 눌러 재시도할 수 있습니다.",
		RequestInProgress:  "이전 요청을 처리하고 있습니다: `%s`\n완료될 때까지 잠시 기다려 주세요.",
		ModelChanged:       "%s 모델이 `%s`로 변경되었습니다.",
		ModelUsage:         "사용법: `!model <provider> <model>`\n예: `!model ollama gemma4:e4b`",
		ProviderNotFound:   "provider '%s'를 찾을 수 없습니다.",
		SeeAttachment:      "... (전체 내용은 첨부 파일 참조)",
	},
	"japanese": {
		SessionReset:       "セッションがリセットされました。",
		AllProvidersFailed: "すべてのAIプロバイダーに接続できません。🔄 リアクションでリトライできます。",
		RequestInProgress:  "前のリクエストを処理中です: `%s`\n完了するまでお待ちください。",
		ModelChanged:       "%s モデルが `%s` に変更されました。",
		ModelUsage:         "使い方: `!model <provider> <model>`\n例: `!model ollama gemma4:e4b`",
		ProviderNotFound:   "プロバイダー '%s' が見つかりません。",
		SeeAttachment:      "... (全文は添付ファイルを参照)",
	},
	"chinese": {
		SessionReset:       "会话已重置。",
		AllProvidersFailed: "无法连接到任何AI提供商。点击 🔄 重试。",
		RequestInProgress:  "正在处理上一个请求: `%s`\n请等待完成。",
		ModelChanged:       "%s 模型已更改为 `%s`。",
		ModelUsage:         "用法: `!model <provider> <model>`\n例: `!model ollama gemma4:e4b`",
		ProviderNotFound:   "找不到提供商 '%s'。",
		SeeAttachment:      "... (完整内容请参阅附件)",
	},
	"": defaultMessages,
}

var defaultMessages = Messages{
	SessionReset:       "Session has been reset.",
	AllProvidersFailed: "Unable to connect to any AI provider. React with 🔄 to retry.",
	RequestInProgress:  "Still processing: `%s`\nPlease wait until it completes.",
	ModelChanged:       "%s model changed to `%s`.",
	ModelUsage:         "Usage: `!model <provider> <model>`\nExample: `!model ollama gemma4:e4b`",
	ProviderNotFound:   "Provider '%s' not found.",
	SeeAttachment:      "... (see attached file for full content)",
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
