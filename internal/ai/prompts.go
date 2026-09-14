package ai

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

const (
	// systemPrompt opens every transcript.
	systemPrompt = "You are a writing assistant inside a markdown note application. " +
		"Return only the requested text, with no preamble, no commentary and no code fence around the whole answer. " +
		"Preserve the author's markdown formatting and language unless the instruction asks you to change them."

	// truncationMarker ends text that was cut to fit the context budget.
	truncationMarker = "\n\n[text truncated]"

	// contextIntro opens the context message.
	contextIntro = "Context\n\nThe sources below are numbered. Cite a source you rely on as [n], using its number."

	// maxHistoryMessages caps how many earlier turns go into an ask prompt.
	maxHistoryMessages = 10

	// minSourceRunes is the smallest cut excerpt worth adding to the context.
	minSourceRunes = 64

	// defaultLanguage applies when a translate request names no language.
	defaultLanguage = "English"

	// Caps on the request fields that are not note text. Without them a
	// question, a history turn or a custom instruction reached the model
	// uncut, whatever its size.
	maxQuestionRunes    = 2000
	maxHistoryRunes     = 4000
	maxInstructionRunes = 1000
	maxLanguageRunes    = 64

	// Caps on the header line of one source in the context message.
	maxSourceTitleRunes   = 120
	maxSourceLocatorRunes = 200
)

// TaskTextBudget returns how many runes of task text a prompt keeps out of a
// context budget. An ask keeps at most half, so the retrieved sources keep
// room next to a long open note. Every other task may use the whole budget.
// References fill whatever the task text leaves.
func TaskTextBudget(task core.AITask, budget int) int {
	if task == core.TaskAsk {
		return budget / 2
	}
	return budget
}

// BuildMessages is BuildPrompt with sources only and the default budget.
func (r *Registry) BuildMessages(req core.AIRequest, sources []core.AISource) []core.AIMessage {
	return r.BuildPrompt(req, ports.PromptInput{Sources: sources})
}

// BuildPrompt turns a task request plus resolved context into a chat
// transcript, in this order:
//
//  1. A system message with the shared system prompt and the task instruction.
//  2. A system message titled Context when any source adds content. Each
//     source is listed as "[n] <kind>: <title> (<url or path>)" followed by
//     its text, where n is its position in in.Sources. Skipped sources and
//     sources with no text are left out without renumbering the rest.
//  3. Up to 10 earlier turns from req.History: the conversation of an ask,
//     or the previous result of a refinement.
//  4. The user message with the task text, the question for an ask, and the
//     images.
//
// The first system message also carries the output mode instruction of
// req.Output, an additional instruction for a canned task, and a refinement
// instruction when the history holds an earlier answer.
//
// The task text keeps at most TaskTextBudget runes of in.Budget, and source
// text fills the rest in order. A source that does not fit is cut, and one
// that would keep fewer than 64 runes is left out. The question, history,
// instruction and language have their own small caps, so the transcript stays
// within the budget plus a fixed overhead.
func (r *Registry) BuildPrompt(req core.AIRequest, in ports.PromptInput) []core.AIMessage {
	budget := in.Budget
	if budget <= 0 {
		budget = MaxContextRunes
	}
	instruction := taskInstruction(req)
	msgs := []core.AIMessage{{Role: "system", Content: systemPrompt + "\n\n" + requestInstruction(req, instruction)}}

	textCap := TaskTextBudget(req.Task, budget)
	textRunes := min(utf8.RuneCountInString(req.Text), textCap)
	text := truncateText(req.Text, textCap)

	block := r.contextBlock(req, in, budget-textRunes)
	if block != "" {
		msgs = append(msgs, core.AIMessage{Role: "system", Content: block})
	}
	msgs = append(msgs, recentHistory(req.History)...)

	var parts []string
	if strings.TrimSpace(text) != "" {
		parts = append(parts, textLabel(req.Task)+":\n\n"+text)
	}
	if req.Task == core.TaskAsk {
		if question := strings.TrimSpace(req.Question); question != "" {
			parts = append(parts, "Question: "+truncateText(question, maxQuestionRunes))
		}
	}
	if len(parts) == 0 {
		switch {
		case len(in.Images) > 0:
			parts = append(parts, "The images are attached.")
		case block != "":
			parts = append(parts, "The material to work on is in the context above.")
		default:
			parts = append(parts, instruction)
		}
	}
	user := core.AIMessage{Role: "user", Content: strings.Join(parts, "\n\n")}
	if len(in.Images) > 0 {
		user.Images = append([]core.AIImage(nil), in.Images...)
	}
	return append(msgs, user)
}

