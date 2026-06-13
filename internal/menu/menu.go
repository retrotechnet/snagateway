// Package menu implements a text-mode, scrolling line-mode "mainframe" menu
// system: a per-user login, multi-level menus/submenus, and a paged text-file
// viewer. It is deliberately decoupled from SNA — a Session consumes typed input
// lines and produces screens as []string, so it can be unit-tested on its own and
// driven over any transport. The gateway renders each screen over the SSCP-LU
// session (no LU-LU BIND required).
package menu

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// maxWidth is the usable display width (one column short of 80 so the applet's
// auto-wrap never adds a line). viewLines is the text-file lines shown per page.
const (
	maxWidth  = 79
	viewLines = 20
)

// Config is the whole menu system, loaded from a JSON file.
type Config struct {
	Title string          `json:"title"` // shown on the login screen
	Users []User          `json:"users"`
	Menus map[string]Menu `json:"menus"`
	// ContentDir is where text files referenced by menu items live. If empty, it
	// defaults to the directory of the config file.
	ContentDir string `json:"contentDir,omitempty"`
}

// User is one login. Passwords are stored in plaintext (fine for an isolated
// retro network; not real security). Menu is the name of this user's top menu.
type User struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Menu     string `json:"menu"`
}

// Menu is a single menu page: a title and a list of selectable items.
type Menu struct {
	Title string     `json:"title"`
	Items []MenuItem `json:"items"`
}

// MenuItem is one selectable line. Key is what the user types to choose it.
// Exactly one of File (view a text file) or Menu (open a submenu) should be set.
type MenuItem struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	File  string `json:"file,omitempty"`
	Menu  string `json:"menu,omitempty"`
}

