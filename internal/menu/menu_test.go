package menu

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testConfig builds a small two-level menu with one content file in a temp dir.
func testConfig(t *testing.T) *Config {
	t.Helper()
	dir := t.TempDir()
	news := "Line one.\nLine two.\n" + strings.Repeat("filler\n", 25) // > viewLines -> 2 pages
	if err := os.WriteFile(filepath.Join(dir, "news.txt"), []byte(news), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{
		Title:      "TEST SYSTEM",
		ContentDir: dir,
		Users:      []User{{Username: "GUEST", Password: "PW", Menu: "main"}},
		Menus: map[string]Menu{
			"main": {Title: "MAIN MENU", Items: []MenuItem{
				{Key: "1", Label: "News", File: "news.txt"},
				{Key: "2", Label: "More", Menu: "sub"},
			}},
			"sub": {Title: "SUB MENU", Items: []MenuItem{
				{Key: "1", Label: "News Again", File: "news.txt"},
			}},
		},
	}
	if err := cfg.validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	return cfg
}

func joined(lines []string) string { return strings.Join(lines, "\n") }

func TestLoginFlow(t *testing.T) {
	s := NewSession(testConfig(t))

	if got := joined(s.Greeting()); !strings.Contains(got, "USERID:") {
		t.Fatalf("greeting missing USERID prompt:\n%s", got)
	}
	if got := joined(s.Input("guest")); !strings.Contains(got, "PASSWORD:") {
		t.Fatalf("after userid, want PASSWORD prompt:\n%s", got)
	}
	// Wrong password returns to login.
	if got := joined(s.Input("nope")); !strings.Contains(got, "INVALID LOGON") || !strings.Contains(got, "USERID:") {
		t.Fatalf("bad password should reject and re-prompt:\n%s", got)
	}
	// Correct login (case-insensitive username) reaches the main menu.
	s.Input("GUEST")
	got := joined(s.Input("PW"))
	if !strings.Contains(got, "MAIN MENU") || !strings.Contains(got, "ENTER SELECTION") {
		t.Fatalf("good login should show main menu:\n%s", got)
	}
	if !strings.Contains(got, "WELCOME, GUEST") {
		t.Fatalf("expected welcome banner:\n%s", got)
	}
}

func login(t *testing.T, s *Session) {
	t.Helper()
	s.Greeting()
	s.Input("GUEST")
	s.Input("PW")
}

func TestSubmenuNavigation(t *testing.T) {
	s := NewSession(testConfig(t))
	login(t, s)

	// Enter submenu via item 2.
	got := joined(s.Input("2"))
	if !strings.Contains(got, "SUB MENU") || !strings.Contains(got, "B=BACK") {
		t.Fatalf("item 2 should open submenu with BACK option:\n%s", got)
	}
	// Back returns to main.
	got = joined(s.Input("B"))
	if !strings.Contains(got, "MAIN MENU") {
		t.Fatalf("B should return to main menu:\n%s", got)
	}
	// Invalid selection re-shows the menu with an error.
	got = joined(s.Input("9"))
	if !strings.Contains(got, "INVALID SELECTION") || !strings.Contains(got, "MAIN MENU") {
		t.Fatalf("invalid key should re-prompt:\n%s", got)
	}
}

func TestFilePagingAndLogoff(t *testing.T) {
	s := NewSession(testConfig(t))
	login(t, s)

	// Open the file: first page should offer MORE.
	got := joined(s.Input("1"))
	if !strings.Contains(got, "Line one.") || !strings.Contains(got, "PRESS ENTER FOR MORE") {
		t.Fatalf("file view page 1 wrong:\n%s", got)
	}
	// Enter advances to the last page (END prompt).
	got = joined(s.Input(""))
	if !strings.Contains(got, "PRESS ENTER TO RETURN") {
		t.Fatalf("file view last page should prompt to return:\n%s", got)
	}
	// Enter again returns to the menu.
	got = joined(s.Input(""))
	if !strings.Contains(got, "MAIN MENU") {
		t.Fatalf("after last page, Enter should return to menu:\n%s", got)
	}
	// Logoff returns to the login screen.
	got = joined(s.Input("X"))
	if !strings.Contains(got, "USERID:") {
		t.Fatalf("X should log off to the login screen:\n%s", got)
	}
}

// TestLoadExample loads the shipped example config so a JSON typo or a dangling
// menu/file reference fails the build's tests rather than a live session.
func TestLoadExample(t *testing.T) {
	cfg, err := Load(filepath.Join("..", "..", "examples", "menu", "app.json"))
	if err != nil {
		t.Fatalf("load example app.json: %v", err)
	}
	if len(cfg.Users) == 0 || len(cfg.Menus) == 0 {
		t.Fatalf("example config looks empty: %d users, %d menus", len(cfg.Users), len(cfg.Menus))
	}
	// Every file referenced by a menu item must exist in the content dir.
	for name, m := range cfg.Menus {
		for _, it := range m.Items {
			if it.File == "" {
				continue
			}
			if _, err := os.Stat(filepath.Join(cfg.ContentDir, it.File)); err != nil {
				t.Errorf("menu %q item %q: file %q not found: %v", name, it.Key, it.File, err)
			}
		}
	}
}

func TestValidateCatchesBadRefs(t *testing.T) {
	bad := &Config{
		Users: []User{{Username: "A", Password: "B", Menu: "nope"}},
		Menus: map[string]Menu{"main": {Title: "M"}},
	}
	if err := bad.validate(); err == nil {
		t.Fatal("expected validate to reject a user pointing at an unknown menu")
	}
}
