# Browser Tests

The audit opens Markspace in headless Chromium and measures the rendered layout of every page and interactive state. It reports layout defects that unit tests and screenshot reviews miss. Examples are a rail panel that repeats its tab title, a scroll container that hides its last button, an overlay with too much padding, a New page button shown twice, a button the colour of its surface, and a transparent layer that takes the clicks.

## Run It

From the repository root:

```
make layout
```

The target builds the server into a new temporary directory. It starts the server on a free port with empty data, starts the fake AI server and the Word count plugin on free ports, and seeds data through the HTTP API. It then audits every state and writes `test/browser/report.md` and `test/browser/report.json`. The exit status is 0 only when the audit finds nothing and reaches every state.

The last lines of the output name the report and the temporary directory. The temporary directory holds the screenshots, the server logs, `seed.json` and `timings.json`.

`LAYOUT_ARGS` passes options to `layout.mjs`. Make exports it, and `layout.mjs` reads it from the environment, so parentheses and `|` in a regex need no quoting. Options are separated by spaces:

| Option | Effect |
| --- | --- |
| `--profiles=REGEX` | Audit only the viewports whose name matches, such as `^390x844`. Make expands `$`, so write `$$` for an end anchor |
| `--themes=light` | Audit only the named themes. The default is `light,dark` |
| `--states=REGEX` | Audit only the states whose id matches, such as `^rail-` |
| `--workers=N` | Number of browser contexts in parallel. The default is 4 |
| `--report-dir=DIR` | Write the reports to DIR instead of `test/browser` |
| `--copy=DIR` | Copy the temporary directory, without the data, to DIR |
| `--base=URL --seed-file=FILE` | Audit a running server that an earlier run seeded |
| `--verbose` | Print one line per state with its duration |
| `--merge=DIR,DIR` | Build one report from the `raw.json` files of several runs, for a run split by `--profiles` |
| `--state-shots=1` | Save a viewport screenshot of every state in `state-shots/` in the temporary directory |
| `--clicks=0` | Skip the click pass |
| `--dismiss=0` | Skip the overlay dismissal pass |
| `--click-profiles=LIST`, `--dismiss-profiles=LIST` | Profiles for the two passes. The defaults are `1440x900,1024x768,390x844` and `1440x900,1024x768,390x844,844x390` |

For example, `make layout LAYOUT_ARGS="--profiles=1440 --themes=light --states=rail"` audits the rail tabs at 1440 × 900 in the light theme.

Make loses every `$` in `make layout LAYOUT_ARGS='...'`. Make reads `$` as the start of a variable reference before the value reaches the shell, and single quotes do not stop it. `--profiles=^1440x900$` reaches `layout.mjs` as `--profiles=^1440x900`, which also matches any longer profile name. Use one of these forms instead:

- Write `$$` for each `$`, as in `make layout LAYOUT_ARGS='--profiles=^1440x900$$'`.
- Leave out the `$` anchor when no other profile name starts with the same text.
- Call the runner directly, as in `node test/browser/run.mjs --bin=./markspace --profiles='^1440x900$'`. The shell passes the quoted `$` through.

## Fixtures

```
make fixtures
```

`make fixtures` serves the pages in `fixtures/` from a local HTTP server and audits each one at 1280 × 720. Each page contains one known defect and names its expected findings in a `<meta name="audit-expect">` tag. It fails when a page produces any other set of findings. `clean.html` holds correct markup and must produce no findings.

A page can name a selector in `<meta name="audit-open">`. `make fixtures` clicks it before the audit. `<meta name="audit-passes" content="overlays,clicks">` runs the overlay dismissal pass and the click pass on the page.

