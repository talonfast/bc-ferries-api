package scraper

import (
	"context"
	"testing"
	"time"
)

// A scrape generation without a deadline can block every later one, because
// both jobs run in gocron's singleton mode.
func TestBrowserContextCarriesGenerationDeadline(t *testing.T) {
	ctx, cancel := newBrowserContext(context.Background(), time.Minute)
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("browser context has no deadline")
	}
	if remaining := time.Until(deadline); remaining <= 0 || remaining > time.Minute {
		t.Fatalf("deadline %v away, want within one minute", remaining)
	}
}
