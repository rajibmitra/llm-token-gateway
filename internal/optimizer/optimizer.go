package optimizer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rajibmitra/llm-token-gateway/internal/classifier"
	"github.com/rajibmitra/llm-token-gateway/internal/config"
)

// Result holds the outcome of an optimization pass.
type Result struct {
	Content         string
	OriginalTokens  int
	OptimizedTokens int
	SavingsPercent  float64
	Strategy        string // "toon", "compact_json", "context_prune", "passthrough"
}

// Optimizer applies token reduction strategies to LLM request payloads.
type Optimizer struct {
	cfg        config.OptimizerConfig
	classifier *classifier.Classifier
}

// New creates a new token optimizer.
func New(cfg config.OptimizerConfig, cls *classifier.Classifier) *Optimizer {
	return &Optimizer{
		cfg:        cfg,
		classifier: cls,
	}
}

// OptimizeMessages processes a slice of chat messages and optimizes their content.
// This is the main entry point — works with the standard messages array format
// used by both Anthropic and OpenAI APIs.
func (o *Optimizer) OptimizeMessages(messages []Message) ([]Message, OptimizationReport) {
	report := OptimizationReport{}
	optimized := make([]Message, 0, len(messages))

	for _, msg := range messages {
		optMsg, msgReport := o.optimizeMessage(msg)
		optimized = append(optimized, optMsg)
		report.Merge(msgReport)
	}

	// Context pruning: deduplicate repeated tool results
	if o.cfg.DeduplicateTools {
		var deduped int
		optimized, deduped = o.deduplicateToolResults(optimized)
		report.DeduplicatedTools = deduped
	}

	return optimized, report
}

// Message represents a chat message in the provider-agnostic format.
type Message struct {
	Role    string        `json:"role"`
	Content interface{}   `json:"content"` // string or []ContentBlock
}

// ContentBlock represents a content block within a message (Anthropic format).
type ContentBlock struct {
	Type    string      `json:"type"`
	Text    string      `json:"text,omitempty"`
	Source  interface{} `json:"source,omitempty"`
	ID      string      `json:"id,omitempty"`
	Name    string      `json:"name,omitempty"`
	Input   interface{} `json:"input,omitempty"`
	Content interface{} `json:"content,omitempty"`
}

// OptimizationReport tracks what the optimizer did across all messages.
type OptimizationReport struct {
	TotalOriginalChars   int
	TotalOptimizedChars  int
	TOONConversions      int
	CompactJSONApplied   int
	WhitespaceStripped   int
	DeduplicatedTools    int
	ContextPruned        int
	Strategies           []string
}

func (r *OptimizationReport) Merge(other OptimizationReport) {
	r.TotalOriginalChars += other.TotalOriginalChars
	r.TotalOptimizedChars += other.TotalOptimizedChars
	r.TOONConversions += other.TOONConversions
	r.CompactJSONApplied += other.CompactJSONApplied
	r.WhitespaceStripped += other.WhitespaceStripped
	r.Strategies = append(r.Strategies, other.Strategies...)
}

func (r OptimizationReport) SavingsPercent() float64 {
	if r.TotalOriginalChars == 0 {
		return 0
	}
	return (1 - float64(r.TotalOptimizedChars)/float64(r.TotalOriginalChars)) * 100
}

// optimizeMessage processes a single message.
func (o *Optimizer) optimizeMessage(msg Message) (Message, OptimizationReport) {
	report := OptimizationReport{}

	switch content := msg.Content.(type) {
	case string:
		// Simple text content
		optimized, r := o.optimizeText(content)
		report.Merge(r)
		return Message{Role: msg.Role, Content: optimized}, report

	case []interface{}:
		// Array of content blocks (Anthropic multi-content format)
		blocks := make([]interface{}, 0, len(content))
		for _, block := range content {
			blockMap, ok := block.(map[string]interface{})
			if !ok {
				blocks = append(blocks, block)
				continue
			}

			blockType, _ := blockMap["type"].(string)

			switch blockType {
			case "text":
				text, _ := blockMap["text"].(string)
				optimized, r := o.optimizeText(text)
				report.Merge(r)
				blockMap["text"] = optimized
			case "tool_result":
				// Tool results are prime optimization targets.
				// Anthropic sends content as either a plain string or an array of text blocks.
				switch c := blockMap["content"].(type) {
				case string:
					optimized, r := o.optimizeText(c)
					report.Merge(r)
					blockMap["content"] = optimized
				case []interface{}:
					// Most common format: [{"type":"text","text":"..."}]
					for _, inner := range c {
						innerBlock, ok := inner.(map[string]interface{})
						if !ok {
							continue
						}
						if innerBlock["type"] == "text" {
							text, _ := innerBlock["text"].(string)
							optimized, r := o.optimizeText(text)
							report.Merge(r)
							innerBlock["text"] = optimized
						}
					}
				}
			}
			blocks = append(blocks, blockMap)
		}
		return Message{Role: msg.Role, Content: blocks}, report
	}

	return msg, report
}

