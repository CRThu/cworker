//go:build windows

package process

import (
	"strings"
	"testing"
)

func TestFormatWindowsVersion(t *testing.T) {
	tests := []struct {
		name           string
		productName    string
		displayVersion string
		releaseId      string
		buildNumber    int
		want           string
	}{
		{
			name:           "Windows 10 22H2 standard",
			productName:    "Windows 10 Pro",
			displayVersion: "22H2",
			releaseId:      "2009",
			buildNumber:    19045,
			want:           "Windows 10 22H2",
		},
		{
			name:           "Windows 10 older build with ReleaseId only",
			productName:    "Windows 10 Enterprise",
			displayVersion: "",
			releaseId:      "1909",
			buildNumber:    18363,
			want:           "Windows 10 1909",
		},
		{
			name:           "Windows 11 with legacy Windows 10 ProductName",
			productName:    "Windows 10 Pro",
			displayVersion: "23H2",
			releaseId:      "2009",
			buildNumber:    22631,
			want:           "Windows 11 23H2",
		},
		{
			name:           "Windows 11 native ProductName",
			productName:    "Windows 11 Pro",
			displayVersion: "24H2",
			releaseId:      "2009",
			buildNumber:    26100,
			want:           "Windows 11 24H2",
		},
		{
			name:           "Windows Server 2022",
			productName:    "Windows Server 2022 Datacenter",
			displayVersion: "21H2",
			releaseId:      "2009",
			buildNumber:    20348,
			want:           "Windows Server 2022 21H2",
		},
		{
			name:           "Windows Server without displayVersion",
			productName:    "Windows Server 2019 Standard",
			displayVersion: "",
			releaseId:      "",
			buildNumber:    17763,
			want:           "Windows Server 2019 (Build 17763)",
		},
		{
			name:           "Fallback empty",
			productName:    "",
			displayVersion: "",
			releaseId:      "",
			buildNumber:    0,
			want:           "Windows",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatWindowsVersion(tt.productName, tt.displayVersion, tt.releaseId, tt.buildNumber)
			if got != tt.want {
				t.Errorf("FormatWindowsVersion() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGetWindowsOSVersion(t *testing.T) {
	osVer := GetWindowsOSVersion()
	if osVer == "" || !strings.Contains(osVer, "Windows") {
		t.Fatalf("expected valid Windows OS version string, got %q", osVer)
	}
}