// Load reads and validates a menu config from a JSON file. ContentDir defaults to
// the config file's directory.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("menu: parse %s: %w", path, err)
	}
	if c.ContentDir == "" {
		c.ContentDir = filepath.Dir(path)
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// validate checks that every user has a starting menu and every menu/submenu
// reference resolves, so navigation can't dead-end at runtime.
func (c *Config) validate() error {
	if len(c.Users) == 0 {
		return fmt.Errorf("menu: config has no users")
	}
	if len(c.Menus) == 0 {
		return fmt.Errorf("menu: config has no menus")
	}
	for _, u := range c.Users {
		if u.Username == "" {
			return fmt.Errorf("menu: a user has an empty username")
		}
		if _, ok := c.Menus[u.Menu]; !ok {
			return fmt.Errorf("menu: user %q references unknown menu %q", u.Username, u.Menu)
		}
	}
	for name, m := range c.Menus {
		for _, it := range m.Items {
			if it.Menu != "" {
				if _, ok := c.Menus[it.Menu]; !ok {
					return fmt.Errorf("menu: menu %q item %q references unknown submenu %q", name, it.Key, it.Menu)
				}
			}
			if it.Menu == "" && it.File == "" {
				return fmt.Errorf("menu: menu %q item %q has neither file nor menu", name, it.Key)
			}
		}
	}
	return nil
}

// findUser returns the user matching a case-insensitive username and exact
// password, or nil.
func (c *Config) findUser(username, password string) *User {
	for i := range c.Users {
		if strings.EqualFold(c.Users[i].Username, username) && c.Users[i].Password == password {
			return &c.Users[i]
		}
	}
	return nil
}

type state int

const (
	stateLoginUser state = iota // awaiting userid
	stateLoginPass              // awaiting password
	stateMenu                   // showing a menu, awaiting a selection
	stateView                   // showing a paged file, awaiting Enter
)

// Session is one terminal's walk through the menu system. It is a small state
// machine: feed it the user's typed lines and it returns the next screen.
type Session struct {
	cfg     *Config
	state   state
	userid  string   // entered userid pending its password
	user    *User    // the authenticated user (nil until login)
	stack   []string // menu-name stack; the last element is the current menu
	pages   [][]string
	pageIdx int
}

// NewSession starts a session at the login screen.
func NewSession(cfg *Config) *Session {
	return &Session{cfg: cfg, state: stateLoginUser}
}

// Greeting returns the initial login screen (also used to return to login on
// logoff). Each returned screen ends with the input prompt as its last line, so
// the terminal cursor lands right after it.
func (s *Session) Greeting() []string {
	s.state = stateLoginUser
	s.user, s.userid, s.stack = nil, "", nil
	out := []string{}
	if s.cfg.Title != "" {
		out = append(out, s.cfg.Title, "")
	}
	return append(out, "USERID:")
}

// Input processes one typed line and returns the next screen to display.
func (s *Session) Input(line string) []string {
	line = strings.TrimSpace(line)
	switch s.state {
	case stateLoginUser:
		s.userid = line
		s.state = stateLoginPass
		return []string{"PASSWORD:"}
	case stateLoginPass:
		u := s.cfg.findUser(s.userid, line)
		if u == nil {
			s.state = stateLoginUser
			s.userid = ""
			return []string{"INVALID LOGON - TRY AGAIN.", "", "USERID:"}
		}
		s.user = u
		s.stack = []string{u.Menu}
		return s.menuScreen(welcomeFor(u))
	case stateMenu:
		return s.handleMenu(line)
	case stateView:
		return s.handleView()
	}
	return s.Greeting()
}

func welcomeFor(u *User) string { return "LOGON ACCEPTED - WELCOME, " + strings.ToUpper(u.Username) }

// currentMenu is the menu at the top of the navigation stack.
func (s *Session) currentMenu() Menu { return s.cfg.Menus[s.stack[len(s.stack)-1]] }

// menuScreen renders the current menu, optionally preceded by a banner line
// (e.g. a welcome or an error). The last line is the selection prompt.
func (s *Session) menuScreen(banner string) []string {
	s.state = stateMenu
	m := s.currentMenu()
	var out []string
	if banner != "" {
		out = append(out, banner, "")
	}
	out = append(out, m.Title, "")
	for _, it := range m.Items {
		out = append(out, "  "+it.Key+".  "+it.Label)
	}
	out = append(out, "")
	if len(s.stack) > 1 {
		out = append(out, "ENTER SELECTION (B=BACK, X=LOGOFF):")
	} else {
		out = append(out, "ENTER SELECTION (X=LOGOFF):")
	}
	return out
}

// handleMenu acts on a selection: a special key (B/X), a submenu, or a file.
func (s *Session) handleMenu(line string) []string {
	key := strings.TrimSpace(line)
	switch strings.ToUpper(key) {
	case "X":
		return s.Greeting()
	case "B":
		if len(s.stack) > 1 {
			s.stack = s.stack[:len(s.stack)-1]
		}
		return s.menuScreen("")
	}
	for _, it := range s.currentMenu().Items {
		if strings.EqualFold(it.Key, key) {
			if it.Menu != "" {
				s.stack = append(s.stack, it.Menu)
				return s.menuScreen("")
			}
			return s.openFile(it.File, it.Label)
		}
	}
	return s.menuScreen("INVALID SELECTION.")
}

// openFile loads a text file, paginates it, and shows the first page.
func (s *Session) openFile(name, label string) []string {
	data, err := os.ReadFile(filepath.Join(s.cfg.ContentDir, name))
	if err != nil {
		return s.menuScreen("FILE NOT AVAILABLE: " + label)
	}
	s.pages = paginate(string(data))
	s.pageIdx = 0
	s.state = stateView
	return s.viewScreen()
}

// viewScreen renders the current file page with a footer prompting for more or
// for return to the menu.
func (s *Session) viewScreen() []string {
	out := append([]string{}, s.pages[s.pageIdx]...)
	out = append(out, "")
	if s.pageIdx < len(s.pages)-1 {
		out = append(out, fmt.Sprintf("-- PAGE %d OF %d -- PRESS ENTER FOR MORE --", s.pageIdx+1, len(s.pages)))
	} else {
		out = append(out, "-- END -- PRESS ENTER TO RETURN TO MENU --")
	}
	return out
}

// handleView advances to the next page, or returns to the menu after the last.
func (s *Session) handleView() []string {
	s.pageIdx++
	if s.pageIdx >= len(s.pages) {
		return s.menuScreen("")
	}
	return s.viewScreen()
}

// paginate normalizes line endings, expands tabs, drops non-printable bytes,
// hard-wraps lines to maxWidth, and groups the result into pages of viewLines.
func paginate(text string) [][]string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	var lines []string
	for _, raw := range strings.Split(text, "\n") {
		raw = sanitize(raw)
		for len(raw) > maxWidth {
			lines = append(lines, raw[:maxWidth])
			raw = raw[maxWidth:]
		}
		lines = append(lines, raw)
	}
	var pages [][]string
	for i := 0; i < len(lines); i += viewLines {
		end := i + viewLines
		if end > len(lines) {
			end = len(lines)
		}
		pages = append(pages, lines[i:end])
	}
	if len(pages) == 0 {
		pages = [][]string{{"(empty file)"}}
	}
	return pages
}

// sanitize expands tabs to spaces and replaces non-printable ASCII with spaces.
func sanitize(s string) string {
	s = strings.ReplaceAll(s, "\t", "    ")
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c > 0x7E {
			b = append(b, ' ')
		} else {
			b = append(b, c)
		}
	}
	return string(b)
}
