# Models, Routing and Prices

Markspace keeps one catalog of every model that the configured providers
offer. An admin chooses which model handles each task, with fallbacks, the way
OpenRouter lets one account reach many models. Prices come from the providers
and from public price lists, so an admin does not type them in. A price typed
by an admin overrides a fetched one.

`ai.md` covers context, budgets and usage records. This document covers
providers, the model catalog, routing and prices.

## Providers

A provider is one account on one model server. Adding a provider starts from
a preset, which fills in the protocol, base URL, headers and price source, so
the admin usually enters only an API key or an address.

| Preset | Protocol | Base URL | Local | Price source |
| --- | --- | --- | --- | --- |
| OpenRouter | openai | `https://openrouter.ai/api/v1` | no | provider |
| OpenAI | openai | `https://api.openai.com/v1` | no | catalog |
| Anthropic | anthropic | `https://api.anthropic.com` | no | catalog |
| Google Gemini | openai | `https://generativelanguage.googleapis.com/v1beta/openai` | no | catalog |
| Mistral | openai | `https://api.mistral.ai/v1` | no | catalog |
| Groq | openai | `https://api.groq.com/openai/v1` | no | catalog |
| DeepSeek | openai | `https://api.deepseek.com/v1` | no | catalog |
| Together | openai | `https://api.together.xyz/v1` | no | catalog |
| Ollama | ollama | `http://ollama:11434` | yes | none |
| llama.cpp, LM Studio, vLLM, LocalAI | openai | the admin's address | yes | none |
| ComfyUI | comfyui | the admin's address | yes | none |
| Custom | any | the admin's address | the admin chooses | catalog |

A local provider's models cost 0 unless an admin sets a price, for example to
account for electricity.

`core.AIProviderConfig` has `Preset string` and `Local bool`. A provider has
no price field. Prices live in the `ai_models` table, and the configuration
file rejects a `prices` or `isDefault` key on a provider.

## Model Catalog

Markspace lists each provider's models when the provider is saved, when an
admin presses Refresh, and every 24 hours.

| Protocol | Listing | Capabilities |
| --- | --- | --- |
| OpenAI-compatible | `GET /models`. OpenRouter adds `context_length`, `architecture.input_modalities`, `architecture.output_modalities`, `supported_parameters` and `pricing` | from the listing when present, otherwise from the catalog entry for the same model, otherwise text only |
| Ollama | `GET /api/tags`, then `POST /api/show` per model | `capabilities` (`completion`, `vision`, `embedding`, `tools`) and `model_info` context length |
| Anthropic | `GET /v1/models` | text and image input for every listed model, and the rest from the catalog entry |
| ComfyUI | `GET /object_info/CheckpointLoaderSimple` | image generation, and image editing when an edit template exists |

```sql
CREATE TABLE ai_models (
	provider_id     TEXT NOT NULL,
	model_id        TEXT NOT NULL,          -- as the provider names it
	name            TEXT NOT NULL DEFAULT '',
	vendor          TEXT NOT NULL DEFAULT '',  -- openai, anthropic, meta, ...
	context_length  INTEGER NOT NULL DEFAULT 0,
	max_output      INTEGER NOT NULL DEFAULT 0,
	inputs          TEXT NOT NULL DEFAULT '["text"]',   -- text, image, file, audio
	outputs         TEXT NOT NULL DEFAULT '["text"]',   -- text, image, embedding
	tools           INTEGER NOT NULL DEFAULT 0,
	price           TEXT NOT NULL DEFAULT '{}',  -- fetched AIPrice
	price_source    TEXT NOT NULL DEFAULT '',    -- provider, openrouter, litellm, local, none
	price_matched   TEXT NOT NULL DEFAULT '',    -- the catalog id the price came from
	price_fetched_at TEXT NOT NULL DEFAULT '',
	price_override  TEXT NOT NULL DEFAULT '',    -- AIPrice set by an admin, empty for none
	enabled         INTEGER NOT NULL DEFAULT 1,  -- offered to users and routes
	available       INTEGER NOT NULL DEFAULT 1,  -- present in the last listing
	last_seen       TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (provider_id, model_id)
);
```

`core.AIPrice` holds prices in US dollars:

```go
type AIPrice struct {
	InputPerMTok      float64 `json:"inputPerMTok"`
	OutputPerMTok     float64 `json:"outputPerMTok"`
	CacheReadPerMTok  float64 `json:"cacheReadPerMTok,omitempty"`
	CacheWritePerMTok float64 `json:"cacheWritePerMTok,omitempty"`
	PerImageIn        float64 `json:"perImageIn,omitempty"`
	PerImageOut       float64 `json:"perImageOut,omitempty"`
	PerRequest        float64 `json:"perRequest,omitempty"`
}
```

