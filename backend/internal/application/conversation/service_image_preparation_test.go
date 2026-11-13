package conversation

import (
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/pkg/imageutil"
)

func waitImageFlightWaiters(t *testing.T, c *preparedConversationImageCache, key string, n int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		f := c.inflight[key]
		ok := f != nil && f.waiters == n
		c.mu.Unlock()
		if ok {
			return
		}
		runtime.Gosched()
	}
	t.Fatalf("image flight never reached %d waiters", n)
}

func TestPreparedImageCacheSingleFlightAndIndependentCancellation(t *testing.T) {
	c := newPreparedConversationImageCache(3, 1024)
	var calls atomic.Int32
	started := make(chan struct{})
	finish := make(chan struct{})
	load := func(ctx context.Context) (preparedConversationImage, error) {
		calls.Add(1)
		close(started)
		select {
		case <-finish:
			return preparedConversationImage{data: []byte("prepared"), mimeType: "image/png"}, nil
		case <-ctx.Done():
			return preparedConversationImage{}, ctx.Err()
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	first := make(chan error, 1)
	go func() { _, err := c.load(ctx, "same", load); first <- err }()
	<-started
	second := make(chan error, 1)
	go func() { _, err := c.load(t.Context(), "same", load); second <- err }()
	waitImageFlightWaiters(t, c, "same", 2)
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("first cancellation: %v", err)
	}
	close(finish)
	if err := <-second; err != nil {
		t.Fatalf("second waiter was canceled by first: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("same key decoded %d times", calls.Load())
	}
	if _, err := c.load(t.Context(), "same", load); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("completed result was not cached")
	}
}

func TestPreparedImageCacheCancelsAbandonedWorkAndAllowsOtherKeys(t *testing.T) {
	c := newPreparedConversationImageCache(3, 1024)
	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan struct{})
	started := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, err := c.load(ctx, "abandoned", func(workCtx context.Context) (preparedConversationImage, error) {
			close(started)
			<-workCtx.Done()
			close(stopped)
			return preparedConversationImage{}, workCtx.Err()
		})
		result <- err
	}()
	<-started
	if _, err := c.load(t.Context(), "independent", func(context.Context) (preparedConversationImage, error) {
		return preparedConversationImage{data: []byte("other")}, nil
	}); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("abandoned preparation did not stop")
	}
	if _, err := c.load(t.Context(), "abandoned", func(context.Context) (preparedConversationImage, error) {
		return preparedConversationImage{data: []byte("retry")}, nil
	}); err != nil {
		t.Fatalf("canceled key could not retry: %v", err)
	}
}

func TestImagePreparationErrorsExposeActionableReason(t *testing.T) {
	for _, test := range []struct {
		err    error
		reason string
	}{{imageutil.ErrAnimatedImage, "unsupported_animation"}, {imageutil.ErrImagePixelLimit, "image_processing_failed"}, {imageutil.ErrInvalidImage, "image_processing_failed"}, {imageutil.ErrImageDecodeBudget, "image_processing_failed"}} {
		err := attachmentImagePreparationError(AttachmentInput{Current: true, FileName: "photo.png"}, test.err)
		detail := AttachmentInputErrorDetails(err)
		if detail == nil || detail.Reason != test.reason || detail.Scope != "current" || !errors.Is(err, test.err) {
			t.Fatalf("error detail lost: %#v %v", detail, err)
		}
		want := ErrImageProcessingFailed
		if test.reason == "unsupported_animation" {
			want = ErrUnsupportedImageAnimation
		}
		if !errors.Is(err, want) {
			t.Fatalf("business error identity lost: %v", err)
		}
	}
}
