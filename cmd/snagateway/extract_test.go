package main

import "testing"

func TestExtractInput(t *testing.T) {
	cases := []struct{ in, want string }{
		// Clean short inputs (display still fits one screen) pass through.
		{"1", "1"},
		{"GUEST", "GUEST"},
		{"", ""},
		{"  X  ", "X"},
		// Bare Enter stays empty so the file viewer advances.
		{"          ", ""},
		// Screen-echo dumps: take the token after the last prompt colon.
		{"ENTER SELECTION (X=LOGOFF):2          ", "2"},
		{"   USERID:GUEST                       ", "GUEST"},
		{"stuff   MAIN MENU   ENTER SELECTION (X=LOGOFF):3   ", "3"},
	}
	for _, c := range cases {
		if got := extractInput(c.in); got != c.want {
			t.Errorf("extractInput(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
