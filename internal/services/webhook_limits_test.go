package services

import (
	"errors"
	"testing"
	"time"

	"github.com/gitduppy/gitduppy/internal/models"
)

func TestValidateDeliveryLimits(t *testing.T) {
	ptr := func(n int) *int { return &n }
	cases := []struct {
		name    string
		retry   *int
		timeout *int
		ok      bool
	}{
		{"unset", nil, nil, true},
		{"zero means default", ptr(0), ptr(0), true},
		{"max values", ptr(webhookMaxRetryCount), ptr(webhookMaxTimeoutSeconds), true},
		{"huge retry_count", ptr(1_000_000), nil, false},
		{"negative retry_count", ptr(-1), nil, false},
		{"huge timeout", nil, ptr(86400), false},
		{"negative timeout", nil, ptr(-5), false},
	}
	for _, c := range cases {
		err := validateDeliveryLimits(c.retry, c.timeout)
		if (err == nil) != c.ok {
			t.Errorf("%s: err=%v, want ok=%v", c.name, err, c.ok)
		}
		if err != nil && !errors.Is(err, ErrValidation) {
			t.Errorf("%s: error must wrap ErrValidation, got %v", c.name, err)
		}
	}
}

func TestCreateWebhookRejectsUnboundedRetries(t *testing.T) {
	// Validation runs before any DB access, so a zero-value service suffices.
	s := &WebhookService{}
	_, err := s.CreateWebhook(t.Context(), [16]byte{}, &CreateWebhookRequest{
		Name: "x", URL: "https://example.com/hook", Events: []string{"clone.failed"}, RetryCount: 1_000_000,
	})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation, got %v", err)
	}
	big := 500
	if _, err := s.UpdateWebhook(t.Context(), [16]byte{}, &UpdateWebhookRequest{RetryCount: &big}); !errors.Is(err, ErrValidation) {
		t.Fatalf("update: expected ErrValidation, got %v", err)
	}
}

func TestClampDeliveryLimits(t *testing.T) {
	for in, want := range map[int]int{-3: 3, 0: 3, 1: 1, 7: 7, 10: 10, 1_000_000: webhookMaxRetryCount} {
		if got := clampAttempts(in); got != want {
			t.Errorf("clampAttempts(%d) = %d, want %d", in, got, want)
		}
	}
	for in, want := range map[int]time.Duration{-1: 30 * time.Second, 0: 30 * time.Second, 45: 45 * time.Second, 99999: webhookMaxTimeoutSeconds * time.Second} {
		if got := clampTimeout(in); got != want {
			t.Errorf("clampTimeout(%d) = %v, want %v", in, got, want)
		}
	}
}

func TestEnqueueIsBounded(t *testing.T) {
	// No workers: the queue (capacity 2) fills and further deliveries are
	// refused immediately instead of spawning goroutines.
	s := &WebhookService{}
	s.startDeliveryWorkers(0, 2)
	job := webhookJob{webhook: models.WebhookConfig{}, eventType: "webhook.test"}
	for i := 0; i < 2; i++ {
		if err := s.enqueue(job); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}
	if err := s.enqueue(job); !errors.Is(err, ErrWebhookQueueFull) {
		t.Fatalf("expected ErrWebhookQueueFull, got %v", err)
	}
	s.Stop()
	s.Stop() // idempotent
	// After Stop nothing more is accepted, even with free capacity.
	<-s.queue
	if err := s.enqueue(job); !errors.Is(err, ErrWebhookQueueFull) {
		t.Fatalf("enqueue after Stop: expected ErrWebhookQueueFull, got %v", err)
	}
}

func TestStopWithoutWorkersIsSafe(t *testing.T) {
	(&WebhookService{}).Stop()
	if err := (&WebhookService{}).enqueue(webhookJob{}); !errors.Is(err, ErrWebhookQueueFull) {
		t.Fatalf("zero-value service must refuse deliveries, got %v", err)
	}
}
