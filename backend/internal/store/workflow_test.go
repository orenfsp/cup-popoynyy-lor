package store

import "testing"

func TestClosureAndReturnPermissions(t *testing.T) {
	for _, role := range []string{"APPLICANT", "OPERATOR", "EXPERT", "ADMIN"} {
		want := role == "APPLICANT" || role == "OPERATOR"
		if AllowedTransition(role, "ANSWER_READY", "COMPLETED") != want {
			t.Errorf("closure permission: %s", role)
		}
		if AllowedTransition(role, "COMPLETED", "RETURNED") != want {
			t.Errorf("return permission: %s", role)
		}
		if AllowedTransition(role, "NEW", "COMPLETED") {
			t.Errorf("premature closure: %s", role)
		}
		if AllowedTransition(role, "DELETED", "IN_PROGRESS") {
			t.Errorf("deleted appeal reopened: %s", role)
		}
	}
	if AllowedTransition("APPLICANT", "ASSIGNED", "IN_PROGRESS") {
		t.Error("applicant changed workflow status")
	}
	if !AllowedTransition("EXPERT", "ASSIGNED", "ANSWER_READY") {
		t.Error("expert cannot prepare answer")
	}
	if !AllowedTransition("APPLICANT", "IN_PROGRESS", "COMPLETED") {
		t.Error("applicant cannot finish an active conversation")
	}
}
