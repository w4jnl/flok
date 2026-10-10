package main

import (
	"strings"

	"github.com/w4jnl/flok/internal/answer"
)

// answerArg builds an answer from the tail flags.
func answerArg(pane, text, keys string) answer.Answer {
	a := answer.Answer{Pane: pane, Text: text}
	for _, k := range strings.Split(keys, ",") {
		if k = strings.TrimSpace(k); k != "" {
			a.Keys = append(a.Keys, k)
		}
	}
	return a
}
