package ai

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

// joinMessages renders a transcript as one string for substring assertions.
func joinMessages(msgs []core.AIMessage) string {
	var out strings.Builder
	for _, msg := range msgs {
		out.WriteString(msg.Role)
		out.WriteString(": ")
		out.WriteString(msg.Content)
		out.WriteString("\n")
	}
	return out.String()
}

func TestBuildMessagesPerTask(t *testing.T) {
	const text = "The quick brown fox jumps over the lazy dog."
	tests := []struct {
		task     core.AITask
		req      core.AIRequest
		contains string
	}{
		{core.TaskSummarize, core.AIRequest{}, "3 to 5 bullet points"},
		{core.TaskImprove, core.AIRequest{}, "clarity and flow"},
		{core.TaskGrammar, core.AIRequest{}, "spelling, grammar and punctuation"},
		{core.TaskShorten, core.AIRequest{}, "half its length"},
		{core.TaskExpand, core.AIRequest{}, "double its length"},
		{core.TaskTranslate, core.AIRequest{Language: "French"}, "into French"},
		{core.TaskContinue, core.AIRequest{}, "Continue the text below from where it stops"},
		{core.TaskTitle, core.AIRequest{}, "at most 60 characters"},
		{core.TaskTags, core.AIRequest{}, "3 to 6 topic tags"},
		{core.TaskOutline, core.AIRequest{}, "nested markdown heading outline"},
		{core.TaskActions, core.AIRequest{}, "action items"},
		{core.TaskAsk, core.AIRequest{Question: "Who jumps?"}, "Cite the sources you rely on as [1], [2]"},
		{core.TaskCustom, core.AIRequest{Instruction: "Make it rhyme"}, "Apply the following instruction to the text below: Make it rhyme"},
		{core.TaskExplain, core.AIRequest{}, "new to the subject"},
		{core.TaskDescribe, core.AIRequest{}, "alt text"},
		{core.TaskExtract, core.AIRequest{}, "Transcribe the text visible"},
	}
	registry := testRegistry()
	for _, tc := range tests {
		t.Run(string(tc.task), func(t *testing.T) {
			req := tc.req
			req.Task = tc.task
			req.Text = text
			msgs := registry.BuildMessages(req, nil)
			if len(msgs) != 2 {
				t.Fatalf("transcript = %s", joinMessages(msgs))
			}
			if msgs[0].Role != "system" || !strings.Contains(msgs[0].Content, "markdown") {
				t.Fatalf("first message = %+v", msgs[0])
			}
			if !strings.Contains(msgs[0].Content, tc.contains) {
				t.Fatalf("instruction %q missing from the system message %q", tc.contains, msgs[0].Content)
			}
			last := msgs[len(msgs)-1]
			if last.Role != "user" || !strings.Contains(last.Content, text) {
				t.Fatalf("last message = %+v", last)
			}
		})
	}
}

func TestBuildMessagesTranslateDefaultsToEnglish(t *testing.T) {
	msgs := testRegistry().BuildMessages(core.AIRequest{Task: core.TaskTranslate, Text: "hola"}, nil)
	if !strings.Contains(msgs[0].Content, "into English") {
		t.Fatalf("instruction = %q", msgs[0].Content)
	}
}

func TestBuildMessagesUnknownTaskUsesInstruction(t *testing.T) {
	msgs := testRegistry().BuildMessages(core.AIRequest{Task: "mystery", Text: "body", Instruction: "Do the thing"}, nil)
	if !strings.Contains(msgs[0].Content, "Do the thing") || !strings.Contains(msgs[1].Content, "body") {
		t.Fatalf("transcript = %s", joinMessages(msgs))
	}
}

