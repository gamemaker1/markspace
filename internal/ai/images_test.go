package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

// textOnlyTranscript is a transcript with no images, including characters
// that JSON escapes.
var textOnlyTranscript = []core.AIMessage{
	{Role: "system", Content: "rules <b> & \"quotes\""},
	{Role: "user", Content: "hi\nthere ✓"},
	{Role: "assistant", Content: "answer"},
	{Role: "user", Content: "again"},
}

// mustJSON encodes v or fails the test.
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return out
}

// TestTextOnlyRequestsEncodeAsBefore compares each protocol's request body
// with the shape it had when messages were core.AIMessage values, so servers
// that never see images get byte for byte the same request.
func TestTextOnlyRequestsEncodeAsBefore(t *testing.T) {
	cfg := &core.AIProviderConfig{ID: "p", BaseURL: "http://x", Model: "m", Temperature: 0.2, MaxTokens: 64}
	opts := core.AIOptions{Stop: []string{"END"}}

	type legacyOpenAI struct {
		Model         string              `json:"model,omitempty"`
		Messages      []core.AIMessage    `json:"messages"`
		Temperature   float64             `json:"temperature"`
		MaxTokens     int                 `json:"max_tokens,omitempty"`
		Stop          []string            `json:"stop,omitempty"`
		Stream        bool                `json:"stream"`
		StreamOptions *openaiStreamOption `json:"stream_options,omitempty"`
	}
	openai := newOpenAIProvider(cfg, nil, testLogger())
	got := mustJSON(t, openai.chatRequest(textOnlyTranscript, opts, true))
	want := mustJSON(t, legacyOpenAI{
		Model: "m", Messages: textOnlyTranscript, Temperature: 0.2, MaxTokens: 64,
		Stop: []string{"END"}, Stream: true, StreamOptions: &openaiStreamOption{IncludeUsage: true},
	})
	if !bytes.Equal(got, want) {
		t.Errorf("openai body changed:\n got %s\nwant %s", got, want)
	}

	type legacyOllama struct {
		Model    string           `json:"model"`
		Messages []core.AIMessage `json:"messages"`
		Stream   bool             `json:"stream"`
		Options  ollamaOptions    `json:"options"`
	}
	ollama := newOllamaProvider(cfg, nil, testLogger())
	got = mustJSON(t, ollama.chatRequest(textOnlyTranscript, opts, false))
	want = mustJSON(t, legacyOllama{
		Model: "m", Messages: textOnlyTranscript,
		Options: ollamaOptions{Temperature: 0.2, NumPredict: 64, Stop: []string{"END"}},
	})
	if !bytes.Equal(got, want) {
		t.Errorf("ollama body changed:\n got %s\nwant %s", got, want)
	}

	type legacyAnthropic struct {
		Model         string           `json:"model,omitempty"`
		MaxTokens     int              `json:"max_tokens"`
		Temperature   float64          `json:"temperature"`
		System        string           `json:"system,omitempty"`
		Messages      []core.AIMessage `json:"messages"`
		StopSequences []string         `json:"stop_sequences,omitempty"`
		Stream        bool             `json:"stream"`
	}
	anthropic := newAnthropicProvider(cfg, nil, testLogger())
	payload, err := anthropic.chatRequest(textOnlyTranscript, opts, true)
	if err != nil {
		t.Fatalf("anthropic chatRequest: %v", err)
	}
	system, turns := splitSystem(textOnlyTranscript)
	got = mustJSON(t, payload)
	want = mustJSON(t, legacyAnthropic{
		Model: "m", MaxTokens: 64, Temperature: 0.2, System: system, Messages: turns,
		StopSequences: []string{"END"}, Stream: true,
	})
	if !bytes.Equal(got, want) {
		t.Errorf("anthropic body changed:\n got %s\nwant %s", got, want)
	}
}

// captureServer answers every call with reply and stores the request body.
func captureServer(t *testing.T, reply string, body *[]byte) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*body = readAll(t, r)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, reply)
	}))
	t.Cleanup(server.Close)
	return server
}

// imageTranscript is a transcript whose last turn carries two images.
func imageTranscript() ([]core.AIMessage, []core.AIImage) {
	images := []core.AIImage{
		{MediaType: "image/png", Data: []byte("\x89PNG one"), Name: "one.png"},
		{MediaType: "image/jpeg", Data: []byte("\xFF\xD8\xFF two"), Name: "two.jpg"},
	}
	return []core.AIMessage{
		{Role: "system", Content: "rules"},
		{Role: "user", Content: "Describe these.", Images: images},
	}, images
}

