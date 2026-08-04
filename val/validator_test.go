package val

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateString(t *testing.T) {
	testCases := []struct {
		name  string
		value string
		min   int
		max   int
		ok    bool
	}{
		{name: "AtMin", value: "abc", min: 3, max: 5, ok: true},
		{name: "AtMax", value: "abcde", min: 3, max: 5, ok: true},
		{name: "BelowMin", value: "ab", min: 3, max: 5, ok: false},
		{name: "AboveMax", value: "abcdef", min: 3, max: 5, ok: false},
		{name: "Empty", value: "", min: 3, max: 5, ok: false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateString(tc.value, tc.min, tc.max)
			if tc.ok {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestValidateUsername(t *testing.T) {
	testCases := []struct {
		name  string
		value string
		ok    bool
	}{
		{name: "Lowercase", value: "ivywang", ok: true},
		{name: "WithDigits", value: "ivy2547", ok: true},
		{name: "WithUnderscore", value: "ivy_wang", ok: true},
		{name: "Uppercase", value: "IvyWang", ok: false},
		{name: "WithHyphen", value: "ivy-wang", ok: false},
		{name: "WithSpace", value: "ivy wang", ok: false},
		{name: "WithSpecialChar", value: "ivy#1", ok: false},
		// SQL is parameterized by sqlc, but a username is also a lookup key,
		// so anything but [a-z0-9_] should never reach the query layer.
		{name: "SQLish", value: "ivy';drop table users;--", ok: false},
		{name: "TooShort", value: "iv", ok: false},
		{name: "TooLong", value: strings.Repeat("a", 101), ok: false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateUsername(tc.value)
			if tc.ok {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestValidateFullName(t *testing.T) {
	testCases := []struct {
		name  string
		value string
		ok    bool
	}{
		{name: "SingleName", value: "Ivy", ok: true},
		{name: "WithSpace", value: "Ivy Wang", ok: true},
		{name: "MixedCase", value: "ivy WANG", ok: true},
		{name: "WithDigits", value: "Ivy 2547", ok: false},
		{name: "WithHyphen", value: "Ivy-Wang", ok: false},
		{name: "TooShort", value: "Iv", ok: false},
		{name: "TooLong", value: strings.Repeat("a", 101), ok: false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateFullName(tc.value)
			if tc.ok {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestValidatePassword(t *testing.T) {
	testCases := []struct {
		name  string
		value string
		ok    bool
	}{
		{name: "AtMinLength", value: "123456", ok: true},
		{name: "WithSpecialChars", value: "p@ssw0rd!", ok: true},
		{name: "TooShort", value: "12345", ok: false},
		{name: "Empty", value: "", ok: false},
		{name: "TooLong", value: strings.Repeat("a", 101), ok: false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePassword(tc.value)
			if tc.ok {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestValidateEmail(t *testing.T) {
	testCases := []struct {
		name  string
		value string
		ok    bool
	}{
		{name: "Simple", value: "ivy@example.com", ok: true},
		{name: "Subdomain", value: "ivy@mail.example.co.uk", ok: true},
		{name: "PlusTag", value: "ivy+jobs@example.com", ok: true},
		{name: "NoAt", value: "ivy.example.com", ok: false},
		{name: "NoDomain", value: "ivy@", ok: false},
		{name: "NoLocalPart", value: "@example.com", ok: false},
		{name: "WithSpace", value: "ivy wang@example.com", ok: false},
		{name: "Empty", value: "", ok: false},
		{name: "TooLong", value: strings.Repeat("a", 200) + "@example.com", ok: false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateEmail(tc.value)
			if tc.ok {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
