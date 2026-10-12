// ocelotctl — management CLI for Ocelot tracker (S-01).
//
// Context model mirrors kubectl: a YAML config at ~/.ocelot/config.json
// stores named contexts, each carrying a host+token pair. Requests go to
// the ControlServer (:34001) via Bearer token authentication.
//
// Usage:
//
//	ocelotctl config set-context prod --host=https://tracker.example.com --token=<sitepassword>
//	ocelotctl config use-context prod
//	ocelotctl stats
//	ocelotctl torrent list [--limit=N]
//	ocelotctl torrent add  --id=<N> --info-hash=<hex> [--free-type=<N>]
//	ocelotctl torrent delete --info-hash=<hex>
//	ocelotctl user add --id=<N> --passkey=<32chars>
//	ocelotctl user delete --passkey=<32chars>
//	ocelotctl peer list --info-hash=<hex> [--limit=N]
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ── Config model ──────────────────────────────────────────────────────────────

type ctlConfig struct {
	Contexts       map[string]ctlContext `json:"contexts"`
	CurrentContext string                `json:"current_context"`
}

type ctlContext struct {
	Host  string `json:"host"`
	Token string `json:"token"`
}

func configPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".ocelot", "config.json")
}

func loadConfig() (*ctlConfig, error) {
	path := configPath()
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return &ctlConfig{Contexts: make(map[string]ctlContext)}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var cfg ctlConfig
	if err := json.NewDecoder(f).Decode(&cfg); err != nil {
		return nil, err
	}
	if cfg.Contexts == nil {
		cfg.Contexts = make(map[string]ctlContext)
	}
	return &cfg, nil
}

func saveConfig(cfg *ctlConfig) error {
	path := configPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0600)
}

func currentContext(cfg *ctlConfig) (ctlContext, error) {
	if cfg.CurrentContext == "" {
		return ctlContext{}, fmt.Errorf("no current context set — run: ocelotctl config use-context <name>")
	}
	ctx, ok := cfg.Contexts[cfg.CurrentContext]
	if !ok {
		return ctlContext{}, fmt.Errorf("context %q not found in config", cfg.CurrentContext)
	}
	return ctx, nil
}

// ── HTTP client ───────────────────────────────────────────────────────────────

func apiDo(ctx ctlContext, method, path string, body interface{}) ([]byte, int, error) {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		reqBody = bytes.NewReader(b)
	}

	url := strings.TrimRight(ctx.Host, "/") + path
	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+ctx.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	return data, resp.StatusCode, err
}

func printJSON(data []byte) {
	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		fmt.Println(string(data))
		return
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
}

// ── Commands ──────────────────────────────────────────────────────────────────

func cmdConfig(args []string, cfg *ctlConfig) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: ocelotctl config <set-context|use-context|get-contexts|current-context>")
	}
	switch args[0] {
	case "set-context":
		if len(args) < 2 {
			return fmt.Errorf("usage: ocelotctl config set-context <name> [--host=<url>] [--token=<tok>]")
		}
		name := args[1]
		ctx := cfg.Contexts[name]
		for _, a := range args[2:] {
			if strings.HasPrefix(a, "--host=") {
				ctx.Host = strings.TrimPrefix(a, "--host=")
			} else if strings.HasPrefix(a, "--token=") {
				ctx.Token = strings.TrimPrefix(a, "--token=")
			}
		}
		cfg.Contexts[name] = ctx
		return saveConfig(cfg)

	case "use-context":
		if len(args) < 2 {
			return fmt.Errorf("usage: ocelotctl config use-context <name>")
		}
		cfg.CurrentContext = args[1]
		return saveConfig(cfg)

	case "get-contexts":
		for name, ctx := range cfg.Contexts {
			mark := " "
			if name == cfg.CurrentContext {
				mark = "*"
			}
			fmt.Printf("%s %s  %s\n", mark, name, ctx.Host)
		}

	case "current-context":
		fmt.Println(cfg.CurrentContext)

	default:
		return fmt.Errorf("unknown config subcommand: %s", args[0])
	}
	return nil
}

func cmdStats(cfg *ctlConfig) error {
	ctx, err := currentContext(cfg)
	if err != nil {
		return err
	}
	data, code, err := apiDo(ctx, "GET", "/api/v1/stats", nil)
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", code, data)
	}
	printJSON(data)
	return nil
}

