package resources

// The fallback of a notice no Purdex mod channel takes (amendment A §2.10, P2d-2).

// LeadNotice is what a member's lead is told when the notice meant for the member could not go into its conversation: the
// notice is addressed to the member, so the lead is told whose it is and that it was not delivered.
func LeadNotice(sessionID, text string) string {
	return "【資源租約】成員（session " + shortSession(sessionID) + "）的 Purdex mod 沒有連線，下面這則通知沒能送進它的對話，請替它處理或轉告：\n" + text
}

// LeaseNoticeAlertText is the text of the user alert for a notice that has no session to go to and no lead to tell.
func LeaseNoticeAlertText(sessionID, text string) string {
	return "session " + shortSession(sessionID) + " 沒有 Purdex mod 通道，資源租約的通知沒送到：\n" + text
}

func shortSession(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// AlertEventType is the WS host event of a user alert; AlertRuleLeaseNotice is the rule of the alert for an undelivered
// notice (the App contract of the v2 plan, plus this rule).
const (
	AlertEventType       = "resources.alert"
	AlertRuleLeaseNotice = "lease_notice"
)

// Alert is the value of a resources.alert event. ClosedAt is 0 while open; a lease_notice alert has no close.
type Alert struct {
	ID        string      `json:"id"`
	Rule      string      `json:"rule"`
	Diagnosis string      `json:"diagnosis"`
	OpenedAt  int64       `json:"opened_at"`
	ClosedAt  int64       `json:"closed_at"`
	Host      *AlertHost  `json:"host"`
	Lease     *AlertLease `json:"lease"`
	Text      string      `json:"text"`
}

// AlertHost is the host's state when the alert opened.
type AlertHost struct {
	Mem      float64 `json:"mem"`
	Pressure int     `json:"pressure"`
}

// AlertLease names what the alert is about: for a lease_notice, the position and its session.
type AlertLease struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	SessionID string `json:"session_id"`
	AgeS      int64  `json:"age_s"`
	RSSBytes  int64  `json:"rss_bytes"`
}
