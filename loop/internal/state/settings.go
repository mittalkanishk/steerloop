package state

// Settings are the parts of the configuration a caller may change while the
// loop runs.
type Settings struct {
	MaxIterations int
	Temperature   float64
}

// Change is a change to Settings. Nil fields are left unchanged.
type Change struct {
	MaxIterations *int
	Temperature   *float64
}

// Apply changes the settings.
func (r *Run) Apply(c Change) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c.MaxIterations != nil {
		r.settings.MaxIterations = *c.MaxIterations
	}
	if c.Temperature != nil {
		r.settings.Temperature = *c.Temperature
	}
}

func (r *Run) MaxIterations() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.settings.MaxIterations
}

func (r *Run) Temperature() float64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.settings.Temperature
}
