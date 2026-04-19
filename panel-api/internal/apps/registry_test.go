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
	if wp.AgentInstallCmd != "app.install" {
		t.Fatalf("wordpress AgentInstallCmd = %q (want %q after M19 dispatcher rewire)", wp.AgentInstallCmd, "app.install")
	}
	if wp.AgentDeleteCmd != "app.delete" || wp.AgentCloneCmd != "app.clone" {
		t.Fatalf("wordpress agent commands = (%q, %q, %q)", wp.AgentInstallCmd, wp.AgentDeleteCmd, wp.AgentCloneCmd)
	}
	if !wp.RequiresDB {
		t.Fatal("wordpress should require a database")
	}
}

func TestRegisterDefaults_RegistersDokuWiki(t *testing.T) {
	r := New()
	if err := RegisterDefaults(r); err != nil {
		t.Fatalf("RegisterDefaults: %v", err)
	}
	dw, ok := r.Get("dokuwiki")
	if !ok {
		t.Fatal("dokuwiki not registered after RegisterDefaults")
	}
	if dw.RequiresDB {
		t.Error("dokuwiki should declare RequiresDB=false (validates the M19 short-circuit)")
	}
	if dw.AgentCloneCmd != "" {
		t.Errorf("dokuwiki should NOT declare a clone command; got %q", dw.AgentCloneCmd)
	}
	if dw.AgentInstallCmd != "app.install" || dw.AgentDeleteCmd != "app.delete" {
		t.Errorf("dokuwiki dispatcher commands = (%q, %q)", dw.AgentInstallCmd, dw.AgentDeleteCmd)
	}
	licenseSpec, ok := dw.InstallParamSchema["license"]
	if !ok {
		t.Fatal("dokuwiki install schema missing license")
	}
	if licenseSpec.Type != "enum" {
		t.Errorf("license should be enum, got %q", licenseSpec.Type)
	}
	if len(licenseSpec.Values) == 0 {
		t.Error("license enum should have values")
	}
}

func TestRegisterDefaults_RegistersMediaWiki(t *testing.T) {
	r := New()
	if err := RegisterDefaults(r); err != nil {
		t.Fatalf("RegisterDefaults: %v", err)
	}
	mw, ok := r.Get("mediawiki")
	if !ok {
		t.Fatal("mediawiki not registered after RegisterDefaults")
	}
	if !mw.RequiresDB {
		t.Error("mediawiki should declare RequiresDB=true (it needs MariaDB)")
	}
	if mw.AgentCloneCmd != "" {
		t.Errorf("mediawiki should NOT declare a clone command yet; got %q", mw.AgentCloneCmd)
	}
	if mw.AgentInstallCmd != "app.install" || mw.AgentDeleteCmd != "app.delete" {
		t.Errorf("mediawiki dispatcher commands = (%q, %q)", mw.AgentInstallCmd, mw.AgentDeleteCmd)
	}
	// admin_username deliberately NOT in the schema — auto-generated
	// server-side per the operator's directive.
	for _, want := range []string{"site_title", "admin_email", "admin_password", "language"} {
		if _, ok := mw.InstallParamSchema[want]; !ok {
			t.Errorf("mediawiki schema missing %q", want)
		}
	}
	if _, present := mw.InstallParamSchema["admin_username"]; present {
		t.Error("mediawiki schema should NOT include admin_username — it's auto-generated server-side")
	}
}

func TestRegisterDefaults_RegistersDrupal(t *testing.T) {
	r := New()
	if err := RegisterDefaults(r); err != nil {
		t.Fatalf("RegisterDefaults: %v", err)
	}
	d, ok := r.Get("drupal")
	if !ok {
		t.Fatal("drupal not registered after RegisterDefaults")
	}
	if !d.RequiresDB {
		t.Error("drupal should declare RequiresDB=true (it needs MariaDB)")
	}
	if d.AgentCloneCmd != "" {
		t.Errorf("drupal should NOT declare a clone command yet; got %q", d.AgentCloneCmd)
	}
	if d.AgentInstallCmd != "app.install" || d.AgentDeleteCmd != "app.delete" {
		t.Errorf("drupal dispatcher commands = (%q, %q)", d.AgentInstallCmd, d.AgentDeleteCmd)
	}
	for _, want := range []string{"site_title", "admin_email", "admin_password", "profile"} {
		if _, ok := d.InstallParamSchema[want]; !ok {
			t.Errorf("drupal schema missing %q", want)
		}
	}
	if _, present := d.InstallParamSchema["admin_username"]; present {
		t.Error("drupal schema should NOT include admin_username — it's auto-generated server-side")
	}
	profileSpec, ok := d.InstallParamSchema["profile"]
	if !ok {
		t.Fatal("drupal profile field missing")
	}
	if profileSpec.Type != "enum" {
		t.Errorf("drupal profile type = %q, want enum", profileSpec.Type)
	}
	if len(profileSpec.Values) == 0 {
		t.Error("drupal profile enum should have values")
	}
}