func cmdTorrent(args []string, cfg *ctlConfig) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: ocelotctl torrent <list|add|delete|patch>")
	}
	ctx, err := currentContext(cfg)
	if err != nil {
		return err
	}
	switch args[0] {
	case "list":
		limit := 100
		for _, a := range args[1:] {
			if strings.HasPrefix(a, "--limit=") {
				limit, _ = strconv.Atoi(strings.TrimPrefix(a, "--limit="))
			}
		}
		data, code, err := apiDo(ctx, "GET", fmt.Sprintf("/api/v1/torrents?limit=%d", limit), nil)
		if err != nil {
			return err
		}
		if code != http.StatusOK {
			return fmt.Errorf("HTTP %d: %s", code, data)
		}
		printJSON(data)

	case "add":
		var id uint64
		var infoHash string
		var freeType uint8
		for _, a := range args[1:] {
			switch {
			case strings.HasPrefix(a, "--id="):
				id, _ = strconv.ParseUint(strings.TrimPrefix(a, "--id="), 10, 32)
			case strings.HasPrefix(a, "--info-hash="):
				infoHash = strings.TrimPrefix(a, "--info-hash=")
			case strings.HasPrefix(a, "--free-type="):
				v, _ := strconv.ParseUint(strings.TrimPrefix(a, "--free-type="), 10, 8)
				freeType = uint8(v)
			}
		}
		if id == 0 || infoHash == "" {
			return fmt.Errorf("--id and --info-hash are required")
		}
		body := map[string]interface{}{"id": id, "info_hash": infoHash, "free_type": freeType}
		data, code, err := apiDo(ctx, "POST", "/api/v1/torrents", body)
		if err != nil {
			return err
		}
		if code != http.StatusCreated && code != http.StatusOK {
			return fmt.Errorf("HTTP %d: %s", code, data)
		}
		printJSON(data)

	case "delete":
		var infoHash string
		for _, a := range args[1:] {
			if strings.HasPrefix(a, "--info-hash=") {
				infoHash = strings.TrimPrefix(a, "--info-hash=")
			}
		}
		if infoHash == "" {
			return fmt.Errorf("--info-hash is required")
		}
		data, code, err := apiDo(ctx, "DELETE", "/api/v1/torrents?info_hash="+infoHash, nil)
		if err != nil {
			return err
		}
		if code != http.StatusOK {
			return fmt.Errorf("HTTP %d: %s", code, data)
		}
		printJSON(data)

	case "patch":
		var infoHash string
		var freeType *uint8
		for _, a := range args[1:] {
			switch {
			case strings.HasPrefix(a, "--info-hash="):
				infoHash = strings.TrimPrefix(a, "--info-hash=")
			case strings.HasPrefix(a, "--free-type="):
				v, _ := strconv.ParseUint(strings.TrimPrefix(a, "--free-type="), 10, 8)
				ft := uint8(v)
				freeType = &ft
			}
		}
		if infoHash == "" {
			return fmt.Errorf("--info-hash is required")
		}
		body := map[string]interface{}{"free_type": freeType}
		data, code, err := apiDo(ctx, "PATCH", "/api/v1/torrents?info_hash="+infoHash, body)
		if err != nil {
			return err
		}
		if code != http.StatusOK {
			return fmt.Errorf("HTTP %d: %s", code, data)
		}
		printJSON(data)

	default:
		return fmt.Errorf("unknown torrent subcommand: %s", args[0])
	}
	return nil
}

