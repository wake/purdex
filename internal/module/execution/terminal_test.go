package execution

import "testing"

func TestClassifyOutcome(t *testing.T) {
	cases := []struct {
		name       string
		exitCode   int
		signaled   bool
		res        ResultOutcome
		wantStatus Status
		wantSource OutcomeSource
	}{
		{
			name:       "exit0 + result ok -> completed(result)",
			exitCode:   0,
			res:        ResultOutcome{HasResult: true, IsError: false, Subtype: "success"},
			wantStatus: StatusCompleted,
			wantSource: OutcomeResult,
		},
		{
			name:       "exit0 + result is_error -> failed(result)",
			exitCode:   0,
			res:        ResultOutcome{HasResult: true, IsError: true, Subtype: "error_during_execution"},
			wantStatus: StatusFailed,
			wantSource: OutcomeResult,
		},
		{
			name:       "exit0 + no result -> completed(exit_only, degraded)",
			exitCode:   0,
			res:        ResultOutcome{HasResult: false},
			wantStatus: StatusCompleted,
			wantSource: OutcomeExitOnly,
		},
		{
			name:       "exit nonzero -> failed regardless of result",
			exitCode:   7,
			res:        ResultOutcome{HasResult: true, IsError: false, Subtype: "success"},
			wantStatus: StatusFailed,
			wantSource: OutcomeResult,
		},
		{
			name:       "exit nonzero + no result -> failed(exit_only)",
			exitCode:   1,
			res:        ResultOutcome{HasResult: false},
			wantStatus: StatusFailed,
			wantSource: OutcomeExitOnly,
		},
		{
			name:       "signaled (exit -1) -> failed",
			exitCode:   -1,
			signaled:   true,
			res:        ResultOutcome{HasResult: false},
			wantStatus: StatusFailed,
			wantSource: OutcomeExitOnly,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotStatus, gotSource := ClassifyOutcome(tc.exitCode, tc.signaled, tc.res)
			if gotStatus != tc.wantStatus {
				t.Errorf("status: got %q want %q", gotStatus, tc.wantStatus)
			}
			if gotSource != tc.wantSource {
				t.Errorf("source: got %q want %q", gotSource, tc.wantSource)
			}
		})
	}
}