func TestBuildPromptNumbersSourcesByPosition(t *testing.T) {
	sources := []core.AISource{
		{Kind: core.ContextNote, NoteID: "n1", Title: "Fox notes", Path: "fox.md", Excerpt: "the fox is quick"},
		{Kind: core.ContextURL, URL: "http://10.0.0.1/", Skipped: "private address not allowed"},
		{Kind: core.ContextLinked, NoteID: "n2", Path: "dog.md", Excerpt: "the dog is lazy"},
		{Kind: core.ContextNote, Title: "Empty", Excerpt: "   "},
		{Kind: core.ContextURL, URL: "https://example.com/spec", Title: "Spec", Excerpt: "spec text"},
	}
	req := core.AIRequest{Task: core.TaskAsk, Question: "Who is quick?", Text: "current note body"}
	msgs := testRegistry().BuildPrompt(req, ports.PromptInput{Sources: sources})
	if len(msgs) != 3 || msgs[1].Role != "system" {
		t.Fatalf("transcript = %s", joinMessages(msgs))
	}
	block := msgs[1].Content
	for _, want := range []string{
		"Context\n\n",
		"[1] note: Fox notes (fox.md)\nthe fox is quick",
		"[3] linked: Untitled (dog.md)\nthe dog is lazy",
		"[5] url: Spec (https://example.com/spec)\nspec text",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("context %q missing %q", block, want)
		}
	}
	if strings.Contains(block, "[2]") || strings.Contains(block, "[4]") || strings.Contains(block, "10.0.0.1") {
		t.Fatalf("a skipped or empty source reached the context: %q", block)
	}
	last := msgs[2].Content
	if !strings.Contains(last, "Current note:\n\ncurrent note body\n\nQuestion: Who is quick?") {
		t.Fatalf("user message = %q", last)
	}
}

func TestBuildPromptCapsSearchSourcesToContextChunks(t *testing.T) {
	registry := NewRegistry(testLogger(), 2)
	sources := []core.AISource{
		{Kind: core.ContextSearch, Title: "a", Excerpt: "one"},
		{Kind: core.ContextSearch, Title: "b", Excerpt: "two"},
		{Kind: core.ContextSearch, Title: "c", Excerpt: "three"},
		{Kind: core.ContextLinked, Title: "d", Excerpt: "four"},
	}
	msgs := registry.BuildMessages(core.AIRequest{Task: core.TaskAsk, Question: "q"}, sources)
	block := msgs[1].Content
	if strings.Contains(block, "[3] search: c") || !strings.Contains(block, "[4] linked: d") {
		t.Fatalf("search sources were not capped alone: %q", block)
	}
}

func TestBuildPromptSourcesShareTheBudget(t *testing.T) {
	long := strings.Repeat("x", MaxContextRunes)
	sources := []core.AISource{{Title: "first", Excerpt: long}, {Title: "second", Excerpt: long}}
	msgs := NewRegistry(testLogger(), 0).BuildMessages(core.AIRequest{Task: core.TaskAsk, Question: "q"}, sources)
	block := msgs[1].Content
	if strings.Contains(block, "[2] note: second") {
		t.Fatal("the second source was not dropped")
	}
	if n := utf8.RuneCountInString(block); n > MaxContextRunes+300 {
		t.Fatalf("context block is %d runes", n)
	}
}

func TestBuildPromptHonorsBudget(t *testing.T) {
	registry := testRegistry()
	in := ports.PromptInput{Budget: 100, Sources: []core.AISource{
		{Kind: core.ContextNote, Title: "a", Excerpt: strings.Repeat("a", 80)},
		{Kind: core.ContextNote, Title: "b", Excerpt: strings.Repeat("b", 80)},
	}}
	msgs := registry.BuildPrompt(core.AIRequest{Task: core.TaskSummarize, Text: "0123456789"}, in)
	block := msgs[1].Content
	if !strings.Contains(block, "[1] note: a") || strings.Contains(block, "[2] note: b") {
		t.Fatalf("budget of 100 kept %q", block)
	}

	in = ports.PromptInput{Budget: 200, Sources: []core.AISource{{Kind: core.ContextNote, Title: "c", Excerpt: strings.Repeat("c", 300)}}}
	msgs = registry.BuildPrompt(core.AIRequest{Task: core.TaskSummarize}, in)
	block = msgs[1].Content
	if strings.Count(block, "c") < 200 || strings.Contains(block, strings.Repeat("c", 201)) || !strings.Contains(block, truncationMarker) {
		t.Fatalf("source was not cut to the budget: %d runes", utf8.RuneCountInString(block))
	}
}