| Fixture | Defect | Expected findings |
| --- | --- | --- |
| `duplicate-title.html` | The Comments panel has an h2 "Comments" | 1 duplicate-title |
| `scroll-cutoff.html` | A fixed pane ends 40px below the window | 2 cannot-scroll-into-view, 6 focus-obscured |
| `misaligned-banner.html` | The banner text sits 6px below the icon and the action | 3 single-line-centres |
| `overpadded-overlay.html` | 16px body padding on top of the ActionList insets | 4 overlay-text-inset-left, 4 overlay-text-inset-top |
| `menu-left-inset.html` | Menu items without an icon start their text 32px in | 4 overlay-text-inset-left |
| `yellow.html` | An amber callout | 7 yellow-or-amber |
| `missing-stylesheet.html` | A stylesheet link that answers 404 | 6 failed-static-asset |
| `duplicate-action.html` | New page in the header and again in the page header | 1 duplicate-action |
| `dead-click.html` | A transparent fixed layer over a button | 2 dead-click, 6 focus-obscured |
| `no-click-effect.html` | A button without a handler | 2 no-click-effect |
| `control-contrast.html` | An Edit button with the fill of its surface, and a field border of 1.4:1 | 7 control-contrast twice |
| `old-token.html` | A colour read from `--text-muted` | 7 old-token |
| `unexplained-indent.html` | Level 1 tree rows 16px right of the NavList | 8 unexplained-indent |
| `metadata-prominence.html` | A comment author in 14px weight 600 | 8 metadata-prominence |
| `inconsistent-spacing.html` | One Box row with 24px padding among rows with 16px | 8 inconsistent-spacing |
| `overlay-escape.html` | A dialog that ignores Escape | 9 overlay-escape |
| `overlay-focus-lost.html` | Focus moves to body after the dialog closes | 9 overlay-focus-return twice |
| `overlay-no-close.html` | A dialog without a Close button | 9 overlay-close-control |
| `dialog-title-tab.html` | A Dialog titled "Comments" above its selected Comments tab | 1 duplicate-title |
| `word-broken.html` | A page title squeezed by 3 actions until a word wraps | 5 word-broken |
| `mixed-leading-visuals.html` | A menu where 2 items have icons and 2 do not | 8 mixed-leading-visuals |
| `clean.html` | None | None |

## Requirements

- Node 20 or later.
- `playwright-core` 1.63. `lib/pw.mjs` looks in `PLAYWRIGHT_CORE`, then in `test/browser/node_modules`, then in every `~/.npm/_npx/<hash>/node_modules` that holds version 1.63.x. `PLAYWRIGHT_CORE` may name a `node_modules` directory or the package directory. Without one, the audit stops and prints the install command `npm install --prefix test/browser playwright-core@1.63.0`.
- A Chromium build in `~/.cache/ms-playwright`, or in `PLAYWRIGHT_BROWSERS_PATH`. The newest `chromium-NNNN` directory wins.
- `python3` for the fake AI server and the plugin.
- The fake OpenAI-compatible server `fakeai.py` in this directory, standard library only. It serves OpenRouter-style models with prices, plain and streamed chat with the usage frame, embeddings and a small PNG for image requests. `FAKEAI_SERVER` may name another script that takes the port as its last argument.
- `sqlite3` and `ffmpeg`, both optional. `sqlite3` adds 30 days of AI usage rows and a paused and a failed job. `ffmpeg` makes the seed video, and without it the video state is skipped.

The audit never uses port 8080 or the `data` directory of the repository.

## Files

