package conversation

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestNormalizeTitle(t *testing.T) {
	title, err := NormalizeTitle("  Assess\tcluster\nhealth\u200bnow  ")
	if err != nil {
		t.Fatalf("NormalizeTitle() error: %v", err)
	}
	if title != "Assess cluster health now" {
		t.Fatalf("NormalizeTitle() = %q", title)
	}
	for _, value := range []string{" \n\t ", strings.Repeat("a", MaxTitleRunes+1)} {
		if _, err := NormalizeTitle(value); !errors.Is(err, ErrInvalid) {
			t.Fatalf("NormalizeTitle(%q) error = %v", value, err)
		}
	}
}

func TestTitleFromPrompt(t *testing.T) {
	if got := TitleFromPrompt("  Assess\ncluster health  "); got != "Assess cluster health" {
		t.Fatalf("TitleFromPrompt() = %q", got)
	}
	got := TitleFromPrompt(strings.Repeat("é", MaxTitleRunes+10))
	if utf8.RuneCountInString(got) != MaxTitleRunes || !strings.HasSuffix(got, "…") {
		t.Fatalf("truncated title = %q (%d runes)", got, utf8.RuneCountInString(got))
	}
	if got := TitleFromPrompt("\x00\n"); got != untitledConversation {
		t.Fatalf("empty prompt title = %q", got)
	}
}
