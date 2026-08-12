package config

import (
	"os"
	"strings"
	"testing"
)

func TestLoadPromptFromEmbed(t *testing.T) {
	os.Setenv("GOSTOCK_DB_PASSWORD", "x")
	os.Setenv("GOSTOCK_AI_PROMPT", "")
	os.Setenv("GOSTOCK_AI_PROMPT_FILE", "does-not-exist.md")
	os.Setenv("GOSTOCK_AI_REVIEW_PROMPT", "")
	os.Setenv("GOSTOCK_AI_REVIEW_PROMPT_FILE", "does-not-exist-review.md")
	c, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(c.AIPrompt) < 200 {
		t.Fatalf("embedded prompt too short: %d", len(c.AIPrompt))
	}
	if !strings.Contains(c.AIPrompt, "recommendations") {
		t.Fatalf("prompt missing json contract")
	}
	if !strings.Contains(c.AIReviewPrompt, "directives") {
		t.Fatalf("review prompt missing directives contract")
	}
}

// 确保内嵌提示词与外部可覆盖文件一致，避免两份漂移。
func TestEmbeddedPromptMatchesExternal(t *testing.T) {
	external, err := os.ReadFile("../../config/ai_prompt.md")
	if err != nil {
		t.Skipf("外部提示词文件不可读: %v", err)
	}
	if strings.TrimSpace(string(external)) != strings.TrimSpace(embeddedAIPrompt) {
		t.Fatal("internal/config/ai_prompt.md 与 config/ai_prompt.md 不一致，请同步")
	}
	reviewExternal, err := os.ReadFile("../../config/review_prompt.md")
	if err != nil {
		t.Skipf("外部复盘提示词文件不可读: %v", err)
	}
	if strings.TrimSpace(string(reviewExternal)) != strings.TrimSpace(embeddedReviewPrompt) {
		t.Fatal("internal/config/review_prompt.md 与 config/review_prompt.md 不一致，请同步")
	}
}
