package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type app struct {
	statsPath string
	script    string
	timeout   time.Duration
}

type apiResponse struct {
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
	Stdout string `json:"stdout,omitempty"`
	Stderr string `json:"stderr,omitempty"`
}

type firewallRequest struct {
	Action string   `json:"action"`
	Args   []string `json:"args"`
}

var allowedActions = map[string]bool{
	"list":   true,
	"add":    true,
	"delete": true,
	"clear":  true,
	"verify": true,
}

func main() {
	var listen, statsPath, script string
	var timeout time.Duration
	flag.StringVar(&listen, "listen", "0.0.0.0:9090", "agent listen address")
	flag.StringVar(&statsPath, "stats", "/tmp/traffic_stats.json", "traffic monitor JSON file")
	flag.StringVar(&script, "firewall-script", "/root/netlab/scripts/firewall.sh", "firewall script path")
	flag.DurationVar(&timeout, "timeout", 8*time.Second, "script execution timeout")
	flag.Parse()

	a := &app{statsPath: statsPath, script: script, timeout: timeout}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /agent/health", a.health)
	mux.HandleFunc("GET /agent/traffic", a.traffic)
	mux.HandleFunc("POST /agent/firewall", a.firewall)

	server := &http.Server{
		Addr:              listen,
		Handler:           logRequests(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("openwrt agent listening on %s, stats=%s, firewall-script=%s", listen, statsPath, script)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func (a *app) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"stats":     a.statsPath,
		"script":    a.script,
		"timestamp": time.Now().Format(time.RFC3339),
	})
}

func (a *app) traffic(w http.ResponseWriter, r *http.Request) {
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

func (a *app) firewall(w http.ResponseWriter, r *http.Request) {
	var req firewallRequest
	if err := readJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Action = strings.TrimSpace(req.Action)
	if !allowedActions[req.Action] {
		httpError(w, http.StatusBadRequest, "invalid firewall action")
		return
	}
	if len(req.Args) > 24 {
		httpError(w, http.StatusBadRequest, "too many firewall arguments")
		return
	}
	for _, arg := range req.Args {
		if strings.Contains(arg, "\x00") || strings.Contains(arg, "\n") || strings.Contains(arg, "\r") {
			httpError(w, http.StatusBadRequest, "invalid argument")
			return
		}
	}

	out, errOut, err := a.runScript(r.Context(), req.Action, req.Args)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error(), Stdout: out, Stderr: errOut})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{OK: true, Stdout: out, Stderr: errOut})
}

func (a *app) runScript(parent context.Context, action string, args []string) (string, string, error) {
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

func readJSON(r *http.Request, dst any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
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

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}
