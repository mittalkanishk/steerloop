// Steer shows the three controls on a running loop: inject a message, change
// the configuration, and stop. It uses a scripted model that takes 200 ms per
// turn so there is time to steer.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/cloudwego/eino/schema"

	"github.com/mittalkanishk/steerloop/loop"
	"github.com/mittalkanishk/steerloop/loop/event"
	"github.com/mittalkanishk/steerloop/steerlooptest"
)

// noTools is the ToolExecutor for a loop that offers the model no tools.
type noTools struct{}

func (noTools) ExecuteTool(context.Context, string, string) (string, error) { return "", nil }

// steer drives the running loop the way a caller would: a message, a
// settings change, then a stop.
func steer(l *loop.Loop) {
	time.Sleep(100 * time.Millisecond)
	l.Inject("Focus on results from this year.") // seen by the next model call

	more := 50
	l.ModifyConfig(loop.ConfigChange{MaxIterations: &more})

	time.Sleep(700 * time.Millisecond)
	l.Stop() // takes effect at the next iteration boundary
}

func main() {
	chatModel := steerlooptest.NewScriptedModel(
		steerlooptest.Step{Content: "Still researching…", Delay: 200 * time.Millisecond},
	)

	cfg := loop.DefaultConfig()
	cfg.StopCondition = loop.StopOnExternal // run until told to stop
	cfg.MaxIterations = 3

	loop, err := loop.New(cfg, chatModel, nil, noTools{}, nil)
	if err != nil {
		log.Fatal(err)
	}
	events := loop.Start(context.Background(), []*schema.Message{{Role: schema.User, Content: "Research this."}})

	go steer(loop)

	for evt := range events {
		switch data := evt.Data.(type) {
		case event.ProgressData:
			fmt.Printf("iteration %d  control: %s\n", evt.Iteration, data.Message)
		case event.ResponseData:
			fmt.Printf("iteration %d  %s\n", evt.Iteration, data.Content)
		case event.DoneData:
			fmt.Printf("done: %s after %d iteration(s)\n", data.Reason, data.Iterations)
		}
	}
}
