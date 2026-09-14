package main

import (
	"fmt"
	"os"
)

// exampleConfig is printed by "markspace config example".
const exampleConfig = `# Markspace configuration.
# Every value here is the default unless noted. Environment variables of the
# form MARKSPACE_LISTEN, MARKSPACE_DATA_DIR and so on override these.

listen: ":8080"
# baseUrl is the public address used in share, invite and attachment links.
# Leave it empty to use the address each request arrives on.
baseUrl: ""
dataDir: "./data"
# dbPath defaults to <dataDir>/markspace.db
instanceName: "Markspace"

# allowSignup lets anyone create an account. Turn it off and use invites.
allowSignup: true
sessionTTL: 720h
# secureCookies also turns on cross-origin request protection. Set it to true
# when serving over HTTPS.
secureCookies: false
# trustProxy reads the client address from X-Forwarded-For.
trustProxy: false
maxUploadMB: 1024
# watchFiles picks up notes edited outside the application.
watchFiles: true
logLevel: info

jobs:
  # workers is the number of background jobs that run at once.
  workers: 4

plugins:
  # markspaceUrl is the address plugins use to reach Markspace. Leave it empty
  # to use baseUrl, then the listen port on 127.0.0.1.
  markspaceUrl: ""
  # allowPrivateURLs lets the web fetch of the plugin host API reach loopback,
  # private and link-local addresses. Keep it off unless a plugin must fetch
  # pages from your own network.
  allowPrivateURLs: false

ai:
  enabled: true
  # allowUserKeys lets users register their own providers.
  allowUserKeys: true
  # fetchPublicPrices downloads the OpenRouter and LiteLLM price lists once a
  # day. Models whose provider reports no price take their price from them.
  # A local provider costs 0 unless an admin sets a price on /admin/ai.
  fetchPublicPrices: true
  contextChunks: 8
  # contextChars is the character budget for the text of one prompt: the
  # selection or note body plus every context reference, such as linked
  # notes, attachments and web pages.
  contextChars: 48000
  # allowPrivateURLs lets url context references fetch pages on loopback,
  # private and link-local addresses. Keep it off on a shared instance.
  allowPrivateURLs: false
  # routes chooses the models of each task, first choice first. A call moves
  # to the next model when one fails with a timeout, a connection error, 429,
  # 5xx or a context-length error. A task without a route uses default. An
  # admin can change routes on /admin/ai, and a route saved there wins.
  # Tasks: default, edit, answer, chat, vision, image.generate, image.edit
  # and embeddings. The embeddings route uses its first model only.
  routes:
    default:
      - {providerId: local, model: qwen3-30b-instruct}
      - {providerId: ollama, model: llama3.2}
    embeddings:
      - {providerId: local, model: bge-m3}
  providers:
    # Any OpenAI-compatible server: vLLM, llama.cpp, LocalAI, LM Studio,
    # text-generation-webui, Ollama's compatibility endpoint. preset fills in
    # the protocol, and local marks a server on your own hardware.
    - id: local
      name: Local vLLM
      preset: openai-local
      baseUrl: http://127.0.0.1:8000/v1
      apiKey: ${MARKSPACE_LOCAL_KEY}
      model: qwen3-30b-instruct
      embedModel: bge-m3
      temperature: 0.3
      maxTokens: 2048
      timeoutSeconds: 180
      enabled: true
      # imageGeneration and imageEditing turn on /v1/images/generations and
      # /v1/images/edits. Enable them on a server that serves images, such as
      # LocalAI or api.openai.com. imageModel names the image model, and
      # imageSizes lists the sizes users may pick. Without imageSizes any
      # WIDTHxHEIGHT up to 4096 is allowed.
      imageGeneration: false
      imageEditing: false
      imageModel: stablediffusion
      imageSizes: ["1024x1024", "1536x1024", "1024x1536"]

    # Native Ollama protocol. The preset marks it local.
    - id: ollama
      name: Ollama
      preset: ollama
      baseUrl: http://127.0.0.1:11434
      model: llama3.2
      embedModel: nomic-embed-text
      temperature: 0.3
      maxTokens: 2048
      enabled: true

    # Hosted Anthropic models. Prices come from the public price lists.
    - id: anthropic
      name: Claude
      preset: anthropic
      apiKey: ${ANTHROPIC_API_KEY}
      model: claude-sonnet-5
      # vision marks every model of the provider as accepting images when
      # neither the listing nor the price lists say so.
      vision: true
      maxTokens: 4096
      enabled: false

    # OpenRouter reaches many vendors with one key and reports its prices.
    # Remove the leading "# " of the lines below to use it.
    # - id: openrouter
    #   name: OpenRouter
    #   preset: openrouter
    #   apiKey: ${OPENROUTER_API_KEY}
    #   enabled: true

    # A ComfyUI server that generates and edits images. It has no chat or
    # embeddings. Remove the leading "# " of the lines below to use it.
    # imageModel is the checkpoint file name. comfyGenerate and comfyEdit may
    # hold your own workflows in ComfyUI API format, as JSON strings, to
    # replace the built-in SDXL workflows. A workflow uses the placeholders
    # {{checkpoint}}, {{prompt}}, {{negative}}, {{width}}, {{height}},
    # {{seed}} and {{batch}}, and for edits {{image}}, {{mask}} and
    # {{denoise}}. timeoutSeconds defaults to 300.
    # - id: comfy
    #   name: ComfyUI
    #   preset: comfyui
    #   baseUrl: http://127.0.0.1:8188
    #   imageGeneration: true
    #   imageEditing: true
    #   imageModel: sd_xl_base_1.0.safetensors
    #   imageSizes: ["1024x1024", "1152x896", "896x1152"]
    #   enabled: true
`

// writeExampleConfig writes the commented example configuration to path. It
// refuses to overwrite an existing file.
func writeExampleConfig(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists", path)
	}
	if err := os.WriteFile(path, []byte(exampleConfig), 0o644); err != nil {
		return err
	}
	fmt.Println("wrote " + path)
	return nil
}
