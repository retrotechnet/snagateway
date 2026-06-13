GATEWAY README
--------------

This document is served from the Documents Library to demonstrate the
multi-level menu and the paged viewer. It is intentionally a little
long so that it spans more than one page - press ENTER to advance.

Configuration:

  The menu system is driven by a single JSON file (app.json). It
  defines users, menus, and the text files each menu item displays.
  Each user has a starting menu, so different logins can see different
  content. Menus can open submenus to any depth.

  Text files referenced by menu items live in the same directory as
  app.json (or in the directory named by "contentDir").

Running:

  snagateway sna-probe -iface ens33 -connect <MAC> -lus 2,3,4 \
      -menu examples/menu/app.json

  Each LU you activate is an independent terminal; several clients can
  be logged in at the same time, each with its own session state.

Notes:

  Lines wider than the screen are wrapped, and any document longer
  than one page is shown a page at a time. Plain text works best.

Press ENTER to return to the menu.