func TestRegisterDefaults_RegistersJoomla(t *testing.T) {
	r := New()
	if err := RegisterDefaults(r); err != nil {
		t.Fatalf("RegisterDefaults: %v", err)
	}
	j, ok := r.Get("joomla")
	if !ok {
		t.Fatal("joomla not registered after RegisterDefaults")
	}
	if !j.RequiresDB {
		t.Error("joomla should declare RequiresDB=true (it needs MariaDB)")
	}
	if j.AgentCloneCmd != "" {
		t.Errorf("joomla should NOT declare a clone command yet; got %q", j.AgentCloneCmd)
	}
	if j.AgentInstallCmd != "app.install" || j.AgentDeleteCmd != "app.delete" {
		t.Errorf("joomla dispatcher commands = (%q, %q)", j.AgentInstallCmd, j.AgentDeleteCmd)
	}
	for _, want := range []string{"site_title", "admin_email", "admin_password", "admin_full_name"} {
		if _, ok := j.InstallParamSchema[want]; !ok {
			t.Errorf("joomla schema missing %q", want)
		}
	}
	if _, present := j.InstallParamSchema["admin_username"]; present {
		t.Error("joomla schema should NOT include admin_username — it's auto-generated server-side")
	}
}

func TestRegisterDefaults_RegistersPhpBB(t *testing.T) {
	r := New()
	if err := RegisterDefaults(r); err != nil {
		t.Fatalf("RegisterDefaults: %v", err)
	}
	p, ok := r.Get("phpbb")
	if !ok {
		t.Fatal("phpbb not registered after RegisterDefaults")
	}
	if !p.RequiresDB {
		t.Error("phpbb should declare RequiresDB=true (it needs MariaDB)")
	}
	if p.AgentInstallCmd != "app.install" || p.AgentDeleteCmd != "app.delete" {
		t.Errorf("phpbb dispatcher commands = (%q, %q)", p.AgentInstallCmd, p.AgentDeleteCmd)
	}
	for _, want := range []string{"site_title", "admin_email", "admin_password", "language"} {
		if _, ok := p.InstallParamSchema[want]; !ok {
			t.Errorf("phpbb schema missing %q", want)
		}
	}
	if p.DefaultSubdirectory != "forum" {
		t.Errorf("phpbb default subdirectory = %q, want %q", p.DefaultSubdirectory, "forum")
	}
}

func TestRegisterDefaults_RegistersGrav(t *testing.T) {
	r := New()
	if err := RegisterDefaults(r); err != nil {
		t.Fatalf("RegisterDefaults: %v", err)
	}
	g, ok := r.Get("grav")
	if !ok {
		t.Fatal("grav not registered after RegisterDefaults")
	}
	if g.RequiresDB {
		t.Error("grav is flat-file — should declare RequiresDB=false")
	}
	if g.AgentInstallCmd != "app.install" || g.AgentDeleteCmd != "app.delete" {
		t.Errorf("grav dispatcher commands = (%q, %q)", g.AgentInstallCmd, g.AgentDeleteCmd)
	}
	for _, want := range []string{"site_title", "admin_email", "admin_password", "admin_full_name"} {
		if _, ok := g.InstallParamSchema[want]; !ok {
			t.Errorf("grav schema missing %q", want)
		}
	}
}

func TestRegisterDefaults_RegistersFreshRSS(t *testing.T) {
	r := New()
	if err := RegisterDefaults(r); err != nil {
		t.Fatalf("RegisterDefaults: %v", err)
	}
	f, ok := r.Get("freshrss")
	if !ok {
		t.Fatal("freshrss not registered after RegisterDefaults")
	}
	if !f.RequiresDB {
		t.Error("freshrss should declare RequiresDB=true (it needs MariaDB)")
	}
	if f.AgentInstallCmd != "app.install" || f.AgentDeleteCmd != "app.delete" {
		t.Errorf("freshrss dispatcher commands = (%q, %q)", f.AgentInstallCmd, f.AgentDeleteCmd)
	}
	for _, want := range []string{"admin_email", "admin_password", "language"} {
		if _, ok := f.InstallParamSchema[want]; !ok {
			t.Errorf("freshrss schema missing %q", want)
		}
	}
}

func TestRegisterDefaults_RegistersMatomo(t *testing.T) {
	r := New()
	if err := RegisterDefaults(r); err != nil {
		t.Fatalf("RegisterDefaults: %v", err)
	}
	m, ok := r.Get("matomo")
	if !ok {
		t.Fatal("matomo not registered after RegisterDefaults")
	}
	if !m.RequiresDB {
		t.Error("matomo should declare RequiresDB=true")
	}
	for _, want := range []string{"admin_email", "admin_password"} {
		if _, ok := m.InstallParamSchema[want]; !ok {
			t.Errorf("matomo schema missing %q", want)
		}
	}
}

