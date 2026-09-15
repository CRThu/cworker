package pathutil

import (
	"errors"
	"strings"
	"testing"
)

func TestParseNodePath(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantNode string
		wantPath string
	}{
		{
			name:     "Node with Windows full path",
			input:    "node-1:D:/data/a.txt",
			wantNode: "node-1",
			wantPath: "D:/data/a.txt",
		},
		{
			name:     "Node with Windows backslash path",
			input:    `DESKTOP-PC:C:\work\output.log`,
			wantNode: "DESKTOP-PC",
			wantPath: `C:\work\output.log`,
		},
		{
			name:     "Local Windows uppercase drive letter should not be treated as node",
			input:    "D:/local/file.txt",
			wantNode: "",
			wantPath: "D:/local/file.txt",
		},
		{
			name:     "Local Windows lowercase drive letter should not be treated as node",
			input:    `c:\local\file.txt`,
			wantNode: "",
			wantPath: `c:\local\file.txt`,
		},
		{
			name:     "Local relative path without colon",
			input:    "./scripts/run.py",
			wantNode: "",
			wantPath: "./scripts/run.py",
		},
		{
			name:     "Local Git Bash path without node",
			input:    "/d/workspace/code",
			wantNode: "",
			wantPath: "/d/workspace/code",
		},
		{
			name:     "Node with Git Bash path",
			input:    "server-4090:/d/workspace/code",
			wantNode: "server-4090",
			wantPath: "/d/workspace/code",
		},
		{
			name:     "Empty input",
			input:    "   ",
			wantNode: "",
			wantPath: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotNode, gotPath := ParseNodePath(tt.input)
			if gotNode != tt.wantNode || gotPath != tt.wantPath {
				t.Errorf("ParseNodePath(%q) = (%q, %q), want (%q, %q)",
					tt.input, gotNode, gotPath, tt.wantNode, tt.wantPath)
			}
		})
	}
}

func TestNormalizeLocalPath(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		want     string
		wantErr  error
	}{
		{
			name:    "Git Bash /d/ to Windows drive",
			input:   "/d/workspace/a.txt",
			want:    `D:\workspace\a.txt`,
			wantErr: nil,
		},
		{
			name:    "Git Bash /d/project/ with trailing slash",
			input:   "/d/project/",
			want:    `D:\project`,
			wantErr: nil,
		},
		{
			name:    "Git Bash root /d/ with trailing slash",
			input:   "/d/",
			want:    `D:\`,
			wantErr: nil,
		},
		{
			name:    "Git Bash lowercase /c/ to uppercase drive",
			input:   "/c/Users/admin",
			want:    `C:\Users\admin`,
			wantErr: nil,
		},
		{
			name:    "Git Bash /c/Users/admin/ with trailing slash",
			input:   "/c/Users/admin/",
			want:    `C:\Users\admin`,
			wantErr: nil,
		},
		{
			name:    "Git Bash root /d",
			input:   "/d",
			want:    `D:\`,
			wantErr: nil,
		},
		{
			name:    "Windows forward slash",
			input:   "D:/workspace/a.txt",
			want:    `D:\workspace\a.txt`,
			wantErr: nil,
		},
		{
			name:    "Windows backslash clean",
			input:   `D:\workspace\\sub\..\a.txt`,
			want:    `D:\workspace\a.txt`,
			wantErr: nil,
		},
		{
			name:    "Windows drive root D:",
			input:   "D:",
			want:    `D:\`,
			wantErr: nil,
		},
		{
			name:    "Empty path error",
			input:   "   ",
			want:    "",
			wantErr: ErrEmptyPath,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeLocalPath(tt.input)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("NormalizeLocalPath(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeLocalPath(%q) unexpected error = %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("NormalizeLocalPath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestJoinRemotePath(t *testing.T) {
	cases := []struct {
		base     string
		rel      string
		expected string
	}{
		{"D:/folder", "sub/file.txt", "D:/folder/sub/file.txt"},
		{"D:\\folder\\", "\\sub\\file.txt", "D:/folder/sub/file.txt"},
		{"", "rel/file.txt", "rel/file.txt"},
		{"/", "rel/file.txt", "/rel/file.txt"},
		{"/var/data", "", "/var/data"},
		{"", "", ""},
		{"/", "", "/"},
		{"D:/base", "sub/file.txt", "D:/base/sub/file.txt"},
		{"D:\\base\\", "/sub/file.txt", "D:/base/sub/file.txt"},
		{"", "file.txt", "file.txt"},
		{"D:/base", "", "D:/base"},
		{"/", "file.txt", "/file.txt"},
	}

	for _, tc := range cases {
		got := JoinRemotePath(tc.base, tc.rel)
		if got != tc.expected {
			t.Errorf("JoinRemotePath(%q, %q) = %q, want %q", tc.base, tc.rel, got, tc.expected)
		}
	}
}

func TestSafeBaseName(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"D:/folder/file.txt", "file.txt"},
		{"D:\\folder\\file.txt", "file.txt"},
		{"D:/folder/sub/", "sub"},
		{"D:\\folder\\sub\\", "sub"},
		{"file.txt", "file.txt"},
		{"D:", ""},
		{"D:/", ""},
		{".", ""},
		{"..", ""},
		{"/.", ""},
		{"/..", ""},
		{"D:/..", ""},
		{"D:/.", ""},
		{"", ""},
		{"/d/project/", "project"},
		{"/d/project", "project"},
		{"/d/", ""},
		{"/d", ""},
		{"/var/log/app.log", "app.log"},
		{"app.log", "app.log"},
	}

	for _, tc := range cases {
		if got := SafeBaseName(tc.input); got != tc.expected {
			t.Errorf("SafeBaseName(%q) = %q; want %q", tc.input, got, tc.expected)
		}
	}
}

func TestGetAvailableDrives(t *testing.T) {
	drives := GetAvailableDrives()
	if len(drives) == 0 {
		t.Fatal("expected at least one drive, got 0")
	}
	foundC := false
	for _, d := range drives {
		if !strings.HasSuffix(d, ":/") {
			t.Errorf("expected drive format 'X:/', got %q", d)
		}
		if strings.EqualFold(d, "C:/") {
			foundC = true
		}
	}
	if !foundC {
		t.Logf("drives found: %v (C:/ not detected, running in non-standard environment)", drives)
	}
}
