package app

import (
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

func TestGetSettingsHidesAdminFields(t *testing.T) {
	h := newHarness(t)
	admin := h.user("Admin", true)
	user := h.user("User", false)
	sp := h.space(admin, "Private")

	in := &core.Settings{
		InstanceName:      "Team notes",
		AllowSignup:       false,
		DefaultSpaceID:    sp.ID,
		AIEnabled:         true,
		AIAllowUserKeys:   true,
		AIAllowUserRoutes: true,
	}
	if _, err := h.app.UpdateSettings(h.ctx, admin, in); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}

	full, err := h.app.GetSettings(h.ctx, admin)
	if err != nil || !full.AIAllowUserRoutes || full.DefaultSpaceID != sp.ID {
		t.Fatalf("admin settings = %+v, %v", full, err)
	}

	anon, err := h.app.GetSettings(h.ctx, &core.Actor{})
	if err != nil {
		t.Fatal(err)
	}
	if anon.InstanceName != "Team notes" || anon.AllowSignup {
		t.Fatalf("anonymous lost the public fields: %+v", anon)
	}
	if anon.AIEnabled || anon.AIAllowUserRoutes || anon.AIAllowUserKeys || anon.DefaultSpaceID != "" {
		t.Fatalf("anonymous sees admin fields: %+v", anon)
	}

	member, err := h.app.GetSettings(h.ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if !member.AIEnabled || !member.AIAllowUserRoutes || !member.AIAllowUserKeys {
		t.Fatalf("signed-in user lost the AI fields: %+v", member)
	}
	if member.AIMonthlyTokenBudget != 0 || member.DefaultSpaceID != "" {
		t.Fatalf("signed-in user sees fields they should not: %+v", member)
	}

	h.member(sp, user, core.RoleViewer)
	member, _ = h.app.GetSettings(h.ctx, user)
	if member.DefaultSpaceID != sp.ID {
		t.Fatal("a member of the default space should see its id")
	}
}
