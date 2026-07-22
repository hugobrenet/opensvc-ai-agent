package agent

import (
	"strings"
	"testing"
)

func TestSystemPromptGroundsToolArguments(t *testing.T) {
	for _, instruction := range []string{
		"Never guess object paths, node names, resource identifiers, or other tool arguments.",
		"Only use identifiers provided by the user or returned by successful tool results.",
		"Examples in tool descriptions or schemas are illustrative and are never discovered identifiers.",
		"Never infer an identifier from an object name, naming convention, example, or failed tool output.",
		"If a prerequisite discovery tool fails or does not return a required identifier, do not call any dependent tool, even when a likely value can be inferred; stop that diagnostic branch and report the uncertainty.",
	} {
		if !strings.Contains(systemPrompt, instruction) {
			t.Errorf("system prompt is missing instruction %q", instruction)
		}
	}
}
