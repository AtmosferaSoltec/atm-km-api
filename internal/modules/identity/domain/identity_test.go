package domain

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUsernames(t *testing.T) {
	assert.Equal(t, "joel", NormalizeUsername("  Joel "))
	for _, ok := range []string{"joel", "71234567", "j.maldonado", "ana_m-2"} {
		var problems ValidationError
		ValidateUsername(ok, &problems)
		assert.NoError(t, problems.OrNil(), ok)
	}
	for _, bad := range []string{"", "jo", ".joel", "joel maldonado", "joél", strings.Repeat("a", 41)} {
		var problems ValidationError
		ValidateUsername(bad, &problems)
		assert.Error(t, problems.OrNil(), bad)
	}
}

func TestPasswordsAndNames(t *testing.T) {
	var problems ValidationError
	ValidateNewPassword("1234567", &problems)
	ValidateNewPassword(strings.Repeat("x", 129), &problems)
	ValidateNewPassword("12345678", &problems)
	ValidateDisplayName(NormalizeDisplayName("   "), &problems)
	ValidateDisplayName(strings.Repeat("n", 61), &problems)
	assert.Equal(t, []FieldError{
		{"password", CodePasswordTooShort},
		{"password", CodePasswordTooLong},
		{"display_name", CodeDisplayNameRequired},
		{"display_name", CodeDisplayNameTooLong},
	}, problems.Fields)
	assert.Equal(t, "Joel Maldonado", NormalizeDisplayName("  Joel   Maldonado "))
}
