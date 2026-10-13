package resources

// FinalNotice is the text that goes with the result of the command a position
// was told would be its last (amendment A §3.3, plan §2.7).
func FinalNotice(kind NoticeKind) string {
	switch kind {
	case NoticeOvertime:
		return "因為執行時間過長，這次指令執行完畢後會釋出執行排序，請自行安排執行方式與內容。"
	case NoticeThreeCommands:
		return "有其他 session 在排隊，下一個指令會是這次的最後一個；之後還要跑請重新排隊。"
	}
	return ""
}
