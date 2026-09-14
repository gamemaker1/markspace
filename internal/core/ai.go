package core

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// AIProviderKind selects the wire protocol used to talk to a model server.
type AIProviderKind string

const (
	// ProviderOpenAI speaks the OpenAI /v1/chat/completions protocol. vLLM,
	// llama.cpp server, LocalAI, text-generation-webui, LM Studio, Groq and
	// OpenRouter all accept it.
	ProviderOpenAI AIProviderKind = "openai"
	// ProviderOllama speaks the native Ollama /api/chat protocol.
	ProviderOllama AIProviderKind = "ollama"
	// ProviderAnthropic speaks the Anthropic /v1/messages protocol.
	ProviderAnthropic AIProviderKind = "anthropic"
	// ProviderComfyUI runs image workflows on a ComfyUI server. It has no
	// chat or embedding support.
	ProviderComfyUI AIProviderKind = "comfyui"
)

// AIProviderKinds lists every supported protocol.
var AIProviderKinds = []AIProviderKind{ProviderOpenAI, ProviderOllama, ProviderAnthropic, ProviderComfyUI}

// AIProviderConfig configures one model server. The yaml tags are the keys
// of a provider entry in the configuration file. Fields tagged yaml:"-" are
// runtime state and cannot be set from the file.
type AIProviderConfig struct {
	ID         string            `json:"id" yaml:"id"`
	Name       string            `json:"name" yaml:"name"`
	Kind       AIProviderKind    `json:"kind" yaml:"kind"`
	BaseURL    string            `json:"baseUrl" yaml:"baseUrl"`
	APIKey     string            `json:"-" yaml:"apiKey"`
	HasAPIKey  bool              `json:"hasApiKey" yaml:"-"`
	Model      string            `json:"model" yaml:"model"`
	EmbedModel string            `json:"embedModel,omitempty" yaml:"embedModel"`
	Models     []string          `json:"models,omitempty" yaml:"models"`
	Headers    map[string]string `json:"headers,omitempty" yaml:"headers"`
	// Vision marks every model of the provider as accepting images when its
	// listing and the catalogs say nothing about inputs.
	Vision bool `json:"vision" yaml:"vision"`
	// Preset names the preset the provider was created from, such as openai
	// or ollama. It supplies the vendor used to match catalog prices.
	Preset string `json:"preset,omitempty" yaml:"preset"`
	// Local marks a provider on the admin's own hardware. Its models cost 0
	// unless an admin sets a price.
	Local bool `json:"local" yaml:"local"`

	// ImageGeneration marks a provider that creates images from a prompt.
	ImageGeneration bool `json:"imageGeneration" yaml:"imageGeneration"`
	// ImageEditing marks a provider that changes an existing image.
	ImageEditing bool `json:"imageEditing" yaml:"imageEditing"`
	// ImageModel is the image model. For ComfyUI it is the checkpoint name.
	ImageModel string `json:"imageModel,omitempty" yaml:"imageModel"`
	// ImageSizes lists the sizes a user may pick, such as "1024x1024". An
	// empty list allows any WIDTHxHEIGHT of 3 or 4 digits up to 4096.
	ImageSizes []string `json:"imageSizes,omitempty" yaml:"imageSizes"`
	// ComfyGenerate and ComfyEdit replace the built-in ComfyUI workflow
	// templates. Each holds a workflow in ComfyUI API format.
	ComfyGenerate string `json:"comfyGenerate,omitempty" yaml:"comfyGenerate"`
	ComfyEdit     string `json:"comfyEdit,omitempty" yaml:"comfyEdit"`

	Temperature    float64 `json:"temperature" yaml:"temperature"`
	MaxTokens      int     `json:"maxTokens" yaml:"maxTokens"`
	TimeoutSeconds int     `json:"timeoutSeconds" yaml:"timeoutSeconds"`

	Enabled bool   `json:"enabled" yaml:"enabled"`
	Source  string `json:"source,omitempty" yaml:"-"` // "file" or "db"
	// OwnerID is the user who registered a personal provider. It is empty for
	// instance providers, which every user may call. A personal provider is
	// visible to and callable by its owner only.
	OwnerID string `json:"ownerId,omitempty" yaml:"-"`

	CreatedAt time.Time `json:"createdAt,omitempty" yaml:"-"`
	UpdatedAt time.Time `json:"updatedAt,omitempty" yaml:"-"`

	// ModelCount, RefreshedAt and RefreshError describe the catalog. Listings
	// fill them in.
	ModelCount   int       `json:"modelCount" yaml:"-"`
	RefreshedAt  time.Time `json:"refreshedAt,omitempty" yaml:"-"`
	RefreshError string    `json:"refreshError,omitempty" yaml:"-"`
}

