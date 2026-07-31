package protocol

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

func DecodeTask(data []byte, path string, refs References) (Task, error) {
	var task Task
	if err := decodeYAML(data, &task); err != nil {
		return task, fmt.Errorf("decode task: %w", err)
	}
	if err := task.Validate(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), refs); err != nil {
		return task, err
	}
	return task, nil
}

func DecodeProject(data []byte, path string) (Project, error) {
	var project Project
	if err := decodeYAML(data, &project); err != nil {
		return project, fmt.Errorf("decode project: %w", err)
	}
	policyPresent, err := projectPolicyFieldsPresent(data)
	if err != nil {
		return project, err
	}
	if !policyPresent.policy && !policyPresent.version && !policyPresent.history {
		project = project.NormalizePolicy()
	} else if !policyPresent.policy || !policyPresent.version {
		return project, fmt.Errorf("project collaboration policy fields are incomplete")
	}
	if err := project.Validate(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))); err != nil {
		return project, err
	}
	return project, nil
}

type projectPolicyFields struct {
	policy  bool
	version bool
	history bool
}

func projectPolicyFieldsPresent(data []byte) (projectPolicyFields, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return projectPolicyFields{}, err
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return projectPolicyFields{}, fmt.Errorf("project yaml must be a mapping")
	}
	fields := projectPolicyFields{}
	mapping := document.Content[0]
	for index := 0; index < len(mapping.Content); index += 2 {
		switch mapping.Content[index].Value {
		case "collaboration_policy":
			fields.policy = true
		case "policy_version":
			fields.version = true
		case "policy_history":
			fields.history = true
		}
	}
	return fields, nil
}

func DecodeClient(data []byte, path string) (Client, error) {
	var client Client
	if err := decodeYAML(data, &client); err != nil {
		return client, fmt.Errorf("decode client: %w", err)
	}
	if err := client.Validate(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))); err != nil {
		return client, err
	}
	return client, nil
}

func decodeYAML(data []byte, target any) error {
	if err := validateYAML(data); err != nil {
		return err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	return decoder.Decode(target)
}

func validateYAML(data []byte) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return fmt.Errorf("decode yaml document: %w", err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("yaml must contain one document")
		}
		return fmt.Errorf("decode additional yaml document: %w", err)
	}
	return inspectNode(&document)
}

func inspectNode(node *yaml.Node) error {
	if node.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i < len(node.Content); i += 2 {
			key, value := node.Content[i], node.Content[i+1]
			if seen[key.Value] {
				return fmt.Errorf("duplicate yaml key %q", key.Value)
			}
			seen[key.Value] = true
			if forbiddenKey(key.Value) {
				return fmt.Errorf("forbidden transferable field %q", key.Value)
			}
			if key.Value == "created_at" {
				if _, err := time.Parse(time.RFC3339, value.Value); err != nil || !strings.HasSuffix(value.Value, "Z") {
					return fmt.Errorf("created_at must be UTC RFC 3339")
				}
			}
			if err := inspectNode(value); err != nil {
				return err
			}
		}
		return nil
	}
	if node.Kind == yaml.ScalarNode && isLocalFilesystemPath(node.Value) {
		return fmt.Errorf("absolute paths are forbidden in transferable yaml")
	}
	for _, child := range node.Content {
		if err := inspectNode(child); err != nil {
			return err
		}
	}
	return nil
}

func forbiddenKey(key string) bool {
	key = strings.ReplaceAll(strings.ToLower(key), "-", "_")
	_, forbidden := map[string]struct{}{
		"local_path": {}, "absolute_path": {}, "pid": {}, "pty": {}, "session_id": {},
		"secret": {}, "token": {}, "access_token": {}, "password": {}, "credential": {}, "api_key": {},
	}[key]
	return forbidden
}

func isLocalFilesystemPath(value string) bool {
	return strings.HasPrefix(value, "~/") || containsLocalFilesystemPath(value)
}
