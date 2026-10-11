package steerloop

import (
	"context"
	"log/slog"
	"time"

	"github.com/cloudwego/eino/schema"
)

// emitEvent offers an intermediate event to the sink and the event channel.
// When the channel is full the event is dropped and counted.
func (l *Loop) emitEvent(t EventType, data any) {
	if !l.cfg.StreamEvents && t != EventDone && t != EventError {
		return
	}

	evt := Event{
		Type:      t,
		Data:      data,
		Iteration: l.GetCurrentIteration(),
		Timestamp: time.Now().UTC(),
	}

	l.evMu.RLock()
	defer l.evMu.RUnlock()
	if l.eventClosed {
		return
	}
	if l.cfg.EventSink != nil {
		l.cfg.EventSink(context.Background(), evt)
	}

	select {
	case l.eventCh <- evt:
	default:
		l.dropped.Add(1)
		l.logger.Warn("agent loop event channel full, dropping event",
			slog.String("type", string(t)), slog.Int("iteration", evt.Iteration))
	}
}

func (l *Loop) emitDone(reason DoneReason, finalContent string) {
	l.deliverDone(DoneData{Reason: reason, FinalContent: finalContent})
}

func (l *Loop) emitDoneAwaiting(awaiting *AwaitingInputData) {
	l.deliverDone(DoneData{
		Reason:       DoneReasonAwaitingHuman,
		FinalContent: awaiting.Question,
		Awaiting:     awaiting,
	})
}

// deliverDone records the outcome and sends the done event. Only the first
// call has any effect, so a run has exactly one outcome. Unlike intermediate
// events the done event is not dropped when the channel is full: the send
// waits for a reader, up to DoneDeliveryGrace.
func (l *Loop) deliverDone(done DoneData) {
	l.doneOnce.Do(func() {
		l.mu.Lock()
		done.Iterations = l.iteration
		done.LLMUsage = l.usage.summary()
		done.DroppedEvents = int(l.dropped.Load())
		if done.Reason == DoneReasonError {
			done.Error = l.lastError
		}
		l.result = done
		l.hasResult = true
		l.mu.Unlock()

		evt := Event{
			Type:      EventDone,
			Data:      done,
			Iteration: done.Iterations,
			Timestamp: time.Now().UTC(),
		}

		l.evMu.RLock()
		defer l.evMu.RUnlock()
		if l.eventClosed {
			return
		}
		if l.cfg.EventSink != nil {
			l.cfg.EventSink(context.Background(), evt)
		}

		select {
		case l.eventCh <- evt:
			return
		default:
		}

		grace := l.cfg.DoneDeliveryGrace
		if grace <= 0 {
			grace = defaultDoneDeliveryGrace
		}
		timer := time.NewTimer(grace)
		defer timer.Stop()
		select {
		case l.eventCh <- evt:
		case <-timer.C:
			l.logger.Warn("agent loop done event not read within grace period; use Result")
		}
	})
}

func (l *Loop) emitLLMTurnEvent(
	turn int,
	usageBefore llmUsageAccumulator,
	requestMsgs []*schema.Message,
	reply *schema.Message,
	durationMs int64,
) {
	usage := l.usageSnapshot()
	inDelta := usage.input - usageBefore.input
	outDelta := usage.output - usageBefore.output

	var provider, modelName string
	if l.cfg.ModelIdentity != nil {
		provider, modelName = l.cfg.ModelIdentity()
	}
	var cost float64
	if l.cfg.CostEstimator != nil {
		cost = l.cfg.CostEstimator(provider, modelName, inDelta, outDelta)
	}

	reqJSON, _, _ := SerializeEinoMessagesForAudit(requestMsgs)
	l.emitEvent(EventLLMTurn, LLMTurnData{
		TurnIndex:              turn,
		InputTokens:            inDelta,
		OutputTokens:           outDelta,
		CostAmount:             cost,
		Provider:               provider,
		Model:                  modelName,
		DurationMs:             durationMs,
		RequestMessagesJSON:    reqJSON,
		ResponseKind:           responseKindForMessage(reply),
		AssistantContent:       reply.Content,
		AssistantToolCallsJSON: SerializeAssistantToolCalls(reply),
		MaxIterations:          l.maxIterations(),
		StopCondition:          StopConditionLabel(l.cfg.StopCondition),
	})
}