// AIMessage is one chat turn. Role is "system", "user" or "assistant".
type AIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	// Images go to a vision model with this turn. Providers without vision
	// support reject a request that carries images.
	Images []AIImage `json:"images,omitempty"`
}

// AIImage is one image attached to a chat turn.
type AIImage struct {
	MediaType string `json:"mediaType"` // image/png, image/jpeg, image/webp or image/gif
	Data      []byte `json:"data"`      // raw bytes, base64 in JSON
	// Name labels the image in the prompt, usually the attachment file name.
	Name string `json:"name,omitempty"`
}

// AIOptions overrides provider defaults for one call.
type AIOptions struct {
	Model       string   `json:"model,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
	MaxTokens   int      `json:"maxTokens,omitempty"`
	Stop        []string `json:"stop,omitempty"`
}

// AIChunk is one streamed fragment. Done or Err ends the stream. A consumer
// must treat a closed channel as terminal, because a stream canceled mid
// flight can close without a final chunk.
type AIChunk struct {
	Delta string `json:"delta,omitempty"`
	Done  bool   `json:"done,omitempty"`
	Err   error  `json:"-"`

	// Sources is set on the final chunk and lists the context that went into
	// the prompt.
	Sources []AISource `json:"sources,omitempty"`
	// PromptTokens and CompletionTokens are set on the final chunk when the
	// provider reports usage.
	PromptTokens     int `json:"promptTokens,omitempty"`
	CompletionTokens int `json:"completionTokens,omitempty"`
	// Model is set on the final chunk with the model that answered, and
	// Provider with its provider id.
	Model    string `json:"model,omitempty"`
	Provider string `json:"provider,omitempty"`
	// Attempts is set on the final chunk with the number of targets tried.
	Attempts int `json:"attempts,omitempty"`
	// Budget is set on the final chunk once the actor or plugin has used 80
	// percent of a monthly budget.
	Budget *AIBudgetWarning `json:"budget,omitempty"`
}

// AITask names a canned prompt.
type AITask string

const (
	TaskSummarize AITask = "summarize"
	TaskImprove   AITask = "improve"
	TaskGrammar   AITask = "grammar"
	TaskShorten   AITask = "shorten"
	TaskExpand    AITask = "expand"
	TaskTranslate AITask = "translate"
	TaskContinue  AITask = "continue"
	TaskTitle     AITask = "title"
	TaskTags      AITask = "tags"
	TaskOutline   AITask = "outline"
	TaskActions   AITask = "actions"
	TaskAsk       AITask = "ask"
	TaskCustom    AITask = "custom"
	// TaskExplain explains the text or media in plain terms.
	TaskExplain AITask = "explain"
	// TaskDescribe describes attached images, for example as alt text.
	TaskDescribe AITask = "describe"
	// TaskExtract transcribes the text visible in attached images or files.
	TaskExtract AITask = "extract"
)

// AITasks lists every task the UI offers.
var AITasks = []AITask{
	TaskSummarize, TaskImprove, TaskGrammar, TaskShorten, TaskExpand,
	TaskTranslate, TaskContinue, TaskTitle, TaskTags, TaskOutline,
	TaskActions, TaskAsk, TaskCustom, TaskExplain, TaskDescribe, TaskExtract,
}

// AIContextKind names one kind of context a client attaches to a request.
type AIContextKind string

const (
	// ContextSelection is text the user selected. Text holds it.
	ContextSelection AIContextKind = "selection"
	// ContextNote is a whole note body. NoteID names it.
	ContextNote AIContextKind = "note"
	// ContextSection is the part of a note under one heading, down to the next
	// heading of the same or a higher level. NoteID and Heading name it.
	ContextSection AIContextKind = "section"
	// ContextLinked is every note that NoteID links to through a wikilink or
	// a relative markdown link, one level deep.
	ContextLinked AIContextKind = "linked"
	// ContextBacklinks is every note that links to NoteID.
	ContextBacklinks AIContextKind = "backlinks"
	// ContextFile is a file in a space. FileID names it. Images go to the
	// model as images, text files as text, PDFs as extracted text.
	ContextFile AIContextKind = "file"
	// ContextURL is a web page fetched by the server. URL names it.
	ContextURL AIContextKind = "url"
	// ContextSearch is the best matching chunks from the space for Query.
	ContextSearch AIContextKind = "search"
)

// AIContextRef is one piece of context attached to an AI request. The server
// resolves every reference with the actor's access, so a reference to a note
// or file the actor cannot view is dropped and reported.
type AIContextRef struct {
	Kind    AIContextKind `json:"kind"`
	NoteID  string        `json:"noteId,omitempty"`
	Heading string        `json:"heading,omitempty"`
	FileID  string        `json:"fileId,omitempty"`
	URL     string        `json:"url,omitempty"`
	Text    string        `json:"text,omitempty"`
	Query   string        `json:"query,omitempty"`
}

// AIRequest is one AI call from the editor or the ask panel.
type AIRequest struct {
	Task        AITask      `json:"task"`
	SpaceID     string      `json:"spaceId,omitempty"`
	NoteID      string      `json:"noteId,omitempty"`
	Text        string      `json:"text,omitempty"`
	Instruction string      `json:"instruction,omitempty"`
	Language    string      `json:"language,omitempty"`
	Question    string      `json:"question,omitempty"`
	History     []AIMessage `json:"history,omitempty"`
	// ProviderID and Model name a model chosen in the request. It goes first
	// when the user may call it.
	ProviderID string `json:"providerId,omitempty"`
	Model      string `json:"model,omitempty"`
	// Route is the routing task, such as edit, answer, chat or vision. Empty
	// derives it from the feature and the task.
	Route string `json:"route,omitempty"`
	// UseContext pulls related note chunks into the prompt for TaskAsk. It is
	// shorthand for one ContextSearch reference.
	UseContext bool `json:"useContext,omitempty"`
	// Context lists what the prompt should draw on, in priority order. When
	// the total exceeds the context budget, later references are cut first.
	Context []AIContextRef `json:"context,omitempty"`
	// Feature labels the call in usage statistics, such as ask, rewrite or
	// chat. Empty means the task name.
	Feature string `json:"feature,omitempty"`
	// PluginID names the plugin that made the call. Only the plugin host sets
	// it. The JSON API clears a client value.
	PluginID string `json:"pluginId,omitempty"`
	// Output is the output mode of an editor request: AIOutputReplace,
	// AIOutputInsert or AIOutputAnswer. Empty adds no mode instruction.
	Output string `json:"output,omitempty"`
}

// AI output modes. The prompt tells the model to return only the text that
// replaces the selection, only the text inserted at the cursor, or a
// markdown answer shown beside the note.
const (
	AIOutputReplace = "replace"
	AIOutputInsert  = "insert"
	AIOutputAnswer  = "answer"
)

// ValidAIOutput reports whether output is empty or a known output mode.
func ValidAIOutput(output string) bool {
	switch output {
	case "", AIOutputReplace, AIOutputInsert, AIOutputAnswer:
		return true
	}
	return false
}

// AIResult is a finished AI call.
type AIResult struct {
	Text     string `json:"text"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	// Route is the routing task and Attempts the number of targets tried.
	Route            string `json:"route,omitempty"`
	Attempts         int    `json:"attempts,omitempty"`
	PromptTokens     int    `json:"promptTokens,omitempty"`
	CompletionTokens int    `json:"completionTokens,omitempty"`
	// Elapsed is serialized as whole milliseconds under "elapsedMs".
	Elapsed time.Duration `json:"-"`
	Sources []AISource    `json:"sources,omitempty"`
	// Budget is set once the actor or plugin has used 80 percent of a
	// monthly budget.
	Budget *AIBudgetWarning `json:"budget,omitempty"`
}

