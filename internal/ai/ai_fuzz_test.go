package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gamemaker1/markspace/internal/core"
)

// fuzzPromptOverhead is the most text BuildMessages may add on top of
// MaxContextRunes of note text: the system prompt, the task instruction, the
// labels, the truncation markers, and the capped question, history,
// instruction and language.
const fuzzPromptOverhead = 10000

// streamBody is the response the fuzz server sends, replaced for each input.
type streamBody struct {
	mu          sync.Mutex
	body        []byte
	contentType string
}

func (s *streamBody) set(body []byte, contentType string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.body, s.contentType = body, contentType
}

func (s *streamBody) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	body, contentType := s.body, s.contentType
	s.mu.Unlock()
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}

var streamContentTypes = []string{"text/event-stream", "application/json", "application/x-ndjson", ""}

// FuzzStreamReaders feeds arbitrary response bodies to the three streaming
// readers. Each stream must close its channel, and deliver exactly one Done
// or Err chunk, with nothing after it.
func FuzzStreamReaders(f *testing.F) {
	for _, seed := range []string{
		"",
		"data: [DONE]\n",
		"data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: {\"usage\":{\"prompt_tokens\":3}}\n\ndata: [DONE]\n\n",
		"data: {\"error\":{\"message\":\"boom\"}}\n",
		"{\"choices\":[{\"message\":{\"content\":\"plain\"}}]}",
		"{\"message\":{\"content\":\"a\"},\"done\":false}\n{\"message\":{\"content\":\"b\"},\"done\":true,\"eval_count\":2}\n",
		"{\"error\":\"model missing\"}\n",
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":5}}}\n\n" +
			"data: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"x\"}}\n\ndata: {\"type\":\"message_stop\"}\n",
		"data: {\"type\":\"error\",\"error\":{\"message\":\"overloaded\"}}\n",
		"data: not json\r\ndata:\r\n\r\n",
		"data: " + strings.Repeat("x", 1<<20+10) + "\n",
		"\x00\xff\xfe",
	} {
		for i := range streamContentTypes {
			f.Add([]byte(seed), uint8(i))
		}
	}
	server := &streamBody{}
	ts := httptest.NewServer(server)
	f.Cleanup(ts.Close)
	registry := NewRegistry(testLogger(), 4)
	var providers []core.AIProvider
	for _, kind := range core.AIProviderKinds {
		if kind == core.ProviderComfyUI {
			// ComfyUI has no chat stream.
			continue
		}
		p, err := registry.New(&core.AIProviderConfig{
			ID: "fuzz-" + string(kind), Kind: kind, BaseURL: ts.URL, Model: "m", TimeoutSeconds: 5,
		})
		if err != nil {
			f.Fatalf("New %s: %v", kind, err)
		}
		providers = append(providers, p)
	}
	msgs := []core.AIMessage{{Role: "system", Content: "s"}, {Role: "user", Content: "hi"}}

	f.Fuzz(func(t *testing.T, body []byte, ct uint8) {
		server.set(body, streamContentTypes[int(ct)%len(streamContentTypes)])
		for _, p := range providers {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			ch, err := p.Stream(ctx, msgs, core.AIOptions{})
			if err != nil {
				cancel()
				t.Fatalf("%s Stream: %v", p.Kind(), err)
			}
			terminal := 0
			watchdog := time.NewTimer(4 * time.Second)
		read:
			for {
				select {
				case chunk, ok := <-ch:
					if !ok {
						break read
					}
					if terminal > 0 {
						t.Fatalf("%s sent %+v after the terminal chunk\nbody: %q", p.Kind(), chunk, body)
					}
					if chunk.Done || chunk.Err != nil {
						terminal++
					}
				case <-watchdog.C:
					t.Fatalf("%s stream did not close within 4 s\nbody: %q", p.Kind(), body)
				}
			}
			watchdog.Stop()
			if terminal != 1 && ctx.Err() == nil {
				t.Fatalf("%s stream closed with %d terminal chunks\nbody: %q", p.Kind(), terminal, body)
			}
			cancel()
		}
	})
}

// fuzzTasks are the tasks BuildMessages knows, plus a slot for a raw string.
var fuzzTasks = []core.AITask{
	core.TaskSummarize, core.TaskImprove, core.TaskGrammar, core.TaskShorten, core.TaskExpand,
	core.TaskTranslate, core.TaskContinue, core.TaskTitle, core.TaskTags, core.TaskOutline,
	core.TaskActions, core.TaskAsk, core.TaskCustom,
}

// grow repeats unit rep times, capped at about 200 000 bytes, so the fuzzer
// reaches large prompts without large inputs.
func grow(unit string, rep uint16) string {
	if unit == "" {
		return ""
	}
	n := int(rep)
	if n*len(unit) > 200000 {
		n = 200000 / len(unit)
	}
	return strings.Repeat(unit, n)
}