| File | Content |
| --- | --- |
| `layout.mjs` | Starts the stack, seeds, runs the states in 4 browser contexts and writes the report |
| `flows.mjs` | Drives regression flows and asserts their outcome. The flows cover one New page, root tree rows, the Avatar sync badge, comment metadata, the history Dialog, admin clicks with the sidebar hidden, the undo Banner, the Edit button contrast, and Find at 320 × 568 on a touch phone. `--base` and `--seed-file` run it against a seeded server, and `--only=9` runs only the Find checks |
| `history.mjs` | Checks the version history rail tab and Dialog at 1440 × 900 and 390 × 844 in both themes. `node test/browser/history.mjs --bin=<markspace binary>` runs it |
| `fakeai.py` | The fake OpenAI-compatible server. `python3 fakeai.py <port>` |
| `fixtures.mjs` | Runs the fixtures |
| `lib/probe.js` | The in-page checks. The runner evaluates it in each page |
| `lib/audit.mjs` | Runs the probe, the keyboard focus walk, console and network checks, screenshots, the click pass and the overlay dismissal pass |
| `lib/states.mjs` | Viewports, themes and the list of states |
| `lib/seed.mjs` | Seed data through the HTTP API |
| `lib/history.mjs` | Seeds a note with a version history spread over several days |
| `lib/media.mjs` | Generates the PNG, PDF, CSV and video files for the seed |
| `lib/stack.mjs`, `lib/procs.mjs` | Free ports and the server, fake AI and plugin processes |
| `lib/report.mjs` | Owner lookup, severity, `report.json` and `report.md` |
| `spec.json` | Expected Primer padding, insets and alignment containers, and the selectors and limits of checks 1, 2, 7, 8 and 9, each with its `docs/primer.md` row |
| `allow.json` | Allowed exceptions |
| `fixtures/` | Self-test pages |

## Seed Data

The seed creates the following through the HTTP API:

- An admin account (ada@example.com) and a member account (grace@example.com), plus 4 people who only receive shares.
- 2 spaces.
- A folder tree 4 levels deep with 40 items. The items are notes, PNG images, an 8-page PDF, a CSV file, a JSON file, text files and an MP4 video.
- A field manual note of about 3000 words with images, tables, code blocks, wikilinks and task lists.
- The note "Harbour survey" with 5 people shares, 3 public links, 25 comment threads and 30 versions. 10 threads are anchored, 5 are resolved and 9 have replies.
- 3 open suggestions on that note, made through `POST /api/notes/{id}/suggestions` with anchors built as `docs/comments.md` describes.
- A fake AI provider, 3 AI calls through the fake server, and 30 days of AI usage rows.
- The Word count example plugin, installed through `POST /api/admin/plugins`, and its jobs.
- 3 trashed items, 2 API tokens, an invite, starred notes and an empty note.
- One sandbox folder per worker. Each holds the notes that the move, trash, remove share, resolve and editor states change. Each such state undoes its change afterwards.

## Viewports and Themes

| Profile | Viewport | Device |
| --- | --- | --- |
| `1440x900` | 1440 × 900 | Desktop |
| `1280x720` | 1280 × 720 | Desktop |
| `1024x768` | 1024 × 768 | Desktop |
| `390x844` | 390 × 844 | Phone, touch, scale 2 |
| `844x390` | 844 × 390 | Phone, touch, scale 2 |
| `320x256` | 320 × 256 | Phone, touch, scale 2 |
| `720x450@2x` | 720 × 450 | 200% zoom, scale 2 |

Every profile runs in the light and the dark theme. The 200% zoom profile runs every page in the light theme, and a subset of the interactive states. The server versions of shell routes run in both themes on 1440 × 900 and 390 × 844, and in the light theme elsewhere.

Editor states run a second time with an emulated on-screen keyboard. The viewport loses 336px of height on 390 × 844 and 200px on 844 × 390.

The keyboard focus walk runs in the light theme on 1440 × 900, 1024 × 768, 390 × 844 and 720 × 450. It presses Tab up to 30 times on each page and dialog state.

## States

The offline client renders the shell routes with its service worker. The audit visits home, space, folder, a folder 4 levels deep, the files page, file views for a PDF, an image, a CSV file, a JSON file and a video, a note, the long note, an empty note, the editor in Live, Source and Reading, search, starred, recent, shared, tag and trash.

The server pages are spaces, space settings, the settings pages, the admin pages, the plugins page, jobs, a plugin page, a 404 error page and the offline page.

Signed out, the audit visits sign-in, sign-up, public links to a note, a long note, a password-protected note, a folder, an item inside the folder and a file, and sign-in and sign-up with validation errors.