A model missing from a new listing keeps its row with `available = 0`, so
routes and usage history still name it. A route skips it.

## Prices

The price of a model is the first of these that exists:

1. `price_override`.
2. The price in the provider's own listing, as OpenRouter and some gateways
   return it.
3. For a local provider, 0.
4. The OpenRouter public catalog, `GET https://openrouter.ai/api/v1/models`,
   which needs no key.
5. The LiteLLM price list,
   `https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json`.
6. None. Usage records tokens and shows cost as unknown.

Catalog matching works on normalised ids. Normalising lower-cases the id,
removes a vendor prefix such as `openai/`, removes a date suffix such as
`-2024-08-06` or `-20250219`, and removes a tag such as `:free` or
`:latest`. A match must also agree on the vendor, which comes from the preset
or from the catalog's own prefix. The matched id is stored, so the admin page
can show where a price came from.

- The two public catalogs download once a day, at most 32 MiB each, with a
  20 s timeout, through an HTTP client allowed only those two hosts.
- The database keeps the last good copy, so a failed download changes
  nothing.
- `ai.fetchPublicPrices` in the configuration, default true, turns the public
  catalogs off. Provider listings still apply.
- The usage record stores the cost at call time, so later price changes do not
  rewrite history.

## Routing

A task is a kind of AI work. Each task has a route, which is an ordered list
of models. The first model that is enabled, available, capable and within
budget handles the call.

| Task | Used by | Needs |
| --- | --- | --- |
| `default` | anything without its own route | text |
| `edit` | ask bar replace and insert, Improve, Shorten, Fix, Tone, Translate | text |
| `answer` | ask bar answers, Explain, Summarize | text |
| `chat` | the AI tab conversation | text |
| `vision` | Describe, Alt text, Extract text, Turn into notes, and any request with images | image input |
| `image.generate` | Generate image, Variations | image output |
| `image.edit` | Edit image | image editing |
| `embeddings` | semantic search and the index | embedding output |
| `plugin.<id>.<purpose>` | purposes a plugin declares in its manifest, such as `plugin.bookmark-wiki.summarise` | declared by the plugin |

A route with no entries uses `default`. The `embeddings` route uses its first
entry only, because vectors from different models cannot be mixed. Changing it
starts a reindex.

```go
type AIRoute struct {
	Task    string         `json:"task"`
	Targets []AIRouteTarget `json:"targets"`
}

type AIRouteTarget struct {
	ProviderID string `json:"providerId"`
	Model      string `json:"model"`
}
```

```sql
CREATE TABLE ai_routes (
	scope    TEXT NOT NULL,   -- "instance" or a user id
	task     TEXT NOT NULL,
	targets  TEXT NOT NULL DEFAULT '[]',
	PRIMARY KEY (scope, task)
);
```

### Resolution

For a call with task `T`:

1. A model chosen in the request, from the ask bar or the AI tab model chip,
   goes first when the user may call it.
2. The user's route for `T`, when `aiAllowUserRoutes` is on and the user set
   one.
3. The instance route for `T`.
4. The instance route for `default`.

Within that list, a target is skipped when:
- its provider is disabled, not callable by the user, or down for the last 60 s,
- the model is disabled or unavailable,
- it lacks a capability the request needs,
- its context length is below the estimated prompt size plus the requested
  output,
- its user or plugin budget is exhausted.

A call that fails with a timeout, a connection error, 429, 5xx, or a
context-length error moves to the next target. A 4xx for a bad request or a
refusal does not move on, because another model would fail the same way. A
streamed call moves on only when no text has reached the client yet. The usage
record stores the task, the model used, and the number of targets tried.

A personal provider's models can appear only in that user's routes.

Routes are the only way to choose a model. The instance settings hold no
default provider, and a user record holds no provider or model.

## API

```
GET    /api/ai/models?capability=&provider=&q=     catalog rows the actor may use, with effective price and source
GET    /api/ai/providers/{id}/models                the catalog rows of one provider
POST   /api/ai/providers/{id}/refresh               list models again, admin or owner
PATCH  /api/ai/models/{providerId}/{model...}       {enabled?, priceOverride?}, admin
GET    /api/ai/routes                               instance routes, and the actor's own when allowed
PUT    /api/ai/routes/instance/{task}               {targets}, admin
PUT    /api/ai/routes/me/{task}                     {targets}
DELETE /api/ai/routes/me/{task}
GET    /api/ai/presets                              the preset table
POST   /api/admin/ai/prices/refresh                 download the public catalogs now
```

