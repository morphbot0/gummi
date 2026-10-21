package engine

import (
	"fmt"

	"github.com/morphis/gummi/internal/config"
)

// layeredConfig reads the user and workspace config the way every probe
// path does: a user path that cannot be resolved is a warning, not a stop.
func (e *Engine) layeredConfig() (config.Config, error) {
	userPath, err := config.UserConfigPath()
	if err != nil {
		if e.envWarn != nil {
			e.envWarn(fmt.Sprintf("user config path could not be resolved: %v", err))
		}
		userPath = ""
	}
	cfg, _, err := config.LoadLayered(userPath, e.cfg.Workspace.ConfigFile())
	return cfg, err
}
