package i18n

import "strings"

type Messages struct {
	SessionReset       string
	AllProvidersFailed string
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
		ModelChanged:       "%s 모델이 `%s`로 변경되었습니다.",
		ModelUsage:         "사용법: `!model <provider> <model>`\n예: `!model ollama gemma4:e4b`",
		ProviderNotFound:   "provider '%s'를 찾을 수 없습니다.",
		SeeAttachment:      "... (전체 내용은 첨부 파일 참조)",
		Help: "**pigeon-claw 명령어**\n" +
			"| 명령어 | 설명 |\n|---|---|\n" +
			"| `!reset` | 현재 채널 세션 초기화 |\n" +
			"| `!cancel` | 처리 중인 요청 + 대기열 취소 |\n" +
			"| `!restart` | 봇 프로세스 재시작 |\n" +
			"| `!update` | 최신 릴리스로 업데이트 후 재시작 |\n" +
			"| `!status` | provider·모델, 처리/대기 현황, 마지막 에러 (= `!debug`) |\n" +
			"| `!model` | 모델 드롭다운에서 선택 |\n",
	},
	"japanese": {
		SessionReset:       "セッションがリセットされました。",
		AllProvidersFailed: "すべてのAIプロバイダーに接続できません。🔄 リアクションでリトライできます。",
		ModelChanged:       "%s モデルが `%s` に変更されました。",
		ModelUsage:         "使い方: `!model <provider> <model>`\n例: `!model ollama gemma4:e4b`",
		ProviderNotFound:   "プロバイダー '%s' が見つかりません。",
		SeeAttachment:      "... (全文は添付ファイルを参照)",
		Help: "**pigeon-claw コマンド**\n" +
			"| コマンド | 説明 |\n|---|---|\n" +
			"| `!reset` | セッションリセット |\n" +
			"| `!cancel` | 実行中のリクエストと待機列をキャンセル |\n" +
			"| `!restart` | ボット再起動 |\n" +
			"| `!update` | 最新リリースに更新して再起動 |\n" +
			"| `!status` | プロバイダー・モデル、処理/待機、最後のエラー (= `!debug`) |\n" +
			"| `!model` | モデルをドロップダウンから選択 |\n",
	},
	"chinese": {
		SessionReset:       "会话已重置。",
		AllProvidersFailed: "无法连接到任何AI提供商。点击 🔄 重试。",
		ModelChanged:       "%s 模型已更改为 `%s`。",
		ModelUsage:         "用法: `!model <provider> <model>`\n例: `!model ollama gemma4:e4b`",
		ProviderNotFound:   "找不到提供商 '%s'。",
		SeeAttachment:      "... (完整内容请参阅附件)",
		Help: "**pigeon-claw 命令**\n" +
			"| 命令 | 说明 |\n|---|---|\n" +
			"| `!reset` | 重置会话 |\n" +
			"| `!cancel` | 取消当前请求和等待队列 |\n" +
			"| `!restart` | 重启机器人 |\n" +
			"| `!update` | 更新到最新版本并重启 |\n" +
			"| `!status` | provider·模型、处理/等待、最近错误 (= `!debug`) |\n" +
			"| `!model` | 从下拉菜单选择模型 |\n",
	},
	"": defaultMessages,
}

var defaultMessages = Messages{
	SessionReset:       "Session has been reset.",
	AllProvidersFailed: "Unable to connect to any AI provider. React with 🔄 to retry.",
	ModelChanged:       "%s model changed to `%s`.",
	ModelUsage:         "Usage: `!model <provider> <model>`\nExample: `!model ollama gemma4:e4b`",
	ProviderNotFound:   "Provider '%s' not found.",
	SeeAttachment:      "... (see attached file for full content)",
	Help: "**pigeon-claw Commands**\n" +
		"| Command | Description |\n|---|---|\n" +
		"| `!reset` | Reset channel session |\n" +
		"| `!cancel` | Cancel the running request and the queue |\n" +
		"| `!restart` | Restart bot process |\n" +
		"| `!update` | Update to the latest release and restart |\n" +
		"| `!status` | Providers, models, queue and last error (= `!debug`) |\n" +
		"| `!model` | Pick a model from a dropdown |\n",
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
