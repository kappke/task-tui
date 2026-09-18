package command

import "context"

// Dispatcher is a small explicit command boundary. It does not start a
// goroutine or retain a context, which keeps ownership and cancellation clear.
type Dispatcher struct {
	handler Handler
}

// NewDispatcher constructs a dispatcher for handler.
func NewDispatcher(handler Handler) *Dispatcher {
	return &Dispatcher{handler: handler}
}

// Dispatch validates and sends a command to the application.
func (d *Dispatcher) Dispatch(ctx context.Context, c Command) (Event, error) {
	if err := c.Validate(); err != nil {
		return Event{}, err
	}
	return d.handler.Handle(ctx, c)
}
