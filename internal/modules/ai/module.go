// Package ai is the built-in module for AI: providers, the model catalog,
// routes, presets, runs and streams, conversations, image generation, the
// embedding reindex, usage reports and budgets.
package ai

import (
	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/module"
)

// Module serves the AI routes.
type Module struct{ api.Env }

// New returns the module.
func New(env api.Env) *Module { return &Module{Env: env.WithDefaults()} }

// Manifest implements module.Module.
func (m *Module) Manifest() module.Manifest {
	return module.Manifest{ID: "ai", Name: "AI", Builtin: true}
}

// Register implements module.Module.
func (m *Module) Register(r *module.Registry) error {
	r.API("GET /api/ai/providers", m.listAIProviders)
	r.API("POST /api/ai/providers", m.createAIProvider)
	r.API("PATCH /api/ai/providers/{id}", m.updateAIProvider)
	r.API("DELETE /api/ai/providers/{id}", m.deleteAIProvider)
	r.API("POST /api/ai/providers/{id}/test", m.testAIProvider)
	r.API("GET /api/ai/providers/{id}/models", m.listAIModels)
	r.API("POST /api/ai/providers/{id}/refresh", m.refreshAIProvider)
	r.API("GET /api/ai/models", m.listAIModels)
	r.API("PATCH /api/ai/models/{providerId}/{model...}", m.updateAIModel)
	r.API("GET /api/ai/routes", m.listAIRoutes)
	r.API("PUT /api/ai/routes/instance/{task}", m.saveInstanceAIRoute)
	r.API("PUT /api/ai/routes/me/{task}", m.saveMyAIRoute)
	r.API("DELETE /api/ai/routes/me/{task}", m.deleteMyAIRoute)
	r.API("GET /api/ai/presets", m.listAIPresets)
	r.API("POST /api/ai/run", m.runAI)
	r.API("POST /api/ai/stream", m.streamAI)
	r.API("POST /api/ai/reindex", m.reindexEmbeddings)
	r.API("GET /api/ai/threads", m.listAIThreads)
	r.API("POST /api/ai/threads", m.createAIThread)
	r.API("POST /api/ai/threads/{id}/messages", m.streamAIThreadMessage)
	r.API("PUT /api/ai/threads/{id}/messages/{messageId}/applied", m.setAIMessageApplied)
	r.API("DELETE /api/ai/threads/{id}", m.deleteAIThread)
	r.API("POST /api/ai/image/stream", m.streamAIImages)
	r.API("GET /api/ai/usage", m.myAIUsage)
	r.API("GET /api/ai/usage/summary", m.myAIUsageSummary)
	r.API("GET /api/ai/usage/calls", m.myAIUsageCalls)
	r.API("POST /api/admin/ai/prices/refresh", m.refreshAIPrices)
	r.API("GET /api/admin/ai/usage", m.adminAIUsage)
	r.API("GET /api/admin/ai/usage/calls", m.adminAIUsageCalls)
	r.API("GET /api/admin/ai/budgets", m.listAIBudgets)
	r.API("PUT /api/admin/ai/budgets/{kind}/{id}", m.saveAIBudget)
	r.API("DELETE /api/admin/ai/budgets/{kind}/{id}", m.deleteAIBudget)
	r.API("POST /api/admin/ai/budgets/{kind}/{id}/lift", m.liftAIBudget)
	return nil
}
