package llm

import "context"

// EmitFunc consumes one ordered event from a streaming model response.
// Returning an error asks the client to stop streaming and return that error.
type EmitFunc func(Event) error

// Client is the provider-neutral contract implemented by LLM protocol adapters.
type Client interface {
	Stream(context.Context, Request, EmitFunc) error
}
