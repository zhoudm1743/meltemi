package driver

import "testing"

func TestConfigValidate(t *testing.T) {
	if err := DefaultConfig().Validate(); err != nil {
		t.Fatal(err)
	}
	for _, size := range []Size{{0, 600}, {800, 0}, {-1, 600}} {
		if err := (Config{Title: "bad", Size: size}).Validate(); err == nil {
			t.Fatalf("expected invalid size %v to fail", size)
		}
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Title != "Meltemi" {
		t.Fatalf("expected title %q, got %q", "Meltemi", cfg.Title)
	}
	if cfg.Size != (Size{Width: 800, Height: 600}) {
		t.Fatalf("expected size %v, got %v", Size{Width: 800, Height: 600}, cfg.Size)
	}
}

// TestDefaultConfigRemainsDecorated pins that opting into frameless windows
// is an explicit Config choice: existing callers keep native decorations.
func TestDefaultConfigRemainsDecorated(t *testing.T) {
	if DefaultConfig().Frameless {
		t.Fatal("DefaultConfig().Frameless = true, want false so existing windows keep their decorations")
	}
}
