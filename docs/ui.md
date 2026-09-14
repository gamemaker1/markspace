# Interface Rules

`primer.md` is the interface specification. The client builds GitHub Primer
components as it describes. The rules below apply to every screen and to
plugin UI.

Controls sit where the person works and close when the work is done.

## Rules

1. **Overlays follow Primer.** At most 2 dialogs stack. Menus, pickers and
   tooltips open over sheets and dialogs. On a narrow viewport, below 768 px,
   the rail opens as a full-screen Dialog, and any Dialog with an input field
   is full screen with a close button.
2. **Menus, pickers and confirmations are overlays.** An ActionMenu holds item
   actions, a SelectPanel or a Dialog chooses from a list, and a
   ConfirmationDialog asks before a permanent action. Inline forms and row
   details still expand in place. A task that needs the whole screen, such as
   editing an image, opens a large Dialog with a close button, and there is
   no separate view with Back.
3. **Undo for reversible actions.** Move to trash, remove access, resolve a
   thread, move, deactivate a user and remove a member act at once. An undo
   row with an Undo button replaces the removed row in place. It stays until
   the person acts in that list again or leaves the page. When the place is
   gone, such as after trashing the open note, a dismissible info Banner at
   the top of the destination page carries Undo. No message has a timer.
4. **ConfirmationDialog for permanent actions.** Delete forever, Empty trash,
   Delete space, Delete account, Delete user, Uninstall plugin, Revoke token,
   Revoke public link, Delete comment and Discard unsaved changes open a
   ConfirmationDialog with a danger button. Frequent and undoable actions
   never ask.
5. **Nothing appears unasked.** No chips, hints or tips appear after an
   action unless the person asked for them. IconButtons show a Tooltip on
   hover and on keyboard focus, and a Tooltip only names or describes its
   control. Link previews on hover are off by default, and a profile setting
   turns them on.
6. **Nothing is hover-only.** Every action can be reached by tap and by
   keyboard. Touch devices get no tooltips, so the information in a Tooltip
   also appears elsewhere.
7. **Menus hold secondary actions.** The primary action of an item is a
   visible button or the item itself. A table or list row keeps one menu
   button. A tree row holds no control besides its chevron, and its actions
   open from the context menu, Shift+F10, the Menu key and the opened item's
   PageHeader menu. On a narrow viewport an ActionMenu opens full screen.
   Menus never nest.
8. **Messages go where the cause is.** A form error sits under its field, and
   3 or more errors add an error summary Banner at the top of the form. A
   failed save shows a critical Banner with Retry near the affected area, and
   the Banner stays until the save succeeds. A visible result needs no
   message, and the status region announces it. A result the page does not
   show gets an InlineMessage beside its control. Background work the person
   started and left, such as an import, reports through a Banner. There are
   no toasts.
9. **No status line.** Sync state shows as a badge on the avatar button in
   the app header, and the badge holds no messages. The avatar menu names the
   state and offers "Sync now". Announcements go to one `role="status"` region. A page shows
   at most one Banner, and a newer Banner replaces an older one of lower
   severity.
10. **Phones.**
    - Page actions sit in the PageHeader at the top, and the app header
      scrolls with the page. There is no bottom action bar.
    - The keyboard bar is the only toolbar while typing. It shows formatting,
      selection actions, image actions or the review of AI changes, depending
      on what is selected.
    - Nothing floats over selected text, because the system selection
      handles sit there.
    - Tap targets are at least 44 px.
11. **Chrome.** Boxes, panes, tables, lists and dialog headers use 1 px
    borders and dividers. Counters are neutral and count only things that
    need action, such as open comments and open suggestions. Icon-only
    buttons have an accessible name and a Tooltip.
12. **Motion.** State changes and overlays take 200 ms, sheets 250 ms and
    buttons 80 ms. `prefers-reduced-motion` turns motion off.

## Patterns

| Need | Pattern |
| --- | --- |
| Rename | The name becomes a small text field with Save and Cancel buttons beside it. Enter saves and Escape cancels |
| Choose a destination (move, copy) | Move to and Copy to open a large Dialog with a TreeView folder picker. Dragging in the tree also moves items |
| Pick a model | A SelectPanel with a filter field |
| Pick a folder | A Dialog with a TreeView, or a SelectPanel of folder paths in plugin forms |
| Pick a person or note | Autocomplete under the text field |
| Settings form | Text inputs, checkbox groups, radio groups and native selects wait for the form's one Save button. Toggle switches, segmented controls and custom single selects save on change with no "Saved" indicator. A save that does not change the page shows an InlineMessage beside Save |
| Version history | A full-screen Dialog with the versions listed beside a diff view of the chosen version. Restore sits in the Dialog footer |
| Row details | The row expands in place. Only one row is expanded at a time |
| Long text entry, such as an invite message | A textarea in place |
| Destructive action | Undo for reversible actions, a ConfirmationDialog for permanent ones |
| Wait for work | No indicator under 1 s. Then a Spinner or skeleton rows where the result will appear, and a ProgressBar for work past 3 s |

## Components in the Client

`internal/web/static/js/ui.js` builds these patterns, and `offline.md`
documents its API. The
helpers are `openMenu` and `actionMenu` for ActionMenu, `selectPanel`,
`autocomplete`, `openDialog`, `confirmDialog` for ConfirmationDialog,
`tooltip`, `banner`, `inlineMessage`, `announce`, `undoRow` and `undoable`,
`editInPlace` and `expandRow`. `ui.js` has no status line, no toasts, no
two-step buttons, no inline action rows and no Undo timer.

## Checks

`make layout` runs the browser layout audit in `test/browser` over every
page, panel, dialog, menu and banner state. It covers 1440 × 900, 1280 × 720,
1024 × 768, 390 × 844, 844 × 390 and 320 × 256, the light and dark themes,
and 200% zoom. It fails on any finding, among them:
- a heading or label that repeats its tab, page header or dialog title,
- an interactive element that cannot scroll fully into view, or that sits
  under a sticky bar or the on-screen keyboard,
- icon, text and actions in a banner, row or toolbar that are more than 2 px
  out of vertical alignment,
- overlapping interactive elements, clipped text without an ellipsis, and
  sideways page scrolling,
- a tap target under 44 px on a phone viewport, or a focus ring that is
  missing or hidden,
- overlay, menu, dialog, banner and panel padding that differs from
  `primer.md`, or nested padding past 24 px,
- yellow or amber colours, and a stylesheet or script that fails to load.
