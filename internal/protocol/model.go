package protocol

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

var idPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]{0,63}$`)

type Project struct {
	ID                  string              `yaml:"id"`
	Name                string              `yaml:"name"`
	CreatedAt           time.Time           `yaml:"created_at"`
	CollaborationPolicy CollaborationPolicy `yaml:"collaboration_policy"`
	PolicyVersion       int64               `yaml:"policy_version"`
	PolicyHistory       []PolicyAuditEntry  `yaml:"policy_history,omitempty"`
}

type SubmissionMode string

const SubmissionModeAgentAuto SubmissionMode = "agent_auto"

type FinalReviewMode string

const (
	FinalReviewHuman FinalReviewMode = "human"
	FinalReviewAgent FinalReviewMode = "agent"
)

type CollaborationPolicy struct {
	SubmissionMode SubmissionMode  `yaml:"submission_mode"`
	FinalReview    FinalReviewMode `yaml:"final_review"`
	AutoDone       bool            `yaml:"auto_done"`
}

type PolicyAuditEntry struct {
	Version  int64               `yaml:"version"`
	Actor    string              `yaml:"actor"`
	Origin   EventOrigin         `yaml:"origin"`
	At       time.Time           `yaml:"at"`
	Previous CollaborationPolicy `yaml:"previous"`
	Current  CollaborationPolicy `yaml:"current"`
}

func DefaultCollaborationPolicy() CollaborationPolicy {
	return CollaborationPolicy{
		SubmissionMode: SubmissionModeAgentAuto,
		FinalReview:    FinalReviewHuman,
		AutoDone:       false,
	}
}

func (p Project) NormalizePolicy() Project {
	if p.CollaborationPolicy == (CollaborationPolicy{}) && p.PolicyVersion == 0 && len(p.PolicyHistory) == 0 {
		p.CollaborationPolicy = DefaultCollaborationPolicy()
		p.PolicyVersion = 1
	}
	return p
}

func (p CollaborationPolicy) Validate() error {
	if p.SubmissionMode != SubmissionModeAgentAuto {
		return fmt.Errorf("invalid submission_mode %q", p.SubmissionMode)
	}
	switch p.FinalReview {
	case FinalReviewHuman:
		if p.AutoDone {
			return fmt.Errorf("human final_review requires auto_done false")
		}
	case FinalReviewAgent:
		if !p.AutoDone {
			return fmt.Errorf("agent final_review requires auto_done true")
		}
	default:
		return fmt.Errorf("invalid final_review %q", p.FinalReview)
	}
	return nil
}

type Client struct {
	ID           string   `yaml:"id"`
	Name         string   `yaml:"name"`
	Capabilities []string `yaml:"capabilities"`
}

type Task struct {
	ID         string    `yaml:"id"`
	ProjectID  string    `yaml:"project_id"`
	Title      string    `yaml:"title"`
	Objective  string    `yaml:"objective"`
	Acceptance []string  `yaml:"acceptance"`
	Creator    string    `yaml:"creator"`
	Reviewer   string    `yaml:"reviewer"`
	CreatedAt  time.Time `yaml:"created_at"`
}

type References interface {
	ProjectExists(id string) bool
	ClientExists(id string) bool
	ClientHasCapability(id, capability string) bool
}

func (p Project) Validate(expectedID string) error {
	if err := validateID("project id", p.ID, expectedID); err != nil {
		return err
	}
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("project name is required")
	}
	if err := ValidatePortableText("project name", p.Name); err != nil {
		return err
	}
	if err := validateUTCTime("project created_at", p.CreatedAt); err != nil {
		return err
	}
	if p.PolicyVersion < 1 {
		return fmt.Errorf("project policy_version must be positive")
	}
	if err := p.CollaborationPolicy.Validate(); err != nil {
		return err
	}
	if int64(len(p.PolicyHistory))+1 != p.PolicyVersion {
		return fmt.Errorf("project policy history does not match policy_version")
	}
	previous := DefaultCollaborationPolicy()
	previousAt := p.CreatedAt
	for index, entry := range p.PolicyHistory {
		if entry.Version != int64(index+2) || !IsValidID(entry.Actor) || entry.Origin != EventOriginHuman || entry.Previous != previous || entry.Current.Validate() != nil || validateUTCTime("project policy audit at", entry.At) != nil || entry.At.Before(previousAt) {
			return fmt.Errorf("invalid project policy audit entry")
		}
		previous = entry.Current
		previousAt = entry.At
	}
	if len(p.PolicyHistory) > 0 && previous != p.CollaborationPolicy {
		return fmt.Errorf("project policy does not match latest audit entry")
	}
	return nil
}

func (c Client) Validate(expectedID string) error {
	if err := validateID("client id", c.ID, expectedID); err != nil {
		return err
	}
	if strings.TrimSpace(c.Name) == "" {
		return fmt.Errorf("client name is required")
	}
	if err := ValidatePortableText("client name", c.Name); err != nil {
		return err
	}
	if len(c.Capabilities) == 0 {
		return fmt.Errorf("client capabilities are required")
	}
	seen := map[string]bool{}
	for _, capability := range c.Capabilities {
		if !knownCapability(capability) || seen[capability] {
			return fmt.Errorf("invalid client capability %q", capability)
		}
		seen[capability] = true
	}
	return nil
}

func (c Client) HasCapability(capability string) bool {
	for _, candidate := range c.Capabilities {
		if candidate == capability {
			return true
		}
	}
	return false
}

func knownCapability(capability string) bool {
	switch capability {
	case "create_task", "execute", "review", "import_export":
		return true
	default:
		return false
	}
}

func (t Task) Validate(expectedID string, refs References) error {
	if err := validateID("task id", t.ID, expectedID); err != nil {
		return err
	}
	if err := validateID("project_id", t.ProjectID, ""); err != nil {
		return err
	}
	if strings.TrimSpace(t.Title) == "" || strings.TrimSpace(t.Objective) == "" || len(t.Acceptance) == 0 {
		return fmt.Errorf("title, objective, and acceptance are required")
	}
	if err := ValidatePortableText("task title", t.Title); err != nil {
		return err
	}
	if err := ValidatePortableText("task objective", t.Objective); err != nil {
		return err
	}
	for _, criterion := range t.Acceptance {
		if strings.TrimSpace(criterion) == "" {
			return fmt.Errorf("acceptance must not contain empty values")
		}
		if err := ValidatePortableText("task acceptance", criterion); err != nil {
			return err
		}
	}
	for field, id := range map[string]string{"creator": t.Creator, "reviewer": t.Reviewer} {
		if err := validateID(field, id, ""); err != nil {
			return err
		}
	}
	if err := validateUTCTime("task created_at", t.CreatedAt); err != nil {
		return err
	}
	if refs == nil {
		return nil
	}
	if !refs.ProjectExists(t.ProjectID) {
		return fmt.Errorf("unknown project_id %q", t.ProjectID)
	}
	for _, id := range []string{t.Creator, t.Reviewer} {
		if id != "" && !refs.ClientExists(id) {
			return fmt.Errorf("unknown client %q", id)
		}
	}
	if !refs.ClientHasCapability(t.Creator, "create_task") {
		return fmt.Errorf("creator %q lacks create_task capability", t.Creator)
	}
	if !refs.ClientHasCapability(t.Reviewer, "review") {
		return fmt.Errorf("reviewer %q lacks review capability", t.Reviewer)
	}
	return nil
}

func validateID(field, value, expected string) error {
	if !IsValidID(value) {
		return fmt.Errorf("%s %q is invalid", field, value)
	}
	if expected != "" && value != expected {
		return fmt.Errorf("%s %q does not match file id %q", field, value, expected)
	}
	return nil
}

func IsValidID(value string) bool {
	return idPattern.MatchString(value)
}

func validateUTCTime(field string, value time.Time) error {
	if value.IsZero() || value.Location() != time.UTC {
		return fmt.Errorf("%s must be a UTC RFC 3339 timestamp", field)
	}
	return nil
}
