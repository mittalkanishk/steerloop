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

	"github.com/mittalkanishk/steerloop"
	"github.com/mittalkanishk/steerloop/steerlooptest"
)

func main() {
	chatModel := steerlooptest.NewScriptedModel(
		steerlooptest.Step{Content: "Still researching…", Delay: 200 * time.Millisecond},
	)
	executor := steerloop.ToolExecutorFunc(func(context.Context, string, string) (string, error) {
		return "", nil
	})

	cfg := steerloop.DefaultConfig()
	cfg.StopCondition = steerloop.StopOnExplicit // run until told to stop
	cfg.MaxIterations = 3

	loop, err := steerloop.New(cfg, chatModel, nil, executor, nil)
	if err != nil {
		log.Fatal(err)
	}
	events := loop.Start(context.Background(), []*schema.Message{{Role: schema.User, Content: "Research this."}})

	go func() {
		time.Sleep(100 * time.Millisecond)
		loop.Inject("Focus on results from this year.") // seen by the next model call

		more := 50
		loop.SendControlSignal(steerloop.NewModifyConfigControl(steerloop.ModifyConfigData{MaxIterations: &more}))

		time.Sleep(700 * time.Millisecond)
		loop.Stop() // takes effect at the next step boundary
	}()

	for evt := range events {
		switch data := evt.Data.(type) {
		case steerloop.ProgressData:
			fmt.Printf("step %d  control: %s\n", evt.Iteration, data.Message)
		case steerloop.ResponseData:
			fmt.Printf("step %d  %s\n", evt.Iteration, data.Content)
		case steerloop.DoneData:
			fmt.Printf("done: %s after %d steps\n", data.Reason, data.Iterations)
		}
	}
}