// Output mode instructions, added after the task instruction.
const (
	outputReplaceInstruction = "Output mode: replace. Return only the text that replaces the selected text, ready to put in its place. " +
		"Do not repeat the text around the selection, and add no preamble or explanation."
	outputInsertInstruction = "Output mode: insert. Return only the new text to insert at the cursor. " +
		"Do not repeat the text before or after the cursor, and add no preamble or explanation."
	outputAnswerInstruction = "Output mode: answer. Write a short markdown answer for the reader. " +
		"The answer is shown beside the note and is not inserted into it. Cite the sources you rely on as [n]."
	// refineInstruction applies when the history holds an earlier result.
	refineInstruction = "The conversation above ends with your previous result. Apply the new instruction to that result " +
		"and return the complete new result in the same output mode."
)

// outputInstruction returns the instruction of one output mode, or the empty
// string for no mode or an unknown one.
func outputInstruction(output string) string {
	switch output {
	case core.AIOutputReplace:
		return outputReplaceInstruction
	case core.AIOutputInsert:
		return outputInsertInstruction
	case core.AIOutputAnswer:
		return outputAnswerInstruction
	}
	return ""
}

// requestInstruction joins the task instruction with the additional
// instruction of a canned task, the refinement instruction and the output
// mode instruction.
func requestInstruction(req core.AIRequest, instruction string) string {
	parts := []string{instruction}
	extra := strings.TrimSpace(req.Instruction)
	if extra != "" && usesOwnInstruction(req.Task) {
		parts = append(parts, "Additional instruction: "+truncateText(extra, maxInstructionRunes))
	}
	if req.Task != core.TaskAsk && hasAssistantTurn(req.History) {
		parts = append(parts, refineInstruction)
	}
	if mode := outputInstruction(req.Output); mode != "" {
		parts = append(parts, mode)
	}
	return strings.Join(parts, "\n\n")
}

// usesOwnInstruction reports whether a task has a canned instruction, so a
// request instruction is added to it rather than replacing it.
func usesOwnInstruction(task core.AITask) bool {
	switch task {
	case core.TaskCustom, "":
		return false
	}
	for _, t := range core.AITasks {
		if t == task {
			return true
		}
	}
	return false
}

// hasAssistantTurn reports whether history holds a non-empty assistant turn.
func hasAssistantTurn(history []core.AIMessage) bool {
	for _, msg := range history {
		if strings.EqualFold(strings.TrimSpace(msg.Role), "assistant") && strings.TrimSpace(msg.Content) != "" {
			return true
		}
	}
	return false
}

// taskInstruction returns the instruction for one task.
func taskInstruction(req core.AIRequest) string {
	switch req.Task {
	case core.TaskSummarize:
		return "Summarize the text below into 3 to 5 bullet points."
	case core.TaskImprove:
		return "Rewrite the text below for clarity and flow. Keep the meaning and the markdown structure."
	case core.TaskGrammar:
		return "Fix spelling, grammar and punctuation in the text below. Change nothing else, not the wording, the structure or the formatting."
	case core.TaskShorten:
		return "Shorten the text below to about half its length. Keep the key points."
	case core.TaskExpand:
		return "Expand the text below with more detail and examples, to roughly double its length."
	case core.TaskTranslate:
		language := strings.TrimSpace(cutRunes(req.Language, maxLanguageRunes))
		if language == "" {
			language = defaultLanguage
		}
		return "Translate the text below into " + language + ". Keep the markdown formatting."
	case core.TaskContinue:
		return "Continue the text below from where it stops. Return only the continuation, and do not repeat the existing text."
	case core.TaskTitle:
		return "Write one title for the text below. Use at most 60 characters. Return the title only, with no quotes and no trailing period."
	case core.TaskTags:
		return "List 3 to 6 topic tags for the text below. Return them lowercase and comma separated, with no hash signs and nothing else."
	case core.TaskOutline:
		return "Write a nested markdown heading outline of the text below."
	case core.TaskActions:
		return "Extract the action items from the text below as a markdown task list, one \"- [ ] item\" per line."
	case core.TaskAsk:
		return "Answer the question using the context and the current note. Cite the sources you rely on as [1], [2] and so on. If the context does not contain the answer, say that the notes do not cover it."
	case core.TaskExplain:
		return "Explain the text or media below for a reader who is new to the subject. Define the terms it relies on and keep the explanation short."
	case core.TaskDescribe:
		return "Describe each attached image in one short paragraph suitable as alt text, in the order the images are attached. Describe what is shown, not how to interpret it."
	case core.TaskExtract:
		return "Transcribe the text visible in the attached images or files. Preserve the line breaks, and write tables as markdown tables. Return only the transcribed text."
	default:
		if instruction := strings.TrimSpace(req.Instruction); instruction != "" {
			return "Apply the following instruction to the text below: " + truncateText(instruction, maxInstructionRunes)
		}
		return "Apply the requested change to the text below."
	}
}

