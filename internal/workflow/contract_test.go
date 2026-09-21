package workflow

import (
	"strings"
	"testing"
)

func TestWorkflowContractAcceptedAndHashed(t *testing.T) {
	template := testTemplate("website.production", "1.1.0")
	template.Contract = &Contract{
		Inputs:           []string{"locked project authority", "source content"},
		Outputs:          []string{"validated page artifact"},
		Authority:        []string{"user instructions > project design and route authority > workflow guidance"},
		ForbiddenChanges: []string{"no guessed routes", "no replacement browser runtime"},
		Validation:       []string{"deterministic gate passes", "browser release gate passes"},
		NextSkill:        "rui-ui",
	}
	if err := validateTemplate(template); err != nil {
		t.Fatalf("valid contract rejected: %v", err)
	}
	if got := templateHash(template); !strings.HasPrefix(got, "sha256:") {
		t.Fatalf("unexpected template hash %q", got)
	}
}

func TestWorkflowContractRejectsIncompleteBoundary(t *testing.T) {
	template := testTemplate("website.production", "1.1.0")
	template.Contract = &Contract{
		Inputs:           []string{"input"},
		Outputs:          []string{"output"},
		Authority:        []string{"authority"},
		ForbiddenChanges: []string{"forbidden"},
		Validation:       nil,
		NextSkill:        "rui-ui",
	}
	if err := validateTemplate(template); err == nil || !strings.Contains(err.Error(), "validation") {
		t.Fatalf("incomplete contract accepted: %v", err)
	}
}
