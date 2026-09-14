// Entry point for the vendored CodeMirror bundle. Re-exports only what
// static/js/editor.js and the history Dialog in panels.js use. Rebuild with ./build.sh after changing it.
//
// The markdown language is assembled here from markdownLanguage and
// @lezer/markdown instead of calling markdown() from lang-markdown, because
// markdown() pulls in the HTML, CSS and JavaScript parsers for embedded HTML.

export {
  EditorState, EditorSelection, StateField, StateEffect, Compartment, Prec, Transaction, Annotation, RangeSetBuilder,
} from "@codemirror/state";

export {
  EditorView, Decoration, ViewPlugin, WidgetType, keymap, placeholder, drawSelection, dropCursor,
  showTooltip, tooltips, highlightActiveLine,
} from "@codemirror/view";

export { history, historyKeymap, defaultKeymap, undo, redo, insertNewlineAndIndent } from "@codemirror/commands";

export {
  syntaxTree, syntaxHighlighting, HighlightStyle, LanguageDescription, StreamLanguage, Language, LanguageSupport,
  indentUnit,
} from "@codemirror/language";

export {
  autocompletion, completionKeymap, startCompletion, closeCompletion, completionStatus, acceptCompletion,
} from "@codemirror/autocomplete";

export {
  search, searchKeymap, SearchQuery, setSearchQuery, getSearchQuery, searchPanelOpen, findNext, findPrevious, replaceNext,
  replaceAll, openSearchPanel, closeSearchPanel,
} from "@codemirror/search";

// Read-only version diffs in the history Dialog.
export { MergeView, unifiedMergeView } from "@codemirror/merge";

export { tags, styleTags } from "@lezer/highlight";

import { Language, LanguageDescription, LanguageSupport, StreamLanguage } from "@codemirror/language";
import { markdownLanguage } from "@codemirror/lang-markdown";
import { parseCode } from "@lezer/markdown";
import { javascript, typescript, json } from "@codemirror/legacy-modes/mode/javascript";
import { python } from "@codemirror/legacy-modes/mode/python";
import { go } from "@codemirror/legacy-modes/mode/go";
import { css } from "@codemirror/legacy-modes/mode/css";
import { html } from "@codemirror/legacy-modes/mode/xml";
import { standardSQL } from "@codemirror/legacy-modes/mode/sql";
import { shell } from "@codemirror/legacy-modes/mode/shell";

function stream(name, alias, mode) {
  return LanguageDescription.of({ name, alias, support: new LanguageSupport(StreamLanguage.define(mode)) });
}

/** Fenced code languages highlighted in the editor. */
export const codeLanguages = [
  stream("javascript", ["js", "jsx", "mjs", "cjs", "node"], javascript),
  stream("typescript", ["ts", "tsx"], typescript),
  stream("json", ["jsonc", "json5"], json),
  stream("python", ["py", "python3"], python),
  stream("go", ["golang"], go),
  stream("css", ["scss", "less"], css),
  stream("html", ["xml", "svg", "htm", "xhtml"], html),
  stream("sql", ["postgresql", "postgres", "mysql", "sqlite"], standardSQL),
  stream("shell", ["sh", "bash", "zsh", "console", "shellscript", "shell-session"], shell),
];

/**
 * Builds GitHub-flavoured markdown language support (tables, task lists,
 * strikethrough are part of markdownLanguage) with fenced code highlighting
 * and extra parser extensions such as wikilinks.
 * @param {object[]} [extensions] @lezer/markdown MarkdownConfig objects
 * @returns {LanguageSupport}
 */
export function markdownSupport(extensions = []) {
  const codeParser = (info) => {
    const desc = LanguageDescription.matchLanguageName(codeLanguages, info, true);
    return desc && desc.support ? desc.support.language.parser : null;
  };
  const parser = markdownLanguage.parser.configure([...extensions, parseCode({ codeParser })]);
  return new LanguageSupport(new Language(markdownLanguage.data, parser, [], "markdown"));
}
