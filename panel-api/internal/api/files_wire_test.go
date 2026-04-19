package api

import (
	"encoding/json"
	"sort"
	"testing"
)

// TestFilesAgentParamsWireShape guards against JSON-tag drift on the
// panel↔agent boundary. The panel-agent files.* commands parse their
// params from these exact JSON keys; a rename here (or there) produces
// silent runtime validation failures that unit tests with mock agents
// do NOT catch (see memory: feedback_cross_boundary_contracts.md).
//
// If this test fails, change it AND the matching struct in
// panel-agent/internal/commands/files_*.go together. Never one without
// the other.
func TestFilesAgentParamsWireShape(t *testing.T) {
	cases := []struct {
		name    string
		payload any
		want    []string
	}{
		{
			name: "files.list",
			payload: filesListAgentParams{
				UserID:   "u",
				Username: "shuki",
				Path:     "/home/shuki",
			},
			want: []string{"path", "user_id", "username"},
		},
		{
			name: "files.read",
			payload: filesReadAgentParams{
				UserID:   "u",
				Username: "shuki",
				Path:     "/home/shuki/file.txt",
				Limit:    1000,
			},
			want: []string{"limit", "path", "user_id", "username"},
		},
		{
			name: "files.read_no_limit",
			payload: filesReadAgentParams{
				UserID:   "u",
				Username: "shuki",
				Path:     "/home/shuki/file.txt",
			},
			want: []string{"path", "user_id", "username"},
		},
		{
			name: "files.write",
			payload: filesWriteAgentParams{
				UserID:   "u",
				Username: "shuki",
				Path:     "/home/shuki/file.txt",
				Content:  "hello",
				Mode:     "",
			},
			want: []string{"content", "path", "user_id", "username"}, // mode omitted when empty
		},
		{
			name: "files.write_with_mode",
			payload: filesWriteAgentParams{
				UserID:   "u",
				Username: "shuki",
				Path:     "/home/shuki/file.txt",
				Content:  "hello",
				Mode:     "append",
			},
			want: []string{"content", "mode", "path", "user_id", "username"},
		},
		{
			name: "files.delete",
			payload: filesDeleteAgentParams{
				UserID:    "u",
				Username:  "shuki",
				Path:      "/home/shuki/file.txt",
				Recursive: false,
			},
			want: []string{"path", "user_id", "username"}, // recursive omitted when false
		},
		{
			name: "files.delete_recursive",
			payload: filesDeleteAgentParams{
				UserID:    "u",
				Username:  "shuki",
				Path:      "/home/shuki/dir",
				Recursive: true,
			},
			want: []string{"path", "recursive", "user_id", "username"},
		},
		{
			name: "files.mkdir",
			payload: filesMkdirAgentParams{
				UserID:   "u",
				Username: "shuki",
				Path:     "/home/shuki/newdir",
				Mode:     "",
			},
			want: []string{"path", "user_id", "username"}, // mode omitted when empty
		},
		{
			name: "files.mkdir_parents",
			payload: filesMkdirAgentParams{
				UserID:   "u",
				Username: "shuki",
				Path:     "/home/shuki/a/b/c",
				Mode:     "parents",
			},
			want: []string{"mode", "path", "user_id", "username"},
		},
		{
			name: "files.rename",
			payload: filesRenameAgentParams{
				UserID:   "u",
				Username: "shuki",
				OldPath:  "/home/shuki/old.txt",
				NewPath:  "/home/shuki/new.txt",
			},
			want: []string{"new_path", "old_path", "user_id", "username"},
		},
		{
			name: "files.move",
			payload: filesMoveAgentParams{
				UserID:   "u",
				Username: "shuki",
				OldPath:  "/home/shuki/dir-a/thing.txt",
				NewPath:  "/home/shuki/dir-b/thing.txt",
			},
			want: []string{"new_path", "old_path", "user_id", "username"},
		},
		{
			name: "files.chmod",
			payload: filesChmodAgentParams{
				UserID:   "u",
				Username: "shuki",
				Path:     "/home/shuki/file.txt",
				Mode:     "0644",
			},
			want: []string{"mode", "path", "user_id", "username"},
		},
		{
			name: "files.archive",
			payload: filesArchiveAgentParams{
				UserID:   "u",
				Username: "shuki",
				Paths:    []string{"/home/shuki/a.txt", "/home/shuki/b"},
			},
			want: []string{"paths", "user_id", "username"},
		},
		{
			name: "files.copy",
			payload: filesCopyAgentParams{
				UserID:   "u",
				Username: "shuki",
				SrcPath:  "/home/shuki/a.txt",
				DstPath:  "/home/shuki/dir/a.txt",
			},
			want: []string{"dst_path", "src_path", "user_id", "username"},
		},
		{
			name: "files.stat",
			payload: filesStatAgentParams{
				UserID:   "u",
				Username: "shuki",
				Path:     "/home/shuki/file.txt",
			},
			want: []string{"path", "user_id", "username"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.payload)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var m map[string]any
			if err := json.Unmarshal(b, &m); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			got := make([]string, 0, len(m))
			for k := range m {
				got = append(got, k)
			}
			sort.Strings(got)
			if len(got) != len(tc.want) {
				t.Fatalf("wrong key count: got %v want %v", got, tc.want)
			}
			for i, k := range got {
				if k != tc.want[i] {
					t.Fatalf("key[%d]: got %q want %q (full got=%v want=%v)", i, k, tc.want[i], got, tc.want)
				}
			}
		})
	}
}

// Agent param struct types are now defined in files.go (production); this
// test file only guards their JSON-tag shape against drift with panel-agent.
