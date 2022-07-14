package main

import (
	"testing"
)

func TestEscapeMarkdown(t *testing.T) {
	var tests = []struct {
		text string
		want string
	}{
		{"foo", "foo"},
		{"thing _ with _ underscores", "thing \\_ with \\_ underscores"},
	}
	for _, tt := range tests {
		testname := tt.text
		t.Run(testname, func(t *testing.T) {
			got := escapeMarkdown(tt.text)
			if got != tt.want {
				t.Errorf("escapeMarkdown(%s) = %s; want %s", tt.text, got, tt.want)
			}
		})
	}
}