func TestOpenAISendsImagesAsContentParts(t *testing.T) {
	var body []byte
	server := captureServer(t, `{"choices":[{"message":{"content":"ok"}}]}`, &body)
	msgs, images := imageTranscript()
	provider := testProvider(t, core.ProviderOpenAI, server.URL, nil)
	if _, err := provider.Complete(context.Background(), msgs, core.AIOptions{}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	var sent struct {
		Messages []json.RawMessage `json:"messages"`
	}
	decodeBody(t, body, &sent)
	if string(sent.Messages[0]) != `{"role":"system","content":"rules"}` {
		t.Fatalf("text turn = %s", sent.Messages[0])
	}
	var turn struct {
		Role    string `json:"role"`
		Content []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			ImageURL struct {
				URL string `json:"url"`
			} `json:"image_url"`
		} `json:"content"`
	}
	decodeBody(t, sent.Messages[1], &turn)
	if len(turn.Content) != 3 || turn.Content[0].Type != "text" || turn.Content[0].Text != "Describe these." {
		t.Fatalf("parts = %+v", turn.Content)
	}
	for i, img := range images {
		part := turn.Content[i+1]
		want := "data:" + img.MediaType + ";base64," + base64.StdEncoding.EncodeToString(img.Data)
		if part.Type != "image_url" || part.ImageURL.URL != want {
			t.Fatalf("part %d = %+v, want url %q", i+1, part, want)
		}
	}
}

func TestOllamaSendsImagesAsBase64Strings(t *testing.T) {
	var body []byte
	server := captureServer(t, `{"message":{"role":"assistant","content":"ok"},"done":true}`, &body)
	msgs, images := imageTranscript()
	provider := testProvider(t, core.ProviderOllama, server.URL, nil)
	if _, err := provider.Complete(context.Background(), msgs, core.AIOptions{}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	var sent struct {
		Messages []json.RawMessage `json:"messages"`
	}
	decodeBody(t, body, &sent)
	if string(sent.Messages[0]) != `{"role":"system","content":"rules"}` {
		t.Fatalf("text turn = %s", sent.Messages[0])
	}
	var turn ollamaMessage
	decodeBody(t, sent.Messages[1], &turn)
	if turn.Content != "Describe these." || len(turn.Images) != 2 {
		t.Fatalf("turn = %+v", turn)
	}
	for i, img := range images {
		if turn.Images[i] != base64.StdEncoding.EncodeToString(img.Data) {
			t.Fatalf("image %d = %q", i, turn.Images[i])
		}
	}
}

func TestAnthropicSendsImageBlocksBeforeText(t *testing.T) {
	var body []byte
	server := captureServer(t, `{"content":[{"type":"text","text":"ok"}]}`, &body)
	msgs, images := imageTranscript()
	// A second user turn right after merges into the image turn.
	msgs = append(msgs, core.AIMessage{Role: "user", Content: "And this.", Images: images[:1]})
	provider := testProvider(t, core.ProviderAnthropic, server.URL, nil)
	if _, err := provider.Complete(context.Background(), msgs, core.AIOptions{}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	var sent struct {
		System   string `json:"system"`
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Type   string `json:"type"`
				Text   string `json:"text"`
				Source struct {
					Type      string `json:"type"`
					MediaType string `json:"media_type"`
					Data      string `json:"data"`
				} `json:"source"`
			} `json:"content"`
		} `json:"messages"`
	}
	decodeBody(t, body, &sent)
	if sent.System != "rules" || len(sent.Messages) != 1 {
		t.Fatalf("request = %s", body)
	}
	blocks := sent.Messages[0].Content
	if len(blocks) != 4 {
		t.Fatalf("blocks = %+v", blocks)
	}
	wantImages := append(append([]core.AIImage(nil), images...), images[0])
	for i, img := range wantImages {
		b := blocks[i]
		if b.Type != "image" || b.Source.Type != "base64" || b.Source.MediaType != img.MediaType ||
			b.Source.Data != base64.StdEncoding.EncodeToString(img.Data) {
			t.Fatalf("block %d = %+v", i, b)
		}
	}
	if blocks[3].Type != "text" || blocks[3].Text != "Describe these.\n\nAnd this." {
		t.Fatalf("text block = %+v", blocks[3])
	}
}

func TestSplitSystemDoesNotAliasImages(t *testing.T) {
	images := make([]core.AIImage, 1, 4)
	images[0] = core.AIImage{MediaType: "image/png", Data: []byte("a")}
	msgs := []core.AIMessage{
		{Role: "user", Content: "one", Images: images},
		{Role: "user", Content: "two", Images: []core.AIImage{{MediaType: "image/gif", Data: []byte("b")}}},
	}
	_, turns := splitSystem(msgs)
	if len(turns) != 1 || len(turns[0].Images) != 2 {
		t.Fatalf("turns = %+v", turns)
	}
	if len(msgs[0].Images) != 1 || images[:2][1].MediaType != "" {
		t.Fatal("merging wrote into the caller's image slice")
	}
}
