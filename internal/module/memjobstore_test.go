package module_test

import (
	"testing"

	"github.com/gamemaker1/markspace/internal/module"
	"github.com/gamemaker1/markspace/internal/module/conformance"
)

func TestMemJobStoreConformance(t *testing.T) {
	conformance.Run(t, func() module.JobStore { return module.NewMemJobStore() })
}
