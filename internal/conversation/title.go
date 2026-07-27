package conversation

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MaxTitleRunes        = 80
	untitledConversation = "Untitled conversation"
)

func NormalizeTitle(value string) (string, error) {
	value = normalizeTitleText(value)
	if value == "" {
		return "", fmt.Errorf("%w: conversation title is empty", ErrInvalid)
	}
	if utf8.RuneCountInString(value) > MaxTitleRunes {
		return "", fmt.Errorf("%w: conversation title exceeds %d characters", ErrInvalid, MaxTitleRunes)
	}
	return value, nil
}

func TitleFromPrompt(prompt string) string {
	value := normalizeTitleText(prompt)
	if value == "" {
		return untitledConversation
	}
	runes := []rune(value)
	if len(runes) <= MaxTitleRunes {
		return value
	}
	return string(runes[:MaxTitleRunes-1]) + "…"
}

func normalizeTitleText(value string) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return ' '
		}
		return r
	}, value)
	return strings.Join(strings.Fields(value), " ")
}