// MarshalJSON writes the result with Elapsed as whole milliseconds in
// "elapsedMs". A time.Duration would otherwise serialize as nanoseconds.
func (r AIResult) MarshalJSON() ([]byte, error) {
	type plain AIResult
	return json.Marshal(struct {
		plain
		ElapsedMs int64 `json:"elapsedMs"`
	}{plain(r), r.Elapsed.Milliseconds()})
}

// AISource cites one piece of context that went into a prompt.
type AISource struct {
	Kind    AIContextKind `json:"kind,omitempty"`
	NoteID  string        `json:"noteId,omitempty"`
	FileID  string        `json:"fileId,omitempty"`
	URL     string        `json:"url,omitempty"`
	Title   string        `json:"title"`
	Path    string        `json:"path,omitempty"`
	Score   float64       `json:"score,omitempty"`
	Excerpt string        `json:"excerpt,omitempty"`
	// Truncated reports that the context budget cut this source short.
	Truncated bool `json:"truncated,omitempty"`
	// Skipped explains why a requested reference was left out, such as no
	// access, an unreachable URL or images on a model without vision.
	Skipped string `json:"skipped,omitempty"`
}

// AIProvider talks to one model server.
type AIProvider interface {
	ID() string
	Kind() AIProviderKind
	Config() *AIProviderConfig
	Complete(ctx context.Context, msgs []AIMessage, opts AIOptions) (*AIResult, error)
	Stream(ctx context.Context, msgs []AIMessage, opts AIOptions) (<-chan AIChunk, error)
	Embed(ctx context.Context, texts []string, model string) ([][]float32, error)
	ListModels(ctx context.Context) ([]string, error)
	Health(ctx context.Context) error
}

