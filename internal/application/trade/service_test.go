package trade

import (
	"testing"

	tradepkg "github.com/lizhaojie/tvbot/internal/trade"
)

// TestServiceCompiles verifies the package builds and the service can be
// constructed. Real end-to-end coverage lives in application/ingest tests.
func TestServiceCompiles(t *testing.T) {
	_ = NewService(nil, nil, nil, nil, tradepkg.NewDryRunTrader())
}
