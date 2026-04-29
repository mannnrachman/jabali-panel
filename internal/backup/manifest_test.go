package backup

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestAccountManifest_Roundtrip(t *testing.T) {
	in := AccountManifest{
		SchemaVersion: ManifestSchemaVersion,
		Kind:          KindAccountBackup,
		JobID:         "01J5JOB000",
		CreatedAt:     time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC),
		Source: ManifestSource{
			Hostname: "mx.example.com",
			PanelSHA: "abc123",
		},
		User: ManifestUser{
			ID: "01J5USER", Username: "alice", Email: "alice@example.com",
		},
		Restic: ManifestRestic{SnapshotID: "snap1", BytesAdded: 1234, BytesTotal: 9999},
		Stages: []ManifestStage{
			{Name: StageHome, Status: StageStatusOK, Tag: "stage=home", SnapshotID: "snapH"},
			{Name: StageMail, Status: StageStatusSkipped, Warnings: []string{"stalwart_down"}},
		},
	}
	b, err := in.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}
	out, err := AccountManifestFromBytes(b)
	if err != nil {
		t.Fatalf("FromBytes: %v", err)
	}
	if out.JobID != in.JobID || out.User.Username != "alice" {
		t.Fatalf("roundtrip lost data: %+v", out)
	}
	if out.Stages[0].Name != StageHome || out.Stages[1].Status != StageStatusSkipped {
		t.Fatalf("stage roundtrip: %+v", out.Stages)
	}
}

func TestAccountManifest_RefusesUnknownSchema(t *testing.T) {
	bad := []byte(`{"schema_version":99,"kind":"account_backup"}`)
	_, err := AccountManifestFromBytes(bad)
	if !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatalf("want ErrUnsupportedSchema, got %v", err)
	}
}

func TestAccountManifest_RefusesWrongKind(t *testing.T) {
	bad := []byte(`{"schema_version":1,"kind":"system_backup"}`)
	_, err := AccountManifestFromBytes(bad)
	if !errors.Is(err, ErrManifestMalformed) {
		t.Fatalf("want ErrManifestMalformed, got %v", err)
	}
}

func TestAccountManifest_DefaultsOnEmptyToBytes(t *testing.T) {
	m := AccountManifest{JobID: "01J5"}
	b, err := m.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}
	if !strings.Contains(string(b), `"schema_version": 1`) {
		t.Fatalf("schema_version not auto-stamped: %s", b)
	}
	if !strings.Contains(string(b), `"kind": "account_backup"`) {
		t.Fatalf("kind not auto-stamped: %s", b)
	}
}

func TestSystemManifest_Roundtrip(t *testing.T) {
	in := SystemManifest{
		SchemaVersion: 1,
		Kind:          KindSystemBackup,
		JobID:         "01J5SYS",
		CreatedAt:     time.Now().UTC(),
		Source: ManifestSource{
			Hostname: "host", PanelSHA: "deadbeef",
		},
		Stages: []ManifestStage{
			{Name: StagePanelDB, Status: StageStatusOK},
		},
		LinkedAccountJobs: []string{"01J5A", "01J5B"},
	}
	b, err := in.ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	out, err := SystemManifestFromBytes(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.LinkedAccountJobs) != 2 {
		t.Fatalf("linked_account_jobs lost: %+v", out)
	}
}

func TestAccountTags_StableOrder(t *testing.T) {
	tags := AccountBackupTags("01J5JOB", "01J5USR", "", StageHome)
	if string(tags[0]) != "jabali" {
		t.Fatalf("blanket tag must come first; got %v", tags)
	}
	got := strings.Join(ToStrings(tags), " ")
	want := "jabali kind=account_backup job-id=01J5JOB user-id=01J5USR stage=home"
	if got != want {
		t.Fatalf("tag order drift\nwant: %s\n got: %s", want, got)
	}
}

func TestSystemTags_HostScoped(t *testing.T) {
	tags := SystemBackupTags("01J5JOB", "host.example.com", "", StagePanelDB)
	got := strings.Join(ToStrings(tags), " ")
	if !strings.Contains(got, "system=host.example.com") {
		t.Fatalf("missing system tag: %s", got)
	}
	if !strings.Contains(got, "kind=system_backup") {
		t.Fatalf("wrong kind: %s", got)
	}
}

func TestManifest_GoldenJSONShape(t *testing.T) {
	m := AccountManifest{
		SchemaVersion: 1,
		Kind:          KindAccountBackup,
		JobID:         "01J",
		CreatedAt:     time.Date(2026, 4, 28, 0, 0, 0, 0, time.UTC),
		Source:        ManifestSource{Hostname: "h"},
		User:          ManifestUser{ID: "u", Username: "u"},
		Restic:        ManifestRestic{},
		Stages:        []ManifestStage{{Name: StageHome, Status: StageStatusOK}},
	}
	b, _ := json.Marshal(m)
	for _, key := range []string{
		`"schema_version":1`,
		`"kind":"account_backup"`,
		`"job_id":"01J"`,
		`"created_at":"2026-04-28T00:00:00Z"`,
		`"stages":[{"name":"home","status":"ok"`,
	} {
		if !strings.Contains(string(b), key) {
			t.Fatalf("missing %s in:\n%s", key, b)
		}
	}
}
