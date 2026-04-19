package agent_test

import "testing"

func TestProcessInfo_ReadsCurrentProcess(t *testing.T) {
	testProcessInfoReadsCurrentProcess(t)
}

func TestProcessInfo_ResolvesSymlinks(t *testing.T) {
	testProcessInfoResolvesSymlinks(t)
}

func TestProcessInfo_MissingProcess(t *testing.T) {
	testProcessInfoMissingProcess(t)
}
