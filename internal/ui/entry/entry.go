// Package entry holds the app's entry flow: Splash, first-run Setup, Login
// and the main Menu.
package entry

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/dursunkoc/jenktui/internal/config"
	"github.com/dursunkoc/jenktui/internal/jenkins"
	"github.com/dursunkoc/jenktui/internal/ui/core"
)

// Factory builds a screen from the session. Later lanes inject real screens.
type Factory func(*core.Session) core.Screen

// Deps are the injected dependencies of the entry flow.
type Deps struct {
	ConfigDir   string        // directory holding config.toml
	Collections Factory       // nil -> "coming soon" placeholder
	Catalog     Factory       // nil -> "coming soon" placeholder
	Overview    Factory       // nil -> "coming soon" placeholder
	MinSplash   time.Duration // 0 -> 1.5s
	// OpenCollection returns the command that opens the named Collection's Run
	// Monitor (the Session's StartCollection); it yields a Start. nil ignores
	// the name.
	OpenCollection func(s *core.Session, name string) tea.Cmd
}

// Start is the result of Deps.OpenCollection. The command does the slow work
// off the UI goroutine; Screen is called on it. A Notice is shown on the
// Menu (an unknown Collection); a nil Screen leaves the Menu on top.
type Start struct {
	Screen func() core.Screen
	Notice string
}

func (d Deps) configPath() string { return filepath.Join(d.ConfigDir, "config.toml") }

func (d Deps) minSplash() time.Duration {
	if d.MinSplash <= 0 {
		return 1500 * time.Millisecond
	}
	return d.MinSplash
}

// ComingSoon returns a placeholder screen with a neutral notice; esc goes back.
func ComingSoon(title string) core.Screen { return &notice{title: title, th: core.NewTheme()} }

type notice struct {
	title string
	th    core.Theme
}

func (n *notice) Init() tea.Cmd { return nil }
func (n *notice) Update(msg tea.Msg) (core.Screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "esc", "q":
			return n, core.Cmd(core.Pop{})
		}
	}
	return n, nil
}
func (n *notice) View() string {
	return n.th.NoticeView(n.title, "coming soon - press esc to go back")
}
func (n *notice) Title() string       { return n.title }
func (n *notice) Keys() []key.Binding { return []key.Binding{core.KeyBack} }

// checkReachable does an unauthenticated request: any HTTP answer (even 401)
// means the server is reachable; only TLS/network failures are errors.
func checkReachable(cfg config.Config) error {
	c, err := jenkins.New(jenkins.Options{BaseURL: cfg.JenkinsURL, CACertPath: cfg.CACert, InsecureSkipVerify: cfg.InsecureSkipVerify})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := c.Ping(ctx); err != nil && !errors.Is(err, jenkins.ErrAuth) {
		return err
	}
	return nil
}

// errHint maps a Jenkins error to a short, actionable hint.
func errHint(err error) string {
	switch {
	case errors.Is(err, jenkins.ErrAuth):
		return "Check your username and password or API token. An API token is recommended (Jenkins > your user > Configure > API Token)."
	case errors.Is(err, jenkins.ErrTLS):
		return "The certificate is not trusted. Set ca_cert to your CA bundle in config.toml, or insecure_skip_verify = true (not recommended)."
	case errors.Is(err, jenkins.ErrNetwork):
		return "Check jenkins_url in config.toml, your network/VPN and proxy settings."
	}
	return "Check config.toml and try again."
}

func errTitle(err error) string {
	switch {
	case errors.Is(err, jenkins.ErrAuth):
		return "Authentication failed"
	case errors.Is(err, jenkins.ErrTLS):
		return "TLS error"
	case errors.Is(err, jenkins.ErrNetwork):
		return "Cannot reach Jenkins"
	}
	return "Error"
}
