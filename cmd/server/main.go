package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

//go:embed web/*
var webFS embed.FS

type app struct {
	statsPath string
	script    string
	timeout   time.Duration
	remote    remoteConfig
}

type remoteConfig struct {
	BaseURL string
}

func (r remoteConfig) enabled() bool {
	return strings.TrimSpace(r.BaseURL) != ""
}

func (r remoteConfig) endpoint(path string) string {
	base := strings.TrimRight(strings.TrimSpace(r.BaseURL), "/")
	return base + path
}

type apiResponse struct {
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
	Stdout string `json:"stdout,omitempty"`
	Stderr string `json:"stderr,omitempty"`
}

type firewallRule struct {
	Name    string `json:"name,omitempty"`
	Proto   string `json:"proto"`
	SrcIP   string `json:"src_ip,omitempty"`
	DestIP  string `json:"dest_ip,omitempty"`
	Port    string `json:"port,omitempty"`
	Action  string `json:"action"`
	Comment string `json:"comment,omitempty"`
}

var (
	validProto  = map[string]bool{"tcp": true, "udp": true, "icmp": true, "all": true}
	validAction = map[string]bool{"accept": true, "reject": true, "drop": true}
	nameRE      = regexp.MustCompile(`^cnslab_[A-Za-z0-9_.-]+$`)
)

func main() {
	var listen, statsPath, script string
	var remote remoteConfig
	var timeout time.Duration
	flag.StringVar(&listen, "listen", ":8080", "HTTP listen address")
	flag.StringVar(&statsPath, "stats", "/tmp/traffic_stats.json", "traffic monitor JSON file")
	flag.StringVar(&script, "firewall-script", "./scripts/firewall.sh", "firewall script path")
	flag.StringVar(&remote.BaseURL, "openwrt-agent", "", "OpenWrt HTTP agent base URL, for example http://192.168.56.2:9090")
	flag.DurationVar(&timeout, "timeout", 8*time.Second, "script execution timeout")
	flag.Parse()
	if remote.enabled() {
		if _, err := url.ParseRequestURI(remote.BaseURL); err != nil {
			log.Fatalf("invalid -openwrt-agent URL: %v", err)
		}
	}

	a := &app{
		statsPath: statsPath,
		script:    script,
		timeout:   timeout,
		remote:    remote,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", a.health)
	mux.HandleFunc("GET /api/traffic", a.traffic)
	mux.HandleFunc("GET /api/firewall/list", a.firewallList)
	mux.HandleFunc("POST /api/firewall/add", a.firewallAdd)
	mux.HandleFunc("POST /api/firewall/delete", a.firewallDelete)
	mux.HandleFunc("POST /api/firewall/clear", a.firewallClear)
	mux.HandleFunc("POST /api/firewall/verify", a.firewallVerify)
	mux.Handle("/", staticHandler())

	server := &http.Server{
		Addr:              listen,
		Handler:           logRequests(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	if remote.enabled() {
		log.Printf("server listening on %s, openwrt-agent=%s", listen, remote.BaseURL)
	} else {
		log.Printf("server listening on %s, stats=%s, firewall-script=%s", listen, statsPath, script)
	}
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func staticHandler() http.Handler {
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		panic(err)
	}
	return http.FileServer(http.FS(sub))
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}

func (a *app) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"stats":     a.statsPath,
		"script":    a.script,
		"remote":    a.remote.enabled(),
		"agent":     a.remote.BaseURL,
		"timestamp": time.Now().Format(time.RFC3339),
	})
}

func (a *app) traffic(w http.ResponseWriter, r *http.Request) {
	if a.remote.enabled() {
		body, err := a.getAgent(r.Context(), "/agent/traffic")
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":      true,
				"flows":   []any{},
				"message": "traffic monitor has not written data yet or OpenWrt agent is unreachable",
				"error":   err.Error(),
			})
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write(body)
		return
	}

	f, err := os.Open(a.statsPath)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "flows": []any{}, "message": "traffic monitor has not written data yet"})
			return
		}
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if _, err := io.Copy(w, f); err != nil {
		log.Printf("copy stats response: %v", err)
	}
}

func (a *app) firewallList(w http.ResponseWriter, r *http.Request) {
	out, errOut, err := a.runScript(r.Context(), "list", nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error(), Stdout: out, Stderr: errOut})
		return
	}

	var rules []firewallRule
	if strings.TrimSpace(out) != "" {
		if err := json.Unmarshal([]byte(out), &rules); err != nil {
			writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "script returned invalid JSON: " + err.Error(), Stdout: out, Stderr: errOut})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "rules": rules, "stderr": errOut})
}

func (a *app) firewallAdd(w http.ResponseWriter, r *http.Request) {
	var rule firewallRule
	if err := readJSON(r, &rule); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateRule(&rule); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}

	args := []string{"--proto", rule.Proto, "--action", rule.Action}
	if rule.SrcIP != "" {
		args = append(args, "--src", rule.SrcIP)
	}
	if rule.DestIP != "" {
		args = append(args, "--dest", rule.DestIP)
	}
	if rule.Port != "" {
		args = append(args, "--port", rule.Port)
	}

	out, errOut, err := a.runScript(r.Context(), "add", args)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error(), Stdout: out, Stderr: errOut})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{OK: true, Stdout: out, Stderr: errOut})
}