func TestBuildPromptAskTextKeepsHalfTheBudget(t *testing.T) {
	long := strings.Repeat("x", MaxContextRunes)
	msgs := testRegistry().BuildMessages(core.AIRequest{Task: core.TaskAsk, Question: "q", Text: long}, nil)
	last := msgs[len(msgs)-1].Content
	if !strings.Contains(last, truncationMarker) {
		t.Fatal("ask note text was not marked as cut")
	}
	if kept := strings.Count(strings.Replace(last, truncationMarker, "", 1), "x"); kept != MaxContextRunes/2 {
		t.Fatalf("ask kept %d runes of note text", kept)
	}
}

func TestBuildMessagesAskHistory(t *testing.T) {
	var history []core.AIMessage
	for i := 0; i < 14; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		history = append(history, core.AIMessage{Role: role, Content: fmt.Sprintf("turn %d", i)})
	}
	history = append(history, core.AIMessage{Role: "system", Content: "dropped"})
	history = append(history, core.AIMessage{Role: "user", Content: "  "})

	req := core.AIRequest{Task: core.TaskAsk, Question: "q", History: history}
	msgs := testRegistry().BuildMessages(req, nil)
	// One system prompt plus ten history turns plus the final user message.
	if len(msgs) != 12 {
		t.Fatalf("transcript length = %d: %s", len(msgs), joinMessages(msgs))
	}
	if msgs[1].Content != "turn 4" || msgs[10].Content != "turn 13" {
		t.Fatalf("history turns = %q .. %q", msgs[1].Content, msgs[10].Content)
	}
	if strings.Contains(joinMessages(msgs), "dropped") {
		t.Fatal("a system turn leaked out of the history")
	}
}

func TestBuildMessagesRefinementCarriesHistory(t *testing.T) {
	req := core.AIRequest{
		Task:        core.TaskImprove,
		Text:        "body",
		Output:      core.AIOutputReplace,
		Instruction: "make it friendlier",
		History: []core.AIMessage{
			{Role: "user", Content: "Improve"},
			{Role: "assistant", Content: "the earlier result"},
		},
	}
	msgs := testRegistry().BuildMessages(req, nil)
	// The system prompt, two history turns and the user message.
	if len(msgs) != 4 {
		t.Fatalf("transcript = %s", joinMessages(msgs))
	}
	if msgs[2].Role != "assistant" || msgs[2].Content != "the earlier result" {
		t.Fatalf("history turn = %+v", msgs[2])
	}
	system := msgs[0].Content
	for _, want := range []string{"clarity and flow", "Additional instruction: make it friendlier", refineInstruction, outputReplaceInstruction} {
		if !strings.Contains(system, want) {
			t.Fatalf("system message lacks %q: %s", want, system)
		}
	}
	if msgs[3].Role != "user" || !strings.Contains(msgs[3].Content, "body") {
		t.Fatalf("user message = %+v", msgs[3])
	}

	// A history of user turns only is no refinement.
	req.History = []core.AIMessage{{Role: "user", Content: "earlier"}}
	msgs = testRegistry().BuildMessages(req, nil)
	if strings.Contains(msgs[0].Content, refineInstruction) {
		t.Fatalf("refinement instruction without an earlier result: %s", msgs[0].Content)
	}
}

