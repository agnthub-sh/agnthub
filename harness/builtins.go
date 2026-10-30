package harness

func builtins() []Adapter {
	return []Adapter{
		claudeAdapter{},
		opencodeAdapter{},
		codexAdapter{},
		copilotAdapter{},
		cursorAdapter{},
		geminiAdapter{},
		ompAdapter{},
	}
}