Without the service worker, the server renders its own versions of the shell routes. The audit visits space, note, editor, search, trash, starred and tag.

Interactive states:

- The rail tabs Outline, Comments, Suggestions, History, Share, Files, AI and Info, and the Word count plugin panel. Share also runs with "Add public link" open.
- The page actions menu, the account menu, the page tree context menu, a folder row menu, the file view menu and a trash row menu.
- The "Delete forever" confirmation dialog, the navigation drawer on phones, the sync status overlay, the quick switcher, and the move and copy dialogs.
- The Undo banners, reached through the actions a person takes. A move through the folder row menu and the Move dialog. A drag of a note onto a folder in the page tree, from 1400px wide, where the sidebar and the rail show together. Move to trash from the page actions menu. The undo rows after removing a share and after resolving a comment.
- The full-screen version history Dialog.
- Validation errors on the new token form.
- The shell while offline.
- PDF thumbnails, which open a dialog on phones and a pane on wider screens.
- In the editor, the slash menu, link completion, the ask bar with an answer, the ask bar with tracked changes, the image actions, the image editor, and the find bar with matches for "tide" (`editor-find`).
- Below 768px, the note view's Reading find bar, opened from "Find in note" in the PageHeader More actions menu (`note-find`).

Layout states run in the light theme on 1440 × 900, 1024 × 768, 390 × 844 and 844 × 390. They open space, the PDF file view, admin, admin users, admin settings and settings with the sidebar hidden and shown from 768px, and with the navigation drawer open and closed below 768px. The note runs with the sidebar hidden and shown, each with the rail open and closed, with the rail and the side sheet open between 768px and 1399px, and below 768px with the drawer open and closed and the rail Dialog open. Each layout state stores its sidebar and rail choice in `localStorage` first and clears it afterwards.

## Passes

Two passes act on the page after the probe. Their findings join the report like any other.

The overlay dismissal pass runs in the light theme on the profiles in `--dismiss-profiles`, on every interactive and layout state. It finds the top open overlay, which is a dialog, a popover, a menu, an AnchoredOverlay or the editor autocomplete. It tests each overlay once per profile.

1. It presses Escape. The overlay must close (`overlay-escape`).
2. Focus must then sit on a visible element outside the overlay, never on body (`overlay-focus-return`).
3. It opens the state again. A dialog, and a menu below 768px, must have a visible Close button (`overlay-close-control`). A confirmation dialog may use its Cancel button. The pass clicks it, and the overlay must close with the same focus rule.

The click pass runs on layout states in the light theme on the profiles in `--click-profiles`. It collects the visible controls, at most 2 per list, and clicks each one at its centre with the mouse, or with a tap on phones. After 450 ms it records the effects of the click: a navigation, a DOM change outside tooltips, a focus move away from the control, a change of value, `checked`, `aria-expanded`, `aria-pressed`, `aria-checked`, `aria-selected` or `open`, a request, a popup or a download. A click with none of these is a `no-click-effect` finding. After a navigation, or when an overlay stays open after Escape, the pass opens the state again. The pass stops after 40 controls or 90 seconds per state.

The pass does not click controls that change data or leave the page, such as Delete, Save, New page, Sign out or Download. `spec.clicks.skipNames` lists the words. Links with a destination navigate, so the pass does not click them. The probe still checks their centre with `dead-click`.

## Checks

Every finding names the check, a rule, a CSS selector for the element, its text, the state, the viewport, the theme and the measured values. The report maps each finding to an owner file. The lookup takes the class names of the element and its ancestors, and finds the CSS file that styles them and the JavaScript or template files that emit them.

