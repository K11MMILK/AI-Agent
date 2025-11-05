package memory

type Memory struct {
	lastAction  string
	lastURL     string
	lastTitle   string
	lastHash    string
	lastToolSel string
	repeatCount int
}

func (m *Memory) UpdatePage(url, title, hash string) {
	m.lastURL, m.lastTitle, m.lastHash = url, title, hash
}
func (m *Memory) LastPage() (string, string, string) { return m.lastURL, m.lastTitle, m.lastHash }

func (m *Memory) UpdateToolSel(s string, pageHash string) {
	if m.lastToolSel == s && m.lastHash == pageHash {
		m.repeatCount++
	} else {
		m.lastToolSel = s
		m.repeatCount = 0
	}
}
func (m *Memory) RepeatCount() int { return m.repeatCount }

func New() *Memory { return &Memory{} }

func (m *Memory) LastAction() string     { return m.lastAction }
func (m *Memory) SetLastAction(s string) { m.lastAction = s }
