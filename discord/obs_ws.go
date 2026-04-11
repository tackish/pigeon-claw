package discord

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/gorilla/websocket"
)

// OBS WebSocket v5 protocol: https://github.com/obsproject/obs-websocket/blob/master/docs/generated/protocol.md

type obsWSConfig struct {
	ServerEnabled  bool   `json:"server_enabled"`
	ServerPort     int    `json:"server_port"`
	ServerPassword string `json:"server_password"`
	AuthRequired   bool   `json:"auth_required"`
}

func loadOBSWSConfig() (*obsWSConfig, error) {
	home, _ := os.UserHomeDir()
	path := filepath.Join(home, "Library", "Application Support", "obs-studio",
		"plugin_config", "obs-websocket", "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read OBS ws config: %w", err)
	}
	var cfg obsWSConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse OBS ws config: %w", err)
	}
	if !cfg.ServerEnabled {
		return nil, fmt.Errorf("OBS WebSocket server is disabled (enable in OBS → Tools → WebSocket Server Settings)")
	}
	return &cfg, nil
}

// obsWSRequest opens a new WebSocket connection, authenticates, sends a
// single request, and returns the response data. Each call is self-contained.
func obsWSRequest(requestType string, requestData map[string]interface{}) (map[string]interface{}, error) {
	cfg, err := loadOBSWSConfig()
	if err != nil {
		return nil, err
	}

	u := url.URL{Scheme: "ws", Host: fmt.Sprintf("localhost:%d", cfg.ServerPort)}
	dialer := websocket.Dialer{HandshakeTimeout: 3 * time.Second}
	conn, _, err := dialer.Dial(u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("dial OBS ws: %w", err)
	}
	defer conn.Close()

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	// 1. Receive Hello (op 0)
	var hello struct {
		Op int `json:"op"`
		D  struct {
			RPCVersion         int `json:"rpcVersion"`
			Authentication     *struct {
				Challenge string `json:"challenge"`
				Salt      string `json:"salt"`
			} `json:"authentication"`
		} `json:"d"`
	}
	if err := conn.ReadJSON(&hello); err != nil {
		return nil, fmt.Errorf("read hello: %w", err)
	}
	if hello.Op != 0 {
		return nil, fmt.Errorf("expected Hello op=0, got op=%d", hello.Op)
	}

	// 2. Send Identify (op 1)
	identify := map[string]interface{}{
		"op": 1,
		"d": map[string]interface{}{
			"rpcVersion": hello.D.RPCVersion,
		},
	}
	if hello.D.Authentication != nil {
		// Compute SHA256(password + salt), then SHA256(b64 + challenge)
		secret := sha256.Sum256([]byte(cfg.ServerPassword + hello.D.Authentication.Salt))
		secretB64 := base64.StdEncoding.EncodeToString(secret[:])
		authHash := sha256.Sum256([]byte(secretB64 + hello.D.Authentication.Challenge))
		authStr := base64.StdEncoding.EncodeToString(authHash[:])
		identify["d"].(map[string]interface{})["authentication"] = authStr
	}
	if err := conn.WriteJSON(identify); err != nil {
		return nil, fmt.Errorf("send identify: %w", err)
	}

	// 3. Receive Identified (op 2)
	var identified struct {
		Op int `json:"op"`
	}
	if err := conn.ReadJSON(&identified); err != nil {
		return nil, fmt.Errorf("read identified: %w", err)
	}
	if identified.Op != 2 {
		return nil, fmt.Errorf("expected Identified op=2, got op=%d", identified.Op)
	}

	// 4. Send Request (op 6)
	reqID := fmt.Sprintf("pigeon-%d", time.Now().UnixNano())
	reqData := map[string]interface{}{
		"requestType": requestType,
		"requestId":   reqID,
	}
	if requestData != nil {
		reqData["requestData"] = requestData
	}
	req := map[string]interface{}{
		"op": 6,
		"d":  reqData,
	}
	if err := conn.WriteJSON(req); err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	// 5. Receive RequestResponse (op 7)
	var resp struct {
		Op int `json:"op"`
		D  struct {
			RequestType   string `json:"requestType"`
			RequestID     string `json:"requestId"`
			RequestStatus struct {
				Result  bool   `json:"result"`
				Code    int    `json:"code"`
				Comment string `json:"comment"`
			} `json:"requestStatus"`
			ResponseData map[string]interface{} `json:"responseData"`
		} `json:"d"`
	}
	if err := conn.ReadJSON(&resp); err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.Op != 7 {
		return nil, fmt.Errorf("expected RequestResponse op=7, got op=%d", resp.Op)
	}
	if !resp.D.RequestStatus.Result {
		return nil, fmt.Errorf("OBS request failed (code=%d): %s",
			resp.D.RequestStatus.Code, resp.D.RequestStatus.Comment)
	}
	return resp.D.ResponseData, nil
}

// obsStartRecording starts OBS recording via WebSocket.
func obsStartRecording() error {
	_, err := obsWSRequest("StartRecord", nil)
	return err
}

// obsStopRecording stops OBS recording via WebSocket and returns the output file path.
func obsStopRecording() (string, error) {
	resp, err := obsWSRequest("StopRecord", nil)
	if err != nil {
		return "", err
	}
	if path, ok := resp["outputPath"].(string); ok {
		return path, nil
	}
	return "", nil
}