// FuzzBuildMessages checks that BuildMessages never panics, always opens with
// the system prompt and ends with a user message, and never exceeds
// MaxContextRunes of text by more than a fixed overhead.
func FuzzBuildMessages(f *testing.F) {
	f.Add(uint8(0), "", "text", uint16(1), "", uint16(0), "", "", uint16(0), "user", "turn", uint16(1), "title", "excerpt", uint16(1))
	f.Add(uint8(11), "", "é", uint16(30000), "", uint16(0), "", "why?", uint16(30000), "assistant", "turn", uint16(3000), "t", "x", uint16(30000))
	f.Add(uint8(13), "mystery", "body", uint16(1), "do it ", uint16(20000), "Klingon ", "", uint16(0), "system", "s", uint16(1), "", "", uint16(0))
	f.Add(uint8(5), "", "hola", uint16(1), "", uint16(0), "French ", "", uint16(0), "", "", uint16(0), "", "", uint16(0))
	f.Add(uint8(11), "", "", uint16(0), "", uint16(0), "", "q", uint16(1), "USER ", "hello ", uint16(40000), "", "  ", uint16(3))
	registry := NewRegistry(testLogger(), 4)
	f.Fuzz(func(t *testing.T, taskIdx uint8, rawTask, text string, textRep uint16, instruction string, instrRep uint16,
		language, question string, questionRep uint16, histRole, histContent string, histRep uint16,
		srcTitle, srcExcerpt string, srcRep uint16) {
		task := core.AITask(rawTask)
		if i := int(taskIdx) % (len(fuzzTasks) + 1); i < len(fuzzTasks) {
			task = fuzzTasks[i]
		}
		req := core.AIRequest{
			Task:        task,
			Text:        grow(text, textRep),
			Instruction: grow(instruction, instrRep),
			Language:    grow(language, instrRep),
			Question:    grow(question, questionRep),
		}
		for i := 0; i < 12; i++ {
			role := histRole
			if i%2 == 1 {
				role = "assistant"
			}
			req.History = append(req.History, core.AIMessage{Role: role, Content: grow(histContent, histRep)})
		}
		var sources []core.AISource
		for i := 0; i < 6; i++ {
			sources = append(sources, core.AISource{Title: grow(srcTitle, srcRep), Excerpt: grow(srcExcerpt, srcRep)})
		}

		msgs := registry.BuildMessages(req, sources)
		if len(msgs) < 2 {
			t.Fatalf("transcript has %d messages", len(msgs))
		}
		if msgs[0].Role != "system" {
			t.Fatalf("first message role = %q", msgs[0].Role)
		}
		if last := msgs[len(msgs)-1]; last.Role != "user" {
			t.Fatalf("last message role = %q", last.Role)
		}
		total := 0
		for _, m := range msgs {
			total += utf8.RuneCountInString(m.Content)
		}
		if total > MaxContextRunes+fuzzPromptOverhead {
			t.Fatalf("task %q transcript is %d runes, over %d (text %d, question %d, history turn %d, source %d, instruction %d)",
				task, total, MaxContextRunes+fuzzPromptOverhead, utf8.RuneCountInString(req.Text),
				utf8.RuneCountInString(req.Question), utf8.RuneCountInString(req.History[0].Content),
				utf8.RuneCountInString(sources[0].Excerpt), utf8.RuneCountInString(req.Instruction))
		}
	})
}

// FuzzEndpoint checks that endpoint never produces a doubled "/v1" segment
// that neither of its inputs contained.
func FuzzEndpoint(f *testing.F) {
	for _, seed := range [][2]string{
		{"http://h/v1", "/v1/chat/completions"},
		{"http://h/v1/", "/v1/models"},
		{"http://h/v1//", "v1/models"},
		{"http://h", "/v1/chat/completions"},
		{"http://h/v1", "/v1"},
		{"http://h/v1", ""},
		{"http://h/api/v1", "/v1x"},
		{"http://v1", "/v1/x"},
		{"", "/v1/"},
		{"/v1", "//v1/a"},
	} {
		f.Add(seed[0], seed[1])
	}
	f.Fuzz(func(t *testing.T, base, path string) {
		got := endpoint(base, path)
		if strings.Contains(base, "v1/v1") || strings.Contains(path, "v1/v1") {
			return
		}
		trimmed := strings.TrimRight(base, "/")
		if strings.HasSuffix(trimmed, "/v1") && (strings.Contains(got, "/v1/v1/") || strings.HasSuffix(got, "/v1/v1")) {
			t.Fatalf("endpoint(%q, %q) = %q doubles /v1", base, path, got)
		}
	})
}
