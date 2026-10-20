package gate

// markFilter passes the remote command's stderr through and takes out the gate's own mark lines, saying which it saw. A mark is a line
// that begins with the record separator and names the sender's nonce; any other line that begins with it is the command's and passes.
type markFilter struct {
	w          interface{ Write([]byte) (int, error) }
	admitted   string
	notStarted string
	sawAdmit   bool
	sawNoStart bool
	line       []byte // a line that began with the separator, kept until it ends
	collecting bool
	atStart    bool
}

func newMarkFilter(w interface{ Write([]byte) (int, error) }, nonce string) *markFilter {
	return &markFilter{w: w, admitted: AdmittedMark(nonce), notStarted: NotStartedMark(nonce), atStart: true}
}

func (m *markFilter) Write(p []byte) (int, error) {
	n := len(p)
	var out []byte
	for _, b := range p {
		switch {
		case m.collecting:
			m.line = append(m.line, b)
			if b == '\n' {
				l := string(m.line)
				switch l {
				case m.admitted:
					m.sawAdmit = true
				case m.notStarted:
					m.sawNoStart = true
				default:
					out = append(out, m.line...)
				}
				m.line, m.collecting, m.atStart = m.line[:0], false, true
			} else if len(m.line) > 256 { // not a mark: too long to be one
				out = append(out, m.line...)
				m.line, m.collecting, m.atStart = m.line[:0], false, false
			}
		case m.atStart && b == 0x1e:
			m.collecting = true
			m.line = append(m.line[:0], b)
		default:
			out = append(out, b)
			m.atStart = b == '\n'
		}
	}
	if len(out) > 0 {
		if _, err := m.w.Write(out); err != nil {
			return n, err
		}
	}
	return n, nil
}

// Flush writes out a partial line that began like a mark but never ended.
func (m *markFilter) Flush() {
	if len(m.line) > 0 {
		_, _ = m.w.Write(m.line)
		m.line, m.collecting = m.line[:0], false
	}
}
