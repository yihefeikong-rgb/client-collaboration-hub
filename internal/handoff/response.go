package handoff

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/protocol"
)

type CandidateEvidence struct {
	ID       string                `json:"id"`
	Kind     protocol.EvidenceKind `json:"kind"`
	Summary  string                `json:"summary"`
	FileRefs []string              `json:"file_refs"`
}

type CandidateResponse struct {
	FormatVersion        string              `json:"format_version"`
	PackageID            string              `json:"package_id"`
	TaskID               string              `json:"task_id"`
	ObservedVersion      int64               `json:"observed_version"`
	ObservedThroughEvent int64               `json:"observed_through_event"`
	Actor                string              `json:"actor"`
	ProposedAction       protocol.Action     `json:"proposed_action"`
	NextAssignee         string              `json:"next_assignee"`
	Message              string              `json:"message"`
	Feedback             string              `json:"feedback"`
	EvidenceRefs         []string            `json:"evidence_refs"`
	Evidence             []CandidateEvidence `json:"evidence"`
}

type CommandStep struct {
	Program string   `json:"program"`
	Args    []string `json:"args"`
}

type ResponseValidation struct {
	Manifest Manifest
	Response CandidateResponse
	Steps    []CommandStep
}

func NewCandidateResponse(manifest Manifest) CandidateResponse {
	return CandidateResponse{
		FormatVersion:        "1",
		PackageID:            manifest.PackageID,
		TaskID:               manifest.TaskID,
		ObservedVersion:      manifest.Version,
		ObservedThroughEvent: manifest.ThroughEvent,
		Actor:                manifest.ActionActor,
		ProposedAction:       "",
		NextAssignee:         "",
		Message:              "",
		Feedback:             "",
		EvidenceRefs:         []string{},
		Evidence:             []CandidateEvidence{},
	}
}