1. **Duplicate titles and actions.**
   - `duplicate-title`: a visible heading or label repeats the text of the selected tab, the page header title or the dialog title in the same surface. The comparison ignores case and surrounding space. Headings inside the note text are the person's own content and do not count.
   - `duplicate-title` also covers a dialog whose title repeats the label of the selected tab inside it, such as a rail Dialog titled "Comments" above a selected Comments tab.
   - `duplicate-action`: two controls in the visible part of the page do the same thing. Two buttons match when they have the same accessible name. Two links match when they have the same destination, or the same name and destination. Two controls also match when they carry the same `data-cmd`, `data-command` or `data-action` value. Repeated controls inside lists, tables, trees, menus and tab lists do not count. Neither does note text.
2. **Reachability.** The probe collects every visible button, link, input, select, textarea, summary, tab, menu item and option.
   - `cannot-scroll-into-view`: the element stays partly hidden after `scrollIntoView` and after scrolling every scroll container and the page to its end. The measured values name the limiting container and the hidden pixels.
   - `clipped-by-overflow`: an ancestor with `overflow: hidden` or `clip` cuts the element. A person cannot scroll such an ancestor, so the probe undoes any scroll the browser applies to it.
   - `covered-by-sticky` and `covered`: after the element scrolls into view, `elementFromPoint` at its centre returns a sticky, fixed or other element. Open menus and dialogs that cover the page do not count.
   - `dead-click`: `elementFromPoint` at the centre returns an element that paints nothing. Such an element has no fill, border, text or image, and nothing between it and the common ancestor with the control paints either. A person sees the control and the click goes to the invisible element. Open overlays do not excuse this finding.
   - `no-click-effect`: the click pass clicked the control and nothing followed. See Passes.
3. **Alignment.** Banners, undo rows, ActionList items, menu items, completion items, NavList and tree rows, list rows, table rows, PageHeaders and dialog headers are checked. In single-line content, the leading icon, the text line and the trailing actions share a vertical centre within 2px (`single-line-centres`). In multi-line content, the icon centre matches the first text line within 2px (`icon-first-line`). A leading visual more than 6px taller than the first line, such as a 32px Avatar beside a name and an email, may instead centre on the whole text block. In toolbars, dialog footers and Banner action rows, the controls of one row share a centre within 2px (`toolbar-centres`). Line centres come from the text's line boxes.
4. **Spacing against the spec.** `padding`: the computed padding of each selector in `spec.json` matches the `docs/primer.md` value within 1px. `overlay-text-inset-left` and `overlay-text-inset-top`: in menus, anchored overlays, SelectPanels, autocomplete lists, dialogs and tooltips, the first text sits no further from the overlay edge than the spec allows. In menus the left limit is 8px list inset + 8px item padding + 16px icon + 8px gap = 40px with a leading visual, and 16px without one. The CodeMirror autocomplete menus count as menus.
5. **Overlap and clipping.** `interactive-overlap`: two interactive elements overlap by more than 1px in both directions, and neither contains the other. `text-clipped`: text overflows a box with `overflow: hidden` or `clip` and no `text-overflow: ellipsis`, or an ancestor with hidden overflow cuts it. `ellipsis-without-full-text`: an ellipsis truncates the text, and no `title`, `aria-label` or tooltip holds the full text. `word-broken`: a heading, dialog title, tab, menu item, button, link, label or table header wraps inside a word, as when page actions squeeze a note title on a phone.
6. **Page level.**
   - `horizontal-page-scroll`: the document is wider than the viewport. The measured values list the widest elements.
   - `tap-target-under-44`: on phone profiles, the tappable area is under 44 × 44px. The probe scrolls the element to the centre of its scroll containers, then walks outward from the element centre with `elementFromPoint`, so a `::before` hit area counts.
   - `focus-ring-missing`, `focus-ring-clipped`, `focus-obscured` and `focus-on-invisible-element`: the focus walk presses Tab and checks each focused element. The element needs a visible outline, or a focus rule that sets a box shadow or border. The element must be inside the visible area and not covered.
   - `banner-count`: a surface shows 2 Banners of the same severity.
   - `console-error` and `page-error`: the page logged an error or threw.
   - `failed-static-asset`: a same-origin stylesheet, script, font or manifest answered with an error or failed to load.