// ImageGenerator is implemented by providers that can create or edit images.
type ImageGenerator interface {
	// GenerateImages creates images, or edits req.Source when it is set.
	// progress, when not nil, receives the images finished so far and the
	// images expected.
	GenerateImages(ctx context.Context, req AIImageRequest, progress func(done, total int)) (*AIImageResult, error)
}

// AIImageRequest is one image generation or edit.
type AIImageRequest struct {
	Prompt         string
	NegativePrompt string
	Size           string // "1024x1024"
	N              int    // 1 to 4
	Model          string
	Seed           int64    // 0 means random
	Source         *AIImage // set for an edit
	Mask           *AIImage // PNG, transparent where the image may change
	// Queue, when not nil, receives the number of jobs ahead of this one
	// while it waits on a server with a queue. 0 means the job is running.
	Queue func(position int)
}

// AIImageResult is a finished image generation or edit.
type AIImageResult struct {
	Images        []AIImage
	RevisedPrompt string
	Model         string
	Usage         AIImageUsage
}

// AIImageUsage counts what one image call consumed. Token counts stay 0
// when the provider reports none.
type AIImageUsage struct {
	ImagesIn         int `json:"imagesIn"`
	ImagesOut        int `json:"imagesOut"`
	PromptTokens     int `json:"promptTokens,omitempty"`
	CompletionTokens int `json:"completionTokens,omitempty"`
}

// Image request limits.
const (
	// MaxImageN is the most images one request produces.
	MaxImageN = 4
	// MaxImageSide is the longest width or height a size may name.
	MaxImageSide = 4096
	// MaxComfyTemplateBytes caps a ComfyUI workflow template.
	MaxComfyTemplateBytes = 1 << 20
)

// ParseImageSize reads a size of the form WIDTHxHEIGHT, where each side has 3
// or 4 digits and is at most MaxImageSide.
func ParseImageSize(size string) (width, height int, ok bool) {
	w, h, found := strings.Cut(size, "x")
	if !found {
		return 0, 0, false
	}
	width, okW := imageSide(w)
	height, okH := imageSide(h)
	if !okW || !okH {
		return 0, 0, false
	}
	return width, height, true
}