// optimizeText is the core optimization function for a piece of text content.
func (o *Optimizer) optimizeText(content string) (string, OptimizationReport) {
	report := OptimizationReport{
		TotalOriginalChars: len(content),
	}

	// Skip if content is too large (safety valve)
	if o.cfg.MaxPayloadSize > 0 && len(content) > o.cfg.MaxPayloadSize*1024 {
		report.TotalOptimizedChars = len(content)
		report.Strategies = append(report.Strategies, "passthrough:too_large")
		return content, report
	}

	// Classify the content
	classified := o.classifier.Classify(content)

	switch classified.Type {
	case classifier.ContentTOONEligible:
		if o.cfg.TOONEnabled {
			encoded, err := o.encodeAsTOON(classified.JSONBlobs[0].Parsed)
			if err == nil && o.meetsSavingsThreshold(content, encoded) {
				report.TotalOptimizedChars = len(encoded)
				report.TOONConversions++
				report.Strategies = append(report.Strategies, "toon")
				return encoded, report
			}
		}
		// Fall through to compact JSON
		if o.cfg.CompactJSONEnabled {
			compacted := o.compactJSON(content)
			report.TotalOptimizedChars = len(compacted)
			report.CompactJSONApplied++
			report.Strategies = append(report.Strategies, "compact_json")
			return compacted, report
		}

	case classifier.ContentJSON:
		if o.cfg.CompactJSONEnabled {
			compacted := o.compactJSON(content)
			report.TotalOptimizedChars = len(compacted)
			report.CompactJSONApplied++
			report.Strategies = append(report.Strategies, "compact_json")
			return compacted, report
		}

	case classifier.ContentMixed:
		// Replace embedded JSON blobs with optimized versions
		if o.cfg.TOONEnabled || o.cfg.CompactJSONEnabled {
			result := o.optimizeMixed(content, classified)
			report.TotalOptimizedChars = len(result)
			report.TOONConversions += classified.JSONBlobs[0].StartIndex // rough count
			report.Strategies = append(report.Strategies, "mixed")
			return result, report
		}

	case classifier.ContentPlainText:
		if o.cfg.StripWhitespace {
			stripped := o.stripExcessWhitespace(content)
			report.TotalOptimizedChars = len(stripped)
			if len(stripped) < len(content) {
				report.WhitespaceStripped++
				report.Strategies = append(report.Strategies, "whitespace_strip")
			} else {
				report.Strategies = append(report.Strategies, "passthrough")
			}
			return stripped, report
		}
	}

	report.TotalOptimizedChars = len(content)
	report.Strategies = append(report.Strategies, "passthrough")
	return content, report
}

// encodeAsTOON converts a parsed JSON value into TOON format.
// This is the integration point with toon-go library.
func (o *Optimizer) encodeAsTOON(v interface{}) (string, error) {
	// TODO: Replace with actual toon-go library call:
	//   import toon "github.com/toon-format/toon-go"
	//   return toon.Encode(v)
	//
	// For now, implement a minimal TOON encoder for uniform arrays of objects,
	// which covers the highest-value optimization case.
	return minimalTOONEncode(v)
}

// minimalTOONEncode is a built-in TOON encoder for the most common case:
// objects containing uniform arrays of flat objects.
func minimalTOONEncode(v interface{}) (string, error) {
	var buf bytes.Buffer

	switch val := v.(type) {
	case map[string]interface{}:
		if err := encodeTOONObject(&buf, val, ""); err != nil {
			return "", err
		}
	case []interface{}:
		if err := encodeTOONArray(&buf, "root", val, ""); err != nil {
			return "", err
		}
	default:
		return fmt.Sprintf("%v", v), nil
	}

	return buf.String(), nil
}