func TestRegisterDefaults_RegistersConcrete(t *testing.T) {
	r := New()
	if err := RegisterDefaults(r); err != nil {
		t.Fatalf("RegisterDefaults: %v", err)
	}
	c, ok := r.Get("concrete")
	if !ok {
		t.Fatal("concrete not registered after RegisterDefaults")
	}
	if !c.RequiresDB {
		t.Error("concrete should declare RequiresDB=true")
	}
	for _, want := range []string{"site_title", "admin_email", "admin_password", "starting_point", "locale"} {
		if _, ok := c.InstallParamSchema[want]; !ok {
			t.Errorf("concrete schema missing %q", want)
		}
	}
	sp, ok := c.InstallParamSchema["starting_point"]
	if !ok {
		t.Fatal("starting_point missing")
	}
	if sp.Type != "enum" || len(sp.Values) == 0 {
		t.Errorf("concrete starting_point should be enum with values")
	}
}

func TestRegisterDefaults_RegistersOpenCart(t *testing.T) {
	r := New()
	if err := RegisterDefaults(r); err != nil {
		t.Fatalf("RegisterDefaults: %v", err)
	}
	o, ok := r.Get("opencart")
	if !ok {
		t.Fatal("opencart not registered after RegisterDefaults")
	}
	if !o.RequiresDB {
		t.Error("opencart should declare RequiresDB=true")
	}
	if o.DefaultSubdirectory != "shop" {
		t.Errorf("opencart default subdirectory = %q, want %q", o.DefaultSubdirectory, "shop")
	}
	for _, want := range []string{"admin_email", "admin_password"} {
		if _, ok := o.InstallParamSchema[want]; !ok {
			t.Errorf("opencart schema missing %q", want)
		}
	}
}

func TestRegisterDefaults_RegistersAbanteCart(t *testing.T) {
	r := New()
	if err := RegisterDefaults(r); err != nil {
		t.Fatalf("RegisterDefaults: %v", err)
	}
	a, ok := r.Get("abantecart")
	if !ok {
		t.Fatal("abantecart not registered after RegisterDefaults")
	}
	if !a.RequiresDB {
		t.Error("abantecart should declare RequiresDB=true")
	}
	if a.DefaultSubdirectory != "shop" {
		t.Errorf("abantecart default subdirectory = %q, want %q", a.DefaultSubdirectory, "shop")
	}
}

func TestRegisterDefaults_RegistersPrestaShop(t *testing.T) {
	r := New()
	if err := RegisterDefaults(r); err != nil {
		t.Fatalf("RegisterDefaults: %v", err)
	}
	p, ok := r.Get("prestashop")
	if !ok {
		t.Fatal("prestashop not registered after RegisterDefaults")
	}
	if !p.RequiresDB {
		t.Error("prestashop should declare RequiresDB=true")
	}
	for _, want := range []string{"site_title", "admin_email", "admin_password", "country", "language"} {
		if _, ok := p.InstallParamSchema[want]; !ok {
			t.Errorf("prestashop schema missing %q", want)
		}
	}
}

func TestRegisterDefaults_RegistersBackdrop(t *testing.T) {
	r := New()
	if err := RegisterDefaults(r); err != nil {
		t.Fatalf("RegisterDefaults: %v", err)
	}
	b, ok := r.Get("backdrop")
	if !ok {
		t.Fatal("backdrop not registered after RegisterDefaults")
	}
	if !b.RequiresDB {
		t.Error("backdrop should declare RequiresDB=true")
	}
	for _, want := range []string{"site_title", "admin_email", "admin_password", "profile"} {
		if _, ok := b.InstallParamSchema[want]; !ok {
			t.Errorf("backdrop schema missing %q", want)
		}
	}
	profile, ok := b.InstallParamSchema["profile"]
	if !ok {
		t.Fatal("backdrop profile field missing")
	}
	if profile.Type != "enum" || len(profile.Values) == 0 {
		t.Errorf("backdrop profile should be enum with values")
	}
}

func TestRegisterDefaults_RegistersMoodle(t *testing.T) {
	r := New()
	if err := RegisterDefaults(r); err != nil {
		t.Fatalf("RegisterDefaults: %v", err)
	}
	m, ok := r.Get("moodle")
	if !ok {
		t.Fatal("moodle not registered after RegisterDefaults")
	}
	if !m.RequiresDB {
		t.Error("moodle should declare RequiresDB=true")
	}
	if m.DefaultSubdirectory != "lms" {
		t.Errorf("moodle default subdirectory = %q, want %q", m.DefaultSubdirectory, "lms")
	}
	for _, want := range []string{"site_title", "admin_email", "admin_password", "language"} {
		if _, ok := m.InstallParamSchema[want]; !ok {
			t.Errorf("moodle schema missing %q", want)
		}
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