7. **Colour.**
   - `yellow-or-amber`: a text, background, border, outline, shadow, SVG fill or stroke colour has a hue from 40° to 65° and an HSL saturation above 0.35. Pseudo-elements count. Images, video, canvas, PDF pages and syntax highlight tokens do not count.
   - `contrast-below-aa`: text against its composited background is below 4.5:1, or below 3:1 for large text. Disabled controls, faded elements and text over background images are skipped.
   - `control-contrast`: a button, or the box of a text field, select or textarea, has neither a border or ring nor a fill of at least 3:1 against the surface behind it. A field's box may be its wrapper, such as `.text-input`. Buttons in the invisible variant have no boundary. They pass when they hold only an icon, or when they sit in a place `docs/primer.md` gives them, such as a Banner, an undo row, a toolbar, a dialog header or a Box row. The icon of an icon-only control needs 3:1 against its background. Tabs, menu items, options, tree rows, NavList items, SegmentedControl segments, switches, DataTable sort headers and controls that an Avatar or image fills are exempt.
   - `old-token`: a stylesheet rule or an inline style that reads an old alias token applies to a visible element. `spec.oldTokens` lists the tokens from the alias block in `base.css`. The finding names the stylesheet, the rule and the declaration. Hover and focus pseudo-classes are removed from the selector before matching.
8. **Consistency.**
   - `unexplained-indent`: in the sidebar, the navigation drawer and the settings and admin navigation, the root rows of every list and tree start at the same left edge within 1px. A row starts at its leftmost visible icon, control or character. A tree whose parent rows show a chevron at the shared edge passes, because the chevron accounts for the offset of its leaves.
   - `mixed-leading-visuals`: in one menu, ActionList or SelectPanel list, the first characters of the items do not share a left edge within 2px. One item has a leading icon or a check column and another has none.
   - `metadata-prominence`: in comment, version, list, Box, search, trash, upload and people rows, secondary text such as a time, an author, an email or a meta line is smaller than the primary text or muted to below 0.8 times its contrast, and no heavier. The primary text is the row's body, title or name.
   - `inconsistent-spacing`: repeated components of one type share padding and gap within 1px. Components are the selectors in `spec.consistency`. Two elements are the same type when they match the same selector, carry the same classes, have the same visible children, sit at the same tree level, have the same size, variant, layout and padding attributes, and sit in the same region: a dialog, menu, header, aside, nav, main, footer or toolbar. Elements with padding or gap in an inline style are skipped.
9. **Overlay dismissal.** `overlay-escape`, `overlay-close-control` and `overlay-focus-return` come from the overlay dismissal pass. See Passes.

## Allowed Exceptions

`allow.json` lists exceptions. Each entry has a CSS selector, a check number, an optional rule and a reason. A finding whose element matches an entry appears in the report's Allowed table and does not affect the exit status. Only exceptions that `docs/primer.md` documents belong in the file, and each reason names the `docs/primer.md` row.

## Report

`report.md` and `report.json` group occurrences into findings by check, rule and selector. The report lists counts by check and by owner file, the 20 most severe findings, every finding with an example measurement and a screenshot path, the allowed findings, the harness errors and the coverage per profile. A screenshot is cropped around the element and marks it with a red outline.

Severity starts from a base value per rule and grows with the number of states and viewports that show the finding. Static asset failures, page errors, unreachable controls and horizontal page scroll rank highest. Padding differences rank lowest.

A harness error means the audit could not open a state, for example because a menu did not open. The report lists each harness error with a screenshot in the temporary directory. Harness errors make the exit status non-zero.

## Adding a State

Add an entry to `buildStates` in `lib/states.mjs`. A state has an `id`, a `kind` (`app`, `fallback` or `anon`), a `run(h)` function that navigates and opens the state, and optionally `teardown(h)`, `editor: true` for the keyboard pass, `focus: true` for the focus walk, `when(profile)` and `expectConsole`. `plan` in the same file selects the states for each profile and theme.