func encodeTOONObject(buf *bytes.Buffer, obj map[string]interface{}, indent string) error {
	for key, val := range obj {
		switch v := val.(type) {
		case map[string]interface{}:
			fmt.Fprintf(buf, "%s%s:\n", indent, key)
			encodeTOONObject(buf, v, indent+"  ")

		case []interface{}:
			if err := encodeTOONArray(buf, key, v, indent); err != nil {
				return err
			}

		case string:
			if needsQuoting(v) {
				fmt.Fprintf(buf, "%s%s: \"%s\"\n", indent, key, escapeString(v))
			} else {
				fmt.Fprintf(buf, "%s%s: %s\n", indent, key, v)
			}

		case float64:
			if v == float64(int64(v)) {
				fmt.Fprintf(buf, "%s%s: %d\n", indent, key, int64(v))
			} else {
				fmt.Fprintf(buf, "%s%s: %g\n", indent, key, v)
			}

		case bool:
			fmt.Fprintf(buf, "%s%s: %t\n", indent, key, v)

		case nil:
			fmt.Fprintf(buf, "%s%s: null\n", indent, key)

		default:
			fmt.Fprintf(buf, "%s%s: %v\n", indent, key, v)
		}
	}
	return nil
}

func encodeTOONArray(buf *bytes.Buffer, key string, arr []interface{}, indent string) error {
	if len(arr) == 0 {
		fmt.Fprintf(buf, "%s%s[0]: \n", indent, key)
		return nil
	}

	// Check if this is a uniform array of objects (prime TOON target)
	if fields := getUniformFields(arr); fields != nil {
		// Tabular format: key[N]{field1,field2,...}:
		fmt.Fprintf(buf, "%s%s[%d]{%s}:\n", indent, key, len(arr), strings.Join(fields, ","))
		for _, item := range arr {
			obj := item.(map[string]interface{})
			values := make([]string, len(fields))
			for i, field := range fields {
				values[i] = formatValue(obj[field])
			}
			fmt.Fprintf(buf, "%s  %s\n", indent, strings.Join(values, ","))
		}
		return nil
	}

	// Check if it's a simple array of primitives
	if allPrimitives(arr) {
		values := make([]string, len(arr))
		for i, v := range arr {
			values[i] = formatValue(v)
		}
		fmt.Fprintf(buf, "%s%s[%d]: %s\n", indent, key, len(arr), strings.Join(values, ","))
		return nil
	}

	// Fall back to YAML-like list format
	for _, item := range arr {
		switch v := item.(type) {
		case map[string]interface{}:
			fmt.Fprintf(buf, "%s- \n", indent)
			encodeTOONObject(buf, v, indent+"  ")
		default:
			fmt.Fprintf(buf, "%s- %s\n", indent, formatValue(item))
			_ = v
		}
	}
	return nil
}

// getUniformFields returns field names if all array elements are objects with identical keys.
func getUniformFields(arr []interface{}) []string {
	if len(arr) < 2 {
		return nil
	}

	first, ok := arr[0].(map[string]interface{})
	if !ok {
		return nil
	}

	fields := make([]string, 0, len(first))
	for k := range first {
		fields = append(fields, k)
	}

	// Check all elements have the same fields and all values are primitives
	for _, item := range arr[1:] {
		obj, ok := item.(map[string]interface{})
		if !ok {
			return nil
		}
		if len(obj) != len(fields) {
			return nil
		}
		for _, f := range fields {
			v, exists := obj[f]
			if !exists {
				return nil
			}
			// Check value is primitive
			switch v.(type) {
			case string, float64, bool, nil:
				// OK
			default:
				return nil
			}
		}
	}

	return fields
}

func allPrimitives(arr []interface{}) bool {
	for _, v := range arr {
		switch v.(type) {
		case string, float64, bool, nil:
			continue
		default:
			return false
		}
	}
	return true
}

