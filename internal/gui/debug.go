package gui

// debugDialog opens one dialog for screenshot review (DDCDECK_DIALOG=save|rename|rule|edit).
func (w *window) debugDialog(name string) {
	switch name {
	case "save":
		w.saveProfileDialog()
	case "rename":
		w.renameInputDialog("hdmi1", func() {})
	case "rule":
		w.addRuleDialog()
	case "edit":
		w.a.profileNames(func(n []string) {
			if len(n) == 0 {
				return
			}
			var p struct {
				Name     string `json:"name"`
				Monitors []struct {
					Match   string         `json:"match"`
					Fields  map[string]any `json:"fields"`
					OnApply []string       `json:"on_apply"`
				} `json:"monitors"`
			}
			w.a.call("profile.show", map[string]any{"name": n[len(n)-1]}, &p, func(err error) {
				if err == nil {
					w.editProfileDialog(p)
				}
			})
		})
	}
}
