package gitops

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain runs goleak after the whole gitops suite so any goroutine spun by the
// keyed-mutex / worker / scheduler tests that is not joined or stopped fails the
// package. The keyed-mutex tests join their goroutines via sync.WaitGroup, so no
// IgnoreTopFunction options are needed here.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