func marshalCandidateResponse(response CandidateResponse) ([]byte, error) {
	data, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func CandidateResponseSchema() []byte {
	return []byte(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["format_version", "package_id", "task_id", "observed_version", "observed_through_event", "actor", "proposed_action", "next_assignee", "message", "feedback", "evidence_refs", "evidence"],
  "properties": {
    "format_version": {"const": "1"},
    "package_id": {"type": "string", "pattern": "^sha256:[0-9a-f]{64}$"},
    "task_id": {"type": "string", "pattern": "^[A-Za-z][A-Za-z0-9-]{0,63}$"},
    "observed_version": {"type": "integer", "minimum": 1},
    "observed_through_event": {"type": "integer", "minimum": 1},
    "actor": {"type": "string", "pattern": "^[A-Za-z][A-Za-z0-9-]{0,63}$"},
    "proposed_action": {"type": "string", "enum": ["", "assign", "accept", "submit", "request_changes", "resume", "approve", "block", "message", "evidence_add"]},
    "next_assignee": {"type": "string", "pattern": "^(|[A-Za-z][A-Za-z0-9-]{0,63})$"},
    "message": {"type": "string"},
    "feedback": {"type": "string"},
    "evidence_refs": {
      "type": "array",
      "uniqueItems": true,
      "items": {"type": "string", "pattern": "^[A-Za-z][A-Za-z0-9-]{0,63}$"}
    },
    "evidence": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["id", "kind", "summary", "file_refs"],
        "properties": {
          "id": {"type": "string", "pattern": "^[A-Za-z][A-Za-z0-9-]{0,63}$"},
          "kind": {"type": "string", "enum": ["diff", "artifact", "test", "blocker"]},
          "summary": {"type": "string", "minLength": 1},
          "file_refs": {
            "type": "array",
            "minItems": 1,
            "uniqueItems": true,
            "items": {"type": "string", "minLength": 1}
          }
        }
      }
    }
  }
}
`)
}

func DecodeCandidateResponse(data []byte) (CandidateResponse, error) {
	if err := validateCandidateResponseShape(data); err != nil {
		return CandidateResponse{}, err
	}
	var response CandidateResponse
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		return CandidateResponse{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return CandidateResponse{}, fmt.Errorf("candidate response contains multiple JSON values")
	}
	return response, nil
}

func validateCandidateResponseShape(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return fmt.Errorf("candidate response must be a JSON object")
	}
	for _, name := range []string{"format_version", "package_id", "task_id", "observed_version", "observed_through_event", "actor", "proposed_action", "next_assignee", "message", "feedback", "evidence_refs", "evidence"} {
		value, present := fields[name]
		if !present || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("candidate response is missing %s", name)
		}
	}
	return nil
}

func ValidateCandidateTemplate(manifest Manifest, response CandidateResponse) error {
	if err := manifest.Validate(); err != nil {
		return err
	}
	if !reflect.DeepEqual(response, NewCandidateResponse(manifest)) {
		return fmt.Errorf("candidate response is not the package template")
	}
	return nil
}

func ValidateCandidateResponse(manifest Manifest, response CandidateResponse) error {
	if err := manifest.Validate(); err != nil {
		return err
	}
	if response.FormatVersion != "1" || response.PackageID != manifest.PackageID || response.TaskID != manifest.TaskID || response.ObservedVersion != manifest.Version || response.ObservedThroughEvent != manifest.ThroughEvent || response.Actor != manifest.ActionActor || !protocol.IsValidID(response.Actor) {
		return fmt.Errorf("candidate response does not match package")
	}
	if response.EvidenceRefs == nil || response.Evidence == nil {
		return fmt.Errorf("candidate response arrays must be present")
	}
	if response.ProposedAction == "" || !containsAction(manifest.AllowedActions, response.ProposedAction) {
		return fmt.Errorf("candidate response action is not allowed by package")
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{"candidate message", response.Message},
		{"candidate feedback", response.Feedback},
	} {
		if err := protocol.ValidatePortableText(field.name, field.value); err != nil {
			return fmt.Errorf("candidate response contains unsafe portable content")
		}
	}
	evidenceKinds, candidateIDs, err := candidateEvidenceKinds(manifest, response.Evidence)
	if err != nil {
		return err
	}
	if err := validateEvidenceRefs(response.EvidenceRefs, evidenceKinds); err != nil {
		return err
	}

	switch response.ProposedAction {
	case protocol.Assign:
		if !protocol.IsValidID(response.NextAssignee) || hasUnexpectedPayload(response, true, false, false, false, false) {
			return fmt.Errorf("assign response requires only next_assignee")
		}
	case protocol.Accept, protocol.Resume, protocol.Approve:
		if hasUnexpectedPayload(response, false, false, false, false, false) {
			return fmt.Errorf("%s response must not carry payload", response.ProposedAction)
		}
	case protocol.Message:
		if strings.TrimSpace(response.Message) == "" || response.NextAssignee != "" || response.Feedback != "" || len(response.EvidenceRefs) != 0 || len(response.Evidence) != 0 {
			return fmt.Errorf("message response requires only message")
		}
	case protocol.AddEvidence:
		if len(response.Evidence) == 0 || response.NextAssignee != "" || response.Message != "" || response.Feedback != "" || len(response.EvidenceRefs) != 0 {
			return fmt.Errorf("evidence_add response requires only evidence")
		}
	case protocol.RequestChanges:
		if strings.TrimSpace(response.Feedback) == "" || response.NextAssignee != "" || response.Message != "" || len(response.EvidenceRefs) != 0 || len(response.Evidence) != 0 {
			return fmt.Errorf("request_changes response requires only feedback")
		}
	case protocol.Submit:
		if response.NextAssignee != "" || response.Message != "" || response.Feedback != "" || len(response.EvidenceRefs) == 0 {
			return fmt.Errorf("submit response requires evidence_refs only")
		}
		if err := requireReferencedCandidateEvidence(candidateIDs, response.EvidenceRefs); err != nil {
			return err
		}
		if !hasEvidenceKind(response.EvidenceRefs, evidenceKinds, protocol.EvidenceDiff, protocol.EvidenceArtifact) || !hasEvidenceKind(response.EvidenceRefs, evidenceKinds, protocol.EvidenceTest) {
			return fmt.Errorf("submit response requires diff or artifact and test evidence")
		}
	case protocol.Block:
		if response.NextAssignee != "" || response.Message != "" || response.Feedback != "" || len(response.EvidenceRefs) == 0 {
			return fmt.Errorf("block response requires evidence_refs only")
		}
		if err := requireReferencedCandidateEvidence(candidateIDs, response.EvidenceRefs); err != nil {
			return err
		}
		if !hasEvidenceKind(response.EvidenceRefs, evidenceKinds, protocol.EvidenceBlocker) {
			return fmt.Errorf("block response requires blocker evidence")
		}
	default:
		return fmt.Errorf("candidate response action is invalid")
	}
	return nil
}

func hasUnexpectedPayload(response CandidateResponse, allowNextAssignee, allowMessage, allowFeedback, allowRefs, allowEvidence bool) bool {
	return (!allowNextAssignee && response.NextAssignee != "") || (!allowMessage && response.Message != "") || (!allowFeedback && response.Feedback != "") || (!allowRefs && len(response.EvidenceRefs) != 0) || (!allowEvidence && len(response.Evidence) != 0)
}

func candidateEvidenceKinds(manifest Manifest, values []CandidateEvidence) (map[string]protocol.EvidenceKind, map[string]bool, error) {
	kinds := make(map[string]protocol.EvidenceKind, len(manifest.Evidence)+len(values))
	for _, evidence := range manifest.Evidence {
		kinds[evidence.ID] = evidence.Kind
	}
	candidateIDs := make(map[string]bool, len(values))
	for _, evidence := range values {
		if !protocol.IsValidID(evidence.ID) || !isEvidenceKind(evidence.Kind) || strings.TrimSpace(evidence.Summary) == "" || candidateIDs[evidence.ID] {
			return nil, nil, fmt.Errorf("candidate evidence is invalid")
		}
		if _, exists := kinds[evidence.ID]; exists {
			return nil, nil, fmt.Errorf("candidate evidence conflicts with manifest evidence")
		}
		if err := protocol.ValidatePortableText("candidate evidence summary", evidence.Summary); err != nil {
			return nil, nil, fmt.Errorf("candidate response contains unsafe portable content")
		}
		if evidence.FileRefs == nil || len(evidence.FileRefs) == 0 {
			return nil, nil, fmt.Errorf("candidate evidence requires file_refs")
		}
		seenRefs := map[string]bool{}
		for _, ref := range evidence.FileRefs {
			if seenRefs[ref] || protocol.ValidatePortableFileRef(ref) != nil {
				return nil, nil, fmt.Errorf("candidate response contains unsafe portable file reference")
			}
			seenRefs[ref] = true
		}
		candidateIDs[evidence.ID] = true
		kinds[evidence.ID] = evidence.Kind
	}
	return kinds, candidateIDs, nil
}

func validateEvidenceRefs(values []string, kinds map[string]protocol.EvidenceKind) error {
	seen := map[string]bool{}
	for _, id := range values {
		if !protocol.IsValidID(id) || seen[id] || !hasEvidenceID(kinds, id) {
			return fmt.Errorf("candidate evidence_refs are invalid")
		}
		seen[id] = true
	}
	return nil
}

func hasEvidenceID(kinds map[string]protocol.EvidenceKind, id string) bool {
	_, exists := kinds[id]
	return exists
}

func requireReferencedCandidateEvidence(candidateIDs map[string]bool, refs []string) error {
	referenced := map[string]bool{}
	for _, id := range refs {
		referenced[id] = true
	}
	for id := range candidateIDs {
		if !referenced[id] {
			return fmt.Errorf("candidate evidence must be referenced by the action")
		}
	}
	return nil
}

func hasEvidenceKind(refs []string, kinds map[string]protocol.EvidenceKind, wanted ...protocol.EvidenceKind) bool {
	for _, id := range refs {
		for _, kind := range wanted {
			if kinds[id] == kind {
				return true
			}
		}
	}
	return false
}

func ValidateResponsePackage(packageDir, inputPath string) (ResponseValidation, error) {
	manifest, err := VerifyPackage(packageDir)
	if err != nil {
		return ResponseValidation{}, err
	}
	data, err := os.ReadFile(inputPath)
	if err != nil {
		return ResponseValidation{}, err
	}
	response, err := DecodeCandidateResponse(data)
	if err != nil {
		return ResponseValidation{}, err
	}
	if err := ValidateCandidateResponse(manifest, response); err != nil {
		return ResponseValidation{}, err
	}
	return ResponseValidation{Manifest: manifest, Response: response, Steps: commandPlan(manifest, response)}, nil
}

func commandPlan(manifest Manifest, response CandidateResponse) []CommandStep {
	version := manifest.Version
	steps := make([]CommandStep, 0, len(response.Evidence)+1)
	for _, evidence := range response.Evidence {
		args := []string{"evidence", "add", "--task", manifest.TaskID, "--id", evidence.ID, "--kind", string(evidence.Kind), "--summary", evidence.Summary, "--created-by", response.Actor}
		for _, ref := range evidence.FileRefs {
			args = append(args, "--file-ref", ref)
		}
		args = append(args, "--expected-version", strconv.FormatInt(version, 10))
		steps = append(steps, CommandStep{Program: "collab", Args: args})
		version++
	}

	var args []string
	switch response.ProposedAction {
	case protocol.AddEvidence:
		return steps
	case protocol.Assign:
		args = []string{"task", "assign", "--task", manifest.TaskID, "--client", response.NextAssignee}
	case protocol.Accept:
		args = []string{"task", "accept", "--task", manifest.TaskID, "--actor", response.Actor}
	case protocol.Message:
		args = []string{"message", "add", "--task", manifest.TaskID, "--actor", response.Actor, "--body", response.Message}
	case protocol.Submit:
		args = []string{"task", "submit", "--task", manifest.TaskID, "--actor", response.Actor}
		for _, id := range response.EvidenceRefs {
			args = append(args, "--evidence", id)
		}
	case protocol.RequestChanges:
		args = []string{"review", "request-changes", "--task", manifest.TaskID, "--actor", response.Actor, "--body", response.Feedback}
	case protocol.Resume:
		args = []string{"task", "resume", "--task", manifest.TaskID, "--actor", response.Actor}
	case protocol.Approve:
		args = []string{"review", "approve", "--task", manifest.TaskID, "--actor", response.Actor}
	case protocol.Block:
		args = []string{"task", "block", "--task", manifest.TaskID, "--actor", response.Actor}
		for _, id := range response.EvidenceRefs {
			args = append(args, "--evidence", id)
		}
	}
	args = append(args, "--expected-version", strconv.FormatInt(version, 10))
	return append(steps, CommandStep{Program: "collab", Args: args})
}
