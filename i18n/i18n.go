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
	Help               string
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
		Help: "**pigeon-claw 명령어**\n" +
			"| 명령어 | 설명 |\n|---|---|\n" +
			"| `!reset` | 현재 채널 세션 초기화 |\n" +
			"| `!cancel` | 처리 중인 요청 취소 |\n" +
			"| `!restart` | 봇 프로세스 재시작 |\n" +
			"| `!status` | 현재 provider 및 메시지 수 |\n" +
			"| `!debug` | 마지막 에러, 세션 정보 |\n" +
			"| `!model` | 모델 목록 / 변경 |\n" +
			"| `!provider` | provider 우선순위 |",
	},
	"japanese": {
		SessionReset:       "セッションがリセットされました。",
		AllProvidersFailed: "すべてのAIプロバイダーに接続できません。🔄 リアクションでリトライできます。",
		RequestInProgress:  "前のリクエストを処理中です: `%s`\n完了するまでお待ちください。",
		ModelChanged:       "%s モデルが `%s` に変更されました。",
		ModelUsage:         "使い方: `!model <provider> <model>`\n例: `!model ollama gemma4:e4b`",
		ProviderNotFound:   "プロバイダー '%s' が見つかりません。",
		SeeAttachment:      "... (全文は添付ファイルを参照)",
		Help: "**pigeon-claw コマンド**\n" +
			"| コマンド | 説明 |\n|---|---|\n" +
			"| `!reset` | セッションリセット |\n" +
			"| `!cancel` | リクエストキャンセル |\n" +
			"| `!restart` | ボット再起動 |\n" +
			"| `!status` | ステータス確認 |\n" +
			"| `!debug` | デバッグ情報 |\n" +
			"| `!model` | モデル一覧/変更 |\n" +
			"| `!provider` | プロバイダー一覧 |",
	},
	"chinese": {
		SessionReset:       "会话已重置。",
		AllProvidersFailed: "无法连接到任何AI提供商。点击 🔄 重试。",
		RequestInProgress:  "正在处理上一个请求: `%s`\n请等待完成。",
		ModelChanged:       "%s 模型已更改为 `%s`。",
		ModelUsage:         "用法: `!model <provider> <model>`\n例: `!model ollama gemma4:e4b`",
		ProviderNotFound:   "找不到提供商 '%s'。",
		SeeAttachment:      "... (完整内容请参阅附件)",
		Help: "**pigeon-claw 命令**\n" +
			"| 命令 | 说明 |\n|---|---|\n" +
			"| `!reset` | 重置会话 |\n" +
			"| `!cancel` | 取消当前请求 |\n" +
			"| `!restart` | 重启机器人 |\n" +
			"| `!status` | 状态信息 |\n" +
			"| `!debug` | 调试信息 |\n" +
			"| `!model` | 模型列表/更改 |\n" +
			"| `!provider` | 提供商列表 |",
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
	Help: "**pigeon-claw Commands**\n" +
		"| Command | Description |\n|---|---|\n" +
		"| `!reset` | Reset channel session |\n" +
		"| `!cancel` | Cancel current request |\n" +
		"| `!restart` | Restart bot process |\n" +
		"| `!status` | Show provider & message count |\n" +
		"| `!debug` | Last error & session info |\n" +
		"| `!model` | List/change models |\n" +
		"| `!provider` | Show provider priority |",
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
