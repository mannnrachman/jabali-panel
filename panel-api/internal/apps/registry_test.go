package apps

import (
	"strings"
	"sync"
	"testing"
)

func TestRegister_HappyPath(t *testing.T) {
	r := New()
	d := App{Name: "demo", DisplayName: "Demo", AgentInstallCmd: "demo.install"}
	if err := r.Register(d); err != nil {
		t.Fatalf("register: %v", err)
	}
	got, ok := r.Get("demo")
	if !ok {
		t.Fatal("Get returned !ok for registered name")
	}
	if got.DisplayName != "Demo" || got.AgentInstallCmd != "demo.install" {
		t.Fatalf("descriptor round-trip mismatch: %+v", got)
	}
}

func TestRegister_RejectsDuplicate(t *testing.T) {
	r := New()
	d := App{Name: "demo"}
	if err := r.Register(d); err != nil {
		t.Fatalf("first register: %v", err)
	}
	err := r.Register(d)
	if err == nil {
		t.Fatal("expected duplicate error, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate-named error, got: %v", err)
	}
}

func TestRegister_RejectsEmptyName(t *testing.T) {
	r := New()
	if err := r.Register(App{}); err == nil {
		t.Fatal("expected error for empty Name")
	}
}

func TestGet_UnknownReturnsFalse(t *testing.T) {
	r := New()
	if _, ok := r.Get("nope"); ok {
		t.Fatal("Get returned ok for unregistered name")
	}
}

func TestList_SortedByName(t *testing.T) {
	r := New()
	for _, n := range []string{"charlie", "alpha", "bravo"} {
		if err := r.Register(App{Name: n}); err != nil {
			t.Fatalf("register %q: %v", n, err)
		}
	}
	got := r.List()
	if len(got) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(got))
	}
	want := []string{"alpha", "bravo", "charlie"}
	for i, d := range got {
		if d.Name != want[i] {
			t.Fatalf("List[%d]=%q, want %q", i, d.Name, want[i])
		}
	}
}

func TestRegister_MutationOfReturnedListDoesNotAffectRegistry(t *testing.T) {
	r := New()
	if err := r.Register(App{Name: "demo", DisplayName: "Demo"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	list := r.List()
	list[0].DisplayName = "Mutated"
	got, _ := r.Get("demo")
	if got.DisplayName != "Demo" {
		t.Fatalf("registry mutated through List() copy: %q", got.DisplayName)
	}
}

func TestParamSpec_AcceptsAllKnownTypes(t *testing.T) {
	pattern := "^[a-z]+$"
	cases := []struct {
		name string
		spec ParamSpec
	}{
		{"string", ParamSpec{Type: "string", Required: true, Pattern: &pattern}},
		{"email", ParamSpec{Type: "email", Required: true}},
		{"password", ParamSpec{Type: "password", Required: true}},
		{"bool", ParamSpec{Type: "bool", Default: true}},
		{"enum", ParamSpec{Type: "enum", Values: []string{"a", "b"}, Default: "a"}},
	}
	r := New()
	schema := make(map[string]ParamSpec, len(cases))
	for _, c := range cases {
		schema[c.name] = c.spec
	}
	if err := r.Register(App{Name: "demo", InstallParamSchema: schema}); err != nil {
		t.Fatalf("register: %v", err)
	}
}

func TestParamSpec_RejectsUnknownType(t *testing.T) {
	r := New()
	err := r.Register(App{
		Name:               "demo",
		InstallParamSchema: map[string]ParamSpec{"bad": {Type: "ufo"}},
	})
	if err == nil {
		t.Fatal("expected error for unknown ParamSpec.Type")
	}
}

func TestParamSpec_EnumRequiresValues(t *testing.T) {
	r := New()
	err := r.Register(App{
		Name:               "demo",
		InstallParamSchema: map[string]ParamSpec{"e": {Type: "enum"}},
	})
	if err == nil {
		t.Fatal("expected error for enum without Values")
	}
}

func TestParamSpec_NonEnumRejectsValues(t *testing.T) {
	r := New()
	err := r.Register(App{
		Name:               "demo",
		InstallParamSchema: map[string]ParamSpec{"s": {Type: "string", Values: []string{"x"}}},
	})
	if err == nil {
		t.Fatal("expected error for non-enum with Values")
	}
}

func TestRegisterDefaults_RegistersWordPress(t *testing.T) {
	r := New()
	if err := RegisterDefaults(r); err != nil {
		t.Fatalf("RegisterDefaults: %v", err)
	}
	wp, ok := r.Get("wordpress")
	if !ok {
		t.Fatal("wordpress not registered after RegisterDefaults")
	}
	if wp.AgentInstallCmd != "wordpress.install" {
		t.Fatalf("wordpress AgentInstallCmd = %q", wp.AgentInstallCmd)
	}
	if !wp.RequiresDB {
		t.Fatal("wordpress should require a database")
	}
}

func TestRegister_ConcurrentSafe(t *testing.T) {
	// Race detector trips here if Register's mutex disappears.
	r := New()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = r.Register(App{Name: string(rune('a' + i))})
		}()
	}
	wg.Wait()
	if got := len(r.List()); got != 16 {
		t.Fatalf("expected 16 registrations, got %d", got)
	}
}
