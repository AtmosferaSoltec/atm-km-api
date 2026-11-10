package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseRate(t *testing.T) {
	var problems ValidationError
	assert.Equal(t, "0.3", ParseRate("wear_rate", " 0.30 ", &problems).String())
	assert.Equal(t, "0.125", ParseRate("lpg_rate", "0.125", &problems).String())
	assert.NoError(t, problems.OrNil())

	for text, code := range map[string]string{
		"":       CodeRequired,
		"abc":    CodeInvalid,
		"-0.1":   CodeNegative,
		"101":    CodeTooLarge,
		"0.1234": CodeTooPrecise,
	} {
		var problems ValidationError
		ParseRate("gasoline_rate", text, &problems)
		assert.Equal(t, []FieldError{{"gasoline_rate", code}}, problems.Fields, text)
	}
}
