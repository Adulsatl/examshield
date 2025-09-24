package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)


type enrollRequest struct {
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	Version  string `json:"version"`
}

type enrollResponse struct {
	AgentID string `json:"agent_id"`
	Token   string `json:"token"`
}

type heartbeatRequest struct {
	AgentID string `json:"agent_id"`
}

type configResponse struct {
	Policy map[string]any `json:"policy"`
}

type LocalConfig struct {
    TelegramBotToken string   `json:"telegram_bot_token"`
    TelegramChatID   string   `json:"telegram_chat_id"`
    AppWhitelist     []string `json:"app_whitelist"` // process executable names to allow, case-insensitive
}

func main() {
	server := getenv("EXAMSHIELD_SERVER", "http://127.0.0.1:8080")
	stateDir := ensureStateDir()
	idPath := filepath.Join(stateDir, "agent_id")
	tokPath := filepath.Join(stateDir, "agent_token")
    cfgPath := filepath.Join(stateDir, "config.json")

	agentID, token := read(idPath), read(tokPath)
	if agentID == "" || token == "" {
		id, tok, err := enroll(server)
		if err != nil {
			panic(err)
		}
		agentID, token = id, tok
		_ = os.WriteFile(idPath, []byte(agentID), 0600)
		_ = os.WriteFile(tokPath, []byte(token), 0600)
		fmt.Println("enrolled agent:", agentID)
	}

	// Fetch config once (PoC)
	cfg, _ := fetchConfig(server)
	if cfg != nil { fmt.Println("policy received:", cfg.Policy) }

    // Load local config created by setup script
    localCfg := loadLocalConfig(cfgPath)
    if len(localCfg.AppWhitelist) > 0 {
        fmt.Println("local whitelist:", localCfg.AppWhitelist)
    }

	// Start process watcher on Windows
	if runtime.GOOS == "windows" {
		go processWatcher(server, agentID, token, cfg, &localCfg, stateDir)
	}

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		if err := heartbeat(server, agentID, token); err != nil {
			fmt.Println("heartbeat error:", err)
		} else {
			fmt.Println("heartbeat ok at", time.Now().Format(time.RFC3339))
		}
		<-ticker.C
	}
}

func enroll(server string) (string, string, error) {
	host, _ := os.Hostname()
	payload := enrollRequest{Hostname: host, OS: runtime.GOOS, Version: runtime.Version()}
	b, _ := json.Marshal(payload)
	resp, err := http.Post(server+"/agents/enroll", "application/json", bytes.NewReader(b))
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		bb, _ := io.ReadAll(resp.Body)
		return "", "", fmt.Errorf("enroll failed: %s", string(bb))
	}
	var er enrollResponse
	if err := json.NewDecoder(resp.Body).Decode(&er); err != nil {
		return "", "", err
	}
	return er.AgentID, er.Token, nil
}

func heartbeat(server, agentID, token string) error {
	b, _ := json.Marshal(heartbeatRequest{AgentID: agentID})
	req, _ := http.NewRequest(http.MethodPost, server+"/agents/heartbeat", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Token", token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		bb, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("heartbeat failed: %s", string(bb))
	}
	return nil
}

func fetchConfig(server string) (*configResponse, error) {
	resp, err := http.Get(server + "/config")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var cr configResponse
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return nil, err
	}
	return &cr, nil
}

func processWatcher(server, agentID, token string, cfg *configResponse, local *LocalConfig, stateDir string) {
    blacklist := map[string]struct{}{}
    screenshotOnBlock := true
    if cfg != nil && cfg.Policy != nil {
        if v, ok := cfg.Policy["app_blacklist"].([]any); ok {
            for _, it := range v {
                name := strings.ToLower(fmt.Sprint(it))
                blacklist[name] = struct{}{}
            }
        } else if vs, ok := cfg.Policy["app_blacklist"].([]string); ok { // alternative for typed decode
            for _, name := range vs { blacklist[strings.ToLower(name)] = struct{}{} }
        }
        if v, ok := cfg.Policy["screenshot_on_block"].(bool); ok { screenshotOnBlock = v }
    }
    if len(blacklist) == 0 {
        // default
        for _, n := range []string{"chrome.exe","msedge.exe","firefox.exe","brave.exe","opera.exe"} {
            blacklist[n] = struct{}{}
        }
    }
    // Build whitelist set (lowercased)
    whitelist := map[string]struct{}{}
    if local != nil {
        for _, n := range local.AppWhitelist {
            whitelist[strings.ToLower(strings.TrimSpace(n))] = struct{}{}
        }
    }

    for {
        procs, err := listProcessesWindows()
        if err == nil {
            for _, p := range procs {
                name := strings.ToLower(p.Name)
                if _, allowed := whitelist[name]; allowed {
                    continue
                }
                if _, blocked := blacklist[name]; blocked {
                    // Optional screenshot
                    var shotPath string
                    if screenshotOnBlock {
                        if sp, err := takeScreenshot(stateDir); err == nil {
                            shotPath = sp
                        }
                    }
                    // Kill process
                    _ = killProcessWindows(p.PID)
                    // Post event
                    event := map[string]any{
                        "type": "app_block",
                        "agent_id": agentID,
                        "payload": map[string]any{
                            "process_name": p.Name,
                            "pid": p.PID,
                            "screenshot_path": shotPath,
                            "at": time.Now().Format(time.RFC3339),
                        },
                    }
                    _ = postEvent(server, token, event)
                    // Telegram alert if configured
                    if local != nil && local.TelegramBotToken != "" && local.TelegramChatID != "" {
                        _ = sendTelegramAlert(local.TelegramBotToken, local.TelegramChatID,
                            fmt.Sprintf("ExamShieldEDU: Blocked %s (PID %d)", p.Name, p.PID))
                    }
                    fmt.Println("blocked and killed:", p.Name, p.PID)
                }
            }
        }
        time.Sleep(2 * time.Second)
    }
}

