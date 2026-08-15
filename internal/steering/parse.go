package steering

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

// ParseResult extracts the final steering object from plain JSON, Markdown
// fences, prose, or Codex JSONL envelopes, then validates it against durable
// state. The last valid steering result wins.
func ParseResult(raw string, state ValidationState) (Result, error) {
	if strings.TrimSpace(raw) == "" {
		return Result{}, fmt.Errorf("%w: empty output", ErrInvalidResult)
	}
	candidates := extractJSONObjects(raw)
	var lastErr error
	for index := len(candidates) - 1; index >= 0; index-- {
		if !looksLikeResult(candidates[index]) {
			continue
		}
		result, err := decodeResult(candidates[index])
		if err != nil {
			lastErr = err
			continue
		}
		result, err = ValidateResult(result, state)
		if err == nil {
			return result, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return Result{}, lastErr
	}
	return Result{}, fmt.Errorf("%w: no structured steering result found", ErrInvalidResult)
}

func decodeResult(raw json.RawMessage) (Result, error) {
	var result Result
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return Result{}, fmt.Errorf("%w: decode result: %v", ErrInvalidResult, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Result{}, fmt.Errorf("%w: result must contain one JSON object", ErrInvalidResult)
	}
	return result, nil
}

func looksLikeResult(raw json.RawMessage) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return false
	}
	_, response := fields["assistant_response"]
	_, effects := fields["effects"]
	return response || effects
}

func extractJSONObjects(raw string) []json.RawMessage {
	var candidates []json.RawMessage
	seen := make(map[string]struct{})
	add := func(encoded []byte) {
		encoded = bytes.TrimSpace(encoded)
		if len(encoded) == 0 || encoded[0] != '{' || !json.Valid(encoded) {
			return
		}
		key := string(encoded)
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		candidates = append(candidates, append(json.RawMessage(nil), encoded...))
	}

	var visit func(any)
	visit = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			if encoded, err := json.Marshal(typed); err == nil {
				add(encoded)
			}
			keys := make([]string, 0, len(typed))
			for key := range typed {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				visit(typed[key])
			}
		case []any:
			for _, item := range typed {
				visit(item)
			}
		case string:
			for _, object := range balancedJSONObjects(typed) {
				add(object)
				var nested any
				if json.Unmarshal(object, &nested) == nil {
					visit(nested)
				}
			}
		}
	}

	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var value any
		if json.Unmarshal([]byte(line), &value) == nil {
			visit(value)
		}
	}
	for _, object := range balancedJSONObjects(raw) {
		add(object)
		var value any
		if json.Unmarshal(object, &value) == nil {
			visit(value)
		}
	}
	return candidates
}

func balancedJSONObjects(text string) [][]byte {
	data := []byte(text)
	objects := make([][]byte, 0)
	start, depth := -1, 0
	inString, escaped := false, false
	for index, character := range data {
		if start < 0 {
			if character == '{' {
				start, depth = index, 1
				inString, escaped = false, false
			}
			continue
		}
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if character == '\\' {
				escaped = true
				continue
			}
			if character == '"' {
				inString = false
			}
			continue
		}
		switch character {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				objects = append(objects, append([]byte(nil), data[start:index+1]...))
				start = -1
			}
		}
	}
	return objects
}
