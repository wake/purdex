package journal

import (
	"testing"
	"time"
)

func TestLabels(t *testing.T) {
	cases := map[string]Entry{
		"使用者請求": {Kind: KindRequest, By: ByUser}, "領導者請求": {Kind: KindRequest, By: ByLeader},
		"成員請求": {Kind: KindRequest, By: ByMember}, "請求": {Kind: KindRequest},
		"任務建立": {Kind: KindTaskNew}, "任務完成": {Kind: KindTaskDone}, "執行": {Kind: KindExec},
		"執行完成": {Kind: KindExecDone}, "溝通": {Kind: KindComm}, "待確認": {Kind: KindConfirm},
	}
	for want, e := range cases {
		if got := e.Label(); got != want {
			t.Errorf("%v: label %q want %q", e, got, want)
		}
	}
}

func TestValidate(t *testing.T) {
	ok := Entry{ID: "a", Kind: KindExec, At: time.Now()}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, e := range map[string]Entry{
		"no id": {Kind: KindExec, At: time.Now()}, "bad kind": {ID: "a", Kind: "x", At: time.Now()},
		"no time": {ID: "a", Kind: KindExec}, "bad by": {ID: "a", Kind: KindRequest, By: "x", At: time.Now()},
	} {
		if e.Validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