func TestBuildMessagesOutputModes(t *testing.T) {
	modes := map[string]string{
		core.AIOutputReplace: outputReplaceInstruction,
		core.AIOutputInsert:  outputInsertInstruction,
		core.AIOutputAnswer:  outputAnswerInstruction,
	}
	all := []string{outputReplaceInstruction, outputInsertInstruction, outputAnswerInstruction}
	for _, task := range []core.AITask{core.TaskImprove, core.TaskContinue, core.TaskAsk, core.TaskCustom, core.TaskDescribe} {
		for mode, want := range modes {
			req := core.AIRequest{Task: task, Text: "text", Question: "why?", Instruction: "do it", Output: mode}
			msgs := testRegistry().BuildMessages(req, nil)
			system := msgs[0].Content
			if !strings.HasSuffix(system, want) {
				t.Fatalf("%s/%s system message does not end with the mode instruction: %s", task, mode, system)
			}
			for _, other := range all {
				if other != want && strings.Contains(system, other) {
					t.Fatalf("%s/%s system message carries another mode: %s", task, mode, system)
				}
			}
			if strings.Contains(msgs[len(msgs)-1].Content, "Output mode") {
				t.Fatalf("%s/%s the mode leaked into the user message", task, mode)
			}
		}
		for _, output := range []string{"", "sideways"} {
			msgs := testRegistry().BuildMessages(core.AIRequest{Task: task, Text: "text", Output: output}, nil)
			if strings.Contains(msgs[0].Content, "Output mode") {
				t.Fatalf("%s with output %q adds a mode instruction: %s", task, output, msgs[0].Content)
			}
		}
	}
	// A custom task keeps its instruction as the task instruction, once.
	msgs := testRegistry().BuildMessages(core.AIRequest{Task: core.TaskCustom, Text: "t", Instruction: "shout", Output: core.AIOutputInsert}, nil)
	if strings.Count(msgs[0].Content, "shout") != 1 || strings.Contains(msgs[0].Content, "Additional instruction") {
		t.Fatalf("custom instruction = %s", msgs[0].Content)
	}
	if !core.ValidAIOutput("") || !core.ValidAIOutput(core.AIOutputAnswer) || core.ValidAIOutput("sideways") {
		t.Fatal("ValidAIOutput")
	}
}

func TestBuildMessagesTruncatesText(t *testing.T) {
	req := core.AIRequest{Task: core.TaskSummarize, Text: strings.Repeat("é", MaxContextRunes+500)}
	msgs := testRegistry().BuildMessages(req, nil)
	last := msgs[len(msgs)-1].Content
	if !strings.HasSuffix(last, truncationMarker) {
		t.Fatalf("truncation marker missing from a %d rune message", utf8.RuneCountInString(last))
	}
	if !utf8.ValidString(last) {
		t.Fatal("truncation broke a rune boundary")
	}
	body := strings.TrimPrefix(strings.TrimSuffix(last, truncationMarker), "Text:\n\n")
	if n := utf8.RuneCountInString(body); n != MaxContextRunes {
		t.Fatalf("kept %d runes", n)
	}
}

func TestTruncateText(t *testing.T) {
	tests := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"short text untouched", "abc", 10, "abc"},
		{"exact length untouched", "abc", 3, "abc"},
		{"cut marks the text", "abcdef", 3, "abc" + truncationMarker},
		{"zero max untouched", "abc", 0, "abc"},
		{"multibyte cut", "ééé", 2, "éé" + truncationMarker},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := truncateText(tc.in, tc.max); got != tc.want {
				t.Fatalf("truncateText(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
			}
		})
	}
}

// transcriptRunes counts the runes of every message.
func transcriptRunes(msgs []core.AIMessage) int {
	n := 0
	for _, m := range msgs {
		n += utf8.RuneCountInString(m.Content)
	}
	return n
}

