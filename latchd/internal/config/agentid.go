package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kayaramazan/latch/proto/model"
)

// AgentID returns this installation's stable agent identifier, creating and
// persisting one (DataDir/agent_id, 0600) on first use. The relay uses it to
// supersede a previous session of the same agent on reconnect.
func AgentID(p Paths) (string, error) {
	path := filepath.Join(p.DataDir, "agent_id")
	b, err := os.ReadFile(path)
	if err == nil {
		if id := strings.TrimSpace(string(b)); model.ValidID(id, model.PrefixAgent) {
			return id, nil
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("config: read agent id: %w", err)
	}
	id := model.NewID(model.PrefixAgent)
	if err := os.WriteFile(path, []byte(id+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("config: write agent id: %w", err)
	}
	return id, nil
}
