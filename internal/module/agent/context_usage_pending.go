package agent

import "context"

// applyPendingStatuslines catches the usage readings up with the payloads the statusline proxy kept while the daemon was down
// (#2545). Scaffold.
func (m *Module) applyPendingStatuslines(ctx context.Context) {}
