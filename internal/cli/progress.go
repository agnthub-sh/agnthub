package cli

import (
	"fmt"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

// busy shows a transient spinner line on stderr while a slow step (fetching a
// source, resolving dependencies, running the skill installer) runs, and erases
// it when the returned function is called. It writes nothing when stderr is not
// a terminal, TERM is dumb or the command prints JSON, so redirected and
// machine-readable output never contains it. No prompt may run while it shows.
func (a *App) busy(asJSON bool, label string) (done func()) {
	if asJSON || !a.styledStderr {
		return func() {}
	}
	w := colorprofile.NewWriter(a.Stderr, a.Env.Pairs())
	style := lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	frames := spinner.MiniDot
	stop := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(frames.FPS)
		defer ticker.Stop()
		for frame := 0; ; frame++ {
			fmt.Fprintf(w, "\r%s %s%s",
				style.Render(frames.Frames[frame%len(frames.Frames)]), label, ansi.EraseLineRight)
			select {
			case <-stop:
				fmt.Fprint(w, "\r"+ansi.EraseEntireLine)
				return
			case <-ticker.C:
			}
		}
	}()
	return func() {
		close(stop)
		<-stopped
	}
}
