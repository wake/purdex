package watch

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateWake_ReasonAndDetailAreChecked(t *testing.T) {
	require.NoError(t, ValidateWake("unattended_changed", "無人值守模式 → 開（by app）"))
	require.NoError(t, ValidateWake("patrol", ""))
	for _, bad := range []string{"", "Patrol", "a b", "1x", strings.Repeat("a", 65)} {
		require.Error(t, ValidateWake(bad, ""), bad)
	}
	require.Error(t, ValidateWake("patrol", "a\nb"), "one line")
	require.Error(t, ValidateWake("patrol", strings.Repeat("x", 501)), "at most 500 bytes")
}

func TestWakeText_NamesTheReasonAndCarriesTheDetail(t *testing.T) {
	require.Equal(t, "[負載守門] 叫醒：無人值守開關改變｜無人值守模式 → 關（by app）", WakeText("unattended_changed", "無人值守模式 → 關（by app）"))
	require.Equal(t, "[負載守門] 叫醒：例行巡檢", WakeText("patrol", ""))
	require.Equal(t, "[負載守門] 叫醒：something_new", WakeText("something_new", ""))
}

func TestWakeTextNamesAWwdAlert(t *testing.T) {
	got := WakeText("wwd_alert", "mlab：磁碟空間不足")
	if want := messagePrefix + " 叫醒：wwd 警報｜mlab：磁碟空間不足"; got != want {
		t.Fatalf("WakeText = %q, want %q", got, want)
	}
}
