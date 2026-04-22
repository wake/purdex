package agent_test

import "testing"

func TestProcessInfo_ReadsCurrentProcess(t *testing.T) {
	testProcessInfoReadsCurrentProcess(t)
}

func TestProcessInfo_PreservesSymlinkInvocationPath(t *testing.T) {
	testProcessInfoPreservesSymlinkInvocationPath(t)
}

func TestProcessInfo_MissingProcess(t *testing.T) {
	testProcessInfoMissingProcess(t)
}
