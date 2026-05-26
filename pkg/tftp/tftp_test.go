package tftp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jeefy/booty/pkg/config"
	"github.com/spf13/viper"
)

func TestRenderIPXETemplate(t *testing.T) {
	viper.Set(config.CoreOSChannel, "stable")
	viper.Set(config.CoreOSArchitecture, "x86_64")
	viper.Set(config.CurrentCoreOSVersion, "39.0.0")

	input := `#!ipxe
set server [[server]]
set menu-default [[menu-default]]
set channel [[coreos-channel]]
set arch [[coreos-arch]]
set version [[coreos-version]]
`
	got := renderIPXETemplate(input, "192.168.1.1:8080", "install")

	expects := map[string]string{
		"[[server]]":         "192.168.1.1:8080",
		"[[menu-default]]":   "install",
		"[[coreos-channel]]": "stable",
		"[[coreos-arch]]":    "x86_64",
		"[[coreos-version]]": "39.0.0",
	}
	for placeholder, want := range expects {
		if strings.Contains(got, placeholder) {
			t.Errorf("placeholder %q was not replaced in output", placeholder)
		}
		if !strings.Contains(got, want) {
			t.Errorf("expected %q to appear in output after replacing %q, but it did not.\nOutput: %s", want, placeholder, got)
		}
	}
}

func TestRenderIPXETemplate_MenuDefaultRunFromDisk(t *testing.T) {
	viper.Set(config.CoreOSChannel, "stable")
	viper.Set(config.CoreOSArchitecture, "x86_64")
	viper.Set(config.CurrentCoreOSVersion, "39.0.0")

	input := `set menu-default [[menu-default]]`
	got := renderIPXETemplate(input, "10.0.0.1", "run-from-disk")
	if !strings.Contains(got, "run-from-disk") {
		t.Errorf("expected 'run-from-disk' in output, got: %s", got)
	}
	if strings.Contains(got, "[[menu-default]]") {
		t.Errorf("placeholder [[menu-default]] was not replaced")
	}
}

func TestRenderIPXETemplate_NoPlaceholders(t *testing.T) {
	input := `#!ipxe\necho hello`
	got := renderIPXETemplate(input, "server", "install")
	if got != input {
		t.Errorf("content without placeholders should be unchanged; got %q", got)
	}
}

func TestSafeDataPath_Valid(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "custom.ipxe")
	if err := os.WriteFile(f, []byte("#!ipxe"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := safeDataPath(dir, "custom.ipxe")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == "" {
		t.Fatal("expected a resolved path, got empty string")
	}
}

func TestSafeDataPath_Traversal(t *testing.T) {
	dir := t.TempDir()
	_, err := safeDataPath(dir, "../escape.ipxe")
	if err == nil {
		t.Fatal("expected error for path traversal, got nil")
	}
}

func TestSafeDataPath_Absolute(t *testing.T) {
	dir := t.TempDir()
	_, err := safeDataPath(dir, "/etc/passwd")
	if err == nil {
		t.Fatal("expected error for absolute path, got nil")
	}
}

func TestSafeDataPath_NotExist(t *testing.T) {
	dir := t.TempDir()
	_, err := safeDataPath(dir, "doesnotexist.ipxe")
	if err == nil {
		t.Fatal("expected error for non-existent file, got nil")
	}
}
