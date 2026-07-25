package handoff

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
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
	Message              string              `json:"message"`
	Feedback             string              `json:"feedback"`
	Evidence             []CandidateEvidence `json:"evidence"`
}

type ResponseValidation struct {
	Manifest     Manifest
	Response     CandidateResponse
	CommandDraft string
}

func NewCandidateResponse(manifest Manifest) CandidateResponse {
	return CandidateResponse{
		FormatVersion:        "1",
		PackageID:            manifest.PackageID,
		TaskID:               manifest.TaskID,
		ObservedVersion:      manifest.Version,
		ObservedThroughEvent: manifest.ThroughEvent,
		Actor:                manifest.ActionActor,
		ProposedAction:       preferredAction(manifest.AllowedActions),
		Evidence:             []CandidateEvidence{},
	}
}

func CandidateResponseSchema() []byte {
	return []byte(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["format_version", "package_id", "task_id", "observed_version", "observed_through_event", "actor", "proposed_action", "message", "feedback", "evidence"],
  "properties": {
    "format_version": {"const": "1"},
    "package_id": {"type": "string"},
    "task_id": {"type": "string"},
    "observed_version": {"type": "integer", "minimum": 1},
    "observed_through_event": {"type": "integer", "minimum": 1},
    "actor": {"type": "string"},
    "proposed_action": {"type": "string"},
    "message": {"type": "string"},
    "feedback": {"type": "string"},
    "evidence": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["id", "kind", "summary", "file_refs"],
        "properties": {
          "id": {"type": "string"},
          "kind": {"type": "string"},
          "summary": {"type": "string"},
          "file_refs": {"type": "array", "items": {"type": "string"}}
        }
      }
    }
  }
}
`)
}

func DecodeCandidateResponse(data []byte) (CandidateResponse, error) {
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

func ValidateCandidateResponse(manifest Manifest, response CandidateResponse) error {
	if err := manifest.Validate(); err != nil {
		return err
	}
	if response.FormatVersion != "1" || response.PackageID != manifest.PackageID || response.TaskID != manifest.TaskID || response.ObservedVersion != manifest.Version || response.ObservedThroughEvent != manifest.ThroughEvent || response.Actor != manifest.ActionActor || !protocol.IsValidID(response.Actor) {
		return fmt.Errorf("candidate response does not match package")
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
	if len(manifest.AllowedActions) == 0 && response.ProposedAction == "" {
		return validateCandidateEvidence(response.Evidence)
	}
	if !containsAction(manifest.AllowedActions, response.ProposedAction) {
		return fmt.Errorf("candidate response action is not allowed by package")
	}
	return validateCandidateEvidence(response.Evidence)
}

func validateCandidateEvidence(values []CandidateEvidence) error {
	seen := map[string]bool{}
	for _, evidence := range values {
		if !protocol.IsValidID(evidence.ID) || !isEvidenceKind(evidence.Kind) || strings.TrimSpace(evidence.Summary) == "" || seen[evidence.ID] {
			return fmt.Errorf("candidate evidence is invalid")
		}
		seen[evidence.ID] = true
		if err := protocol.ValidatePortableText("candidate evidence summary", evidence.Summary); err != nil {
			return fmt.Errorf("candidate response contains unsafe portable content")
		}
		for _, ref := range evidence.FileRefs {
			if err := protocol.ValidatePortableFileRef(ref); err != nil {
				return fmt.Errorf("candidate response contains unsafe portable file reference")
			}
		}
	}
	return nil
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
	return ResponseValidation{Manifest: manifest, Response: response, CommandDraft: commandForAction(manifest.TaskID, manifest.Version, manifest.ActionActor, response.ProposedAction)}, nil
}

func preferredAction(actions []protocol.Action) protocol.Action {
	for _, wanted := range []protocol.Action{protocol.Submit, protocol.Approve, protocol.RequestChanges, protocol.Accept, protocol.Resume, protocol.Assign, protocol.Block, protocol.Message, protocol.AddEvidence} {
		if containsAction(actions, wanted) {
			return wanted
		}
	}
	return ""
}

func commandForAction(taskID string, version int64, actor string, action protocol.Action) string {
	switch action {
	case protocol.Assign:
		return fmt.Sprintf("collab task assign --task %s --client <executor-client> --expected-version %d", taskID, version)
	case protocol.Accept:
		return fmt.Sprintf("collab task accept --task %s --actor %s --expected-version %d", taskID, actor, version)
	case protocol.Message:
		return fmt.Sprintf("collab message add --task %s --actor %s --body <message> --expected-version %d", taskID, actor, version)
	case protocol.AddEvidence:
		return fmt.Sprintf("collab evidence add --task %s --id <evidence-id> --kind <diff|artifact|test|blocker> --summary <summary> --created-by %s --file-ref <project-relative-path> --expected-version %d", taskID, actor, version)
	case protocol.Submit:
		return fmt.Sprintf("collab task submit --task %s --actor %s --evidence <diff-or-artifact-id> --evidence <test-id> --expected-version %d", taskID, actor, version)
	case protocol.RequestChanges:
		return fmt.Sprintf("collab review request-changes --task %s --actor %s --body <feedback> --expected-version %d", taskID, actor, version)
	case protocol.Resume:
		return fmt.Sprintf("collab task resume --task %s --actor %s --expected-version %d", taskID, actor, version)
	case protocol.Approve:
		return fmt.Sprintf("collab review approve --task %s --actor %s --expected-version %d", taskID, actor, version)
	case protocol.Block:
		return fmt.Sprintf("collab task block --task %s --actor %s --evidence <blocker-evidence-id> --expected-version %d", taskID, actor, version)
	default:
		return ""
	}
}