func formatValue(v interface{}) string {
	switch val := v.(type) {
	case string:
		if needsQuoting(val) {
			return fmt.Sprintf("\"%s\"", escapeString(val))
		}
		return val
	case float64:
		if val == float64(int64(val)) {
			return fmt.Sprintf("%d", int64(val))
		}
		return fmt.Sprintf("%g", val)
	case bool:
		return fmt.Sprintf("%t", val)
	case nil:
		return "null"
	default:
		return fmt.Sprintf("%v", v)
	}
}

func needsQuoting(s string) bool {
	return strings.ContainsAny(s, ",\n\"{}[]:")
}

func escapeString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

// compactJSON minifies JSON by removing all unnecessary whitespace.
func (o *Optimizer) compactJSON(content string) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(content)); err != nil {
		return content // Return original if compaction fails
	}
	return buf.String()
}

// optimizeMixed replaces embedded JSON blobs within text with optimized versions.
func (o *Optimizer) optimizeMixed(content string, classified classifier.ClassifiedContent) string {
	// Process blobs in reverse order to preserve indices
	result := content
	for i := len(classified.JSONBlobs) - 1; i >= 0; i-- {
		blob := classified.JSONBlobs[i]

		var replacement string
		if blob.Uniformity >= o.classifier.Classify(blob.Raw).TabularEligibility && o.cfg.TOONEnabled {
			encoded, err := o.encodeAsTOON(blob.Parsed)
			if err == nil {
				replacement = encoded
			}
		}

		if replacement == "" && o.cfg.CompactJSONEnabled {
			replacement = o.compactJSON(blob.Raw)
		}

		if replacement != "" {
			result = result[:blob.StartIndex] + replacement + result[blob.EndIndex:]
		}
	}

	return result
}

// stripExcessWhitespace removes redundant whitespace from plain text.
func (o *Optimizer) stripExcessWhitespace(content string) string {
	// Collapse multiple blank lines into one
	lines := strings.Split(content, "\n")
	var result []string
	blankCount := 0

	for _, line := range lines {
		trimmed := strings.TrimRight(line, " \t")
		if trimmed == "" {
			blankCount++
			if blankCount <= 1 {
				result = append(result, "")
			}
		} else {
			blankCount = 0
			result = append(result, trimmed)
		}
	}

	return strings.Join(result, "\n")
}

// meetsSavingsThreshold checks if the optimization saves enough to be worth it.
func (o *Optimizer) meetsSavingsThreshold(original, optimized string) bool {
	if o.cfg.MinSavingsPercent == 0 {
		return true
	}
	savings := (1 - float64(len(optimized))/float64(len(original))) * 100
	return savings >= o.cfg.MinSavingsPercent
}

// deduplicateToolResults removes duplicate tool call results in conversation history.
// Anthropic sends tool results as "user" role messages with tool_result content blocks,
// not as a separate "tool" role (which is OpenAI's format).
func (o *Optimizer) deduplicateToolResults(messages []Message) ([]Message, int) {
	seen := make(map[string]bool)
	deduped := 0

	for _, msg := range messages {
		// Handle OpenAI-style "tool" role messages
		if msg.Role == "tool" {
			if content, ok := msg.Content.(string); ok {
				if seen[content] {
					msg.Content = "[duplicate tool result — see earlier in conversation]"
					deduped++
				} else {
					seen[content] = true
				}
			}
			continue
		}

		// Handle Anthropic-style: user messages containing tool_result blocks
		if msg.Role != "user" {
			continue
		}
		blocks, ok := msg.Content.([]interface{})
		if !ok {
			continue
		}
		for _, block := range blocks {
			blockMap, ok := block.(map[string]interface{})
			if !ok || blockMap["type"] != "tool_result" {
				continue
			}
			// Extract text content for dedup key
			var key string
			switch c := blockMap["content"].(type) {
			case string:
				key = c
			case []interface{}:
				var parts []string
				for _, inner := range c {
					if ib, ok := inner.(map[string]interface{}); ok {
						if ib["type"] == "text" {
							parts = append(parts, ib["text"].(string))
						}
					}
				}
				key = strings.Join(parts, "")
			}
			if key == "" {
				continue
			}
			if seen[key] {
				blockMap["content"] = "[duplicate tool result — see earlier in conversation]"
				deduped++
			} else {
				seen[key] = true
			}
		}
	}

	return messages, deduped
}