func (a *app) firewallDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !nameRE.MatchString(req.Name) {
		httpError(w, http.StatusBadRequest, "invalid rule name")
		return
	}
	out, errOut, err := a.runScript(r.Context(), "delete", []string{"--name", req.Name})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error(), Stdout: out, Stderr: errOut})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{OK: true, Stdout: out, Stderr: errOut})
}

func (a *app) firewallClear(w http.ResponseWriter, r *http.Request) {
	out, errOut, err := a.runScript(r.Context(), "clear", nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error(), Stdout: out, Stderr: errOut})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{OK: true, Stdout: out, Stderr: errOut})
}

func (a *app) firewallVerify(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Host string `json:"host"`
		Port string `json:"port"`
	}
	if err := readJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	if net.ParseIP(req.Host) == nil && !validHostname(req.Host) {
		httpError(w, http.StatusBadRequest, "invalid host")
		return
	}
	if err := validatePort(req.Port, true); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	out, errOut, err := a.runScript(r.Context(), "verify", []string{"--host", req.Host, "--port", req.Port})
	if err != nil {
		writeJSON(w, http.StatusOK, apiResponse{OK: false, Error: err.Error(), Stdout: out, Stderr: errOut})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{OK: true, Stdout: out, Stderr: errOut})
}

func (a *app) runScript(parent context.Context, action string, args []string) (string, string, error) {
	if a.remote.enabled() {
		return a.runAgentScript(parent, action, args)
	}

	ctx, cancel := context.WithTimeout(parent, a.timeout)
	defer cancel()

	script := a.script
	if !filepath.IsAbs(script) {
		if abs, err := filepath.Abs(script); err == nil {
			script = abs
		}
	}

	cmdArgs := append([]string{script, action}, args...)
	cmd := exec.CommandContext(ctx, "/bin/sh", cmdArgs...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return stdout.String(), stderr.String(), fmt.Errorf("script timed out after %s", a.timeout)
	}
	if err != nil {
		return stdout.String(), stderr.String(), err
	}
	return stdout.String(), stderr.String(), nil
}

func (a *app) getAgent(parent context.Context, path string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, a.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.remote.endpoint(path), nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("agent returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func (a *app) runAgentScript(parent context.Context, action string, args []string) (string, string, error) {
	ctx, cancel := context.WithTimeout(parent, a.timeout)
	defer cancel()

	payload, err := json.Marshal(map[string]any{
		"action": action,
		"args":   args,
	})
	if err != nil {
		return "", "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.remote.endpoint("/agent/firewall"), strings.NewReader(string(payload)))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", "", err
	}

	var result apiResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "", "", fmt.Errorf("agent returned invalid JSON: %w", err)
	}
	if resp.StatusCode >= 400 || !result.OK {
		if result.Error == "" {
			result.Error = fmt.Sprintf("agent returned HTTP %d", resp.StatusCode)
		}
		return result.Stdout, result.Stderr, errors.New(result.Error)
	}
	return result.Stdout, result.Stderr, nil
}

func validateRule(rule *firewallRule) error {
	rule.Proto = strings.ToLower(strings.TrimSpace(rule.Proto))
	rule.Action = strings.ToLower(strings.TrimSpace(rule.Action))
	rule.SrcIP = strings.TrimSpace(rule.SrcIP)
	rule.DestIP = strings.TrimSpace(rule.DestIP)
	rule.Port = strings.TrimSpace(rule.Port)
	if !validProto[rule.Proto] {
		return fmt.Errorf("invalid proto")
	}
	if !validAction[rule.Action] {
		return fmt.Errorf("invalid action")
	}
	if rule.SrcIP != "" && !validCIDROrIP(rule.SrcIP) {
		return fmt.Errorf("invalid source address")
	}
	if rule.DestIP != "" && !validCIDROrIP(rule.DestIP) {
		return fmt.Errorf("invalid destination address")
	}
	if err := validatePort(rule.Port, false); err != nil {
		return err
	}
	if rule.Proto == "icmp" || rule.Proto == "all" {
		if rule.Port != "" {
			return fmt.Errorf("port is only valid for tcp or udp")
		}
	}
	return nil
}

func validCIDROrIP(s string) bool {
	s = strings.TrimSpace(s)
	if net.ParseIP(s) != nil {
		return true
	}
	ip, _, err := net.ParseCIDR(s)
	return err == nil && ip != nil
}

func validatePort(s string, required bool) error {
	s = strings.TrimSpace(s)
	if s == "" {
		if required {
			return fmt.Errorf("port is required")
		}
		return nil
	}
	if strings.Contains(s, "-") {
		parts := strings.Split(s, "-")
		if len(parts) != 2 {
			return fmt.Errorf("invalid port range")
		}
		a, errA := strconv.Atoi(parts[0])
		b, errB := strconv.Atoi(parts[1])
		if errA != nil || errB != nil || a < 1 || b > 65535 || a > b {
			return fmt.Errorf("invalid port range")
		}
		return nil
	}
	p, err := strconv.Atoi(s)
	if err != nil || p < 1 || p > 65535 {
		return fmt.Errorf("invalid port")
	}
	return nil
}

func validHostname(s string) bool {
	if len(s) == 0 || len(s) > 253 {
		return false
	}
	for _, part := range strings.Split(s, ".") {
		if len(part) == 0 || len(part) > 63 {
			return false
		}
		for i, r := range part {
			ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-'
			if !ok || (r == '-' && (i == 0 || i == len(part)-1)) {
				return false
			}
		}
	}
	return true
}

func readJSON(r *http.Request, dst any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("encode response: %v", err)
	}
}

func httpError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, apiResponse{OK: false, Error: msg})
}
