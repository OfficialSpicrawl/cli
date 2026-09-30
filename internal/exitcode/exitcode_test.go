package exitcode

import "testing"

func TestForProblem(t *testing.T) {
	for _, tc := range []struct {
		code      string
		retryable bool
		want      int
	}{
		{"ERR::INTERNAL::UNAVAILABLE", false, Engine},
		{"ERR::INTERNAL::UNAVAILABLE", true, Internal},
		{"ERR::INTERNAL::ERROR", false, Internal},
		{"ERR::EXTRACT::INVALID_RULES", false, Request},
		{"ERR::EXTRACT::FAILED", false, Engine},
		{"ERR::PROXY::EXHAUSTED", true, Proxy},
	} {
		if got := ForProblem(tc.code, tc.retryable); got != tc.want {
			t.Errorf("ForProblem(%s, %v) = %d, want %d", tc.code, tc.retryable, got, tc.want)
		}
	}
}

func TestTableIsComplete(t *testing.T) {
	if len(Table) != Timeout+1 {
		t.Fatalf("%d entries, want %d", len(Table), Timeout+1)
	}
	for i, e := range Table {
		if e.Code != i || e.Name == "" || e.Meaning == "" {
			t.Errorf("entry %d: %+v", i, e)
		}
	}
}
