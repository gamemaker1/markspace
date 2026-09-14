package ui

// Node is one UI node, form field or list item. It encodes as a JSON object.
type Node map[string]any

// Action is an action object {id, payload}. The client posts it to the
// plugin's /actions/{id} route.
type Action map[string]any

// With sets an optional field and returns n. A nil value deletes the field.
func (n Node) With(key string, v any) Node {
	if v == nil {
		delete(n, key)
		return n
	}
	n[key] = v
	return n
}

func nodes(children []Node) []Node {
	if children == nil {
		return []Node{}
	}
	return children
}

// Act returns an action object. A nil payload is omitted.
func Act(id string, payload map[string]any) Action {
	a := Action{"id": id}
	if payload != nil {
		a["payload"] = payload
	}
	return a
}

// Stack returns a stack node. Set gap with With("gap", "s"|"m"|"l").
func Stack(children ...Node) Node {
	return Node{"type": "stack", "children": nodes(children)}
}

// Section returns a titled section. description and count are optional.
func Section(title string, children ...Node) Node {
	n := Node{"type": "section", "title": title}
	if len(children) > 0 {
		n["children"] = children
	}
	return n
}

// Form returns a form that posts its values with action.
func Form(id string, action Action, fields ...Node) Node {
	return Node{"type": "form", "id": id, "action": action, "fields": nodes(fields)}
}

// Field returns a form field. typ is text, textarea, number, url, select,
// checkbox, file, note, folder or space. An empty label is omitted.
func Field(typ, name, label string) Node {
	n := Node{"type": typ, "name": name}
	if label != "" {
		n["label"] = label
	}
	return n
}

// Option returns one option of a select field.
func Option(value, label string) map[string]any {
	return map[string]any{"value": value, "label": label}
}

// List returns a list node of items made with Item.
func List(items ...Node) Node {
	return Node{"type": "list", "items": nodes(items)}
}

// Item returns a list item. subtitle, icon, link, badge and actions are
// optional.
func Item(title string) Node {
	return Node{"title": title}
}

// Col returns a table column.
func Col(key, label string) map[string]any {
	return map[string]any{"key": key, "label": label}
}

// Table returns a table node.
func Table(columns []map[string]any, rows []map[string]any) Node {
	if columns == nil {
		columns = []map[string]any{}
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	return Node{"type": "table", "columns": columns, "rows": rows}
}

// Text returns a plain text node.
func Text(value string) Node { return Node{"type": "text", "value": value} }

// Markdown returns a markdown node. The client escapes raw HTML.
func Markdown(value string) Node { return Node{"type": "markdown", "value": value} }

// Code returns a code block. An empty language is omitted.
func Code(value, language string) Node {
	n := Node{"type": "code", "value": value}
	if language != "" {
		n["language"] = language
	}
	return n
}

// Progress returns a progress bar. max, label and jobId are optional.
func Progress(value float64) Node { return Node{"type": "progress", "value": value} }

// Badge returns a badge. An empty tone is omitted.
func Badge(label, tone string) Node {
	n := Node{"type": "badge", "label": label}
	if tone != "" {
		n["tone"] = tone
	}
	return n
}

// Button returns a button that runs action.
func Button(label string, action Action) Node {
	return Node{"type": "button", "label": label, "action": action}
}

// ToItem returns a link target for an item.
func ToItem(id string) map[string]any { return map[string]any{"itemId": id} }

// ToURL returns a link target for an http or https URL.
func ToURL(url string) map[string]any { return map[string]any{"url": url} }

// LinkItem returns a link to an item.
func LinkItem(label, itemID string) Node {
	return Node{"type": "link", "label": label, "to": ToItem(itemID)}
}

// LinkURL returns a link to an external URL.
func LinkURL(label, url string) Node {
	return Node{"type": "link", "label": label, "to": ToURL(url)}
}

// Empty returns an empty state. body and a button action are optional.
func Empty(title string) Node { return Node{"type": "empty", "title": title} }
