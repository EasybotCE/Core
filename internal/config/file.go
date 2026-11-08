// SPDX-License-Identifier: AGPL-3.0-only
package config

import (
	"bytes"
	"errors"
	"io"
	"os"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const maxFileBytes = 64 * 1024

var bootstrapKeys = map[string]struct{}{
	"database_driver":  {},
	"database_dsn":     {},
	"listen_addr":      {},
	"admin_token":      {},
	"log_level":        {},
	"shutdown_timeout": {},
}

// readFile deliberately returns fixed errors: YAML diagnostics can include
// source lines containing credentials, and file paths can also contain secrets.
func readFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("cannot open bootstrap YAML file")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return nil, errors.New("cannot read bootstrap YAML file")
	}
	if len(data) > maxFileBytes {
		return nil, errors.New("bootstrap YAML file exceeds 64 KiB")
	}
	if !utf8.Valid(data) {
		return nil, errors.New("bootstrap YAML must be valid UTF-8")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := decoder.Decode(&doc); err != nil {
		return nil, errors.New("bootstrap YAML must contain one valid mapping document")
	}
	if err := decoder.Decode(new(yaml.Node)); err != io.EOF {
		return nil, errors.New("bootstrap YAML must contain exactly one document")
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode || doc.Content[0].Tag != "!!map" || doc.Content[0].Anchor != "" {
		return nil, errors.New("bootstrap YAML root must be a mapping without anchors")
	}
	root := doc.Content[0]
	values := make(map[string]string, len(root.Content)/2)
	for i := 0; i < len(root.Content); i += 2 {
		key, value := root.Content[i], root.Content[i+1]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || key.Anchor != "" {
			return nil, errors.New("bootstrap YAML keys must be plain string field names")
		}
		if _, known := bootstrapKeys[key.Value]; !known {
			return nil, errors.New("bootstrap YAML contains an unknown field")
		}
		if _, duplicate := values[key.Value]; duplicate {
			return nil, errors.New("bootstrap YAML contains a duplicate field")
		}
		if value.Kind != yaml.ScalarNode || value.Tag != "!!str" || value.Anchor != "" {
			return nil, errors.New("bootstrap YAML values must be strings without anchors or aliases")
		}
		values[key.Value] = value.Value
	}
	return values, nil
}
