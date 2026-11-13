package approval

import (
	"slices"
	"testing"
)

// confirm_review: a binary file not seen before has to be typed out; one that is unchanged is an ordinary question.
func TestConfirmReview(t *testing.T) {
	bin := BinaryMark + "aa"
	c := ConfirmReview("approve", map[string]string{"t": "echo hi\n"}, map[string]string{"t": bin, "u": bin}, "no terminal")
	if !slices.Equal(c.Accept, []string{"binary"}) || c.NoTerminal != "no terminal" {
		t.Errorf("unseen binary: %+v", c)
	}
	if want := "can't show t, u. Type binary to approve it unseen:"; c.Question != want {
		t.Errorf("question %q, want %q", c.Question, want)
	}
	c = ConfirmReview("approve", map[string]string{"t": bin, "u": bin}, map[string]string{"t": bin, "u": bin}, "")
	if c.Accept != nil || c.Question != "approve?" {
		t.Errorf("seen binary: %+v", c)
	}
}
