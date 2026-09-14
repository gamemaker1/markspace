// Package links parses, resolves and rewrites links in note bodies. It
// implements the Links section of docs/files.md. The browser module
// static/js/links.js is a port of this package, and test/vectors/links.json holds
// cases that both must pass. The Go package uses UTF-8 byte offsets and the
// port uses UTF-16 code unit offsets.
//
// # Parsing
//
// Parse reads a subset of CommonMark with wikilinks. A block pass drops
// fenced code, indented code, and HTML comment blocks. It tracks blockquote
// markers and list item indentation so list continuations are not taken for
// code. An inline pass then scans each paragraph, heading or list item from
// left to right:
//
//   - A backslash escapes the ASCII punctuation character after it.
//   - A backtick run opens a code span when a run of the same length follows
//     in the block. Code spans hide the links inside them.
//   - HTML comments and URI autolinks such as <https://x> hide their content.
//     Other raw HTML is plain text.
//   - [[ or ![[ opens a wikilink when "]]" follows on the same line. The
//     target ends at the first "|", or at "\|" as in a table cell, and the
//     heading starts at the first "#". Target, heading and label are trimmed
//     of spaces and tabs. An empty target or an empty label makes it plain
//     text, except [[#heading]], which is an external anchor.
//   - [ and ![ push an opener. A "]" pops the nearest opener and forms an
//     inline link when "(destination "title")" follows. A destination is
//     <...> or a run without spaces and with balanced parentheses. The target
//     ends at the first "?" or "#", and backslash escapes and %XX sequences
//     are decoded. Invalid UTF-8 after decoding keeps the %XX text.
//   - Link text may hold images and embeds, which Parse returns as nested
//     links. A markdown link or a non-embed wikilink inside link text or
//     image text makes every enclosing opener plain text.
//
// Reference links, entities and table cell splitting are not supported.
// Frontmatter must be removed before Parse.
//
// A markdown destination is external when it is empty, starts with "#" or
// "//", or starts with a URI scheme of 2 to 32 characters. A wikilink is
// external only when its target is empty.
//
// # Resolving
//
// Resolution follows docs/files.md, "Resolving a name", with these refinements:
//
//   - A wikilink target containing "/" uses the path rule, and any other
//     target the name rule, whatever the form. A leading "/" means the space
//     root and turns off suffix matching, so [[/x.png]] is a path wikilink to
//     a root-level entry.
//   - Exact and suffix path matches are case-sensitive, as docs/files.md states.
//     Markdown paths also match with ".md" appended.
//   - The folder note match by folder name is case-insensitive and ranks with
//     the title match. A folder itself matches only by path.
//   - The note-folder preference is tree distance 0. The shortest path is
//     counted in UTF-8 bytes and the lexical order is byte order, which is
//     code point order.
//
// # Rewriting
//
// Rewrite applies the changes to a copy of the snapshot and checks every link
// that resolved before. A link whose text still resolves to the same entry id
// after the changes is left alone. This keeps title links, links between two
// moved items and unchanged relative links as they are. Any other link gets
// the first text of its form that resolves to the same id:
//
//   - A markdown link keeps its root or relative form and its heading, query
//     and title. ".md" is dropped only when the link was written without it
//     and the short path still resolves. A relative path whose first segment
//     looks like a URI scheme gets "./". A path with a space or a parenthesis
//     uses <...>. Destinations percent-encode control bytes and % \ # ? < > [
//     ] ` |, and keep other bytes as written.
//   - A name wikilink tries the new file name, the note stem or the folder
//     name of a folder note, then the note title, then a path wikilink.
//     docs/files.md updates name wikilinks only for file renames. A note rename
//     also breaks a stem match such as [[b]], so this package rewrites any
//     name wikilink that stops resolving, which keeps every link on its
//     target.
//   - A path wikilink tries the entry path without ".md" when it was written
//     without it, then with ".md", then the rooted forms. A rooted link stays
//     rooted.
//   - A wikilink target may not contain | # [ ] ` < > \ or a control
//     character, or start or end with a space. When no wikilink text fits,
//     the link becomes [label](<relative path#heading>), where the label is
//     the wikilink label or the target as written. An embed becomes
//     ![label](<relative path>), with the label only when one was written.
//     Link text escapes \ [ ] < > | with a backslash and writes a backtick
//     as &#96;.
//
// Rewritten text never adds a character that could close a code span,
// comment, wikilink or bracket opened before the link, so the links of the
// new body match the old ones one to one. FuzzRewriteStable checks this, for
// links that resolved before the changes. A link that resolved to nothing may
// resolve to an entry afterwards. One case is not covered. A line that starts
// with three or more backticks and holds a backtick later is paragraph text,
// and percent-encoding a backtick in a link on that line turns it into a code
// fence.
//
// # Affected notes
//
// Affected finds the notes to rewrite from an outbound-link index keyed by
// LinkKeys. It collects EntryKeys, before and after the changes, of every
// entry whose path changes. A link can resolve differently only when its old
// or new target is such an entry, or when its own note moved.
package links