func cmdUser(args []string, cfg *ctlConfig) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: ocelotctl user <add|delete|patch>")
	}
	ctx, err := currentContext(cfg)
	if err != nil {
		return err
	}
	switch args[0] {
	case "add":
		var id uint64
		var passkey string
		canLeech := true
		protectIP := false
		for _, a := range args[1:] {
			switch {
			case strings.HasPrefix(a, "--id="):
				id, _ = strconv.ParseUint(strings.TrimPrefix(a, "--id="), 10, 32)
			case strings.HasPrefix(a, "--passkey="):
				passkey = strings.TrimPrefix(a, "--passkey=")
			case a == "--no-leech":
				canLeech = false
			case a == "--protect-ip":
				protectIP = true
			}
		}
		if id == 0 || passkey == "" {
			return fmt.Errorf("--id and --passkey are required")
		}
		body := map[string]interface{}{
			"id": id, "passkey": passkey,
			"can_leech": canLeech, "protect_ip": protectIP,
		}
		data, code, err := apiDo(ctx, "POST", "/api/v1/users", body)
		if err != nil {
			return err
		}
		if code != http.StatusCreated && code != http.StatusOK {
			return fmt.Errorf("HTTP %d: %s", code, data)
		}
		printJSON(data)

	case "delete":
		var passkey string
		for _, a := range args[1:] {
			if strings.HasPrefix(a, "--passkey=") {
				passkey = strings.TrimPrefix(a, "--passkey=")
			}
		}
		if passkey == "" {
			return fmt.Errorf("--passkey is required")
		}
		data, code, err := apiDo(ctx, "DELETE", "/api/v1/users?passkey="+passkey, nil)
		if err != nil {
			return err
		}
		if code != http.StatusOK {
			return fmt.Errorf("HTTP %d: %s", code, data)
		}
		printJSON(data)

	case "patch":
		var passkey string
		body := make(map[string]interface{})
		for _, a := range args[1:] {
			switch {
			case strings.HasPrefix(a, "--passkey="):
				passkey = strings.TrimPrefix(a, "--passkey=")
			case a == "--can-leech=true":
				v := true
				body["can_leech"] = &v
			case a == "--can-leech=false":
				v := false
				body["can_leech"] = &v
			case a == "--protect-ip=true":
				v := true
				body["protect_ip"] = &v
			case a == "--protect-ip=false":
				v := false
				body["protect_ip"] = &v
			}
		}
		if passkey == "" {
			return fmt.Errorf("--passkey is required")
		}
		data, code, err := apiDo(ctx, "PATCH", "/api/v1/users?passkey="+passkey, body)
		if err != nil {
			return err
		}
		if code != http.StatusOK {
			return fmt.Errorf("HTTP %d: %s", code, data)
		}
		printJSON(data)

	default:
		return fmt.Errorf("unknown user subcommand: %s", args[0])
	}
	return nil
}

func cmdPeer(args []string, cfg *ctlConfig) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: ocelotctl peer list --info-hash=<hash> [--limit=N]")
	}
	ctx, err := currentContext(cfg)
	if err != nil {
		return err
	}
	switch args[0] {
	case "list":
		var infoHash string
		limit := 100
		for _, a := range args[1:] {
			switch {
			case strings.HasPrefix(a, "--info-hash="):
				infoHash = strings.TrimPrefix(a, "--info-hash=")
			case strings.HasPrefix(a, "--limit="):
				limit, _ = strconv.Atoi(strings.TrimPrefix(a, "--limit="))
			}
		}
		if infoHash == "" {
			return fmt.Errorf("--info-hash is required")
		}
		url := fmt.Sprintf("/api/v1/peers?info_hash=%s&limit=%d", infoHash, limit)
		data, code, err := apiDo(ctx, "GET", url, nil)
		if err != nil {
			return err
		}
		if code != http.StatusOK {
			return fmt.Errorf("HTTP %d: %s", code, data)
		}
		printJSON(data)
	default:
		return fmt.Errorf("unknown peer subcommand: %s", args[0])
	}
	return nil
}

// ── Entry point ───────────────────────────────────────────────────────────────

func usage() {
	fmt.Fprintln(os.Stderr, `ocelotctl — Ocelot tracker management CLI

Usage:
  ocelotctl config set-context <name> --host=<url> --token=<tok>
  ocelotctl config use-context <name>
  ocelotctl config get-contexts
  ocelotctl config current-context
  ocelotctl stats
  ocelotctl torrent list [--limit=N]
  ocelotctl torrent add --id=<N> --info-hash=<hex> [--free-type=N]
  ocelotctl torrent delete --info-hash=<hex>
  ocelotctl torrent patch --info-hash=<hex> [--free-type=N]
  ocelotctl user add --id=<N> --passkey=<32chars> [--no-leech] [--protect-ip]
  ocelotctl user delete --passkey=<32chars>
  ocelotctl user patch --passkey=<32chars> [--can-leech=true|false] [--protect-ip=true|false]
  ocelotctl peer list --info-hash=<hex> [--limit=N]

Config is stored at ~/.ocelot/config.json`)
}

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		usage()
		os.Exit(1)
	}

	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}

	cmd := args[0]
	rest := args[1:]

	switch cmd {
	case "config":
		err = cmdConfig(rest, cfg)
	case "stats":
		err = cmdStats(cfg)
	case "torrent":
		err = cmdTorrent(rest, cfg)
	case "user":
		err = cmdUser(rest, cfg)
	case "peer":
		err = cmdPeer(rest, cfg)
	case "help", "--help", "-h":
		usage()
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		usage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
