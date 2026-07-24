package protocol

import (
	"fmt"
	"regexp"
	"time"
)

var idPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]{0,63}$`)

type Project struct {
	ID        string    `yaml:"id"`
	Name      string    `yaml:"name"`
	CreatedAt time.Time `yaml:"created_at"`
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
	Assignee   string    `yaml:"assignee"`
	Reviewer   string    `yaml:"reviewer"`
	CreatedAt  time.Time `yaml:"created_at"`
}

type References interface {
	ProjectExists(id string) bool
	ClientExists(id string) bool
}

func (p Project) Validate(expectedID string) error {
	if err := validateID("project id", p.ID, expectedID); err != nil {
		return err
	}
	if p.Name == "" {
		return fmt.Errorf("project name is required")
	}
	return validateUTCTime("project created_at", p.CreatedAt)
}

func (c Client) Validate(expectedID string) error {
	if err := validateID("client id", c.ID, expectedID); err != nil {
		return err
	}
	if c.Name == "" {
		return fmt.Errorf("client name is required")
	}
	return nil
}

func (t Task) Validate(expectedID string, refs References) error {
	if err := validateID("task id", t.ID, expectedID); err != nil {
		return err
	}
	if err := validateID("project_id", t.ProjectID, ""); err != nil {
		return err
	}
	if t.Title == "" || t.Objective == "" || len(t.Acceptance) == 0 {
		return fmt.Errorf("title, objective, and acceptance are required")
	}
	for _, criterion := range t.Acceptance {
		if criterion == "" {
			return fmt.Errorf("acceptance must not contain empty values")
		}
	}
	for field, id := range map[string]string{"creator": t.Creator, "reviewer": t.Reviewer} {
		if err := validateID(field, id, ""); err != nil {
			return err
		}
	}
	if t.Assignee != "" {
		if err := validateID("assignee", t.Assignee, ""); err != nil {
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
	for _, id := range []string{t.Creator, t.Reviewer, t.Assignee} {
		if id != "" && !refs.ClientExists(id) {
			return fmt.Errorf("unknown client %q", id)
		}
	}
	return nil
}

func validateID(field, value, expected string) error {
	if !idPattern.MatchString(value) {
		return fmt.Errorf("%s %q is invalid", field, value)
	}
	if expected != "" && value != expected {
		return fmt.Errorf("%s %q does not match file id %q", field, value, expected)
	}
	return nil
}

func validateUTCTime(field string, value time.Time) error {
	if value.IsZero() || value.Location() != time.UTC {
		return fmt.Errorf("%s must be a UTC RFC 3339 timestamp", field)
	}
	return nil
}
