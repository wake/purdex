package fleet

import "testing"

var _ RoleView = (*FakeRole)(nil)

func TestFakeRoleNotifiesInOrder(t *testing.T) {
	f := NewFakeRole(RoleState{Role: RoleOff, Self: "a", Reason: "boot"})
	var got []string
	f.OnChange(func(s RoleState) { got = append(got, "1:"+s.Role) })
	f.OnChange(func(s RoleState) { got = append(got, "2:"+s.Role) })
	f.Set(RoleState{Role: RolePrimary, Term: 3, Self: "a"})
	f.Set(RoleState{Role: RoleStandby, Term: 4, Self: "a", Primary: "b"})
	want := []string{"1:primary", "2:primary", "1:standby", "2:standby"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
	if s := f.State(); s.Role != RoleStandby || s.Term != 4 || s.Primary != "b" {
		t.Fatalf("state = %+v", s)
	}
}
