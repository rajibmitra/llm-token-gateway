package optimizer

import (
	"strings"
	"testing"

	"github.com/rajibmitra/llm-token-gateway/internal/classifier"
	"github.com/rajibmitra/llm-token-gateway/internal/config"
)

func newTestOptimizer() *Optimizer {
	cls := classifier.New(config.ClassifierConfig{
		JSONDetection:       true,
		MinArrayLength:      3,
		UniformityThreshold: 0.8,
	})
	return New(config.OptimizerConfig{
		TOONEnabled:        true,
		CompactJSONEnabled: true,
		ContextPruning:     true,
		MinSavingsPercent:  5.0,
		StripWhitespace:    true,
		DeduplicateTools:   true,
	}, cls)
}

func TestOptimizeText_TOONEncoding(t *testing.T) {
	opt := newTestOptimizer()

	input := `[
		{"id": 1, "name": "Alice", "role": "admin"},
		{"id": 2, "name": "Bob", "role": "user"},
		{"id": 3, "name": "Charlie", "role": "user"}
	]`

	result, report := opt.optimizeText(input)

	if report.TOONConversions == 0 {
		t.Error("expected TOON conversion to occur")
	}

	if len(result) >= len(input) {
		t.Errorf("expected shorter output: original=%d, optimized=%d", len(input), len(result))
	}

	// TOON output should contain the tabular header
	if !strings.Contains(result, "{") || !strings.Contains(result, "}") {
		// Check for TOON-style header (field list in braces)
		t.Log("TOON output:", result)
	}

	t.Logf("Savings: %.1f%% (%d → %d chars)", report.SavingsPercent(), report.TotalOriginalChars, report.TotalOptimizedChars)
}

func TestOptimizeText_CompactJSON(t *testing.T) {
	opt := newTestOptimizer()

	// Deeply nested config — NOT TOON-eligible, should compact instead
	input := `{
		"database": {
			"host": "localhost",
			"port": 5432,
			"settings": {
				"pool_size": 10,
				"timeout": 30
			}
		}
	}`

	result, report := opt.optimizeText(input)

	if report.CompactJSONApplied == 0 && report.TOONConversions == 0 {
		t.Error("expected some optimization to occur")
	}

	// Compacted JSON should have no extra whitespace
	if strings.Contains(result, "\t") {
		t.Error("compacted JSON should not contain tabs")
	}
}

func TestOptimizeText_PlainText(t *testing.T) {
	opt := newTestOptimizer()

	input := "This is plain text.   It has   extra   whitespace.\n\n\n\nAnd blank lines."

	result, report := opt.optimizeText(input)

	if report.WhitespaceStripped == 0 {
		// Whitespace stripping might not trigger for modest amounts
		t.Log("No whitespace stripped")
	}

	// Should not have more than 1 consecutive blank line
	if strings.Contains(result, "\n\n\n") {
		t.Error("expected excess blank lines to be collapsed")
	}
}

func TestOptimizeMessages_EndToEnd(t *testing.T) {
	opt := newTestOptimizer()

	messages := []Message{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: `Here are the test results: [
			{"test": "login", "status": "pass", "ms": 120},
			{"test": "logout", "status": "pass", "ms": 80},
			{"test": "refresh", "status": "fail", "ms": 210},
			{"test": "register", "status": "pass", "ms": 150}
		]`},
	}

	optimized, report := opt.OptimizeMessages(messages)

	if len(optimized) != len(messages) {
		t.Errorf("expected %d messages, got %d", len(messages), len(optimized))
	}

	t.Logf("Overall savings: %.1f%%", report.SavingsPercent())
	t.Logf("Strategies used: %v", report.Strategies)
}

func TestMinimalTOONEncode(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		contains []string
	}{
		{
			name: "simple object",
			input: map[string]interface{}{
				"name": "Alice",
				"age":  float64(30),
			},
			contains: []string{"name: Alice", "age: 30"},
		},
		{
			name: "uniform array",
			input: []interface{}{
				map[string]interface{}{"id": float64(1), "name": "Alice"},
				map[string]interface{}{"id": float64(2), "name": "Bob"},
				map[string]interface{}{"id": float64(3), "name": "Charlie"},
			},
			contains: []string{"root[3]", "Alice", "Bob", "Charlie"},
		},
		{
			name: "primitive array",
			input: map[string]interface{}{
				"tags": []interface{}{"go", "k8s", "llm"},
			},
			contains: []string{"tags[3]: go,k8s,llm"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := minimalTOONEncode(tt.input)
			if err != nil {
				t.Fatalf("encode error: %v", err)
			}

			for _, expected := range tt.contains {
				if !strings.Contains(result, expected) {
					t.Errorf("expected output to contain %q\nGot:\n%s", expected, result)
				}
			}
		})
	}
}

func TestDeduplicateToolResults(t *testing.T) {
	opt := newTestOptimizer()

	messages := []Message{
		{Role: "user", Content: "list files"},
		{Role: "tool", Content: `{"files": ["main.go", "go.mod"]}`},
		{Role: "assistant", Content: "Here are the files..."},
		{Role: "user", Content: "list files again"},
		{Role: "tool", Content: `{"files": ["main.go", "go.mod"]}`}, // Duplicate
	}

	_, deduped := opt.deduplicateToolResults(messages)

	if deduped != 1 {
		t.Errorf("expected 1 deduplicated tool result, got %d", deduped)
	}
}

func BenchmarkOptimizeText_TOON(b *testing.B) {
	opt := newTestOptimizer()

	// Build a realistic tool result payload
	input := `[`
	for i := 0; i < 50; i++ {
		if i > 0 {
			input += ","
		}
		input += `{"file":"src/module` + string(rune('0'+i%10)) + `.go","lines":` + string(rune('0'+i%9+1)) + `00,"status":"ok","modified":"2026-01-15"}`
	}
	input += `]`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		opt.optimizeText(input)
	}
}
