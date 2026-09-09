package config

import (
	"strings"
	"testing"
)

func cronTrigger(tmpl string) *TriggerConfig {
	t := &TriggerConfig{Type: "cron", Schedule: "*/5 * * * *"}
	t.Template = tmpl
	return t
}

// Validate parses the template against a stub func map, so a function added to the templater and
// not here is rejected on save: the UI reports "invalid template" for a template that works.
func TestValidate_AcceptsEveryTemplateFunction(t *testing.T) {
	for _, tmpl := range []string{
		`{{now.Year}}`,
		`{{date now "15:04"}}`,
		`{{date now "15:04" "Pacific/Auckland"}}`,
		`{{date .Timestamp "2006-01-02"}}`,
		`{{formatPathBytes .PathHashes}}`,
	} {
		if err := cronTrigger(tmpl).Validate(); err != nil {
			t.Errorf("%s should validate: %v", tmpl, err)
		}
	}
}

func TestValidate_RejectsUnknownFunction(t *testing.T) {
	err := cronTrigger(`{{strftime now "%H:%M"}}`).Validate()
	if err == nil {
		t.Fatal("want an error for a function that does not exist")
	}
	if !strings.Contains(err.Error(), "invalid template") {
		t.Errorf("got %v", err)
	}
}