// TestBuildMessagesCapsNonTextFields covers the FuzzBuildMessages failures
// where a huge question, history turn, instruction or language went into the
// prompt uncut.
func TestBuildMessagesCapsNonTextFields(t *testing.T) {
	huge := strings.Repeat("é", 200000)
	registry := testRegistry()

	ask := registry.BuildMessages(core.AIRequest{
		Task:     core.TaskAsk,
		Question: huge,
		History: []core.AIMessage{
			{Role: "user", Content: "old turn"},
			{Role: "assistant", Content: huge},
			{Role: "user", Content: "newest turn"},
		},
	}, nil)
	if n := transcriptRunes(ask); n > maxQuestionRunes+maxHistoryRunes+2000 {
		t.Fatalf("ask transcript is %d runes", n)
	}
	if ask[len(ask)-2].Content != "newest turn" {
		t.Fatalf("newest history turn was not kept: %q", ask[len(ask)-2].Content)
	}
	for _, m := range ask {
		if m.Content == "old turn" {
			t.Fatal("a turn older than the cut one was kept")
		}
	}

	custom := registry.BuildMessages(core.AIRequest{Task: core.TaskCustom, Instruction: huge, Text: "body"}, nil)
	if n := transcriptRunes(custom); n > maxInstructionRunes+2000 {
		t.Fatalf("custom transcript is %d runes", n)
	}

	translate := registry.BuildMessages(core.AIRequest{Task: core.TaskTranslate, Language: huge, Text: "hola"}, nil)
	if n := transcriptRunes(translate); n > maxLanguageRunes+2000 {
		t.Fatalf("translate transcript is %d runes", n)
	}

	titled := registry.BuildMessages(core.AIRequest{Task: core.TaskAsk, Question: "q"},
		[]core.AISource{{Title: huge, Path: huge, Excerpt: "x"}})
	if n := transcriptRunes(titled); n > maxSourceTitleRunes+maxSourceLocatorRunes+2000 {
		t.Fatalf("source header transcript is %d runes", n)
	}
}

func TestBuildMessagesEmptyTextOmitsBlock(t *testing.T) {
	msgs := testRegistry().BuildMessages(core.AIRequest{Task: core.TaskTitle}, nil)
	if len(msgs) != 2 {
		t.Fatalf("transcript = %+v", msgs)
	}
	if strings.Contains(msgs[1].Content, "Text:") || strings.TrimSpace(msgs[1].Content) == "" {
		t.Fatalf("user message = %q", msgs[1].Content)
	}
}

func TestBuildPromptPutsImagesOnTheUserMessage(t *testing.T) {
	image := core.AIImage{MediaType: "image/png", Data: []byte("png"), Name: "chart.png"}
	in := ports.PromptInput{
		Images: []core.AIImage{image},
		Sources: []core.AISource{{
			Kind: core.ContextFile, FileID: "a1", Title: "chart.png", Path: "/files/s/a1/chart.png",
		}},
	}
	msgs := testRegistry().BuildPrompt(core.AIRequest{Task: core.TaskDescribe}, in)
	if len(msgs) != 3 {
		t.Fatalf("transcript = %s", joinMessages(msgs))
	}
	if !strings.Contains(msgs[1].Content, "[1] file: chart.png (/files/s/a1/chart.png)\nImage attached to the user message.") {
		t.Fatalf("context = %q", msgs[1].Content)
	}
	last := msgs[2]
	if len(last.Images) != 1 || last.Images[0].Name != "chart.png" || last.Content != "The images are attached." {
		t.Fatalf("user message = %+v", last)
	}
	for _, m := range msgs[:2] {
		if len(m.Images) > 0 {
			t.Fatalf("images on a %s message", m.Role)
		}
	}
}

func TestBuildPromptDoesNotRepeatTheSelection(t *testing.T) {
	const selection = "the selected paragraph"
	sources := []core.AISource{
		{Kind: core.ContextSelection, Title: "Selection", Excerpt: selection},
		{Kind: core.ContextNote, NoteID: "n1", Title: "Plan", Path: "plan.md", Excerpt: "intro, " + selection + ", outro"},
	}
	msgs := testRegistry().BuildMessages(core.AIRequest{Task: core.TaskImprove, Text: selection}, sources)
	joined := joinMessages(msgs)
	if !strings.Contains(msgs[1].Content, "[1] selection: Selection\nThe text in the user message.") {
		t.Fatalf("context = %q", msgs[1].Content)
	}
	if n := strings.Count(joined, selection); n != 2 {
		t.Fatalf("selection appears %d times, want once in the user message and once in the note", n)
	}
}

func TestBuildPromptContextWithoutText(t *testing.T) {
	sources := []core.AISource{{Kind: core.ContextURL, URL: "https://example.com", Title: "Example", Excerpt: "page text"}}
	msgs := testRegistry().BuildMessages(core.AIRequest{Task: core.TaskSummarize}, sources)
	if last := msgs[len(msgs)-1].Content; !strings.Contains(last, "context above") {
		t.Fatalf("user message = %q", last)
	}
}