// textLabel names the task text block for one task.
func textLabel(task core.AITask) string {
	switch task {
	case core.TaskContinue:
		return "Text so far"
	case core.TaskAsk:
		return "Current note"
	default:
		return "Text"
	}
}

// contextBlock renders the sources as the numbered context message, with the
// source text limited to remaining runes. contextChunks caps how many search
// sources are listed.
func (r *Registry) contextBlock(req core.AIRequest, in ports.PromptInput, remaining int) string {
	var block strings.Builder
	searches := 0
	taskText := strings.TrimSpace(req.Text)
	for i, source := range in.Sources {
		if source.Skipped != "" {
			continue
		}
		if source.Kind == "" || source.Kind == core.ContextSearch {
			searches++
			if r.contextChunks > 0 && searches > r.contextChunks {
				continue
			}
		}
		excerpt := strings.TrimSpace(source.Excerpt)
		isImage := source.Kind == core.ContextFile && excerpt == ""
		inMessage := source.Kind == core.ContextSelection && excerpt != "" && excerpt == taskText
		if excerpt == "" && !isImage {
			continue
		}

		entry := sourceHeader(i+1, source)
		switch {
		case isImage:
			entry += "\nImage attached to the user message."
		case inMessage:
			entry += "\nThe text in the user message."
		default:
			n := utf8.RuneCountInString(excerpt)
			if n > remaining {
				if remaining < minSourceRunes {
					continue
				}
				excerpt = cutRunes(excerpt, remaining) + truncationMarker
				n = remaining
			}
			remaining -= n
			entry += "\n" + excerpt
		}
		if block.Len() > 0 {
			block.WriteString("\n\n")
		}
		block.WriteString(entry)
	}
	if block.Len() == 0 {
		return ""
	}
	return contextIntro + "\n\n" + block.String()
}

// sourceHeader renders "[n] <kind>: <title> (<url or path>)".
func sourceHeader(n int, source core.AISource) string {
	kind := string(source.Kind)
	if kind == "" {
		kind = string(core.ContextNote)
	}
	title := strings.TrimSpace(cutRunes(source.Title, maxSourceTitleRunes))
	if title == "" {
		title = "Untitled"
	}
	header := fmt.Sprintf("[%d] %s: %s", n, kind, title)
	locator := source.URL
	if locator == "" {
		locator = source.Path
	}
	if locator = strings.TrimSpace(cutRunes(locator, maxSourceLocatorRunes)); locator != "" {
		header += " (" + locator + ")"
	}
	return header
}

// recentHistory returns the last maxHistoryMessages user and assistant turns.
func recentHistory(history []core.AIMessage) []core.AIMessage {
	out := make([]core.AIMessage, 0, len(history))
	for _, msg := range history {
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		if role != "user" && role != "assistant" {
			continue
		}
		if strings.TrimSpace(msg.Content) == "" {
			continue
		}
		out = append(out, core.AIMessage{Role: role, Content: msg.Content})
	}
	if len(out) > maxHistoryMessages {
		out = out[len(out)-maxHistoryMessages:]
	}
	// Keep the newest turns within maxHistoryRunes. The turn that crosses
	// the cap is cut when enough room is left, and older turns are dropped.
	budget := maxHistoryRunes
	start := len(out)
	for start > 0 {
		n := utf8.RuneCountInString(out[start-1].Content)
		if n > budget {
			if budget >= minSourceRunes {
				out[start-1].Content = truncateText(out[start-1].Content, budget)
				start--
			}
			break
		}
		budget -= n
		start--
	}
	return out[start:]
}

// truncateText cuts s to max runes on a rune boundary and marks the cut.
func truncateText(s string, max int) string {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	return cutRunes(s, max) + truncationMarker
}

// cutRunes returns the first max runes of s.
func cutRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max])
}
