package classifier

import (
	"testing"

	"github.com/rajibmitra/llm-token-gateway/internal/config"
)

func newTestClassifier() *Classifier {
	return New(config.ClassifierConfig{
		JSONDetection:       true,
		MinArrayLength:      3,
		UniformityThreshold: 0.8,
	})
}

func TestClassify_PlainText(t *testing.T) {
	c := newTestClassifier()
	result := c.Classify("Hello, this is a regular text message with no JSON.")

	if result.Type != ContentPlainText {
		t.Errorf("expected ContentPlainText, got %v", result.Type)
	}
	if result.TabularEligibility != 0 {
		t.Errorf("expected 0 eligibility, got %f", result.TabularEligibility)
	}
}

func TestClassify_UniformArray(t *testing.T) {
	c := newTestClassifier()
	input := `[
		{"id": 1, "name": "Alice", "role": "admin"},
		{"id": 2, "name": "Bob", "role": "user"},
		{"id": 3, "name": "Charlie", "role": "user"},
		{"id": 4, "name": "Diana", "role": "admin"}
	]`

	result := c.Classify(input)

	if result.Type != ContentTOONEligible {
		t.Errorf("expected ContentTOONEligible, got %v", result.Type)
	}
	if result.TabularEligibility < 0.8 {
		t.Errorf("expected high eligibility, got %f", result.TabularEligibility)
	}
}

func TestClassify_NestedJSON(t *testing.T) {
	c := newTestClassifier()
	input := `{
		"config": {
			"database": {"host": "localhost", "port": 5432},
			"cache": {"host": "redis", "port": 6379}
		}
	}`

	result := c.Classify(input)

	// Deeply nested config should NOT be TOON-eligible
	if result.Type == ContentTOONEligible {
		t.Error("deeply nested config should not be TOON-eligible")
	}
}

func TestClassify_ObjectWithUniformArray(t *testing.T) {
	c := newTestClassifier()
	input := `{
		"metadata": {"version": "1.0"},
		"users": [
			{"id": 1, "name": "Alice", "active": true},
			{"id": 2, "name": "Bob", "active": false},
			{"id": 3, "name": "Charlie", "active": true}
		]
	}`

	result := c.Classify(input)

	if result.TabularEligibility == 0 {
		t.Error("expected some tabular eligibility for object with uniform array")
	}
}

func TestClassify_SmallArray(t *testing.T) {
	c := newTestClassifier()
	input := `[{"id": 1, "name": "Alice"}, {"id": 2, "name": "Bob"}]`

	result := c.Classify(input)

	// Array with only 2 elements should not meet min_array_length=3
	if result.Type == ContentTOONEligible {
		t.Error("small array (2 elements) should not be TOON-eligible with min_array_length=3")
	}
}

func TestClassify_MixedContent(t *testing.T) {
	c := newTestClassifier()
	input := `Here are the test results:
	[
		{"test": "auth_login", "status": "pass", "duration": 1.2},
		{"test": "auth_logout", "status": "pass", "duration": 0.8},
		{"test": "auth_refresh", "status": "fail", "duration": 2.1},
		{"test": "auth_register", "status": "pass", "duration": 1.5}
	]
	Total: 4 tests, 3 passed, 1 failed.`

	result := c.Classify(input)

	if result.Type != ContentMixed {
		t.Errorf("expected ContentMixed, got %v", result.Type)
	}
	if len(result.JSONBlobs) == 0 {
		t.Error("expected at least one JSON blob detected")
	}
}

func TestFindMatchingBracket(t *testing.T) {
	c := newTestClassifier()

	tests := []struct {
		input    string
		start    int
		expected int
	}{
		{`{"a": 1}`, 0, 7},
		{`[1, 2, 3]`, 0, 8},
		{`{"a": {"b": 1}}`, 0, 14},
		{`{"a": "value with }"}`, 0, 20},
		{`{unclosed`, 0, -1},
	}

	for _, tt := range tests {
		got := c.findMatchingBracket(tt.input, tt.start)
		if got != tt.expected {
			t.Errorf("findMatchingBracket(%q, %d) = %d, want %d",
				tt.input, tt.start, got, tt.expected)
		}
	}
}

func BenchmarkClassify_UniformArray(b *testing.B) {
	c := newTestClassifier()
	input := `[`
	for i := 0; i < 100; i++ {
		if i > 0 {
			input += ","
		}
		input += `{"id":` + string(rune('0'+i%10)) + `,"name":"user","role":"admin","active":true}`
	}
	input += `]`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Classify(input)
	}
}
