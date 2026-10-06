package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMCPInstallBacksUpAndWritesThroughSymlink(t *testing.T) {
	dir, _ := agentTestEnv(t)
	real := filepath.Join(dir, "dotfiles", "mcp.json")
	orig := `{"mcpServers": {"other": {"command": "x"}}}`
	agentWrite(t, real, orig)
	link := filepath.Join(dir, ".mcp.json")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("symlinks unavailable:", err)
	}

	rs, errOut, code := agentRun(t, "mcp", "install", "--client", "claude", "--dir", dir)
	if code != 0 || rs[0].Action != "updated" {
		t.Fatalf("exit %d %+v: %s", code, rs, errOut)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("symlink was replaced: %v %v", fi, err)
	}
	servers := agentReadJSON(t, real)["mcpServers"].(map[string]any)
	if _, ok := servers["spicrawl"]; !ok || servers["other"] == nil {
		t.Errorf("target not merged: %v", servers)
	}
	if bak, _ := os.ReadFile(real + ".bak"); string(bak) != orig {
		t.Errorf("backup = %q, want the original", bak)
	}
	if rs[0].Backup != real+".bak" {
		t.Errorf("Backup = %q", rs[0].Backup)
	}
}

func TestMCPInstallRefusesDifferentEntryUnlessForce(t *testing.T) {
	dir, _ := agentTestEnv(t)
	path := filepath.Join(dir, ".mcp.json")
	orig := `{"mcpServers": {"spicrawl": {"type": "http", "url": "https://mine.example/mcp", "headers": {"Authorization": "Bearer sk-secret"}}}}`
	agentWrite(t, path, orig)

	_, errOut, code := agentRun(t, "mcp", "install", "--client", "claude", "--dir", dir)
	if code != 2 || !strings.Contains(errOut, "--force") || !strings.Contains(errOut, "mine.example") {
		t.Fatalf("exit %d, want refusal with a diff: %s", code, errOut)
	}
	if strings.Contains(errOut, "sk-secret") {
		t.Error("diff leaked the literal key")
	}
	if b, _ := os.ReadFile(path); string(b) != orig {
		t.Error("file changed without --force")
	}

	if _, errOut, code = agentRun(t, "mcp", "install", "--client", "claude", "--dir", dir, "--force"); code != 0 {
		t.Fatalf("--force: exit %d: %s", code, errOut)
	}
	if got := agentReadJSON(t, path)["mcpServers"].(map[string]any)["spicrawl"].(map[string]any)["url"]; got != agentTestMCPURL {
		t.Errorf("url = %v", got)
	}
	if bak, _ := os.ReadFile(path + ".bak"); string(bak) != orig {
		t.Errorf("backup = %q", bak)
	}
}

func TestMCPInstallAcceptsJSONCAndBOM(t *testing.T) {
	dir, _ := agentTestEnv(t)
	path := filepath.Join(dir, ".vscode", "mcp.json")
	// A BOM, both comment styles, a trailing comma, and "//" inside a string.
	orig := "\xef\xbb\xbf{\n  // my servers\n  \"servers\": {\n    \"other\": {\"url\": \"http://x//y\", /* inline */ },\n  },\n}\n"
	agentWrite(t, path, orig)

	rs, errOut, code := agentRun(t, "mcp", "install", "--client", "vscode", "--dir", dir)
	if code != 0 || rs[0].Action != "updated" {
		t.Fatalf("exit %d %+v: %s", code, rs, errOut)
	}
	if !strings.Contains(errOut, "comments") {
		t.Errorf("no warning about dropped comments: %s", errOut)
	}
	servers := agentReadJSON(t, path)["servers"].(map[string]any)
	if servers["spicrawl"] == nil || servers["other"].(map[string]any)["url"] != "http://x//y" {
		t.Errorf("servers = %v", servers)
	}
	if bak, _ := os.ReadFile(path + ".bak"); string(bak) != orig {
		t.Error("backup does not hold the original, comments included")
	}

	// A BOM alone is not lossy: no warning, and a second run is a no-op.
	bom := filepath.Join(dir, "b", ".vscode", "mcp.json")
	agentWrite(t, bom, "\xef\xbb\xbf{\"servers\": {}}")
	if _, errOut, code = agentRun(t, "mcp", "install", "--client", "vscode", "--dir", filepath.Join(dir, "b")); code != 0 || strings.Contains(errOut, "comments") {
		t.Errorf("BOM only: exit %d: %s", code, errOut)
	}
}
