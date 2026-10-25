package autoinstall

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	yaml "go.yaml.in/yaml/v3"
)

const dummyValue = "0"

func Validate(filename, content string) error {
	substituted := placeholderPattern.ReplaceAllLiteralString(content, dummyValue)
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".yaml", ".yml":
		return validateYAML(substituted)
	case ".xml":
		return validateXML(substituted)
	case ".json":
		return validateJSON(substituted)
	}
	return nil
}

func validateYAML(content string) error {
	dec := yaml.NewDecoder(strings.NewReader(content))
	for {
		var doc interface{}
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("invalid YAML: %s", strings.TrimPrefix(err.Error(), "yaml: "))
		}
	}
}

func validateXML(content string) error {
	dec := xml.NewDecoder(strings.NewReader(content))
	for {
		_, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			var syntaxErr *xml.SyntaxError
			if errors.As(err, &syntaxErr) {
				return fmt.Errorf("invalid XML: line %d: %s", syntaxErr.Line, syntaxErr.Msg)
			}
			return fmt.Errorf("invalid XML: %s", err.Error())
		}
	}
}

func validateJSON(content string) error {
	var doc interface{}
	err := json.Unmarshal([]byte(content), &doc)
	if err == nil {
		return nil
	}
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return fmt.Errorf("invalid JSON: line %d: %s", lineAt(content, syntaxErr.Offset), syntaxErr.Error())
	}
	return fmt.Errorf("invalid JSON: %s", err.Error())
}

func lineAt(content string, offset int64) int {
	if offset > int64(len(content)) {
		offset = int64(len(content))
	}
	return 1 + strings.Count(content[:offset], "\n")
}
