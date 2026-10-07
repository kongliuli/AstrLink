package hunyuanapi

import (
	"os"
	"testing"
)

func TestOptionalRealProviderSkippedByDefault(t *testing.T) {
	if os.Getenv("ASTRLINK_HUNYUAN_REAL") == "1" {
		t.Skip("set only for manual real-upstream checks; unit suite uses httptest instead")
	}
	if value := os.Getenv("ASTRLINK_HUNYUAN_REAL"); value != "" && value != "1" {
		t.Fatalf("ASTRLINK_HUNYUAN_REAL must be unset or 1, got %q", value)
	}
	if RealEnabled() {
		t.Fatal("real provider must stay disabled by default")
	}
}