// imageSide parses one side of an image size.
func imageSide(s string) (int, bool) {
	if len(s) < 3 || len(s) > 4 {
		return 0, false
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, n > 0 && n <= MaxImageSide
}

// ClampImageN limits an image count to 1 through MaxImageN.
func ClampImageN(n int) int {
	return min(max(n, 1), MaxImageN)
}

// ValidateImageFields checks the image settings of a provider. Only the
// openai and comfyui kinds may set the capability flags. Every size must
// parse, or be "auto" on an openai provider. A ComfyUI template must be a
// JSON object of at most MaxComfyTemplateBytes.
func (c *AIProviderConfig) ValidateImageFields() error {
	if (c.ImageGeneration || c.ImageEditing) && c.Kind != ProviderOpenAI && c.Kind != ProviderComfyUI {
		return Invalid("imageGeneration", "the "+string(c.Kind)+" kind cannot generate or edit images")
	}
	if len(c.ImageModel) > 512 {
		return Invalid("imageModel", "is longer than 512 bytes")
	}
	if len(c.ImageSizes) > 32 {
		return Invalid("imageSizes", "lists more than 32 sizes")
	}
	for _, size := range c.ImageSizes {
		if size == "auto" && c.Kind == ProviderOpenAI {
			continue
		}
		if _, _, ok := ParseImageSize(size); !ok {
			return Invalid("imageSizes", "size "+size+" is not WIDTHxHEIGHT with 3 or 4 digit sides up to 4096")
		}
	}
	if (c.ComfyGenerate != "" || c.ComfyEdit != "") && c.Kind != ProviderComfyUI {
		return Invalid("comfyGenerate", "workflow templates need the comfyui kind")
	}
	for field, tmpl := range map[string]string{"comfyGenerate": c.ComfyGenerate, "comfyEdit": c.ComfyEdit} {
		if tmpl == "" {
			continue
		}
		if len(tmpl) > MaxComfyTemplateBytes {
			return Invalid(field, "is larger than 1 MiB")
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(tmpl), &obj); err != nil {
			return Invalid(field, "is not a JSON object: "+err.Error())
		}
	}
	return nil
}

// AIThreadRetention is how long a conversation lives without activity.
const AIThreadRetention = 90 * 24 * time.Hour

// AI thread message roles.
const (
	AIRoleUser      = "user"
	AIRoleAssistant = "assistant"
)

// AI thread scopes. AIScopeNote draws on the note, and AIScopeSpace also
// searches the note's space.
const (
	AIScopeNote  = "note"
	AIScopeSpace = "space"
)

// AIThread is one user's conversation about a note in the AI rail tab. Only
// its owner can read, extend or delete it.
type AIThread struct {
	ID        string    `json:"id"`
	UserID    string    `json:"userId"`
	NoteID    string    `json:"noteId,omitempty"`
	SpaceID   string    `json:"spaceId,omitempty"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	// Messages lists the turns oldest first. A listing fills it.
	Messages []*AIThreadMessage `json:"messages"`
}

// AIThreadMessage is one turn of a conversation.
type AIThreadMessage struct {
	ID        string         `json:"id"`
	ThreadID  string         `json:"threadId"`
	Role      string         `json:"role"`
	Content   string         `json:"content"`
	Context   []AIContextRef `json:"context"`
	Sources   []AISource     `json:"sources"`
	Applied   bool           `json:"applied"`
	CreatedAt time.Time      `json:"createdAt"`
}

// AIThreadInput creates a conversation about a note.
type AIThreadInput struct {
	NoteID string `json:"noteId"`
	Title  string `json:"title,omitempty"`
}

// AIThreadMessageInput is one user turn. Context lists the chips of the
// composer. Scope is AIScopeNote or AIScopeSpace. ProviderID and Model name a
// model chosen with the model chip.
type AIThreadMessageInput struct {
	Content    string         `json:"content"`
	Context    []AIContextRef `json:"context,omitempty"`
	Scope      string         `json:"scope,omitempty"`
	ProviderID string         `json:"providerId,omitempty"`
	Model      string         `json:"model,omitempty"`
}

// AIThreadChunk is one streamed fragment of an answer in a conversation. The
// chunk with Done set carries the stored user and assistant turns and the
// routing task.
type AIThreadChunk struct {
	AIChunk
	Route       string           `json:"route,omitempty"`
	UserMessage *AIThreadMessage `json:"userMessage,omitempty"`
	Message     *AIThreadMessage `json:"message,omitempty"`
}

// AIThreadService is the conversation part of the AI service. Every method
// answers ErrNotFound for a thread of another user.
type AIThreadService interface {
	// ListAIThreads returns the actor's conversations about a note with
	// their messages, newest first. It needs view access on the note.
	ListAIThreads(ctx context.Context, actor *Actor, noteID string) ([]*AIThread, error)
	// CreateAIThread starts a conversation about a note the actor may view.
	CreateAIThread(ctx context.Context, actor *Actor, in AIThreadInput) (*AIThread, error)
	// StreamAIThreadMessage stores a user turn and streams the answer with
	// the routing and usage of StreamAI. The answer is stored when the
	// stream ends with Done.
	StreamAIThreadMessage(ctx context.Context, actor *Actor, threadID string, in AIThreadMessageInput) (<-chan AIThreadChunk, error)
	// SetAIMessageApplied marks an answer as applied to the note.
	SetAIMessageApplied(ctx context.Context, actor *Actor, threadID, messageID string, applied bool) error
	// DeleteAIThread deletes a conversation and its messages.
	DeleteAIThread(ctx context.Context, actor *Actor, threadID string) error
}

// AI image save modes of an image request.
const (
	// AIImageSaveNone returns the images as data URLs and saves nothing.
	AIImageSaveNone = "none"
	// AIImageSaveNew saves each image as a new file in the folder.
	AIImageSaveNew = "new"
	// AIImageSaveVersion saves an edit as a new version of the source file.
	AIImageSaveVersion = "version"
	// AIImageSaveCopy saves an edit as a copy named "<stem> edited.<ext>".
	AIImageSaveCopy = "copy"
)

// AIImageGenerateRequest is the body of POST /api/ai/image/stream. FileID
// makes the request an edit of that image file. Mask is a base64 PNG, with or
// without a data URL prefix, transparent where the image may change.
type AIImageGenerateRequest struct {
	SpaceID        string `json:"spaceId"`
	NoteID         string `json:"noteId,omitempty"`
	ParentID       string `json:"parentId,omitempty"`
	Prompt         string `json:"prompt"`
	NegativePrompt string `json:"negativePrompt,omitempty"`
	Size           string `json:"size,omitempty"`
	N              int    `json:"n,omitempty"`
	FileID         string `json:"fileId,omitempty"`
	Mask           string `json:"mask,omitempty"`
	ProviderID     string `json:"providerId,omitempty"`
	Model          string `json:"model,omitempty"`
	Save           string `json:"save"`
	Name           string `json:"name,omitempty"`
	// Route is the routing task, such as plugin.<id>.<purpose>. Empty means
	// image.generate, or image.edit when FileID is set.
	Route string `json:"route,omitempty"`
	// PluginID names the plugin that made the call, so usage and budgets
	// charge the plugin. Only the plugin host sets it. The JSON API clears a
	// client value.
	PluginID string `json:"pluginId,omitempty"`
}

// AIImageProgress is one progress frame of an image request. QueuePosition
// is set while the job waits on a server with a queue.
type AIImageProgress struct {
	Done          int  `json:"done"`
	Total         int  `json:"total"`
	QueuePosition *int `json:"queuePosition,omitempty"`
}

// AIImageOutput is one image returned without saving.
type AIImageOutput struct {
	DataURL string `json:"dataUrl"`
}

// AIImageGenerateResult is the done frame of an image request. Files holds
// the saved files, and Images the data URLs when the save mode is none.
type AIImageGenerateResult struct {
	Files         []*File         `json:"files"`
	Images        []AIImageOutput `json:"images,omitempty"`
	Usage         AIImageUsage    `json:"usage"`
	Provider      string          `json:"provider,omitempty"`
	Model         string          `json:"model,omitempty"`
	Route         string          `json:"route,omitempty"`
	Attempts      int             `json:"attempts,omitempty"`
	RevisedPrompt string          `json:"revisedPrompt,omitempty"`
}

// AIImageService creates and edits images for the editor.
type AIImageService interface {
	// GenerateImages runs the image.generate route, or image.edit when
	// req.FileID is set, and saves the results as req.Save asks. progress,
	// when not nil, receives progress frames while the call runs.
	GenerateImages(ctx context.Context, actor *Actor, req AIImageGenerateRequest, progress func(AIImageProgress)) (*AIImageGenerateResult, error)
}

// EmbeddingChunk is one indexed slice of a note.
type EmbeddingChunk struct {
	ID        string    `json:"id"`
	NoteID    string    `json:"noteId"`
	SpaceID   string    `json:"spaceId"`
	Ordinal   int       `json:"ordinal"`
	Text      string    `json:"text"`
	Vector    []float32 `json:"-"`
	Model     string    `json:"model"`
	Hash      string    `json:"hash"`
	UpdatedAt time.Time `json:"updatedAt"`
}
