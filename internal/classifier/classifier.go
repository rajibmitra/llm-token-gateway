package classifier

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/rajibmitra/llm-token-gateway/internal/config"
)

// ContentType represents the classification of content within a message.
type ContentType int

const (
	ContentPlainText ContentType = iota
	ContentJSON                  // Valid JSON but not TOON-eligible
	ContentTOONEligible          // Uniform arrays of objects — prime TOON target
	ContentMixed                 // Text with embedded JSON blobs
)

// ClassifiedContent holds the analysis result for a piece of content.
type ClassifiedContent struct {
	Type               ContentType
	TabularEligibility float64 // 0.0 - 1.0, ratio of content eligible for tabular encoding
	JSONBlobs          []JSONBlob
	OriginalSize       int
	EstimatedSavings   float64 // Estimated token savings percentage
}

// JSONBlob represents a detected JSON structure within the content.
type JSONBlob struct {
	StartIndex int
	EndIndex   int
	Raw        string
	Parsed     interface{}
	IsArray    bool
	ArrayLen   int
	FieldCount int
	Uniformity float64 // How consistent the fields are across array elements
}

// Classifier analyzes message content to determine optimization strategy.
type Classifier struct {
	cfg            config.ClassifierConfig
	jsonBlobRegex  *regexp.Regexp
}

// New creates a new content classifier.
func New(cfg config.ClassifierConfig) *Classifier {
	if cfg.MinArrayLength == 0 {
		cfg.MinArrayLength = 3
	}
	if cfg.UniformityThreshold == 0 {
		cfg.UniformityThreshold = 0.8
	}

	return &Classifier{
		cfg:           cfg,
		// Match JSON objects/arrays that are at least 50 chars (skip tiny ones)
		jsonBlobRegex: regexp.MustCompile(`(?s)(\{[^{}]{50,}\}|\[[^\[\]]{50,}\])`),
	}
}

// Classify analyzes content and returns classification with optimization hints.
func (c *Classifier) Classify(content string) ClassifiedContent {
	result := ClassifiedContent{
		OriginalSize: len(content),
	}

	trimmed := strings.TrimSpace(content)

	// Try parsing entire content as JSON first
	if c.isTopLevelJSON(trimmed) {
		return c.classifyJSON(trimmed)
	}

	// Scan for embedded JSON blobs within text
	if c.cfg.JSONDetection {
		blobs := c.detectJSONBlobs(content)
		if len(blobs) > 0 {
			result.Type = ContentMixed
			result.JSONBlobs = blobs
			result.TabularEligibility = c.calculateOverallEligibility(blobs, len(content))
			result.EstimatedSavings = result.TabularEligibility * 0.4 // TOON typically saves ~40%
			return result
		}
	}

	result.Type = ContentPlainText
	return result
}

// isTopLevelJSON checks if the entire content is a single JSON value.
func (c *Classifier) isTopLevelJSON(s string) bool {
	if len(s) == 0 {
		return false
	}
	return (s[0] == '{' || s[0] == '[') && json.Valid([]byte(s))
}

// classifyJSON analyzes a top-level JSON value.
func (c *Classifier) classifyJSON(raw string) ClassifiedContent {
	result := ClassifiedContent{
		OriginalSize: len(raw),
	}

	var parsed interface{}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		result.Type = ContentPlainText
		return result
	}

	eligibility := c.analyzeValue(parsed)
	if eligibility >= c.cfg.UniformityThreshold {
		result.Type = ContentTOONEligible
	} else {
		result.Type = ContentJSON
	}
	result.TabularEligibility = eligibility
	result.EstimatedSavings = eligibility * 0.4

	result.JSONBlobs = []JSONBlob{{
		Raw:        raw,
		Parsed:     parsed,
		Uniformity: eligibility,
	}}

	return result
}

// analyzeValue recursively calculates the tabular eligibility of a JSON value.
func (c *Classifier) analyzeValue(v interface{}) float64 {
	switch val := v.(type) {
	case []interface{}:
		return c.analyzeArray(val)
	case map[string]interface{}:
		return c.analyzeObject(val)
	default:
		return 0.0 // Primitives have no tabular eligibility
	}
}

