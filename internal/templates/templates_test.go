package templates

import (
	"strings"
	"testing"
)

func Test_Has_when_KnownTemplate_thenReturnTrue(t *testing.T) {
	for _, name := range []string{"go.Dockerfile", "node.Dockerfile", "vue.Dockerfile", "java.Dockerfile"} {
		if !Has(name) {
			t.Errorf("Has(%q) = false, want true", name)
		}
	}
}

func Test_Has_when_UnknownTemplate_thenReturnFalse(t *testing.T) {
	if Has("nope.Dockerfile") {
		t.Error("Has(nope.Dockerfile) = true, want false")
	}
}

func Test_Get_when_KnownTemplate_thenReturnContentContainingFROM(t *testing.T) {
	for _, name := range Names() {
		b, err := Get(name)
		if err != nil {
			t.Fatalf("Get(%q) error = %v", name, err)
		}
		if !strings.Contains(string(b), "FROM") {
			t.Errorf("Get(%q) content missing FROM", name)
		}
	}
}

func Test_Get_when_UnknownTemplate_thenReturnError(t *testing.T) {
	if _, err := Get("nope.Dockerfile"); err == nil {
		t.Fatal("Get(nope.Dockerfile) error = nil, want error")
	}
}

func Test_Names_when_Called_thenReturnAllFourTemplates(t *testing.T) {
	names := Names()
	if len(names) != 4 {
		t.Fatalf("Names() = %v, want 4 entries", names)
	}
	want := map[string]bool{"go.Dockerfile": true, "java.Dockerfile": true, "node.Dockerfile": true, "vue.Dockerfile": true}
	for _, n := range names {
		if !want[n] {
			t.Errorf("unexpected name %q", n)
		}
	}
}