type Proc struct{ Name string; PID int }

func listProcessesWindows() ([]Proc, error) {
    // Use tasklist to avoid extra dependencies for PoC
    cmd := exec.Command("tasklist", "/fo", "csv", "/nh")
    out, err := cmd.Output()
    if err != nil { return nil, err }
    r := csv.NewReader(bytes.NewReader(out))
    records, err := r.ReadAll()
    if err != nil { return nil, err }
    procs := make([]Proc, 0, len(records))
    for _, rec := range records {
        // CSV: Image Name, PID, Session Name, Session#, Mem Usage
        if len(rec) < 2 { continue }
        name := strings.Trim(strings.TrimSpace(rec[0]), "\"")
        pidStr := strings.Trim(strings.TrimSpace(rec[1]), "\"")
        pid, _ := strconv.Atoi(pidStr)
        procs = append(procs, Proc{Name: name, PID: pid})
    }
    return procs, nil
}

func killProcessWindows(pid int) error {
    // taskkill /PID <pid> /F
    cmd := exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/F")
    return cmd.Run()
}

func takeScreenshot(stateDir string) (string, error) {
    // Use PowerShell to capture the primary screen to a PNG.
    // This avoids external Go screenshot dependencies and works on Windows with .NET available.
    if runtime.GOOS != "windows" {
        return "", fmt.Errorf("screenshot not supported on %s", runtime.GOOS)
    }
    name := fmt.Sprintf("shot_%d.png", time.Now().Unix())
    path := filepath.Join(stateDir, name)
    ps := `[void][Reflection.Assembly]::LoadWithPartialName('System.Windows.Forms');`+
        `[void][Reflection.Assembly]::LoadWithPartialName('System.Drawing');`+
        `$bounds = [System.Windows.Forms.Screen]::PrimaryScreen.Bounds;`+
        `$bmp = New-Object System.Drawing.Bitmap($bounds.Width, $bounds.Height);`+
        `$g = [System.Drawing.Graphics]::FromImage($bmp);`+
        `$g.CopyFromScreen($bounds.Location, [System.Drawing.Point]::Empty, $bounds.Size);`+
        `$bmp.Save('` + strings.ReplaceAll(path, "\\", "\\\\") + `');`+
        `$g.Dispose(); $bmp.Dispose();`
    cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", ps)
    if err := cmd.Run(); err != nil { return "", err }
    if _, err := os.Stat(path); err != nil { return "", err }
    return path, nil
}

func postEvent(server, token string, event map[string]any) error {
    b, _ := json.Marshal(event)
    req, _ := http.NewRequest(http.MethodPost, server+"/events", bytes.NewReader(b))
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("X-Agent-Token", token)
    resp, err := http.DefaultClient.Do(req)
    if err != nil { return err }
    defer resp.Body.Close()
    if resp.StatusCode != http.StatusOK {
        bb, _ := io.ReadAll(resp.Body)
        return fmt.Errorf("postEvent failed: %s", string(bb))
    }
    return nil
}

func loadLocalConfig(path string) LocalConfig {
    var c LocalConfig
    b, err := os.ReadFile(path)
    if err != nil { return c }
    _ = json.Unmarshal(b, &c)
    return c
}

func sendTelegramAlert(botToken, chatID, text string) error {
    if botToken == "" || chatID == "" || text == "" { return nil }
    url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", botToken)
    payload := map[string]any{"chat_id": chatID, "text": text}
    b, _ := json.Marshal(payload)
    req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
    req.Header.Set("Content-Type", "application/json")
    resp, err := http.DefaultClient.Do(req)
    if err != nil { return err }
    defer resp.Body.Close()
    if resp.StatusCode != http.StatusOK {
        bb, _ := io.ReadAll(resp.Body)
        return fmt.Errorf("telegram send failed: %s", string(bb))
    }
    return nil
}

func ensureStateDir() string {
	// Prefer OS-specific app data locations
	if runtime.GOOS == "windows" {
		base := os.Getenv("ProgramData")
		if base == "" { // fallback to user profile
			u, _ := user.Current()
			base = filepath.Join(u.HomeDir, "AppData", "Local")
		}
		dir := filepath.Join(base, "ExamShieldEDU")
		_ = os.MkdirAll(dir, 0700)
		return dir
	}
	// Linux/macOS fallback
	dir := filepath.Join("/var", "lib", "examshieldedu")
	_ = os.MkdirAll(dir, 0700)
	return dir
}

func read(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(bytes.TrimSpace(b))
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
