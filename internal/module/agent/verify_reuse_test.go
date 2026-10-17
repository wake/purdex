package agent

import (
	"testing"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/tmux"
)

// #3092: the info verifyEvent read is handed to the frame path, which must not read the same process again.
func TestVerify_AcceptedDecisionCarriesInfoAndFramePathReusesIt(t *testing.T) {
	m := newVerifyTestModule(t)
	m.tmux.(*tmux.FakeExecutor).SetPanePID("%5", "100")
	m.registry.Register(&fakeAgentProvider{typeName: "cc", identify: func(agentpkg.ProcessInfo) bool { return true }})
	stubVerifySeams(t)
	isPidAliveFn = func(int) bool { return true }
	processStartTimeFn = func(int) (string, error) { return "Sun Apr 20 01:30:00 2026", nil }
	pidAncestorIncludesFn = func(int, int) bool { return true }
	reads := 0
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		reads++
		return agentpkg.ProcessInfo{PID: pid, PPID: 100, ExePath: "/usr/local/bin/claude"}, nil
	}
	req := EventRequest{TmuxPaneID: "%5", AgentType: "cc", SenderPID: 200, SenderStartTime: "Sun Apr 20 01:30:00 2026"}
	decision := m.verifyEvent(req)
	if !decision.Accepted || decision.Info == nil || decision.Info.PPID != 100 {
		t.Fatalf("decision = %+v, want accepted with the info read", decision)
	}
	req.verifiedInfo = decision.Info
	info, err := m.senderProcessInfo(req)
	if err != nil || info.PPID != 100 {
		t.Fatalf("senderProcessInfo = %+v, %v", info, err)
	}
	if reads != 1 {
		t.Fatalf("process read %d times for one event, want 1", reads)
	}
}

func TestSenderProcessInfo_ReadsWhenNothingVerifiedOrPidDiffers(t *testing.T) {
	m := newVerifyTestModule(t)
	stubVerifySeams(t)
	reads := 0
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		reads++
		return agentpkg.ProcessInfo{PID: pid, PPID: 7}, nil
	}
	if _, err := m.senderProcessInfo(EventRequest{SenderPID: 200}); err != nil || reads != 1 {
		t.Fatalf("no verified info: reads=%d err=%v, want one read", reads, err)
	}
	req := EventRequest{SenderPID: 200, verifiedInfo: &agentpkg.ProcessInfo{PID: 999, PPID: 1}}
	info, err := m.senderProcessInfo(req)
	if err != nil || reads != 2 || info.PPID != 7 {
		t.Fatalf("info for another pid reused: reads=%d info=%+v err=%v", reads, info, err)
	}
}
