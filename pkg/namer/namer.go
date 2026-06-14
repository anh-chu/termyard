// Package namer generates short, human-friendly session names by asking a
// custom OpenAI-compatible chat endpoint to synthesize context (working dir,
// branch, agent, user prompt, agent message, or recent shell commands) into a
// concise label.
//
// It is fully optional: if no endpoint is configured the namer is disabled and
// every call is a no-op. Network failures never block the caller — they return
// an error that the caller is expected to log and ignore, keeping the existing
// session name.
package namer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// Kind distinguishes the two context profiles the namer understands.
type Kind string

const (
	KindAgent Kind = "agent"
	KindShell Kind = "shell"
)

// Context carries everything the namer may use to synthesize a name. Callers
// build it; the namer does not care which Kind it is beyond prompt shaping.
type Context struct {
	Kind       Kind
	Workdir    string
	Branch     string
	Agent      string   // agent type, e.g. "claude" (agent kind only)
	UserPrompt string   // first user message (agent kind only)
	AgentMsg   string   // latest agent message (agent kind only)
	Commands   []string // recent shell commands (shell kind only)
}

// Config holds the endpoint settings. Endpoint + Model must be non-empty for
// the namer to be enabled. APIKey is optional (some local endpoints need none).
type Config struct {
	Endpoint string // base URL, e.g. https://api.openai.com/v1
	APIKey   string
	Model    string
	Timeout  time.Duration
}

// ConfigFromEnv reads namer configuration from GUPPI_NAMER_* environment
// variables. Falls back to GUPPI_OPENAI_* for endpoint/key/model so a single
// OpenAI-compatible config can be shared.
func ConfigFromEnv() Config {
	get := func(keys ...string) string {
		for _, k := range keys {
			if v := strings.TrimSpace(os.Getenv(k)); v != "" {
				return v
			}
		}
		return ""
	}
	c := Config{
		Endpoint: get("GUPPI_NAMER_ENDPOINT", "GUPPI_OPENAI_ENDPOINT", "GUPPI_OPENAI_BASE_URL"),
		APIKey:   get("GUPPI_NAMER_API_KEY", "GUPPI_OPENAI_API_KEY"),
		Model:    get("GUPPI_NAMER_MODEL", "GUPPI_OPENAI_MODEL"),
		Timeout:  8 * time.Second,
	}
	if c.Model == "" && c.Endpoint != "" {
		c.Model = "gpt-4o-mini"
	}
	return c
}

// Configure builds a Config from explicit settings (e.g. user preferences),
// falling back to GUPPI_NAMER_* / GUPPI_OPENAI_* environment variables for any
// empty field. If enabled is false it returns a disabled (zero-endpoint)
// Config.
func Configure(enabled bool, endpoint, apiKey, model string) Config {
	if !enabled {
		return Config{}
	}
	env := ConfigFromEnv()
	c := Config{
		Endpoint: firstNonEmpty(endpoint, env.Endpoint),
		APIKey:   firstNonEmpty(apiKey, env.APIKey),
		Model:    firstNonEmpty(model, env.Model),
		Timeout:  8 * time.Second,
	}
	if c.Model == "" && c.Endpoint != "" {
		c.Model = "gpt-4o-mini"
	}
	return c
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// Namer talks to the configured endpoint. The zero value (Config{}) yields a
// disabled namer whose Generate always returns ("", ErrDisabled).
type Namer struct {
	cfg    Config
	client *http.Client
}

// ErrDisabled is returned when no endpoint is configured.
var ErrDisabled = fmt.Errorf("namer: no endpoint configured")

// New builds a Namer from cfg.
func New(cfg Config) *Namer {
	if cfg.Timeout == 0 {
		cfg.Timeout = 8 * time.Second
	}
	return &Namer{
		cfg:    cfg,
		client: &http.Client{Timeout: cfg.Timeout},
	}
}

// Enabled reports whether the namer has a usable endpoint + model.
func (n *Namer) Enabled() bool {
	return n != nil && n.cfg.Endpoint != "" && n.cfg.Model != ""
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

const systemPrompt = `You name terminal sessions. Reply with ONLY a short label, 2-4 words, kebab-case, lowercase, ASCII letters/digits/hyphens only. No quotes, no punctuation, no explanation. Examples: fix-auth-token, db-migration, docker-logs-debug, rebase-feature-branch.`

// Generate synthesizes a sanitized session name from ctx. Returns ErrDisabled
// if no endpoint is configured. On any network/parse error returns ("", err);
// callers should log and keep the existing name.
func (n *Namer) Generate(ctx context.Context, nc Context) (string, error) {
	if !n.Enabled() {
		return "", ErrDisabled
	}

	user := buildUserPrompt(nc)
	if strings.TrimSpace(user) == "" {
		return "", fmt.Errorf("namer: empty context")
	}

	reqBody := chatRequest{
		Model: n.cfg.Model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: user},
		},
		Temperature: 0.3,
		MaxTokens:   24,
	}
	buf, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	url := strings.TrimRight(n.cfg.Endpoint, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if n.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+n.cfg.APIKey)
	}

	resp, err := n.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var out chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("namer: decode response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		if out.Error != nil {
			return "", fmt.Errorf("namer: endpoint error (%d): %s", resp.StatusCode, out.Error.Message)
		}
		return "", fmt.Errorf("namer: endpoint status %d", resp.StatusCode)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("namer: empty response")
	}

	name := Sanitize(out.Choices[0].Message.Content)
	if name == "" {
		return "", fmt.Errorf("namer: model returned unusable name")
	}
	return name, nil
}

func buildUserPrompt(nc Context) string {
	var b strings.Builder
	if nc.Workdir != "" {
		fmt.Fprintf(&b, "Working directory: %s\n", nc.Workdir)
	}
	if nc.Branch != "" {
		fmt.Fprintf(&b, "Git branch: %s\n", nc.Branch)
	}
	switch nc.Kind {
	case KindAgent:
		if nc.Agent != "" {
			fmt.Fprintf(&b, "Coding agent: %s\n", nc.Agent)
		}
		if nc.UserPrompt != "" {
			fmt.Fprintf(&b, "User asked: %s\n", truncate(nc.UserPrompt, 600))
		}
		if nc.AgentMsg != "" {
			fmt.Fprintf(&b, "Agent replied: %s\n", truncate(nc.AgentMsg, 400))
		}
	case KindShell:
		if len(nc.Commands) > 0 {
			b.WriteString("Recent shell commands:\n")
			for _, c := range nc.Commands {
				c = strings.TrimSpace(c)
				if c != "" {
					fmt.Fprintf(&b, "  %s\n", truncate(c, 200))
				}
			}
		}
	}
	b.WriteString("\nName this session.")
	return b.String()
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
