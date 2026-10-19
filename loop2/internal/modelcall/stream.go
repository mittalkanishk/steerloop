package modelcall

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// received is one result of reading the stream.
type received struct {
	chunk *schema.Message
	err   error
}

// collectStream runs a streamed call and joins the chunks. If more than
// idleTimeout passes between chunks after the first one, the call is treated
// as hung and returns a transient error.
//
// Goroutine: one reader per call moves chunks from the blocking Recv onto a
// channel, so this function can also watch the idle timer and ctx. It ends
// when the stream ends or this function returns (closing done).
func collectStream(ctx context.Context, m model.ToolCallingChatModel, msgs []*schema.Message, opts []model.Option, idleTimeout time.Duration) (*schema.Message, error) {
	stream, err := m.Stream(ctx, msgs, opts...)
	if err != nil {
		return nil, err
	}
	defer stream.Close()

	results := make(chan received)
	done := make(chan struct{})
	defer close(done)

	go func() {
		defer close(results)
		for {
			chunk, err := stream.Recv()
			select {
			case results <- received{chunk, err}:
			case <-done:
				return
			}
			if err != nil {
				return
			}
		}
	}()

	var chunks []*schema.Message
	firstChunkSeen := false
	for {
		// Before the first chunk only the step timeout applies: a reply that
		// is only tool calls may send nothing until its final chunk.
		var idle <-chan time.Time
		if firstChunkSeen {
			idle = time.After(idleTimeout)
		}

		select {
		case r, ok := <-results:
			if !ok || errors.Is(r.err, io.EOF) {
				return schema.ConcatMessages(chunks)
			}
			if r.err != nil {
				return nil, r.err
			}
			firstChunkSeen = true
			if r.chunk != nil {
				chunks = append(chunks, r.chunk)
			}
		case <-idle:
			return nil, fmt.Errorf("stream idle: no chunk received for %s", idleTimeout)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}
