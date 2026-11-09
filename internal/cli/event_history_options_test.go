package cli

import (
	"go/ast"
	"go/parser"
	"testing"
)

func TestEventCleanupHistoryOptionsAreStaticallyRecognized(t *testing.T) {
	imports := map[string]string{"s": spineImportPath, "other": "example.com/untrusted"}
	for _, tc := range []struct {
		expression string
		want       bool
	}{
		{`s.WithEventPublisherErrorHistory(boot.CleanupErrorHistoryOptions{MaxEntries:64})`, true},
		{`s.WithEventConsumerErrorHistory(boot.CleanupErrorHistoryOptions{MaxAge:time.Hour})`, true},
		{`s.WithEventPublisherErrorHistory()`, false},
		{`s.WithEventConsumerErrorHistory(first,second)`, false},
		{`s.WithEventConsumerErrorHistory(options...)`, false},
		{`other.WithEventConsumerErrorHistory(policy)`, false},
		{`customHistoryOption(policy)`, false},
	} {
		expression, err := parser.ParseExpr(tc.expression)
		if err != nil {
			t.Fatal(err)
		}
		if got := knownEventOptions([]ast.Expr{expression}, imports); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.expression, got, tc.want)
		}
	}
}
