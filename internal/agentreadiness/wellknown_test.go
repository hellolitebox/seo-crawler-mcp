package agentreadiness

import "testing"

func TestBodyHasFieldsRequiresJSONAndFields(t *testing.T) {
	if !bodyHasFields(`{"linkset":[]}`, []string{"linkset"}) {
		t.Fatal("expected linkset body to pass")
	}
	if bodyHasFields(`not json`, []string{"linkset"}) {
		t.Fatal("expected non-json body to fail")
	}
	if bodyHasFields(`{"ok":true}`, []string{"linkset"}) {
		t.Fatal("expected missing field to fail")
	}
}