// analyzeArray determines if an array is uniform (all objects with same fields).
func (c *Classifier) analyzeArray(arr []interface{}) float64 {
	if len(arr) < c.cfg.MinArrayLength {
		return 0.0
	}

	// Check if all elements are objects
	objectCount := 0
	var fieldSets []map[string]bool

	for _, item := range arr {
		obj, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		objectCount++

		fields := make(map[string]bool)
		for k := range obj {
			fields[k] = true
		}
		fieldSets = append(fieldSets, fields)
	}

	if objectCount == 0 || float64(objectCount)/float64(len(arr)) < 0.8 {
		return 0.0
	}

	// Calculate field uniformity — how consistent are the fields across objects?
	if len(fieldSets) == 0 {
		return 0.0
	}

	// Use the first object's fields as the reference schema
	reference := fieldSets[0]
	totalScore := 0.0

	for i := 1; i < len(fieldSets); i++ {
		matching := 0
		total := len(reference)
		if len(fieldSets[i]) > total {
			total = len(fieldSets[i])
		}
		for field := range reference {
			if fieldSets[i][field] {
				matching++
			}
		}
		if total > 0 {
			totalScore += float64(matching) / float64(total)
		}
	}

	uniformity := totalScore / float64(len(fieldSets)-1)

	// Check if values are primitives (TOON tabular format requires primitive values)
	primitiveRatio := c.checkPrimitiveValues(arr)

	return uniformity * primitiveRatio
}

// analyzeObject checks nested arrays within an object for tabular eligibility.
func (c *Classifier) analyzeObject(obj map[string]interface{}) float64 {
	if len(obj) == 0 {
		return 0.0
	}

	totalEligibility := 0.0
	eligibleFields := 0

	for _, v := range obj {
		if arr, ok := v.([]interface{}); ok {
			e := c.analyzeArray(arr)
			if e > 0 {
				totalEligibility += e
				eligibleFields++
			}
		}
	}

	if eligibleFields == 0 {
		return 0.0
	}

	// Weight by how much of the object is tabular-eligible
	return (totalEligibility / float64(eligibleFields)) * (float64(eligibleFields) / float64(len(obj)))
}

// checkPrimitiveValues checks what fraction of array element values are primitives.
func (c *Classifier) checkPrimitiveValues(arr []interface{}) float64 {
	totalValues := 0
	primitiveValues := 0

	for _, item := range arr {
		obj, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		for _, v := range obj {
			totalValues++
			switch v.(type) {
			case string, float64, bool, nil:
				primitiveValues++
			}
		}
	}

	if totalValues == 0 {
		return 0.0
	}
	return float64(primitiveValues) / float64(totalValues)
}

// detectJSONBlobs finds JSON structures embedded within text content.
func (c *Classifier) detectJSONBlobs(content string) []JSONBlob {
	var blobs []JSONBlob

	// Use bracket matching for more reliable detection
	for i := 0; i < len(content); i++ {
		if content[i] == '{' || content[i] == '[' {
			end := c.findMatchingBracket(content, i)
			if end == -1 {
				continue
			}

			candidate := content[i : end+1]
			if len(candidate) < 50 {
				continue // Skip tiny JSON
			}

			var parsed interface{}
			if err := json.Unmarshal([]byte(candidate), &parsed); err != nil {
				continue
			}

			blob := JSONBlob{
				StartIndex: i,
				EndIndex:   end + 1,
				Raw:        candidate,
				Parsed:     parsed,
			}

			if arr, ok := parsed.([]interface{}); ok {
				blob.IsArray = true
				blob.ArrayLen = len(arr)
				blob.Uniformity = c.analyzeArray(arr)
			} else if obj, ok := parsed.(map[string]interface{}); ok {
				blob.FieldCount = len(obj)
				blob.Uniformity = c.analyzeObject(obj)
			}

			blobs = append(blobs, blob)
			i = end // Skip past this blob
		}
	}

	return blobs
}

// findMatchingBracket finds the closing bracket for an opening bracket.
func (c *Classifier) findMatchingBracket(s string, start int) int {
	open := s[start]
	var close byte
	if open == '{' {
		close = '}'
	} else {
		close = ']'
	}

	depth := 0
	inString := false
	escaped := false

	for i := start; i < len(s); i++ {
		if escaped {
			escaped = false
			continue
		}
		if s[i] == '\\' && inString {
			escaped = true
			continue
		}
		if s[i] == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		if s[i] == open {
			depth++
		} else if s[i] == close {
			depth--
			if depth == 0 {
				return i
			}
		}
	}

	return -1
}

// calculateOverallEligibility computes the fraction of content that's TOON-eligible.
func (c *Classifier) calculateOverallEligibility(blobs []JSONBlob, totalSize int) float64 {
	eligibleSize := 0
	for _, b := range blobs {
		if b.Uniformity >= c.cfg.UniformityThreshold {
			eligibleSize += len(b.Raw)
		}
	}
	if totalSize == 0 {
		return 0.0
	}
	return float64(eligibleSize) / float64(totalSize)
}
