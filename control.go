package steerloop

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
