package run

import (
	"strings"
	"testing"
	"time"
)

// ExtraProperties lets a measurement add a hardening property (WWD-4d-3: ProtectSystem=strict) without a code change. It is an
// allow list of properties that only take rights away, so it cannot become a way to take the sandbox apart.

func TestExtraPropertiesThatOnlyTightenAreAccepted(t *testing.T) {
	for _, p := range []string{"ProtectSystem=strict", "ProtectSystem=full", "ProtectKernelTunables=yes", "PrivateDevices=yes", "TasksMax=512", "ReadOnlyPaths=/etc",
		"ReadOnlyPaths=-/srv/backups", "CPUWeight=50", "IOWeight=100", "LimitCORE=0", "ProtectProc=invisible"} {
		sb := Sandbox{User: "wake", ExtraProperties: []string{p}}
		if err := sb.validate(Spec{JobID: "j_1", Workspace: "/ws"}); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
}

func TestExtraPropertiesCannotTouchWhatTheSandboxSets(t *testing.T) {
	bad := []string{
		"ProtectHome=no", "ReadWritePaths=/", "NoNewPrivileges=no", "InaccessiblePaths=", "PrivateTmp=no", "WorkingDirectory=/",
		"MemoryMax=infinity", "CPUQuota=", "RuntimeMaxSec=0", "User=root", "Group=root", "DynamicUser=no", "Environment=A=1", "EnvironmentFile=/etc/x",
		"CapabilityBoundingSet=CAP_SYS_ADMIN", "AmbientCapabilities=CAP_SYS_ADMIN", "ExecStartPre=/bin/sh", "BindPaths=/run/docker.sock", "BindReadOnlyPaths=/",
		"Slice=system.slice", "Delegate=yes", "SupplementaryGroups=docker", "PermissionsStartOnly=yes", "TemporaryFileSystem=/", "UMask=0",
	}
	for _, p := range bad {
		sb := Sandbox{User: "wake", ExtraProperties: []string{p}}
		if err := sb.validate(Spec{JobID: "j_1", Workspace: "/ws"}); err == nil {
			t.Errorf("%s must be refused", p)
		}
	}
}

func TestAJobIDWithADashIsAUnitNameLikeTheJobMarkerAllows(t *testing.T) {
	if err := (Sandbox{User: "wake"}).validate(Spec{JobID: "j_4d3-A", Workspace: "/ws"}); err != nil {
		t.Fatal(err)
	}
	if err := (Sandbox{User: "wake"}).validate(Spec{JobID: "j_4d3/A", Workspace: "/ws"}); err == nil {
		t.Fatal("a slash cannot be in a unit name")
	}
}

func TestExtraPropertiesAreCheckedForShape(t *testing.T) {
	for _, p := range []string{"", "ProtectSystem", "=strict", "ProtectSystem=", "ProtectSystem=str ict", "ProtectSystem=strict\nUser=root", "ProtectSystem=strict;x", "protectsystem=strict", "ProtectSystem=\"strict\"", "Protect System=strict"} {
		sb := Sandbox{User: "wake", ExtraProperties: []string{p}}
		if err := sb.validate(Spec{JobID: "j_1", Workspace: "/ws"}); err == nil {
			t.Errorf("%q must be refused", p)
		}
	}
}

func TestExtraPropertiesComeAfterTheFixedOnesAndBeforeTheCommand(t *testing.T) {
	sb := Sandbox{User: "wake", ExtraProperties: []string{"ProtectSystem=strict"}}.withDefaults()
	got := sandboxArgv(sb, "wwd-j", "/ws", "/ws", "/ws/.wwd-exit", time.Minute, time.Second, nil, "/bin/true", nil)
	j := strings.Join(got, " ")
	i, e := strings.Index(j, "-p ProtectSystem=strict"), strings.Index(j, " env -i")
	d := strings.Index(j, "InaccessiblePaths=")
	if i < 0 || e < 0 || i < d || i > e {
		t.Fatalf("argv = %s", j)
	}
}

func TestExtraPropertiesWithAValueThatLoosensAreRefused(t *testing.T) {
	for _, p := range []string{"PrivateDevices=no", "RestrictNamespaces=no", "ProtectSystem=no", "ProtectSystem=false", "TasksMax=infinity", "TasksMax=100000",
		"TasksMax=0", "TasksMax=+5", "TasksMax=5K", "CPUWeight=10000", "IOWeight=1000", "LimitCORE=infinity", "LimitCORE=100", "ProtectProc=default",
		"ReadOnlyPaths=etc", "ReadOnlyPaths=/a/../b", "ProcSubset=all", "LockPersonality=no"} {
		sb := Sandbox{User: "wake", ExtraProperties: []string{p}}
		if err := sb.validate(Spec{JobID: "j_1", Workspace: "/ws"}); err == nil {
			t.Errorf("%s must be refused: it does not tighten", p)
		}
	}
}
