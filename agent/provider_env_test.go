package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Autumn-27/norma/llm"
)

// ARTEX_LLM_PROVIDER is matched regardless of case and surrounding spaces. A
// miss used to fall through to anthropic: "Ollama" then sent ANTHROPIC_API_KEY
// to the Ollama base URL.
func TestFromEnvProviderIsCaseInsensitive(t *testing.T) {
	t.Setenv("ARTEX_ZEUS_API_KEY", "")
	t.Setenv("ARTEX_ZEUS_BASE_URL", "")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-secret")
	t.Setenv("OPENAI_API_KEY", "sk-oai")
	t.Setenv("ARTEX_LLM_BASE_URL", "")
	t.Setenv("ARTEX_LLM_MODEL", "")
	for _, tc := range []struct {
		env  string
		want llm.Format
		key  string
	}{
		{"Ollama", FormatOllama, ""},
		{" OLLAMA ", FormatOllama, ""},
		{"OpenAI", llm.FormatOpenAI, "sk-oai"},
		{"OpenAI-Responses", llm.FormatOpenAIResponses, "sk-oai"},
		{"Anthropic", llm.FormatAnthropic, "sk-ant-secret"},
	} {
		t.Setenv("ARTEX_LLM_PROVIDER", tc.env)
		c, ok := FromEnv()
		if !ok {
			t.Fatalf("%q: FromEnv returned ok=false", tc.env)
		}
		if c.Format != tc.want || c.APIKey != tc.key {
			t.Fatalf("%q: format=%v key=%q, want format=%v key=%q", tc.env, c.Format, c.APIKey, tc.want, tc.key)
		}
	}
}

func TestFromEnvZeusPrimaryAndProfileHeader(t *testing.T) {
	t.Setenv("ARTEX_LLM_PROVIDER", "")
	t.Setenv("ARTEX_LLM_BASE_URL", "")
	t.Setenv("ARTEX_LLM_MODEL", "")
	t.Setenv("ARTEX_ZEUS_BASE_URL", "http://127.0.0.1:3188/v1")
	t.Setenv("ARTEX_ZEUS_API_KEY", "zeus_sk_artex_test")
	t.Setenv("ARTEX_ZEUS_PROFILE", "artex-pentest")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-should-not-win")
	t.Setenv("OPENAI_API_KEY", "sk-oai-should-not-win")

	c, ok := FromEnv()
	if !ok {
		t.Fatalf("FromEnv returned ok=false for Zeus config")
	}
	if c.Format != llm.FormatOpenAI || c.BaseURL != "http://127.0.0.1:3188/v1" || c.Model != "zeus/profile/artex-pentest" || c.ZeusProfile != "artex-pentest" || c.APIKey != "zeus_sk_artex_test" {
		t.Fatalf("unexpected Zeus FromEnv config: %+v", c)
	}

	var gotProfile, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotProfile = r.Header.Get("X-Zeus-Profile")
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()

	cfg := ConfigFrom("zeus", "", srv.URL, "zeus_sk_artex_test", "")
	prov, err := cfg.NewProvider()
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	_, _, _, err = prov.Complete(context.Background(), llm.CompletionRequest{
		Messages: []llm.Message{llm.UserText("ping")},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if gotProfile != "artex-pentest" {
		t.Fatalf("X-Zeus-Profile = %q, want artex-pentest", gotProfile)
	}
	if gotAuth != "Bearer zeus_sk_artex_test" {
		t.Fatalf("Authorization = %q, want Bearer zeus_sk_artex_test", gotAuth)
	}
}
