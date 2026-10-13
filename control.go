package steerloop

import (
	"errors"

	"github.com/cloudwego/eino/schema"
)

// ControlType classifies a control signal.
type ControlType string

const (
	// ControlStop stops the loop at the next iteration boundary.
	ControlStop ControlType = "stop"

	// ControlInject appends a user message that the model sees on its next
	// call. Inside a pause function's Block, it is the reply being waited for.
	ControlInject ControlType = "inject"

	// ControlModifyConfig changes configuration during the run.
	ControlModifyConfig ControlType = "modify_config"
)

// Control is a signal sent to a running loop with SendControlSignal.
type Control struct {
	Type ControlType
	Data any
}

// InjectData is the payload of ControlInject.
type InjectData struct {
	Message string
}

// ModifyConfigData is the payload of ControlModifyConfig. Nil fields are left
// unchanged.
type ModifyConfigData struct {
	MaxIterations    *int
	Temperature      *float64
	EnableReflection *bool
}

// NewStopControl returns a stop signal.
func NewStopControl() Control {
	return Control{Type: ControlStop}
}

// NewInjectControl returns an inject signal carrying message.
func NewInjectControl(message string) Control {
	return Control{Type: ControlInject, Data: InjectData{Message: message}}
}

// NewModifyConfigControl returns a configuration change signal.
func NewModifyConfigControl(data ModifyConfigData) Control {
	return Control{Type: ControlModifyConfig, Data: data}
}

// ErrControlQueueFull is returned when a control could not be queued.
var ErrControlQueueFull = errors.New("steerloop: control queue full")

// SendControlSignal queues a control for the loop. It returns false when the
// control queue is full. Use Stop to stop a loop: it cannot be lost.
func (l *Loop) SendControlSignal(ctrl Control) bool {
	select {
	case l.controlCh <- ctrl:
		return true
	default:
		return false
	}
}

// Stop asks the loop to stop. It takes effect at the next iteration boundary,
// or at once if a pause function is blocked waiting for a reply. It does not
// interrupt a model call or a tool that is already running.
func (l *Loop) Stop() {
	l.stopOnce.Do(func() { close(l.stopCh) })
}

// Inject queues a user message. It returns false when the control queue is full.
func (l *Loop) Inject(message string) bool {
	return l.SendControlSignal(NewInjectControl(message))
}

// drainControls applies every queued control. It returns true when one of them
// was a stop.
func (l *Loop) drainControls() bool {
	for {
		select {
		case ctrl := <-l.controlCh:
			if l.handleControl(ctrl) {
				return true
			}
		default:
			return false
		}
	}
}

func (l *Loop) handleControl(ctrl Control) bool {
	switch ctrl.Type {
	case ControlStop:
		return true

	case ControlInject:
		if data, ok := ctrl.Data.(InjectData); ok && data.Message != "" {
			l.appendRunningMessages(&schema.Message{Role: schema.User, Content: data.Message})
			l.emitEvent(EventProgress, ProgressData{Message: "User instruction received", Step: "inject"})
		}

	case ControlModifyConfig:
		if data, ok := ctrl.Data.(ModifyConfigData); ok {
			l.applyConfigModification(data)
			l.emitEvent(EventProgress, ProgressData{Message: "Configuration updated", Step: "config_modify"})
		}
	}
	return false
}

func (l *Loop) applyConfigModification(data ModifyConfigData) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if data.MaxIterations != nil {
		l.cfg.MaxIterations = *data.MaxIterations
	}
	if data.Temperature != nil {
		l.cfg.Temperature = *data.Temperature
	}
	if data.EnableReflection != nil {
		l.cfg.EnableReflection = *data.EnableReflection
	}
}