`AIRequest` has `route`, the routing task, because `task` already names the
prompt task such as `summarize`. An empty `route` derives from the prompt task
and the feature, in this order:

1. The tasks `describe` and `extract` give `vision`.
2. The feature `chat` gives `chat`, `ask` gives `answer` and `rewrite` gives
   `edit`.
3. The tasks `ask`, `explain` and `summarize` give `answer`.
4. The writing tasks `improve`, `grammar`, `shorten`, `expand`, `translate`,
   `continue`, `custom`, `title`, `tags`, `outline` and `actions` give `edit`.
5. Any other request gives `default`.

### Routes Without a Saved Entry

A route saved on `/admin/ai` wins. A task without one uses `ai.routes` from
the configuration file. A task without either uses an implicit route, so an
instance with providers and no routes still answers. An implicit route lists
only enabled instance providers:

- `default` lists the `model` of every such provider that is not `comfyui`,
  in configuration order, then database order.
- `embeddings` lists the `embedModel` of the first such provider that has one
  and is not `comfyui` or `anthropic`.
- `image.generate` and `image.edit` list the `imageModel` of every such
  provider with the matching image flag.

The chat, embedding and image models a provider configures count as catalog
rows before its first listing. A route with no targets counts as absent.

## Pages

`/admin/ai` has four sections on one page, in this order: Tasks, Models,
Providers and Semantic index. An admin sets up AI in the reverse order of the
first three:

1. Add a provider. Add provider in the Providers section opens a Dialog. The
   form starts with the preset list and then shows only the fields that preset
   needs, usually an API key or an address.
2. Save the provider. Markspace fetches the provider's model list on save, and
   the models fill the Models table.
3. Choose models for each task. Add model in an expanded task row picks from
   those models.

Before any provider exists, the Models section shows "No models yet" and the
Providers section shows "No provider is configured". The Default row of Tasks
shows "No models", and every other row shows "Uses Default". An expanded
Default row shows "No models. Add one so AI features work.", and Add model has
no model to list.

The sections hold:

1. **Tasks.** One row per task with the chosen models as tokens, primary
   first. Tapping a row expands it in place. The expanded row shows the target
   list with drag handles and a menu per model with Move up, Move down and
   Remove, and Add model, which opens a SelectPanel of the enabled, available
   and capable models not yet in the list. The panel has a filter field, shows
   each model's provider and price per million tokens, and orders the models
   by price. Save stores the list and Cancel restores the saved one.
2. **Models.** A table of the catalog with model, provider, context length,
   inputs and outputs as small icons, input and output price, price source,
   an Enabled switch and Edit price. Filters cover capability, provider and
   name. Edit price opens a Dialog, and Use fetched price in it removes the
   override. Refresh prices downloads the public catalogs.
3. **Providers.** A table with name, preset, status, model count and last
   refresh. The row menu holds Test, Refresh, Edit and Delete. A provider from
   the configuration file has no Edit or Delete.
4. **Semantic index.** Rebuild embeddings starts a reindex.

`/settings/ai` shows the same Tasks section. Each row shows the instance
choice, and when `aiAllowUserRoutes` is on the expanded row adds "Use a
different model" and "Use the instance choice". When users may add their own
keys, the page also lists the user's personal providers.

The ask bar and the AI tab show the model that will run as a chip. Tapping it
opens a SelectPanel of capable models for that task, ordered by price.

## Testing

- Listing parsers for each protocol against recorded responses, including
  OpenRouter pricing strings such as `"0.0000025"`, `-1` for variable pricing,
  and missing fields.
- Id normalisation and matching vectors, including false matches that must not
  happen, such as `gpt-4o` against `gpt-4o-mini`.
- Price precedence with every combination of override, provider, local and
  catalog, and a failed download keeping the last copy.
- Routing:
  - capability skips, context skips, budget skips,
  - fallback on each error class, and no fallback on refusals,
  - no fallback after streamed text,
  - user routes only when allowed,
  - personal providers never in another user's route.
- `FuzzResolveRoute` in `internal/ai/route_fuzz_test.go` compares
  `ResolveRoute` against a small reference implementation over random
  catalogs, routes, users and failures.
- `FuzzHTTPAPI` in `test/server/fuzz_test.go` covers the routes above.
